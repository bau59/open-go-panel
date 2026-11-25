package mcpaccess

import (
    "encoding/json"
    "errors"
    "html/template"
    "io"
    "net/http"
    "net/url"
    "strings"
)

const Scope = "project"

type OAuth struct { store *Store }

// NewOAuth exposes only OAuth and discovery routes. Administrative approval
// must be implemented on the existing private authenticated panel.
func NewOAuth(store *Store) http.Handler {
    o:=&OAuth{store:store}
    mux:=http.NewServeMux()
    mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp/{id}",o.resourceMetadata)
    mux.HandleFunc("GET /.well-known/oauth-authorization-server/oauth/{id}",o.serverMetadata)
    mux.HandleFunc("POST /oauth/{id}/register",o.register)
    mux.HandleFunc("GET /oauth/{id}/authorize",o.authorize)
    mux.HandleFunc("POST /oauth/{id}/continue",o.continueAuthorization)
    mux.HandleFunc("POST /oauth/{id}/token",o.token)
    return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        w.Header().Set("Cache-Control","no-store")
        w.Header().Set("X-Content-Type-Options","nosniff")
        w.Header().Set("Referrer-Policy","no-referrer")
        w.Header().Set("X-Frame-Options","DENY")
        w.Header().Set("Content-Security-Policy","default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
        r.Body=http.MaxBytesReader(w,r.Body,32<<10)
        mux.ServeHTTP(w,r)
    })
}

func jsonResponse(w http.ResponseWriter,status int,value any){
    w.Header().Set("Content-Type","application/json")
    w.WriteHeader(status)
    _=json.NewEncoder(w).Encode(value)
}

func oauthError(w http.ResponseWriter,status int,code string){
    jsonResponse(w,status,map[string]string{"error":code})
}

func issuer(c Connection)string{
    return strings.TrimSuffix(c.Resource,"/mcp/"+c.ID)+"/oauth/"+c.ID
}

func (o *OAuth) connection(w http.ResponseWriter,r *http.Request)(Connection,bool){
    c,err:=o.store.ByID(r.Context(),r.PathValue("id"))
    if err!=nil{
        if errors.Is(err,ErrUnauthorized){http.NotFound(w,r)}else{oauthError(w,500,"server_error")}
        return Connection{},false
    }
    return c,true
}

func (o *OAuth) resourceMetadata(w http.ResponseWriter,r *http.Request){
    c,ok:=o.connection(w,r);if !ok{return}
    jsonResponse(w,200,map[string]any{
        "resource":c.Resource,"authorization_servers":[]string{issuer(c)},
        "scopes_supported":[]string{Scope},"bearer_methods_supported":[]string{"header"},
    })
}

func (o *OAuth) serverMetadata(w http.ResponseWriter,r *http.Request){
    c,ok:=o.connection(w,r);if !ok{return}
    base:=issuer(c)
    jsonResponse(w,200,map[string]any{
        "issuer":base,"authorization_endpoint":base+"/authorize","token_endpoint":base+"/token",
        "registration_endpoint":base+"/register","response_types_supported":[]string{"code"},
        "grant_types_supported":[]string{"authorization_code","refresh_token"},
        "token_endpoint_auth_methods_supported":[]string{"none"},
        "code_challenge_methods_supported":[]string{"S256"},
        "scopes_supported":[]string{Scope},"authorization_response_iss_parameter_supported":true,
    })
}

func (o *OAuth) register(w http.ResponseWriter,r *http.Request){
    c,ok:=o.connection(w,r);if !ok{return}
    var req struct{
        RedirectURIs []string `json:"redirect_uris"`
        TokenMethod string `json:"token_endpoint_auth_method"`
        GrantTypes []string `json:"grant_types"`
        ResponseTypes []string `json:"response_types"`
        ClientName string `json:"client_name"`
    }
    decoder:=json.NewDecoder(r.Body)
    if err:=decoder.Decode(&req);err!=nil{oauthError(w,400,"invalid_client_metadata");return}
    if err:=decoder.Decode(new(any));!errors.Is(err,io.EOF){oauthError(w,400,"invalid_client_metadata");return}
    if len(req.RedirectURIs)!=1||req.RedirectURIs[0]!=RedirectURI{oauthError(w,400,"invalid_redirect_uri");return}
    if req.TokenMethod!=""&&req.TokenMethod!="none"{oauthError(w,400,"invalid_client_metadata");return}
    for _,v:=range req.GrantTypes{if v!="authorization_code"&&v!="refresh_token"{oauthError(w,400,"invalid_client_metadata");return}}
    for _,v:=range req.ResponseTypes{if v!="code"{oauthError(w,400,"invalid_client_metadata");return}}
    id,err:=o.store.Register(r.Context(),c.ID,req.RedirectURIs[0])
    if err!=nil{if errors.Is(err,ErrUnauthorized){oauthError(w,403,"invalid_client")}else{oauthError(w,500,"server_error")};return}
    jsonResponse(w,201,map[string]any{"client_id":id,"redirect_uris":req.RedirectURIs,
        "token_endpoint_auth_method":"none","grant_types":[]string{"authorization_code","refresh_token"},
        "response_types":[]string{"code"},"scope":Scope})
}

var approvalPage=template.Must(template.New("approval").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Open Go Panel authorization</title><style>body{font:16px system-ui;max-width:620px;margin:5vh auto;padding:20px;color:#172033}code{overflow-wrap:anywhere}button{padding:12px 20px}</style><h1>Authorize ChatGPT</h1><p>Open the private Open Go Panel session and approve the matching request for this application. Do not approve an unrelated request.</p><p>Linux user: <strong>{{.Owner}}</strong></p><p>Application root: <code>{{.Root}}</code></p>{{if .RequestID}}<p>Request ID: <code>{{.RequestID}}</code></p>{{end}}<p>Requested access: project files and isolated command execution.</p><form method="post" action="{{.Action}}"><input type="hidden" name="poll" value="{{.Poll}}"><button type="submit">Continue after approval</button></form></html>`))

func renderApproval(w http.ResponseWriter,c Connection,poll,requestID string){
    w.Header().Set("Content-Type","text/html; charset=utf-8")
    _=approvalPage.Execute(w,struct{Owner,Root,Poll,RequestID,Action string}{c.Owner,c.Root,poll,requestID,"/oauth/"+c.ID+"/continue"})
}

func (o *OAuth) authorize(w http.ResponseWriter,r *http.Request){
    c,ok:=o.connection(w,r);if !ok{return}
    q:=r.URL.Query()
    if q.Get("response_type")!="code"||q.Get("code_challenge_method")!="S256"||
        (q.Get("scope")!=""&&q.Get("scope")!=Scope)||q.Get("state")==""{
        oauthError(w,400,"invalid_request");return
    }
    req,err:=o.store.Begin(r.Context(),c.ID,q.Get("client_id"),q.Get("resource"),q.Get("redirect_uri"),q.Get("code_challenge"),q.Get("state"))
    if err!=nil{if errors.Is(err,ErrUnauthorized){oauthError(w,400,"invalid_request")}else{oauthError(w,500,"server_error")};return}
    renderApproval(w,c,req.PollSecret,req.ID)
}

func (o *OAuth) continueAuthorization(w http.ResponseWriter,r *http.Request){
    c,ok:=o.connection(w,r);if !ok{return}
    if err:=r.ParseForm();err!=nil{oauthError(w,400,"invalid_request");return}
    poll:=r.PostForm.Get("poll")
    if len(poll)!=43{oauthError(w,400,"invalid_request");return}
    code,err:=o.store.Poll(r.Context(),c.ID,poll)
    if errors.Is(err,ErrPending){renderApproval(w,c,poll,"");return}
    if err!=nil&&!errors.Is(err,ErrDenied){
        if errors.Is(err,ErrUnauthorized){oauthError(w,400,"invalid_request")}else{oauthError(w,500,"server_error")};return
    }
    // The redirect came from the validated registration, never the request.
    if code.RedirectURI!=RedirectURI{oauthError(w,400,"invalid_request");return}
    u,_:=url.Parse(code.RedirectURI)
    q:=u.Query();q.Set("state",code.State);q.Set("iss",issuer(c))
    if errors.Is(err,ErrDenied){q.Set("error","access_denied")}else{q.Set("code",code.Code)}
    u.RawQuery=q.Encode()
    http.Redirect(w,r,u.String(),http.StatusSeeOther)
}

func (o *OAuth) token(w http.ResponseWriter,r *http.Request){
    c,ok:=o.connection(w,r);if !ok{return}
    if err:=r.ParseForm();err!=nil{oauthError(w,400,"invalid_request");return}
    f:=r.PostForm
    var tokens Tokens
    var err error
    resource:=f.Get("resource")
    if f.Get("scope")!=""&&f.Get("scope")!=Scope{oauthError(w,400,"invalid_scope");return}
    switch f.Get("grant_type"){
    case "authorization_code":
        tokens,err=o.store.Exchange(r.Context(),c.ID,f.Get("client_id"),resource,f.Get("redirect_uri"),f.Get("code"),f.Get("code_verifier"))
    case "refresh_token":
        // The original resource is immutable. Omitted resource on refresh
        // means the same resource, not a new audience.
        if resource==""{resource=c.Resource}
        tokens,err=o.store.Refresh(r.Context(),c.ID,f.Get("client_id"),resource,f.Get("refresh_token"))
    default:oauthError(w,400,"unsupported_grant_type");return
    }
    if err!=nil{
        if errors.Is(err,ErrUnauthorized){oauthError(w,400,"invalid_grant")}else{oauthError(w,500,"server_error")};return
    }
    jsonResponse(w,200,map[string]any{"access_token":tokens.AccessToken,"refresh_token":tokens.RefreshToken,
        "expires_in":tokens.ExpiresIn,"token_type":"Bearer","scope":Scope})
}
