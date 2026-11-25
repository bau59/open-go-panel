package yandexdisk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDisk struct {
	files map[string][]byte
	dirs map[string]bool
	fail bool
}

func (d *fakeDisk) RoundTrip(r *http.Request) (*http.Response,error) {
	status:=200
	var body []byte
	remote:=r.URL.Query().Get("path")
	switch {
	case r.URL.Host=="cloud-api.yandex.net":
		if r.Header.Get("Authorization")!="OAuth test-token" {return nil,fmt.Errorf("missing authorization")}
		switch r.URL.Path {
		case "/v1/disk/resources":
			if r.Method=="PUT" { d.dirs[remote]=true;status=201
			} else if d.dirs[remote] {body=[]byte(`{"type":"dir"}`)
			} else if data,ok:=d.files[remote];ok{
				h:=sha256.Sum256(data)
				body,_=json.Marshal(map[string]any{"type":"file","size":len(data),"sha256":hex.EncodeToString(h[:])})
			} else {status=404}
		case "/v1/disk/resources/upload","/v1/disk/resources/download":
			body,_=json.Marshal(map[string]string{"href":"https://transfer.example/file?"+r.URL.Query().Encode()})
		default: status=404
		}
	case r.URL.Host=="transfer.example":
		if r.Header.Get("Authorization")!="" {return nil,fmt.Errorf("token sent to transfer host")}
		if r.Method=="PUT" {
			defer r.Body.Close()
			if d.fail && strings.Contains(remote,"archive.part"){status=503;break}
			data,err:=io.ReadAll(r.Body)
			if err!=nil{return nil,err}
			if int64(len(data))!=r.ContentLength{return nil,fmt.Errorf("incorrect Content-Length")}
			d.files[remote]=data;status=201
		} else if data,ok:=d.files[remote];ok{body=data}else{status=404}
	default:return nil,fmt.Errorf("unexpected host")
	}
	return &http.Response{StatusCode:status,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(string(body)))},nil
}

func TestArchiveRoundTrip(t *testing.T){
	ctx:=context.Background()
	d:=&fakeDisk{files:map[string][]byte{},dirs:map[string]bool{}}
	c:=Client{Token:"test-token",HTTP:&http.Client{Transport:d}}
	input:=filepath.Join(t.TempDir(),"input.tar.gz")
	if err:=os.WriteFile(input,[]byte("archive contents"),0600);err!=nil{t.Fatal(err)}
	remote:="disk:/server/2026-11-25"
	m,err:=c.UploadArchive(ctx,remote,input)
	if err!=nil{t.Fatal(err)}
	if len(m.Parts)!=1||m.Size!=16{t.Fatalf("unexpected manifest: %+v",m)}
	if len(d.files[remote+"/manifest.json"])==0{t.Fatal("missing manifest")}
	dst:=filepath.Join(t.TempDir(),"restored")
	if err:=c.DownloadArchive(ctx,remote,dst);err!=nil{t.Fatal(err)}
	data,err:=os.ReadFile(dst)
	if err!=nil||string(data)!="archive contents"{t.Fatalf("unexpected restored data: %q, %v",data,err)}
	if err:=c.DownloadArchive(ctx,remote,dst);err==nil{t.Fatal("existing destination overwritten")}
	d.files[remote+"/archive.part000001"]=[]byte("corrupt contents")
	bad:=filepath.Join(t.TempDir(),"bad")
	if err:=c.DownloadArchive(ctx,remote,bad);err==nil{t.Fatal("accepted corrupt part")}
	if _,err:=os.Stat(bad);!os.IsNotExist(err){t.Fatal("invalid archive published")}
}

func TestFailedUploadDoesNotPublishManifest(t *testing.T){
	d:=&fakeDisk{files:map[string][]byte{},dirs:map[string]bool{},fail:true}
	c:=Client{Token:"test-token",HTTP:&http.Client{Transport:d}}
	input:=filepath.Join(t.TempDir(),"input")
	if err:=os.WriteFile(input,[]byte("data"),0600);err!=nil{t.Fatal(err)}
	remote:="disk:/server/failed"
	if _,err:=c.UploadArchive(context.Background(),remote,input);err==nil{t.Fatal("accepted failed transfer")}
	if _,ok:=d.files[remote+"/manifest.json"];ok{t.Fatal("published incomplete manifest")}
}

func TestArchiveManifestRequiresCompletion(t *testing.T){
    d:=&fakeDisk{files:map[string][]byte{},dirs:map[string]bool{}}
    c:=Client{Token:"test-token",HTTP:&http.Client{Transport:d}}
    remote:="disk:/sql/backup.sql.gz"
    if _,err:=c.ArchiveManifest(context.Background(),remote);!errors.Is(err,ErrNotFound){t.Fatalf("missing completion marker: %v",err)}
    d.files[remote+"/manifest.json"]=[]byte(`{"version":1,"size":10,"parts":[]}`)
    if _,err:=c.ArchiveManifest(context.Background(),remote);err==nil{t.Fatal("accepted malformed manifest")}
}


type redirectDiskTransport func(*http.Request)(*http.Response,error)
func (f redirectDiskTransport) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}

func TestSignedDownloadFollowsHTTPSRedirectWithoutOAuth(t *testing.T){
	for _,target:=range []string{"https://storage.example/manifest","http://storage.example/manifest"}{
		t.Run(target,func(t *testing.T){
			reached:=false
			client:=New("private-token")
			client.HTTP.Transport=redirectDiskTransport(func(r *http.Request)(*http.Response,error){
				header:=make(http.Header)
				status:=200
				body:=""
				switch r.URL.Host{
				case "cloud-api.yandex.net":
					if r.Header.Get("Authorization")!="OAuth private-token"{t.Fatal("API authorization missing")}
					body=`{"href":"https://downloader.example/manifest?signature=private"}`
				case "downloader.example":
					if r.Header.Get("Authorization")!=""{t.Fatal("OAuth leaked to downloader")}
					status=302;header.Set("Location",target)
				case "storage.example":
					reached=true
					if r.Header.Get("Authorization")!=""||r.Header.Get("Referer")!=""||r.Header.Get("Cookie")!=""{t.Fatal("credentials leaked during redirect")}
					body=`{"version":1}`
				default:t.Fatalf("unexpected host: %s",r.URL.Host)
				}
				return &http.Response{StatusCode:status,Header:header,Body:io.NopCloser(strings.NewReader(body))},nil
			})
			var result struct{Version int `json:"version"`}
			err:=client.GetJSON(context.Background(),"app:/backup/manifest.json",&result)
			if strings.HasPrefix(target,"http:"){
				if err==nil||reached{t.Fatal("accepted insecure redirect")}
			}else if err!=nil||!reached||result.Version!=1{t.Fatalf("signed download failed: %v",err)}
		})
	}
}
