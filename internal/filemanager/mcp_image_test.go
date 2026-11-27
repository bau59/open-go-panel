package filemanager

import (
    "bytes"
    "encoding/base64"
    "image"
    "image/png"
    "os"
    "path/filepath"
    "testing"
)

func TestMCPImageContent(t *testing.T){
    root:=t.TempDir()
    var buf bytes.Buffer
    if err:=png.Encode(&buf,image.NewRGBA(image.Rect(0,0,4,3)));err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"shot.png"),buf.Bytes(),0600);err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"fake.png"),[]byte("<svg onload='alert(1)'/>"),0600);err!=nil{t.Fatal(err)}
    if err:=os.Symlink("shot.png",filepath.Join(root,"link.png"));err!=nil{t.Fatal(err)}
    m,err:=Open(root);if err!=nil{t.Fatal(err)};defer m.Close()
    got,err:=mcpOperation(m,"view_image",MCPArgs{Path:"shot.png"});if err!=nil{t.Fatal(err)}
    value:=got.(map[string]any)
    data,err:=base64.StdEncoding.DecodeString(value["data"].(string))
    if err!=nil||value["mimeType"]!="image/png"||!bytes.Equal(data,buf.Bytes()){t.Fatal("original image bytes or MIME type lost")}
    for _,path:=range []string{"../shot.png","/shot.png","link.png","fake.png"}{if _,err:=mcpOperation(m,"view_image",MCPArgs{Path:path});err==nil{t.Errorf("accepted %s",path)}}
    if err:=os.Link(filepath.Join(root,"shot.png"),filepath.Join(root,"hard.png"));err!=nil{t.Fatal(err)}
    if _,err:=mcpOperation(m,"view_image",MCPArgs{Path:"hard.png"});err==nil{t.Fatal("accepted hardlink")}
}
