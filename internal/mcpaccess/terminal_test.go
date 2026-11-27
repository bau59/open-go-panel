package mcpaccess

import (
    "context"
    "errors"
    "path/filepath"
    "strings"
    "testing"

    "github.com/bau59/open-go-panel/internal/state"
)

func TestTerminalPolicyInvalidatesTokens(t *testing.T){
    ctx:=context.Background()
    db,err:=state.Open(filepath.Join(t.TempDir(),"panel.db"));if err!=nil{t.Fatal(err)};defer db.Close()
    insertApp(t,db,1,"demo","2026-11-25T01:00:00Z")
    c,client,tokens:=grant(t,New(db),1)
    if c.TerminalMode()!="user"{t.Fatal("existing/default grants must be user")}
    s:=New(db)
    if _,err:=s.SetTerminalMode(ctx,1,"root");err!=nil{t.Fatal(err)}
    next,err:=s.Connection(ctx,1);if err!=nil{t.Fatal(err)}
    if next.ID!=c.ID||next.TerminalMode()!="root"||next.AuthorizedAt!=0||next.TerminalPolicy==c.TerminalPolicy{t.Fatalf("incorrect mode change: %+v",next)}
    if _,err=s.Authenticate(ctx,c.ID,c.Resource,tokens.AccessToken);!errors.Is(err,ErrUnauthorized){t.Fatal("old token accepted")}
    if _,err=s.Refresh(ctx,c.ID,client,c.Resource,tokens.RefreshToken);!errors.Is(err,ErrUnauthorized){t.Fatal("old refresh token accepted")}
    if _,err=s.SetTerminalMode(ctx,1,"invalid");err==nil{t.Fatal("invalid mode accepted")}
}
func TestTerminalCommandArguments(t *testing.T){
    c:=Connection{Owner:"demo",Root:"/home/demo/apps/app",TerminalPolicy:"user"}
    args,err:=terminalArgs(c,strings.Repeat("a",43),"echo $HOME; sleep 600","../other","/dev/pts/4")
    if err!=nil{t.Fatal(err)}
    joined:=strings.Join(args,"\n")
    for _,want:=range []string{"--property=User=demo","--property=WorkingDirectory=/home/demo/apps/other","--expand-environment=no","--property=KillMode=control-group","--property=StandardInput=tty-force","--property=TTYPath=/dev/pts/4","echo $HOME; sleep 600"}{
        if !strings.Contains(joined,want){t.Fatalf("missing argument %s",want)}
    }
    c.TerminalPolicy="root:random"
    args,err=terminalArgs(c,strings.Repeat("a",43),"","","/dev/pts/4");if err!=nil{t.Fatal(err)}
    if !strings.Contains(strings.Join(args,"\n"),"--property=User=root"){t.Fatal("root not selected")}
}
func TestTerminalOutputOffsets(t *testing.T){
    s:=&terminalSession{}
    s.append([]byte("abc"))
    s.append([]byte("def"))
    got:=s.read(2)
    if got["stdout"]!="cdef"||got["next_offset"]!=int64(6){t.Fatalf("unexpected result %+v",got)}
    s.append([]byte(strings.Repeat("x",terminalHistory)))
    got=s.read(0)
    if got["truncated"]!=true||got["oldest_offset"]!=int64(6){t.Fatalf("lost offset: %+v",got)}
}


func TestTerminalOwnershipIsClientAndPolicyScoped(t *testing.T){
    c:=Connection{AppID:1,ID:"grant",ClientID:"client-a",TerminalPolicy:"user"}
    session:=&terminalSession{conn:c,info:TerminalInfo{ID:"session",AppID:1}}
    manager:=&TerminalManager{sessions:map[string]*terminalSession{"session":session}}
    if _,err:=manager.get(c,"session");err!=nil{t.Fatal(err)}
    for _,other:=range []Connection{
        {AppID:1,ID:"grant",ClientID:"client-b",TerminalPolicy:"user"},
        {AppID:1,ID:"grant",ClientID:"client-a",TerminalPolicy:"root:new"},
        {AppID:2,ID:"other",ClientID:"client-a",TerminalPolicy:"user"},
    }{
        if _,err:=manager.get(other,"session");err==nil{t.Fatal("unrelated client or policy accessed terminal")}
    }
    if got:=manager.list(1,"grant","client-b","user");len(got)!=0{t.Fatal("unrelated client listed session")}
}

func TestTerminalBufferWrap(t *testing.T){
    s:=&terminalSession{}
    s.append([]byte(strings.Repeat("a",terminalHistory-2)))
    s.append([]byte("bcde"))
    if got:=s.read(terminalHistory-4);got["stdout"]!="aabcde"||got["next_offset"]!=int64(terminalHistory+2){t.Fatalf("incorrect wrap: %+v",got)}
    if got:=s.read(0);got["oldest_offset"]!=int64(2)||got["truncated"]!=true{t.Fatal("missing truncation marker")}
}
