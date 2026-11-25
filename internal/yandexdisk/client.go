package yandexdisk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const PartSize int64 = 900 * 1024 * 1024

type Client struct {
	Token string
	HTTP *http.Client
}

type Part struct {
	Name string `json:"name"`
	Size int64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version int `json:"version"`
	Size int64 `json:"size"`
	Parts []Part `json:"parts"`
}

func (c Client) request(ctx context.Context, method, endpoint, remote string, out any) error {
	u := "https://cloud-api.yandex.net/v1/disk/resources"+endpoint+"?"+url.Values{"path":{remote},"overwrite":{"true"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil { return err }
	req.Header.Set("Authorization", "OAuth "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil { return fmt.Errorf("disk API request failed") }
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 { return fmt.Errorf("disk API: HTTP %d",res.StatusCode) }
	if out != nil { return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out) }
	return nil
}

func New(token string) Client {
	return Client{Token:token, HTTP:&http.Client{Transport:&http.Transport{Proxy:http.ProxyFromEnvironment, ResponseHeaderTimeout: 2*time.Minute, TLSHandshakeTimeout: 15*time.Second}, CheckRedirect:func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c Client) mkdir(ctx context.Context, remote string) error {
	var meta struct { Type string `json:"type"` }
	if err := c.request(ctx,"GET","",remote,&meta); err == nil {
		if meta.Type != "dir" { return fmt.Errorf("backup path is not a directory") }
		return nil
	}
	return c.request(ctx,"PUT","",remote,nil)
}

func (c Client) upload(ctx context.Context, remote string, size int64, reader io.Reader) error {
	var link struct { Href string `json:"href"` }
	if err := c.request(ctx,"GET","/upload",remote,&link); err != nil { return err }
	u, err := url.Parse(link.Href)
	if err != nil || u.Scheme != "https" || u.Hostname()=="" { return fmt.Errorf("invalid upload URL") }
	req, err := http.NewRequestWithContext(ctx,"PUT",link.Href,reader)
	if err != nil { return fmt.Errorf("invalid upload request") }
	req.ContentLength = size
	res, err := c.HTTP.Do(req)
	if err != nil { return fmt.Errorf("disk upload failed") }
	defer res.Body.Close()
	if res.StatusCode != 201 && res.StatusCode != 200 { return fmt.Errorf("disk upload: HTTP %d",res.StatusCode) }
	return nil
}

// UploadArchive publishes a manifest only after all parts have been uploaded.
// The caller must supply a unique snapshot directory and an immutable archive.
func (c Client) UploadArchive(ctx context.Context, remote, filename string) (Manifest,error) {
	m := Manifest{Version:1}
	prefix, relative, ok := strings.Cut(remote,":/")
	if !ok || (prefix!="app" && prefix!="disk") || relative=="" { return m,fmt.Errorf("expected app:/ or disk:/ backup directory") }
	dir := prefix+":/"
	for _, segment := range strings.Split(relative,"/") {
		if segment=="" || segment=="." || segment==".." { return m,fmt.Errorf("invalid backup directory") }
		dir = strings.TrimSuffix(dir,"/")+"/"+segment
		if err := c.mkdir(ctx,dir); err != nil { return m,err }
	}
	f, err := os.Open(filename)
	if err != nil { return m,err }
	defer f.Close()
	info, err := f.Stat()
	if err != nil { return m,err }
	if !info.Mode().IsRegular() || info.Size()==0 { return m,fmt.Errorf("backup must be a nonempty regular file") }
	m.Size=info.Size()
	for offset:=int64(0); offset<m.Size; {
		if err:=ctx.Err(); err!=nil { return m,err }
		n:=min(PartSize,m.Size-offset)
		part:=Part{Name:fmt.Sprintf("archive.part%06d",len(m.Parts)+1),Size:n}
		h:=sha256.New()
		if _,err:=io.Copy(h,io.NewSectionReader(f,offset,n)); err!=nil { return m,err }
		part.SHA256=hex.EncodeToString(h.Sum(nil))
		var uploadErr error
		for attempt:=0;attempt<3;attempt++ {
			uploadErr=c.upload(ctx,remote+"/"+path.Base(part.Name),n,io.NewSectionReader(f,offset,n))
			if uploadErr==nil { break }
			if ctx.Err()!=nil { return m,ctx.Err() }
		}
		if uploadErr!=nil { return m,uploadErr }
		m.Parts=append(m.Parts,part)
		offset+=n
	}
	data,err:=json.Marshal(m)
	if err!=nil { return m,err }
	if err:=c.upload(ctx,remote+"/manifest.json",int64(len(data)),bytes.NewReader(data));err!=nil { return m,err }
	return m,nil
}
