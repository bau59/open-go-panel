package filemanager

import (
    "bytes"
    "encoding/base64"
    "image"
    "image/color"
    "reflect"
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


const testWebP = "UklGRiAAAABXRUJQVlA4TBQAAAAvA0AAEA8QEfMfQiGAAChoIvofDw=="

func decodeMCPPNG(t *testing.T, value any) image.Image{
    t.Helper()
    obj:=value.(map[string]any)
    if obj["mimeType"]!="image/png"{t.Fatalf("expected PNG: %v",obj["mimeType"])}
    data,err:=base64.StdEncoding.DecodeString(obj["data"].(string));if err!=nil{t.Fatal(err)}
    if len(data)>maxImageBytes{t.Fatal("oversized encoded PNG")}
    img,err:=png.Decode(bytes.NewReader(data));if err!=nil{t.Fatal(err)}
    return img
}

func TestMCPWebPAndContactSheet(t *testing.T){
    root:=t.TempDir()
    data,err:=base64.StdEncoding.DecodeString(testWebP);if err!=nil{t.Fatal(err)}
    // Extension deliberately differs from the actual format.
    for _,name:=range []string{"icon.png","second.webp"}{
        if err:=os.WriteFile(filepath.Join(root,name),data,0600);err!=nil{t.Fatal(err)}
    }
    if err:=os.WriteFile(filepath.Join(root,"broken.webp"),data[:12],0600);err!=nil{t.Fatal(err)}
    if err:=os.Symlink("second.webp",filepath.Join(root,"linked.webp"));err!=nil{t.Fatal(err)}
    if err:=os.Link(filepath.Join(root,"second.webp"),filepath.Join(root,"hard.webp"));err!=nil{t.Fatal(err)}
    m,err:=Open(root);if err!=nil{t.Fatal(err)};defer m.Close()
    before,err:=os.ReadDir(root);if err!=nil{t.Fatal(err)}
    get:=func(args MCPArgs)image.Image{
        value,err:=mcpOperation(m,"view_image",args);if err!=nil{t.Fatal(err)}
        return decodeMCPPNG(t,value)
    }
    img:=get(MCPArgs{Path:"icon.png"})
    if img.Bounds().Size()!=image.Pt(4,2){t.Fatal("wrong WebP dimensions")}
    if _,_,_,a:=img.At(3,0).RGBA();a!=0{t.Fatal("WebP transparency lost")}
    if got:=color.NRGBAModel.Convert(img.At(0,0));got!=(color.NRGBA{A:255}){t.Fatalf("black icon lost: %v",got)}
    img=get(MCPArgs{Path:"icon.png",Background:"#ffffff"})
    if got:=color.NRGBAModel.Convert(img.At(3,0));got!=(color.NRGBA{R:255,G:255,B:255,A:255}){t.Fatalf("white background missing: %v",got)}
    img=get(MCPArgs{Path:"icon.png",MaxWidth:2,MaxHeight:2})
    if img.Bounds().Size()!=image.Pt(2,1){t.Fatal("resize aspect ratio changed")}
    img=get(MCPArgs{Path:"icon.png",MaxWidth:300,MaxHeight:300})
    if img.Bounds().Size()!=image.Pt(4,2){t.Fatal("image was upscaled")}
    for _,args:=range []MCPArgs{{Path:"broken.webp"},{Path:"linked.webp"},{Path:"hard.webp"},{Path:"../icon.png"},{Path:"icon.png",Background:"white"},{Path:"icon.png",MaxWidth:-1}}{
        if _,err:=mcpOperation(m,"view_image",args);err==nil{t.Fatalf("accepted invalid request %+v",args)}
    }
    args:=MCPArgs{Paths:[]string{"icon.png","missing.webp","../outside.webp","broken.webp"},MaxWidth:300,MaxHeight:300,Background:"#ffffff",Columns:2}
    value,err:=mcpOperation(m,"view_images",args);if err!=nil{t.Fatal(err)}
    img=decodeMCPPNG(t,value)
    if img.Bounds().Size()!=image.Pt(632,724){t.Fatalf("wrong contact sheet size: %v",img.Bounds())}
    if got:=color.NRGBAModel.Convert(img.At(156,157));got!=(color.NRGBA{A:255}){t.Fatalf("first image not in first cell: %v",got)}
    if got:=color.NRGBAModel.Convert(img.At(330,20));got!=(color.NRGBA{R:255,G:235,B:235,A:255}){t.Fatalf("missing file not labeled in its cell: %v",got)}
    if _,err:=mcpOperation(m,"view_images",MCPArgs{Paths:make([]string,13)});err==nil{t.Fatal("accepted 13 images")}
    after,err:=os.ReadDir(root);if err!=nil{t.Fatal(err)}
    names:=func(entries []os.DirEntry)[]string{var out []string;for _,e:=range entries{out=append(out,e.Name())};return out}
    if !reflect.DeepEqual(names(before),names(after)){t.Fatal("image operations created files")}
    original,err:=os.ReadFile(filepath.Join(root,"icon.png"));if err!=nil||!bytes.Equal(original,data){t.Fatal("source file modified")}
}
