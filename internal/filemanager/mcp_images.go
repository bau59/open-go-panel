package filemanager

import (
    "bytes"
    "encoding/base64"
    "errors"
    "fmt"
    "image"
    "image/color"
    "image/draw"
    _ "image/gif"
    _ "image/jpeg"
    "image/png"
    "io"
    "path"
    "strconv"
    "strings"

    xdraw "golang.org/x/image/draw"
    "golang.org/x/image/font"
    "golang.org/x/image/font/gofont/goregular"
    "golang.org/x/image/font/opentype"
    "golang.org/x/image/math/fixed"
    _ "golang.org/x/image/webp"
)

const maxImageBytes = 2 << 20
const maxImagePixels = 16000000
const maxSheetPixels = 1500000
var errImageSize = errors.New("encoded PNG exceeds 2 MiB limit; specify smaller max_width/max_height")

type imageOptions struct {
    width, height int
    background *color.NRGBA
}
func parseImageOptions(args MCPArgs)(imageOptions,error){
    o:=imageOptions{width:args.MaxWidth,height:args.MaxHeight}
    if o.width<0||o.height<0||o.width>maxImagePixels||o.height>maxImagePixels{return o,errors.New("invalid image dimensions")}
    if args.Background!=""{
        if len(args.Background)!=7||args.Background[0]!='#'{return o,errors.New("background must be #RRGGBB")}
        n,err:=strconv.ParseUint(args.Background[1:],16,24);if err!=nil{return o,errors.New("background must be #RRGGBB")}
        c:=color.NRGBA{R:uint8(n>>16),G:uint8(n>>8),B:uint8(n),A:255};o.background=&c
    }
    return o,nil
}
func loadMCPImage(m *Manager,folder,name string)(image.Image,[]byte,string,error){
    f,info,err:=m.Download(folder,name);if err!=nil{return nil,nil,"",errors.New("file unavailable")}
    defer f.Close()
    if info.Size()>maxImageBytes{return nil,nil,"",errors.New("file exceeds 2 MiB")}
    data,err:=io.ReadAll(io.LimitReader(f,maxImageBytes+1));if err!=nil{return nil,nil,"",errors.New("cannot read file")}
    if len(data)>maxImageBytes{return nil,nil,"",errors.New("file exceeds 2 MiB")}
    cfg,format,err:=image.DecodeConfig(bytes.NewReader(data))
    if err!=nil{return nil,nil,"",errors.New("invalid or unsupported image")}
    if cfg.Width<=0||cfg.Height<=0||cfg.Width>maxImagePixels/cfg.Height{return nil,nil,"",errors.New("image exceeds 16 megapixels")}
    if format!="png"&&format!="jpeg"&&format!="gif"&&format!="webp"{return nil,nil,"",errors.New("unsupported raster image")}
    img,decoded,err:=image.Decode(bytes.NewReader(data))
    if err!=nil||decoded!=format{return nil,nil,"",errors.New("invalid or truncated image")}
    w,h:=img.Bounds().Dx(),img.Bounds().Dy()
    if w<=0||h<=0||w>maxImagePixels/h||w>cfg.Width||h>cfg.Height{return nil,nil,"",errors.New("invalid decoded image dimensions")}
    return img,data,format,nil
}
// Compute bounded integer dimensions without upscaling. Only the limiting
// axis is rounded, so narrow images retain at least one pixel.
func fitImage(w,h,maxW,maxH int)(int,int){
    if maxW<=0||maxW>w{maxW=w}
    if maxH<=0||maxH>h{maxH=h}
    if int64(maxW)*int64(h)<=int64(maxH)*int64(w){return maxW,max(1,int(int64(h)*int64(maxW)/int64(w)))}
    return max(1,int(int64(w)*int64(maxH)/int64(h))),maxH
}
func imageResult(data []byte,mime string)map[string]any{
    return map[string]any{"mimeType":mime,"data":base64.StdEncoding.EncodeToString(data)}
}
type boundedPNG struct{bytes.Buffer}
func(b *boundedPNG)Write(p []byte)(int,error){
    if len(p)>maxImageBytes-b.Len(){return 0,errImageSize}
    return b.Buffer.Write(p)
}
func encodeImage(img image.Image)(map[string]any,error){
    var buf boundedPNG
    if err:=png.Encode(&buf,img);err!=nil{return nil,err}
    return imageResult(buf.Bytes(),"image/png"),nil
}
func mcpViewImage(m *Manager,folder,name string,args MCPArgs)(any,error){
    o,err:=parseImageOptions(args);if err!=nil{return nil,err}
    img,data,format,err:=loadMCPImage(m,folder,name);if err!=nil{return nil,err}
    w,h:=fitImage(img.Bounds().Dx(),img.Bounds().Dy(),o.width,o.height)
    // Original PNG/JPEG/GIF bytes (including animation) remain unchanged
    // when no transformation is needed. WebP is always normalized to PNG.
    if format!="webp"&&o.background==nil&&w==img.Bounds().Dx()&&h==img.Bounds().Dy(){
        mime:=map[string]string{"png":"image/png","jpeg":"image/jpeg","gif":"image/gif"}[format]
        return imageResult(data,mime),nil
    }
    if o.background==nil&&w==img.Bounds().Dx()&&h==img.Bounds().Dy(){return encodePreview(img)}
    dst:=image.NewNRGBA(image.Rect(0,0,w,h))
    if o.background!=nil{draw.Draw(dst,dst.Bounds(),image.NewUniform(*o.background),image.Point{},draw.Src)}
    xdraw.ApproxBiLinear.Scale(dst,dst.Bounds(),img,img.Bounds(),draw.Over,nil)
    return encodePreview(dst)
}

// Render a fixed grid, decoding one source at a time. All file opens go
// through the same sandboxed manager (no symlinks, hardlinks or mount escape).
func mcpViewImages(m *Manager,args MCPArgs)(any,error){
    if len(args.Paths)<1||len(args.Paths)>12{return nil,errors.New("expected 1–12 image paths")}
    o,err:=parseImageOptions(args);if err!=nil{return nil,err}
    cols:=args.Columns
    if cols==0{cols=min(3,len(args.Paths))}
    if cols<1||cols>12{return nil,errors.New("columns must be 1–12")}
    cols=min(cols,len(args.Paths))
    rows:=(len(args.Paths)+cols-1)/cols
    w,h:=o.width,o.height
    if w==0{w=300};if h==0{h=300}
    if w>2048||h>2048{return nil,errors.New("contact sheet cell dimensions must not exceed 2048")}
    // The preview area has a minimum size so labels and errors remain readable
    // even if the requested image bound is only a few pixels.
    imageW,imageH:=w,h
    w=max(160,w);h=max(80,h)
    const pad=8
    const labelH=46
    // Bound the composite independently from the per-image decode limit.
    // Reduce all cells equally if necessary, not individual source images.
    for int64(cols*(w+2*pad))*int64(rows*(h+2*pad+labelH))>maxSheetPixels {
        w=max(1,w*9/10);h=max(1,h*9/10)
    }
    cellW,cellH:=w+2*pad,h+2*pad+labelH
    sheet:=image.NewNRGBA(image.Rect(0,0,cols*cellW,rows*cellH))
    draw.Draw(sheet,sheet.Bounds(),image.NewUniform(color.NRGBA{R:235,G:238,B:242,A:255}),image.Point{},draw.Src)
    faceFont,err:=opentype.Parse(goregular.TTF);if err!=nil{return nil,err}
    face,err:=opentype.NewFace(faceFont,&opentype.FaceOptions{Size:12,DPI:72,Hinting:font.HintingFull});if err!=nil{return nil,err}
    defer face.Close()
    ink:=image.NewUniform(color.NRGBA{R:31,G:41,B:55,A:255})
    red:=image.NewUniform(color.NRGBA{R:175,G:32,B:32,A:255})
    bg:=color.NRGBA{R:255,G:255,B:255,A:255}
    if o.background!=nil{bg=*o.background}
    for i,relative:=range args.Paths{
        x,y:=(i%cols)*cellW,(i/cols)*cellH
        rect:=image.Rect(x+pad,y+pad,x+pad+w,y+pad+h)
        draw.Draw(sheet,rect,image.NewUniform(bg),image.Point{},draw.Src)
        folder,name,e:=splitMCPPath(relative)
        var img image.Image
        if len(relative)>4096{e=errors.New("invalid project path")}
        if e==nil{img,_,_,e=loadMCPImage(m,folder,name)}
        if e!=nil{
            // Never disclose host paths from failed file opens.
            draw.Draw(sheet,rect,image.NewUniform(color.NRGBA{R:255,G:235,B:235,A:255}),image.Point{},draw.Src)
            labelLines(sheet,face,red,rect.Min.X+3,rect.Min.Y+14,max(1,w-6),max(1,h/15), "Error: "+e.Error())
        }else{
            iw,ih:=fitImage(img.Bounds().Dx(),img.Bounds().Dy(),min(w,imageW),min(h,imageH))
            left,top:=rect.Min.X+(w-iw)/2,rect.Min.Y+(h-ih)/2
            xdraw.ApproxBiLinear.Scale(sheet,image.Rect(left,top,left+iw,top+ih),img,img.Bounds(),draw.Over,nil)
        }
        name=path.Base(relative)
        if name=="."||name=="/"{name=relative}
        labelLines(sheet,face,ink,x+pad,y+pad+h+15,w,3,fmt.Sprintf("%d. %s",i+1,name))
    }
    // Highly noisy previews can exceed the encoded byte budget. Reduce the
    // entire sheet in memory, retaining the grid and ordering.
    return encodePreview(sheet)
}

// A compressed WebP can decode into a PNG larger than the transport limit.
// Reduce only transformed images, keeping proportions and alpha intact.
func encodePreview(preview image.Image)(map[string]any,error){
    for {
        result,err:=encodeImage(preview)
        if err==nil{return result,nil}
        if !errors.Is(err,errImageSize){return nil,err}
        b:=preview.Bounds()
        if b.Dx()<2&&b.Dy()<2{return nil,err}
        dst:=image.NewNRGBA(image.Rect(0,0,max(1,b.Dx()*3/4),max(1,b.Dy()*3/4)))
        xdraw.ApproxBiLinear.Scale(dst,dst.Bounds(),preview,b,draw.Src,nil)
        preview=dst
    }
}

func labelLines(dst *image.NRGBA,face font.Face,ink image.Image,x,y,width,lines int,text string){
    text=strings.Map(func(r rune)rune{if r<' '||r==127{return ' '};return r},text)
    runes:=[]rune(text)
    // Limit pathological filenames/error labels before measuring them.
    if len(runes)>512{runes=append(runes[:511],'…')}
    d:=font.Drawer{Dst:dst,Src:ink,Face:face}
    for row:=0;row<lines&&len(runes)>0;row++{
        n:=0
        for n<len(runes)&&d.MeasureString(string(runes[:n+1])).Ceil()<=width{n++}
        if n==0{return}
        part:=string(runes[:n])
        if row==lines-1&&n<len(runes){
            for n>0&&d.MeasureString(string(runes[:n])+"…").Ceil()>width{n--}
            part=string(runes[:n])+"…"
        }
        d.Dot=fixed.P(x,y+row*14);d.DrawString(part)
        runes=runes[n:]
    }
}
