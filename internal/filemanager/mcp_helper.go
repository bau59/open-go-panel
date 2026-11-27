package filemanager

import (
    "bytes"
    "encoding/base64"
    "encoding/json"
    "image"
    _ "image/png"
    _ "image/jpeg"
    _ "image/gif"
    "errors"
    "fmt"
    "io"
    "os"
    "path"
    "strings"
)

type MCPArgs struct{
    Path string `json:"path"`
    Query string `json:"query"`
    StartLine int `json:"start_line"`
    EndLine int `json:"end_line"`
    Content string `json:"content"`
    Version string `json:"version"`
    Recursive bool `json:"recursive"`
    ContextLines int `json:"context_lines"`
}

type SearchMatch struct{
    Path string `json:"path"`
    Line int `json:"line"`
    Text string `json:"text"`
    Context []string `json:"context"`
}

func splitMCPPath(value string)(string,string,error){
    if !validPath(value)||value==""||value=="."{return "","",errors.New("expected a relative file path")}
    folder,name:=path.Split(value)
    return strings.TrimSuffix(folder,"/"),name,nil
}

// RunMCPHelper is called only inside the OS-isolated app subprocess. The
// filemanager still validates every path using openat2 and directory FDs.
func RunMCPHelper(operation string,input io.Reader,output io.Writer)error{
    if os.Geteuid()==0{return errors.New("MCP file helper refuses root")}
    var args MCPArgs
    decoder:=json.NewDecoder(io.LimitReader(input,3<<20))
    decoder.DisallowUnknownFields()
    if err:=decoder.Decode(&args);err!=nil{return errors.New("invalid tool arguments")}
    if err:=decoder.Decode(new(any));!errors.Is(err,io.EOF){return errors.New("invalid tool arguments")}
    m,err:=Open("/workspace");if err!=nil{return err};defer m.Close()
    value,err:=mcpOperation(m,operation,args)
    if err!=nil{return err}
    return json.NewEncoder(output).Encode(value)
}

func mcpOperation(m *Manager,operation string,args MCPArgs)(any,error){
    m.rejectHardlinks=true
    switch operation{
    case "list_directory":
        entries,limited,err:=m.List(args.Path)
        return map[string]any{"entries":entries,"truncated":limited},err
    case "search_files":
        return mcpSearch(m,args)
    }
    folder,name,err:=splitMCPPath(args.Path)
    if err!=nil{return nil,err}
    switch operation{
    case "view_image":
        return mcpViewImage(m,folder,name)
    case "read_file":
        content,err:=m.ReadText(folder,name);if err!=nil{return nil,err}
        lines:=strings.Split(content,"\n")
        start,end:=args.StartLine,args.EndLine
        if start==0{start=1};if end==0{end=len(lines)}
        if start<1||end<start||start>len(lines){return nil,errors.New("invalid line range")}
        if end>len(lines){end=len(lines)}
        return map[string]any{"path":args.Path,"content":strings.Join(lines[start-1:end],"\n"),"version":textVersion(content),"start_line":start,"end_line":end,"total_lines":len(lines)},nil
    case "create_file":
        if err:=m.Save(folder,name,args.Content,true);err!=nil{return nil,err}
        return map[string]any{"path":args.Path,"version":textVersion(args.Content)},nil
    case "edit_file":
        if err:=m.SaveVersioned(folder,name,args.Content,args.Version);err!=nil{return nil,err}
        return map[string]any{"path":args.Path,"version":textVersion(args.Content)},nil
    case "create_directory":
        if err:=m.Mkdir(folder,name);err!=nil{return nil,err}
    case "delete_path":
        if err:=m.DeletePath(folder,name,args.Recursive);err!=nil{return nil,err}
    default:return nil,errors.New("unknown file operation")
    }
    return map[string]any{"path":args.Path,"success":true},nil
}

func mcpSearch(m *Manager,args MCPArgs)(any,error){
    if args.Query==""||len(args.Query)>4096||strings.ContainsAny(args.Query,"\r\n\x00")||args.ContextLines<0||args.ContextLines>3{return nil,errors.New("invalid text query or context")}
    if !validPath(args.Path){return nil,errors.New("invalid search directory")}
    type searchResult struct{
        Matches []SearchMatch `json:"matches"`
        Truncated bool `json:"truncated"`
        Skipped int `json:"skipped_files"`
    }
    result:=searchResult{Matches:[]SearchMatch{}}
    remaining:=5000
    budget:=int64(64<<20)
    outputBudget:=128<<10
    var walk func(string,int)error
    walk=func(dir string,depth int)error{
        if depth>maxDepth{result.Truncated=true;return nil}
        if remaining<=0||budget<=0||len(result.Matches)>=100||outputBudget<=0{result.Truncated=true;return nil}
        entries,limited,err:=m.List(dir)
        if err!=nil{if depth==0{return err};result.Skipped++;return nil}
        if limited{result.Truncated=true}
        for _,entry:=range entries{
            if remaining<=0||budget<=0||len(result.Matches)>=100||outputBudget<=0{result.Truncated=true;break}
            remaining--
            if entry.Symlink{result.Skipped++;continue}
            relative:=path.Join(dir,entry.Name)
            if entry.Directory{
                if entry.Name==".git"{result.Skipped++;continue}
                if err:=walk(relative,depth+1);err!=nil{return err}
                continue
            }
            if entry.Size<0||entry.Size>MaxEditBytes||entry.Size>budget{result.Skipped++;continue}
            budget-=entry.Size
            content,err:=m.ReadText(dir,entry.Name);if err!=nil{result.Skipped++;continue}
            lines:=strings.Split(content,"\n")
            for i,line:=range lines{
                if !strings.Contains(line,args.Query){continue}
                if len(result.Matches)>=100||outputBudget<=0{result.Truncated=true;break}
                shorten:=func(value string)string{if len(value)>512{return value[:512]+"…"};return value}
                match:=SearchMatch{Path:relative,Line:i+1,Text:shorten(line)}
                for n:=max(0,i-args.ContextLines);n<=min(len(lines)-1,i+args.ContextLines);n++{
                    match.Context=append(match.Context,fmt.Sprintf("%d: %s",n+1,shorten(lines[n])))
                }
                outputBudget-=len(relative)+len(match.Text)
                for _,line:=range match.Context{outputBudget-=len(line)}
                result.Matches=append(result.Matches,match)
            }
        }
        return nil
    }
    if err:=walk(args.Path,0);err!=nil{return nil,err}
    return result,nil
}


// Return original raster bytes, never execute active SVG/HTML content.
// The sandbox's 4 MiB output budget includes base64 and JSON overhead.
func mcpViewImage(m *Manager,folder,name string)(any,error){
    const maxBytes=2<<20
    f,info,err:=m.Download(folder,name);if err!=nil{return nil,err};defer f.Close()
    if info.Size()>maxBytes{return nil,errors.New("image exceeds 2 MiB limit; resize or compress it first")}
    data,err:=io.ReadAll(io.LimitReader(f,maxBytes+1));if err!=nil{return nil,err}
    if len(data)>maxBytes{return nil,errors.New("image exceeds 2 MiB limit")}
    cfg,format,err:=image.DecodeConfig(bytes.NewReader(data))
    if err!=nil{return nil,errors.New("expected a PNG, JPEG or GIF image")}
    if cfg.Width<=0||cfg.Height<=0||int64(cfg.Width)*int64(cfg.Height)>16000000{return nil,errors.New("image exceeds 16 megapixel limit")}
    mime:=map[string]string{"png":"image/png","jpeg":"image/jpeg","gif":"image/gif"}[format]
    if mime==""{return nil,errors.New("unsupported raster image format")}
    if _,_,err:=image.Decode(bytes.NewReader(data));err!=nil{return nil,errors.New("invalid or truncated image")}
    return map[string]any{"mimeType":mime,"data":base64.StdEncoding.EncodeToString(data)},nil
}
