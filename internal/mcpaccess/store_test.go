package mcpaccess

import (
    "context"
    "errors"
    "path/filepath"
    "strings"
    "testing"

    "github.com/bau59/open-go-panel/internal/state"
)

func insertApp(t *testing.T,s *state.Store,id int64,name,created string){
    t.Helper()
    _,err:=s.DB().Exec("INSERT INTO apps(id,owner,name,type,root,created_at) VALUES(?,? ,?,'go',?,?)",id,"sameuser",name,"/home/sameuser/apps/"+name,created)
    if err!=nil{t.Fatal(err)}
}

func grant(t *testing.T,s *Store,appID int64)(Connection,string,Tokens){
    t.Helper()
    ctx:=context.Background()
    c,err:=s.Enable(ctx,appID,"https://mcp.example.test");if err!=nil{t.Fatal(err)}
    client,err:=s.Register(ctx,c.ID,RedirectURI);if err!=nil{t.Fatal(err)}
    verifier:=strings.Repeat("a",43)
    req,err:=s.Begin(ctx,c.ID,client,c.Resource,RedirectURI,hash(verifier),"state");if err!=nil{t.Fatal(err)}
    if _,err:=s.Poll(ctx,c.ID,req.PollSecret);!errors.Is(err,ErrPending){t.Fatalf("poll before approval: %v",err)}
    if err:=s.Decide(ctx,appID,req.ID,true);err!=nil{t.Fatal(err)}
    current,err:=s.Connection(ctx,appID);if err!=nil||current.AuthorizedAt!=0{t.Fatalf("approval incorrectly marks connected: %+v %v",current,err)}
    code,err:=s.Poll(ctx,c.ID,req.PollSecret);if err!=nil{t.Fatal(err)}
    if code.State!="state"||code.RedirectURI!=RedirectURI{t.Fatal("callback mismatch")}
    if _,err:=s.Poll(ctx,c.ID,req.PollSecret);!errors.Is(err,ErrUnauthorized){t.Fatal("poll replay accepted")}
    if _,err:=s.Exchange(ctx,c.ID,client,c.Resource,RedirectURI,code.Code,strings.Repeat("b",43));!errors.Is(err,ErrUnauthorized){t.Fatal("wrong PKCE accepted")}
    tokens,err:=s.Exchange(ctx,c.ID,client,c.Resource,RedirectURI,code.Code,verifier);if err!=nil{t.Fatal(err)}
    if _,err:=s.Exchange(ctx,c.ID,client,c.Resource,RedirectURI,code.Code,verifier);!errors.Is(err,ErrUnauthorized){t.Fatal("authorization code replay accepted")}
    current,err=s.Connection(ctx,appID);if err!=nil||current.AuthorizedAt==0{t.Fatal("token exchange did not mark connected")}
    return c,client,tokens
}

func TestAppScopedGrantLifecycle(t *testing.T){
    ctx:=context.Background()
    dbPath:=filepath.Join(t.TempDir(),"panel.db")
    db,err:=state.Open(dbPath);if err!=nil{t.Fatal(err)}
    defer func(){if db!=nil{db.Close()}}()
    insertApp(t,db,1,"yo4","2026-11-25T01:00:00Z")
    insertApp(t,db,2,"svg4","2026-11-25T01:00:01Z")
    s:=New(db)
    a,clientA,tokA:=grant(t,s,1)
    b,clientB,tokB:=grant(t,s,2)
    if _,err:=s.Authenticate(ctx,b.ID,b.Resource,tokA.AccessToken);!errors.Is(err,ErrUnauthorized){t.Fatal("cross-app token accepted")}
    if _,err:=s.Refresh(ctx,b.ID,clientB,b.Resource,tokA.RefreshToken);!errors.Is(err,ErrUnauthorized){t.Fatal("cross-app refresh accepted")}
    if _,err:=s.Begin(ctx,b.ID,clientA,b.Resource,RedirectURI,hash(strings.Repeat("a",43)),"state");!errors.Is(err,ErrUnauthorized){t.Fatal("cross-app client accepted")}
    if _,err:=s.Register(ctx,a.ID,"https://chatgpt.com.evil.test/callback");err==nil{t.Fatal("untrusted callback accepted")}
    current,err:=s.Authenticate(ctx,a.ID,a.Resource,tokA.AccessToken);if err!=nil||current.AppID!=1||current.Owner!="sameuser"||current.Root!="/home/sameuser/apps/yo4"{t.Fatalf("incorrect principal: %+v %v",current,err)}
    if err:=db.Close();err!=nil{t.Fatal(err)}
    db=nil
    db,err=state.Open(dbPath);if err!=nil{t.Fatal(err)}
    s=New(db)
    if _,err:=s.Authenticate(ctx,a.ID,a.Resource,tokA.AccessToken);err!=nil{t.Fatalf("grant lost after reopening DB: %v",err)}
    rotated,err:=s.Refresh(ctx,a.ID,clientA,a.Resource,tokA.RefreshToken);if err!=nil{t.Fatal(err)}
    if _,err:=s.Refresh(ctx,a.ID,clientA,a.Resource,tokA.RefreshToken);!errors.Is(err,ErrUnauthorized){t.Fatal("old refresh token accepted")}
    if _,err:=s.Authenticate(ctx,a.ID,a.Resource,tokA.AccessToken);!errors.Is(err,ErrUnauthorized){t.Fatal("old access token accepted after rotation")}
    if err:=s.Revoke(ctx,1);err!=nil{t.Fatal(err)}
    if _,err:=s.Authenticate(ctx,a.ID,a.Resource,rotated.AccessToken);!errors.Is(err,ErrUnauthorized){t.Fatal("revoked access token accepted")}
    if _,err:=s.Refresh(ctx,a.ID,clientA,a.Resource,rotated.RefreshToken);!errors.Is(err,ErrUnauthorized){t.Fatal("revoked refresh token accepted")}
    if _,err:=s.Authenticate(ctx,b.ID,b.Resource,tokB.AccessToken);err!=nil{t.Fatal("unrelated app was revoked")}
    newA,err:=s.Enable(ctx,1,"https://mcp.example.test");if err!=nil||newA.ID==a.ID{t.Fatal("connection ID reused")}
    if _,err:=db.DB().Exec("DELETE FROM apps WHERE id=2");err!=nil{t.Fatal(err)}
    insertApp(t,db,2,"svg4","2026-11-25T02:00:00Z")
    if _,err:=s.Authenticate(ctx,b.ID,b.Resource,tokB.AccessToken);!errors.Is(err,ErrUnauthorized){t.Fatal("recreated app inherited old grant")}
    if _,err:=s.Connection(ctx,2);!errors.Is(err,ErrUnauthorized){t.Fatal("deleted app retained connection")}
}
