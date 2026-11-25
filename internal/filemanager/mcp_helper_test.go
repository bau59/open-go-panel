package filemanager

import (
    "errors"
    "os"
    "path/filepath"
    "testing"
)

func TestMCPVersionedEditAndDelete(t *testing.T){
    root:=t.TempDir()
    if err:=os.WriteFile(filepath.Join(root,"demo.txt"),[]byte("first"),0600);err!=nil{t.Fatal(err)}
    m,err:=Open(root);if err!=nil{t.Fatal(err)};defer m.Close()
    if err:=m.SaveVersioned("","demo.txt","second",textVersion("first"));err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"demo.txt"),[]byte("manual edit"),0600);err!=nil{t.Fatal(err)}
    if err:=m.SaveVersioned("","demo.txt","lost",textVersion("second"));!errors.Is(err,ErrEditConflict){t.Fatalf("expected conflict: %v",err)}
    content,err:=m.ReadText("","demo.txt");if err!=nil||content!="manual edit"{t.Fatal("manual edit was lost")}
    if err:=m.Mkdir("","dir");err!=nil{t.Fatal(err)}
    if err:=m.Save("dir","child","data",true);err!=nil{t.Fatal(err)}
    if err:=m.DeletePath("","dir",false);err==nil{t.Fatal("nonrecursive deletion removed nonempty directory")}
    if err:=m.DeletePath("","dir",true);err!=nil{t.Fatal(err)}
    if _,err:=mcpOperation(m,"read_file",MCPArgs{Path:"../other.txt"});err==nil{t.Fatal("traversal accepted")}
}
