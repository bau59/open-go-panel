// Package mcpaccess stores project-scoped OAuth grants in the existing panel
// database. It deliberately contains no public HTTP handlers or command runner.
package mcpaccess

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "crypto/subtle"
    "database/sql"
    "encoding/base64"
    "errors"
    "fmt"
    "net/url"
    "regexp"
    "strings"
    "time"

    "github.com/bau59/open-go-panel/internal/state"
)

var ErrUnauthorized = errors.New("invalid or revoked MCP authorization")
var ErrPending = errors.New("administrator approval required")
var ErrDenied = errors.New("administrator denied access")
var ErrAlreadyEnabled = errors.New("application access is already enabled")

const requestLifetime = 10*time.Minute
const codeLifetime = 2*time.Minute
const accessLifetime = time.Hour
const refreshLifetime = 30*24*time.Hour

type Store struct { db *sql.DB; state *state.Store }

func New(s *state.Store) *Store { return &Store{db:s.DB(),state:s} }

type Connection struct {
    AppID int64
    ID string
    Resource string
    Owner string
    Root string
    CreatedAt string
    EnabledAt int64
    AuthorizedAt int64
    LastSuccessAt int64
    RevokedAt int64
}

type PendingRequest struct {
    ID string
    ClientID string
    RedirectURI string
    ExpiresAt int64
}

type AuthorizationRequest struct {
    ID string
    PollSecret string
}

type AuthorizationCode struct {
    RequestID string
    Code string
    RedirectURI string
    State string
}

type Tokens struct {
    AccessToken string
    RefreshToken string
    ExpiresIn int64
}

func secret() (string,error) {
    b:=make([]byte,32)
    if _,err:=rand.Read(b);err!=nil{return "",err}
    return base64.RawURLEncoding.EncodeToString(b),nil
}

func hash(value string) string {
    sum:=sha256.Sum256([]byte(value))
    return base64.RawURLEncoding.EncodeToString(sum[:])
}

var challengeRE=regexp.MustCompile("^[A-Za-z0-9_-]{43}$")
var verifierRE=regexp.MustCompile("^[A-Za-z0-9._~-]{43,128}$")

// Use the stable OpenAI callback only. The future HTTP discovery layer must
// advertise issuer identification and include iss in authorization responses.
const RedirectURI="https://chatgpt.com/connector_platform_oauth_redirect"

func resourceURL(origin,id string)(string,error){
    u,err:=url.Parse(origin)
    if err!=nil||u.Scheme!="https"||u.Hostname()==""||u.User!=nil||u.RawQuery!=""||u.Fragment!=""||(u.Path!=""&&u.Path!="/")||u.Port()!=""{
        return "",errors.New("expected an HTTPS origin without a path or port")
    }
    return strings.TrimSuffix(origin,"/")+"/mcp/"+id,nil
}

func (s *Store) Enable(ctx context.Context,appID int64,origin string)(Connection,error){
    var conn Connection
    id,err:=secret();if err!=nil{return conn,err}
    resource,err:=resourceURL(origin,id);if err!=nil{return conn,err}
    tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return conn,err}
    defer tx.Rollback()
    var revoked int64
    err=tx.QueryRowContext(ctx,"SELECT revoked_at FROM mcp_connections WHERE app_id=?",appID).Scan(&revoked)
    if err==nil&&revoked==0{return conn,ErrAlreadyEnabled}
    if err!=nil&&!errors.Is(err,sql.ErrNoRows){return conn,err}
    if _,err:=tx.ExecContext(ctx,"DELETE FROM mcp_connections WHERE app_id=?",appID);err!=nil{return conn,err}
    now:=time.Now().Unix()
    result,err:=tx.ExecContext(ctx,`INSERT INTO mcp_connections(app_id,connection_id,resource,owner,root,app_created_at,enabled_at)
        SELECT id,?,?,owner,root,created_at,? FROM apps WHERE id=?`,id,resource,now,appID)
    if err!=nil{return conn,err}
    if n,err:=result.RowsAffected();err!=nil||n!=1{return conn,ErrUnauthorized}
    conn,err=readConnection(tx.QueryRowContext(ctx,connectionQuery+" WHERE c.app_id=?",appID))
    if err!=nil{return conn,err}
    if err:=tx.Commit();err!=nil{return Connection{},err}
    return conn,nil
}

// A matching current application row is required in addition to the grant.
// Changing its identity or root invalidates the existing connection.
const connectionQuery=`SELECT c.app_id,c.connection_id,c.resource,c.owner,c.root,c.app_created_at,c.enabled_at,c.authorized_at,c.last_success_at,c.revoked_at
    FROM mcp_connections c JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at`

func readConnection(row *sql.Row)(Connection,error){
    var c Connection
    err:=row.Scan(&c.AppID,&c.ID,&c.Resource,&c.Owner,&c.Root,&c.CreatedAt,&c.EnabledAt,&c.AuthorizedAt,&c.LastSuccessAt,&c.RevokedAt)
    if errors.Is(err,sql.ErrNoRows){return c,ErrUnauthorized}
    return c,err
}

func (s *Store) Connection(ctx context.Context,appID int64)(Connection,error){
    return readConnection(s.db.QueryRowContext(ctx,connectionQuery+" WHERE c.app_id=?",appID))
}

func (s *Store) Revoke(ctx context.Context,appID int64)error{
    tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return err};defer tx.Rollback()
    if _,err:=tx.ExecContext(ctx,"DELETE FROM mcp_clients WHERE connection_id=(SELECT connection_id FROM mcp_connections WHERE app_id=?)",appID);err!=nil{return err}
    if _,err:=tx.ExecContext(ctx,"UPDATE mcp_connections SET revoked_at=? WHERE app_id=? AND revoked_at=0",time.Now().Unix(),appID);err!=nil{return err}
    return tx.Commit()
}

func (s *Store) Register(ctx context.Context,connectionID,redirect string)(string,error){
    if redirect!=RedirectURI{return "",errors.New("unsupported OAuth redirect URI")}
    clientID,err:=secret();if err!=nil{return "",err}
    now:=time.Now().Unix()
    // Bound unapproved registrations and require a current connection.
    result,err:=s.db.ExecContext(ctx,`INSERT INTO mcp_clients(client_id,connection_id,redirect_uri,created_at)
        SELECT ?,c.connection_id,?,? FROM mcp_connections c JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at
        WHERE c.connection_id=? AND c.revoked_at=0
        AND (SELECT COUNT(*) FROM mcp_clients WHERE connection_id=c.connection_id)<32`,clientID,redirect,now,connectionID)
    if err!=nil{return "",err}
    n,err:=result.RowsAffected();if err!=nil{return "",err};if n!=1{return "",ErrUnauthorized}
    return clientID,nil
}

func (s *Store) Begin(ctx context.Context,connectionID,clientID,resource,redirect,challenge,oauthState string)(AuthorizationRequest,error){
    var req AuthorizationRequest
    if !challengeRE.MatchString(challenge)||len(oauthState)>2048||redirect!=RedirectURI{return req,ErrUnauthorized}
    id,err:=secret();if err!=nil{return req,err}
    poll,err:=secret();if err!=nil{return req,err}
    now:=time.Now().Unix()
    tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return req,err};defer tx.Rollback()
    if _,err:=tx.ExecContext(ctx,"DELETE FROM mcp_requests WHERE expires_at<=?",now);err!=nil{return req,err}
    result,err:=tx.ExecContext(ctx,`INSERT INTO mcp_requests(request_id,poll_hash,client_id,challenge,oauth_state,expires_at)
        SELECT ?,?,cl.client_id,?,?,? FROM mcp_clients cl JOIN mcp_connections c ON c.connection_id=cl.connection_id
        JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at
        WHERE c.connection_id=? AND cl.client_id=? AND c.resource=? AND cl.redirect_uri=? AND c.revoked_at=0
        AND (SELECT COUNT(*) FROM mcp_requests WHERE client_id=cl.client_id)<4`,
        id,hash(poll),challenge,oauthState,now+int64(requestLifetime/time.Second),connectionID,clientID,resource,redirect)
    if err!=nil{return req,err}
    n,err:=result.RowsAffected();if err!=nil{return req,err};if n!=1{return req,ErrUnauthorized}
    if err:=tx.Commit();err!=nil{return req,err}
    return AuthorizationRequest{ID:id,PollSecret:poll},nil
}

func (s *Store) Pending(ctx context.Context,appID int64)([]PendingRequest,error){
    rows,err:=s.db.QueryContext(ctx,`SELECT r.request_id,r.client_id,cl.redirect_uri,r.expires_at FROM mcp_requests r
        JOIN mcp_clients cl ON cl.client_id=r.client_id JOIN mcp_connections c ON c.connection_id=cl.connection_id
        JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at
        WHERE c.app_id=? AND c.revoked_at=0 AND r.decision='pending' AND r.expires_at>? ORDER BY r.expires_at`,appID,time.Now().Unix())
    if err!=nil{return nil,err};defer rows.Close()
    var result []PendingRequest
    for rows.Next(){var r PendingRequest;if err:=rows.Scan(&r.ID,&r.ClientID,&r.RedirectURI,&r.ExpiresAt);err!=nil{return nil,err};result=append(result,r)}
    return result,rows.Err()
}

// Decide is an administrative action, not a public OAuth endpoint. The UI
// must show the same request ID in the public browser and private panel.
func (s *Store) Decide(ctx context.Context,appID int64,requestID string,approve bool)error{
    decision:="denied";if approve{decision="approved"}
    result,err:=s.db.ExecContext(ctx,`UPDATE mcp_requests SET decision=? WHERE request_id=? AND decision='pending' AND expires_at>?
        AND client_id IN (SELECT cl.client_id FROM mcp_clients cl JOIN mcp_connections c ON c.connection_id=cl.connection_id
        JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at
        WHERE c.app_id=? AND c.revoked_at=0)`,decision,requestID,time.Now().Unix(),appID)
    if err!=nil{return err};n,err:=result.RowsAffected();if err!=nil{return err};if n!=1{return ErrUnauthorized};return nil
}

// Poll issues a short-lived code once, only to the browser possessing the
// private polling secret. Approving alone does not mark an app connected.
func (s *Store) Poll(ctx context.Context,connectionID,poll string)(AuthorizationCode,error){
    var out AuthorizationCode
    tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return out,err};defer tx.Rollback()
    now:=time.Now().Unix()
    var id,clientID,challenge,decision string
    err=tx.QueryRowContext(ctx,`SELECT r.request_id,r.client_id,r.challenge,r.decision,cl.redirect_uri,r.oauth_state
        FROM mcp_requests r JOIN mcp_clients cl ON cl.client_id=r.client_id JOIN mcp_connections c ON c.connection_id=cl.connection_id
        JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at
        WHERE c.connection_id=? AND r.poll_hash=? AND r.expires_at>? AND c.revoked_at=0`,connectionID,hash(poll),now).Scan(&id,&clientID,&challenge,&decision,&out.RedirectURI,&out.State)
    if errors.Is(err,sql.ErrNoRows){return out,ErrUnauthorized};if err!=nil{return out,err}
    out.RequestID=id
    if decision=="pending"{return out,ErrPending}
    if _,err:=tx.ExecContext(ctx,"DELETE FROM mcp_requests WHERE request_id=?",id);err!=nil{return out,err}
    if decision=="denied"{if err:=tx.Commit();err!=nil{return out,err};return out,ErrDenied}
    out.Code,err=secret();if err!=nil{return out,err}
    if _,err:=tx.ExecContext(ctx,"INSERT INTO mcp_codes(code_hash,client_id,challenge,expires_at) VALUES(?,?,?,?)",hash(out.Code),clientID,challenge,now+int64(codeLifetime/time.Second));err!=nil{return out,err}
    if err:=tx.Commit();err!=nil{return AuthorizationCode{},err};return out,nil
}

func mint(ctx context.Context,tx *sql.Tx,clientID string,now int64)(Tokens,error){
    var tokens Tokens
    var err error
    tokens.AccessToken,err=secret();if err!=nil{return tokens,err}
    tokens.RefreshToken,err=secret();if err!=nil{return tokens,err}
    tokens.ExpiresIn=int64(accessLifetime/time.Second)
    // One active token pair per registered client. Refresh always rotates.
    if _,err:=tx.ExecContext(ctx,"DELETE FROM mcp_tokens WHERE client_id=?",clientID);err!=nil{return Tokens{},err}
    _,err=tx.ExecContext(ctx,`INSERT INTO mcp_tokens(access_hash,refresh_hash,client_id,access_expires_at,refresh_expires_at)
        VALUES(?,?,?,?,?)`,hash(tokens.AccessToken),hash(tokens.RefreshToken),clientID,now+tokens.ExpiresIn,now+int64(refreshLifetime/time.Second))
    if err!=nil{return Tokens{},err}
    return tokens,nil
}

func (s *Store) Exchange(ctx context.Context,connectionID,clientID,resource,redirect,code,verifier string)(Tokens,error){
    if !verifierRE.MatchString(verifier){return Tokens{},ErrUnauthorized}
    tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return Tokens{},err};defer tx.Rollback()
    now:=time.Now().Unix()
    var challenge string
    err=tx.QueryRowContext(ctx,`SELECT k.challenge FROM mcp_codes k JOIN mcp_clients cl ON cl.client_id=k.client_id
        JOIN mcp_connections c ON c.connection_id=cl.connection_id JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at
        WHERE k.code_hash=? AND k.expires_at>? AND cl.client_id=? AND cl.redirect_uri=? AND c.connection_id=? AND c.resource=? AND c.revoked_at=0`,hash(code),now,clientID,redirect,connectionID,resource).Scan(&challenge)
    if errors.Is(err,sql.ErrNoRows){return Tokens{},ErrUnauthorized};if err!=nil{return Tokens{},err}
    if subtle.ConstantTimeCompare([]byte(challenge),[]byte(hash(verifier)))!=1{return Tokens{},ErrUnauthorized}
    if _,err:=tx.ExecContext(ctx,"DELETE FROM mcp_codes WHERE code_hash=?",hash(code));err!=nil{return Tokens{},err}
    tokens,err:=mint(ctx,tx,clientID,now);if err!=nil{return Tokens{},err}
    if _,err:=tx.ExecContext(ctx,"UPDATE mcp_connections SET authorized_at=? WHERE connection_id=?",now,connectionID);err!=nil{return Tokens{},err}
    if err:=tx.Commit();err!=nil{return Tokens{},err}
    if conn,err:=s.ByID(ctx,connectionID);err==nil{s.state.Audit(ctx,"mcp.authorized",fmt.Sprintf("app:%d",conn.AppID),"OAuth authorization completed")}
    return tokens,nil
}

func (s *Store) Refresh(ctx context.Context,connectionID,clientID,resource,refresh string)(Tokens,error){
    tx,err:=s.db.BeginTx(ctx,nil);if err!=nil{return Tokens{},err};defer tx.Rollback()
    now:=time.Now().Unix()
    var found string
    err=tx.QueryRowContext(ctx,`SELECT t.client_id FROM mcp_tokens t JOIN mcp_clients cl ON cl.client_id=t.client_id
        JOIN mcp_connections c ON c.connection_id=cl.connection_id JOIN apps a ON a.id=c.app_id AND a.owner=c.owner AND a.root=c.root AND a.created_at=c.app_created_at
        WHERE t.refresh_hash=? AND t.refresh_expires_at>? AND cl.client_id=? AND c.connection_id=? AND c.resource=? AND c.revoked_at=0 AND c.authorized_at>0`,
        hash(refresh),now,clientID,connectionID,resource).Scan(&found)
    if errors.Is(err,sql.ErrNoRows){return Tokens{},ErrUnauthorized};if err!=nil{return Tokens{},err}
    tokens,err:=mint(ctx,tx,found,now);if err!=nil{return Tokens{},err}
    if err:=tx.Commit();err!=nil{return Tokens{},err};return tokens,nil
}

func (s *Store) Authenticate(ctx context.Context,connectionID,resource,access string)(Connection,error){
    if len(access)!=43{return Connection{},ErrUnauthorized}
    return readConnection(s.db.QueryRowContext(ctx,connectionQuery+`
        JOIN mcp_clients cl ON cl.connection_id=c.connection_id JOIN mcp_tokens t ON t.client_id=cl.client_id
        WHERE c.connection_id=? AND c.resource=? AND c.revoked_at=0 AND c.authorized_at>0 AND t.access_hash=? AND t.access_expires_at>?`,
        connectionID,resource,hash(access),time.Now().Unix()))
}

func (s *Store) RecordSuccess(ctx context.Context,conn Connection)error{
    _,err:=s.db.ExecContext(ctx,"UPDATE mcp_connections SET last_success_at=? WHERE app_id=? AND connection_id=? AND revoked_at=0",time.Now().Unix(),conn.AppID,conn.ID)
    return err
}



func (s *Store) ByID(ctx context.Context,id string)(Connection,error){
    return readConnection(s.db.QueryRowContext(ctx,connectionQuery+" WHERE c.connection_id=? AND c.revoked_at=0",id))
}


func (s *Store) HasRefresh(ctx context.Context,connectionID string)(bool,error){
    var active bool
    err:=s.db.QueryRowContext(ctx,`SELECT EXISTS(SELECT 1 FROM mcp_tokens t JOIN mcp_clients cl ON cl.client_id=t.client_id
        JOIN mcp_connections c ON c.connection_id=cl.connection_id WHERE c.connection_id=? AND c.revoked_at=0 AND t.refresh_expires_at>?)`,
        connectionID,time.Now().Unix()).Scan(&active)
    return active,err
}
