package server

import (
    "encoding/base64"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "testing"
)

func TestEditorEncodingPreservesSource(t *testing.T) {
    source := "\n<html>\r\nПривет & </textarea>\r\n</html>\n"
    for _,encoding := range []string{"","base64"} {
        v:=url.Values{"content":{source},"content_encoding":{encoding}}
        if encoding=="base64"{v.Set("content",base64.StdEncoding.EncodeToString([]byte(source)))}
        req:=httptest.NewRequest("POST","/apps/1/files",strings.NewReader(v.Encode()))
        req.Header.Set("Content-Type","application/x-www-form-urlencoded")
        got,err:=fileEditorText(req)
        if err!=nil||got!=source{t.Fatalf("encoding=%q got=%q err=%v",encoding,got,err)}
    }
}

func TestEditorRejectsMalformedEncoding(t *testing.T) {
    for _,v:=range []url.Values{{"content":{"!!!"},"content_encoding":{"base64"}},{"content":{"hello"},"content_encoding":{"unknown"}}} {
        req:=httptest.NewRequest("POST","/apps/1/files",strings.NewReader(v.Encode()))
        req.Header.Set("Content-Type","application/x-www-form-urlencoded")
        if _,err:=fileEditorText(req);err==nil{t.Fatal("accepted invalid encoding")}
    }
}

func TestEditorLineEndings(t *testing.T){
    for source,want:=range map[string]string{"":"lf","a\nb\n":"lf","a\r\nb\r\n":"crlf","a\rb":"cr","a\r\nb\n":"mixed"}{
        if got:=fileEditorEOL(source);got!=want{t.Errorf("%q: %s != %s",source,got,want)}
    }
}

func TestEmbeddedEditorAssets(t *testing.T){
    mux:=http.NewServeMux()
    registerUIAssets(mux)
    for _,path:=range []string{"/assets/editor.js","/assets/editor.css"}{
        w:=httptest.NewRecorder()
        mux.ServeHTTP(w,httptest.NewRequest("GET",path,nil))
        if w.Code!=200||!strings.Contains(w.Body.String(),"CodeMirror"){t.Errorf("missing %s",path)}
    }
}
