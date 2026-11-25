package mcpaccess

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "net/url"
    "path/filepath"
    "regexp"
    "strings"
    "testing"

    "github.com/bau59/open-go-panel/internal/state"
)

func TestOAuthHTTPFlow(t *testing.T){
    ctx:=context.Background()
    db,err:=state.Open(filepath.Join(t.TempDir(),"panel.db"));if err!=nil{t.Fatal(err)};defer db.Close()
    insertApp(t,db,1,"yo4","2026-11-25T01:00:00Z")
    s:=New(db)
    c,err:=s.Enable(ctx,1,"https://mcp.example.test");if err!=nil{t.Fatal(err)}
    if err:=db.SetSetting(DomainSetting,"mcp.example.test");err!=nil{t.Fatal(err)}
    handler:=NewHandler(db,nil)
    request:=func(method,path,contentType,body string)*httptest.ResponseRecorder{
        req:=httptest.NewRequest(method,"https://mcp.example.test"+path,strings.NewReader(body))
        if method=="POST"&&strings.HasSuffix(path,"/continue"){
            req.Header.Set("Origin","null")
            req.Header.Set("Sec-Fetch-Site","same-origin")
            req.Header.Set("Sec-Fetch-Mode","navigate")
        }
        if contentType!=""{req.Header.Set("Content-Type",contentType)}
        rec:=httptest.NewRecorder()
        handler.ServeHTTP(rec,req)
        return rec
    }
    read:=func(r *httptest.ResponseRecorder,code int)map[string]any{
        t.Helper()
        if r.Code!=code{t.Fatalf("HTTP %d, expected %d: %s",r.Code,code,r.Body.String())}
        var result map[string]any
        if err:=json.Unmarshal(r.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
        return result
    }
    base:="/oauth/"+c.ID
    prm:=read(request("GET","/.well-known/oauth-protected-resource/mcp/"+c.ID,"",""),200)
    if prm["resource"]!=c.Resource{t.Fatal("resource metadata mismatch")}
    as:=read(request("GET","/.well-known/oauth-authorization-server/oauth/"+c.ID,"",""),200)
    if as["issuer"]!=issuer(c)||as["authorization_response_iss_parameter_supported"]!=true{t.Fatal("issuer identification missing")}
    registration:=read(request("POST",base+"/register","application/json",`{"redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`),201)
    client:=registration["client_id"].(string)
    verifier:=strings.Repeat("v",43)
    q:=url.Values{"response_type":{"code"},"client_id":{client},"redirect_uri":{RedirectURI},"resource":{c.Resource},"code_challenge_method":{"S256"},"code_challenge":{hash(verifier)},"state":{"test-state"},"scope":{Scope}}
    auth:=request("GET",base+"/authorize?"+q.Encode(),"","")
    if auth.Header().Get("Referrer-Policy")!="strict-origin"{t.Fatal("approval form must preserve browser Origin without leaking query")}
    if auth.Code!=200{t.Fatalf("authorization page: %d %s",auth.Code,auth.Body.String())}
    match:=regexp.MustCompile(`name="poll" value="([A-Za-z0-9_-]+)"`).FindStringSubmatch(auth.Body.String())
    if len(match)!=2{t.Fatal("missing poll secret")}
    pending,err:=s.Pending(ctx,1);if err!=nil||len(pending)!=1{t.Fatalf("pending request: %+v %v",pending,err)}
    if !strings.Contains(auth.Body.String(),pending[0].ID){t.Fatal("request ID not displayed")}
    poll:=url.Values{"poll":{match[1]}}.Encode()
    if r:=request("POST",base+"/continue","application/x-www-form-urlencoded",poll);r.Code!=200{t.Fatal("unapproved browser received code")}
    if err:=s.Decide(ctx,1,pending[0].ID,true);err!=nil{t.Fatal(err)}
    callback:=request("POST",base+"/continue","application/x-www-form-urlencoded",poll)
    if callback.Code!=303{t.Fatalf("callback status: %d",callback.Code)}
    redirect,err:=url.Parse(callback.Header().Get("Location"));if err!=nil{t.Fatal(err)}
    if redirect.Scheme!="https"||redirect.Host!="chatgpt.com"||redirect.Query().Get("state")!="test-state"||redirect.Query().Get("iss")!=issuer(c){t.Fatal("incorrect callback")}
    tokenForm:=url.Values{"grant_type":{"authorization_code"},"client_id":{client},"redirect_uri":{RedirectURI},"resource":{c.Resource},"code":{redirect.Query().Get("code")},"code_verifier":{verifier}}
    token:=read(request("POST",base+"/token","application/x-www-form-urlencoded",tokenForm.Encode()),200)
    if token["token_type"]!="Bearer"||token["scope"]!=Scope{t.Fatal("invalid token response")}
    if _,err:=s.Authenticate(ctx,c.ID,c.Resource,token["access_token"].(string));err!=nil{t.Fatal(err)}
    if r:=request(http.MethodPost,"/admin/approve","application/x-www-form-urlencoded","");r.Code!=404{t.Fatal("admin route exposed publicly")}
    if err:=s.Revoke(ctx,1);err!=nil{t.Fatal(err)}
    if r:=request("GET","/.well-known/oauth-protected-resource/mcp/"+c.ID,"","");r.Code!=404{t.Fatal("revoked resource still exposed")}
}

func TestRequestOriginIsolation(t *testing.T){
    id:=strings.Repeat("a",43)
    for _,tc:=range []struct{name,method,path,origin,site,mode string;allow bool}{
        {"server client","POST","/mcp/"+id,"","","",true},
        {"ChatGPT","POST","/mcp/"+id,"https://chatgpt.com","","",true},
        {"own origin","POST","/oauth/"+id+"/continue","https://mcp.example.test","same-origin","navigate",true},
        {"old approval page","POST","/oauth/"+id+"/continue","null","same-origin","navigate",true},
        {"opaque MCP","POST","/mcp/"+id,"null","same-origin","navigate",false},
        {"opaque token","POST","/oauth/"+id+"/token","null","same-origin","navigate",false},
        {"opaque cross site","POST","/oauth/"+id+"/continue","null","cross-site","navigate",false},
        {"opaque fetch","POST","/oauth/"+id+"/continue","null","same-origin","cors",false},
        {"opaque missing metadata","POST","/oauth/"+id+"/continue","null","","",false},
        {"foreign browser","POST","/oauth/"+id+"/continue","https://evil.test","same-origin","navigate",false},
        {"lookalike","POST","/mcp/"+id,"https://chatgpt.com.evil.test","","",false},
    }{
        t.Run(tc.name,func(t *testing.T){
            req:=httptest.NewRequest(tc.method,"https://mcp.example.test"+tc.path,nil)
            if tc.origin!=""{req.Header.Set("Origin",tc.origin)}
            if tc.site!=""{req.Header.Set("Sec-Fetch-Site",tc.site)}
            if tc.mode!=""{req.Header.Set("Sec-Fetch-Mode",tc.mode)}
            if got:=validRequestOrigin(req,"mcp.example.test");got!=tc.allow{t.Fatalf("allow=%t, want %t",got,tc.allow)}
        })
    }
}
