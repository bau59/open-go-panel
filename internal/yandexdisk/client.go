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
	"regexp"
	"os"
	"path"
	"path/filepath"
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
	values := url.Values{"path":{remote}}
	if endpoint=="/upload" { values.Set("overwrite","true") }
	u := "https://cloud-api.yandex.net/v1/disk/resources"+endpoint+"?"+values.Encode()
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

// Check creates the destination if needed and verifies its accessibility.
func (c Client) Check(ctx context.Context, remote string) error {
	prefix, relative, ok := strings.Cut(remote,":/")
	if !ok || (prefix!="app" && prefix!="disk") || relative=="" { return fmt.Errorf("expected app:/ or disk:/ backup directory") }
	dir := prefix+":/"
	for _, segment := range strings.Split(relative,"/") {
		if segment=="" || segment=="." || segment==".." || strings.ContainsAny(segment,"\\\r\n") { return fmt.Errorf("invalid backup directory") }
		dir = strings.TrimSuffix(dir,"/")+"/"+segment
		if err := c.mkdir(ctx,dir); err != nil { return err }
	}
	return nil
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
	if err != nil || u.Scheme != "https" || u.Hostname()=="" || u.User!=nil { return fmt.Errorf("invalid upload URL") }
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
	if err := c.Check(ctx,remote); err != nil { return m,err }
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
		var existing struct { Size int64 `json:"size"`; SHA256 string `json:"sha256"` }
		if c.request(ctx,"GET","",remote+"/"+part.Name,&existing)==nil && existing.Size==n && strings.EqualFold(existing.SHA256,part.SHA256) {
			m.Parts=append(m.Parts,part);offset+=n;continue
		}
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

// DownloadArchive reconstructs a published archive into an exclusive temporary
// file, verifies all parts, then publishes it without replacing an existing file.
func (c Client) DownloadArchive(ctx context.Context, remote, filename string) error {
	var m Manifest
	res,err:=c.download(ctx,remote+"/manifest.json")
	if err!=nil{return err}
	err=json.NewDecoder(io.LimitReader(res.Body,4<<20)).Decode(&m)
	res.Body.Close()
	if err!=nil{return err}
	if m.Version!=1 || m.Size<=0 || len(m.Parts)==0 {return fmt.Errorf("invalid manifest")}
	validHash:=regexp.MustCompile("^[0-9a-f]{64}$")
	var total int64
	for i,p:=range m.Parts{
		if p.Name!=fmt.Sprintf("archive.part%06d",i+1)||p.Size<=0||p.Size>PartSize||!validHash.MatchString(p.SHA256)||p.Size>m.Size-total{return fmt.Errorf("invalid manifest part")}
		total+=p.Size
	}
	if total!=m.Size{return fmt.Errorf("manifest size mismatch")}
	tmp,err:=os.CreateTemp(filepath.Dir(filename),".ogp-restore-*")
	if err!=nil{return err}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	for _,p:=range m.Parts{
		res,err:=c.download(ctx,remote+"/"+p.Name)
		if err!=nil{return err}
		h:=sha256.New()
		n,copyErr:=io.Copy(io.MultiWriter(tmp,h),io.LimitReader(res.Body,p.Size+1))
		res.Body.Close()
		if copyErr!=nil{return copyErr}
		if n!=p.Size||hex.EncodeToString(h.Sum(nil))!=p.SHA256{return fmt.Errorf("part verification failed: %s",p.Name)}
	}
	if err:=tmp.Sync();err!=nil{return err}
	if err:=tmp.Close();err!=nil{return err}
	return os.Link(tmp.Name(),filename)
}

func (c Client) download(ctx context.Context,remote string)(*http.Response,error){
	var link struct{Href string `json:"href"`}
	if err:=c.request(ctx,"GET","/download",remote,&link);err!=nil{return nil,err}
	u,err:=url.Parse(link.Href)
	if err!=nil||u.Scheme!="https"||u.Hostname()==""||u.User!=nil{return nil,fmt.Errorf("invalid download URL")}
	req,err:=http.NewRequestWithContext(ctx,"GET",link.Href,nil)
	if err!=nil{return nil,fmt.Errorf("invalid download request")}
	res,err:=c.HTTP.Do(req)
	if err!=nil{return nil,fmt.Errorf("disk download failed")}
	if res.StatusCode!=200{res.Body.Close();return nil,fmt.Errorf("disk download: HTTP %d",res.StatusCode)}
	return res,nil
}

// PutJSON publishes a small completion index after its referenced archives.
func (c Client) PutJSON(ctx context.Context, remote string, value any) error {
	data,err:=json.Marshal(value)
	if err!=nil{return err}
	if len(data)>4<<20{return fmt.Errorf("snapshot index too large")}
	return c.upload(ctx,remote,int64(len(data)),bytes.NewReader(data))
}

func (c Client) GetJSON(ctx context.Context,remote string,out any) error {
	res,err:=c.download(ctx,remote)
	if err!=nil{return err}
	defer res.Body.Close()
	return json.NewDecoder(io.LimitReader(res.Body,4<<20)).Decode(out)
}

// ListDirectories lists immediate child directories with pagination.
func (c Client) ListDirectories(ctx context.Context,remote string)([]string,error){
	var names []string
	for offset:=0;;{
		values:=url.Values{"path":{remote},"limit":{"100"},"offset":{fmt.Sprint(offset)}}
		req,err:=http.NewRequestWithContext(ctx,"GET","https://cloud-api.yandex.net/v1/disk/resources?"+values.Encode(),nil)
		if err!=nil{return nil,err}
		req.Header.Set("Authorization","OAuth "+c.Token)
		res,err:=c.HTTP.Do(req)
		if err!=nil{return nil,fmt.Errorf("disk directory listing failed")}
		if res.StatusCode!=200{res.Body.Close();return nil,fmt.Errorf("disk listing: HTTP %d",res.StatusCode)}
		var data struct{Embedded struct{Items []struct{Name string `json:"name"`;Type string `json:"type"`} `json:"items"`;Total int `json:"total"`} `json:"_embedded"`}
		err=json.NewDecoder(io.LimitReader(res.Body,8<<20)).Decode(&data)
		res.Body.Close()
		if err!=nil{return nil,err}
		for _,item:=range data.Embedded.Items{if item.Type=="dir"{names=append(names,item.Name)}}
		offset+=len(data.Embedded.Items)
		if len(data.Embedded.Items)==0||offset>=data.Embedded.Total{return names,nil}
	}
}
