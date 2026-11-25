package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bau59/open-go-panel/internal/yandexdisk"
)

type snapshotCloudTransport func(*http.Request)(*http.Response,error)
func (f snapshotCloudTransport) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}

func TestCloudInventoryDistinguishesIncompleteAndReadFailure(t *testing.T){
	now:=time.Date(2026,11,25,5,0,0,0,time.UTC)
	okID:=now.Format(snapshotStamp)
	pendingID:=now.Add(-time.Hour).Format(snapshotStamp)
	failedID:=now.Add(-2*time.Hour).Format(snapshotStamp)
	index:=snapshotIndex{Version:1,ID:okID,AppID:7,Owner:"demo",Application:"demo",CreatedAt:now,Files:[]snapshotFile{{Path:"config.tar.gz",Size:100,SHA256:strings.Repeat("a",64)}}}
	client:=yandexdisk.New("token")
	client.HTTP.Transport=snapshotCloudTransport(func(r *http.Request)(*http.Response,error){
		status:=200
		var body []byte
		if r.URL.Host=="cloud-api.yandex.net"&&r.URL.Path=="/v1/disk/resources"{
			body,_=json.Marshal(map[string]any{"_embedded":map[string]any{"total":3,"items":[]map[string]string{{"type":"dir","name":okID},{"type":"dir","name":pendingID},{"type":"dir","name":failedID}}}})
		}else if r.URL.Host=="cloud-api.yandex.net"&&r.URL.Path=="/v1/disk/resources/download"{
			path:=r.URL.Query().Get("path")
			switch{
			case strings.Contains(path,okID):body=[]byte(`{"href":"https://storage.example/manifest"}`)
			case strings.Contains(path,pendingID):status=404
			case strings.Contains(path,failedID):status=503
			default:t.Fatalf("unexpected remote path: %s",path)
			}
		}else if r.URL.Host=="storage.example"{body,_=json.Marshal(index)}else{t.Fatalf("unexpected request: %s",r.URL.Path)}
		return &http.Response{StatusCode:status,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(string(body)))},nil
	})
	inventory,err:=scanCloudSnapshots(context.Background(),client,"app:/backup/projects/demo/demo")
	if err==nil||!strings.Contains(err.Error(),failedID){t.Fatalf("read failure hidden: %v",err)}
	if len(inventory.Snapshots)!=1||inventory.Snapshots[0].ID!=okID{t.Fatalf("completed snapshot missing: %+v",inventory)}
	if len(inventory.Incomplete)!=1||inventory.Incomplete[0]!=pendingID{t.Fatalf("incomplete snapshot missing: %+v",inventory)}
	page:=renderCloudInventory(inventory,err,7)
	for _,part:=range []string{okID,pendingID,failedID,"Download &amp; verify","Cloud listing incomplete"}{
		if !strings.Contains(page,part){t.Errorf("missing UI: %s",part)}
	}
	if strings.Contains(page,"No completed cloud snapshots"){t.Fatal("misleading empty inventory")}
}
