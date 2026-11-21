package server

import (
    "crypto/sha256"
    "errors"
    "time"
    "fmt"
    "html"
    "io"
    "mime"
    "net/http"
    "mime/multipart"
    "net/url"
    "path"
    "strconv"
    "strings"

    panelapp "github.com/bau59/open-go-panel/internal/app"
    "github.com/bau59/open-go-panel/internal/filemanager"
)

func appFilesURL(id int64, folder string) string {
    target := fmt.Sprintf("/apps/%d/files", id)
    if folder == "" || folder == "." { return target }
    return target + "?dir=" + url.QueryEscape(folder)
}

func registerAppFileRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
    mux.Handle("GET /apps/{id}/files", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        app, files, ok := openAppFiles(w, r, cfg)
        if !ok { return }
        folder := r.URL.Query().Get("dir")
        page, err := fileManagerPage(app, files, folder, r.URL.Query().Get("edit"), r.URL.Query().Get("new") == "1", "", r.URL.Query().Get("notice"))
        if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
        writeHTML(w, cfg.Logger, http.StatusOK, page)
    })))

    mux.Handle("GET /apps/{id}/files/download", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _, files, ok := openAppFiles(w, r, cfg)
        if !ok { return }
        name := r.URL.Query().Get("name")
        folder := r.URL.Query().Get("dir")
        size, err := files.FileSize(folder, name)
        if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
        disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
        w.Header().Set("Content-Disposition", disposition)
        w.Header().Set("Content-Type", "application/octet-stream")
        w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
        w.Header().Set("Cache-Control", "private, no-store")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        if err := files.Download(folder, name, w); err != nil {
            cfg.Logger.Warn("app file download interrupted", "err", err)
        }
    })))

    mux.Handle("POST /apps/{id}/files", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if action := r.FormValue("action"); action == "extract" || action == "move_contents" || action == "bulk_move" || action == "bulk_delete" {
            // Archive extraction can write tens of thousands of PHP files.
            // The panel's normal 30s HTTP write deadline is far too short;
            // extend only this operation without changing other requests.
            controller := http.NewResponseController(w)
            if err := controller.SetWriteDeadline(time.Now().Add(time.Hour)); err != nil && !errors.Is(err, http.ErrNotSupported) {
                http.Error(w, "cannot set extraction response deadline", http.StatusServiceUnavailable)
                return
            }
        }
        // Take the same lock used by Git deploy/rollback before opening the
        // application root. A release exchange cannot discard these changes.
        lockErr := cfg.Apps.WithFileOperation(r.Context(), func() error {
        app, files, ok := openAppFiles(w, r, cfg)
        if !ok { return nil }
        folder := r.FormValue("dir")
        name := r.FormValue("name")
        action := r.FormValue("action")
        var err error
        count := 0
        redirectFolder := folder
        notice := action + " completed"
        switch action {
        case "mkdir":
            err = files.Mkdir(folder, name)
        case "create":
            err = files.Save(folder, name, r.FormValue("content"), true)
        case "save":
            current, readErr := files.ReadText(folder, name)
            if readErr != nil {
                err = readErr
            } else {
                digest := fmt.Sprintf("%x", sha256.Sum256([]byte(current)))
                if r.FormValue("base_sha256") != digest {
                    err = fmt.Errorf("file changed since it was opened; copy your edits before reloading")
                } else {
                    err = files.Save(folder, name, r.FormValue("content"), false)
                }
            }
        case "move":
            err = files.Move(folder, name, r.FormValue("destination"))
        case "bulk_move":
            count, err = files.MoveMany(folder, r.PostForm["selected"], r.FormValue("destination"))
            if err == nil { notice = fmt.Sprintf("Moved %d selected items", count) }
        case "bulk_delete":
            count, err = files.DeleteMany(folder, r.PostForm["selected"])
            if err == nil { notice = fmt.Sprintf("Deleted %d selected items", count) }
        case "move_contents":
            count, err = files.MoveContentsUp(folder, name)
            if err == nil {
                notice = fmt.Sprintf("Moved %d items to the parent folder; source folder is now empty", count)
                if name == "" {
                    redirectFolder = path.Dir(folder)
                    if redirectFolder == "." { redirectFolder = "" }
                }
            }
        case "copy":
            err = files.Copy(folder, name, r.FormValue("destination"))
        case "delete":
            err = files.Delete(folder, name)
        case "extract":
            _, err = files.Extract(folder, name)
        case "upload":
            var input io.ReadCloser
            var header *multipart.FileHeader
            input, header, err = r.FormFile("file")
            if err == nil {
                defer input.Close()
                if header.Size > filemanager.MaxUploadBytes {
                    err = fmt.Errorf("upload exceeds 1 GiB limit")
                } else {
                    // Browser-selected file size is recorded before transfer.
                    // A mismatch means the HTTP multipart body is incomplete.
                    clientSize := r.FormValue("client_size")
                    if clientSize != "" {
                        selected, sizeErr := strconv.ParseInt(clientSize, 10, 64)
                        if sizeErr != nil || selected != header.Size {
                            err = fmt.Errorf("incomplete upload: browser selected %s bytes, server received %d", clientSize, header.Size)
                        }
                    }
                    if err == nil {
                        // SHA-256 is verified across the helper pipe against
                        // the saved file read back from disk.
                        err = files.Upload(folder, header.Filename, input, header.Size)
                    }
                }
            }
        default:
            err = fmt.Errorf("unsupported file operation")
        }
        if err != nil {
            edit, newFile := "", false
            if action == "save" { edit = name }
            if action == "create" { edit, newFile = name, true }
            var page string
            var renderErr error
            if action == "save" || action == "create" {
                page, renderErr = fileManagerPage(app, files, folder, edit, newFile, err.Error(), "", r.FormValue("content"), r.FormValue("base_sha256"))
            } else {
                page, renderErr = fileManagerPage(app, files, folder, "", false, err.Error(), "")
            }
            if renderErr != nil { http.Error(w, err.Error(), http.StatusBadRequest); return nil }
            writeHTML(w, cfg.Logger, http.StatusBadRequest, page)
            return nil
        }
        destination := appFilesURL(app.ID, redirectFolder)
        delimiter := "?"
        if strings.Contains(destination, "?") { delimiter = "&" }
        if action == "upload" { notice = "Upload complete · SHA-256 verified" }
        http.Redirect(w, r, destination+delimiter+"notice="+url.QueryEscape(notice), http.StatusSeeOther)
        return nil
        })
        if lockErr != nil { http.Error(w, lockErr.Error(), http.StatusRequestTimeout) }
    })))
}

func openAppFiles(w http.ResponseWriter, r *http.Request, cfg Config) (panelapp.App, *filemanager.Client, bool) {
    id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
    if err != nil || id <= 0 {
        http.Error(w, "invalid application", http.StatusBadRequest)
        return panelapp.App{}, nil, false
    }
    app, err := cfg.Apps.Get(id)
    if err != nil {
        http.Error(w, "application not found", http.StatusNotFound)
        return panelapp.App{}, nil, false
    }
    files, err := filemanager.NewClient(r.Context(), app.Root, app.User)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return panelapp.App{}, nil, false
    }
    return app, files, true
}

func fileHidden(action, folder, name string) string {
    result := `<input type="hidden" name="action" value="`+html.EscapeString(action)+`">`+
        `<input type="hidden" name="dir" value="`+html.EscapeString(folder)+`">`
    if name != "" { result += `<input type="hidden" name="name" value="`+html.EscapeString(name)+`">` }
    return result
}

type fileBrowser interface {
    List(folder string) ([]filemanager.Entry, bool, error)
    ReadText(folder, name string) (string, error)
}

func fileManagerPage(app panelapp.App, files fileBrowser, folder, editName string, create bool, message, notice string, draft ...string) (string, error) {
    items, limited, err := files.List(folder)
    if err != nil { return "", err }
    base := appFilesURL(app.ID, folder)
    actionURL := fmt.Sprintf("/apps/%d/files", app.ID)
    header := pageHead(app.Name + " · Files") + `<body>` + appHeader("apps")
    alert := ""
    if message != "" { alert = `<div class="alert">` + html.EscapeString(message) + `</div>` }
    if notice != "" { alert += `<p class="note">` + html.EscapeString(notice) + `</p>` }

    var crumbs strings.Builder
    crumbs.WriteString(`<a href="` + appFilesURL(app.ID, "") + `">Root</a>`)
    current := ""
    if folder != "" && folder != "." {
        for _, part := range strings.Split(folder, "/") {
            if current == "" { current = part } else { current += "/" + part }
            crumbs.WriteString(`<span class="muted">/</span><a href="`+appFilesURL(app.ID, current)+`">`+html.EscapeString(part)+`</a>`)
        }
    }

    var content strings.Builder
    if editName != "" || create {
        source, checksumField := "", ""
        if editName != "" && !create {
            digest := ""
            if len(draft) > 0 {
                source = draft[0]
                if len(draft) > 1 { digest = draft[1] }
            } else {
                original, readErr := files.ReadText(folder, editName)
                if readErr != nil { return "", readErr }
                source = original
                digest = fmt.Sprintf("%x", sha256.Sum256([]byte(original)))
            }
            checksumField = `<input type="hidden" name="base_sha256" value="`+html.EscapeString(digest)+`">`
        }
        if create && len(draft) > 0 { source = draft[0] }
        operation := "save"
        filenameField := `<input type="hidden" name="name" value="`+html.EscapeString(editName)+`">`
        title := "Edit " + editName
        if create {
            operation = "create"
            filenameField = `<label>Filename</label><input name="name" maxlength="255" required placeholder="index.php" value="`+html.EscapeString(editName)+`">`
            title = "New text file"
        }
        fmt.Fprintf(&content, `<section class="panel panel-pad fileman-panel">
            <h2>%s</h2>
            <form method="post" action="%s">
                %s%s%s
                <textarea name="content" class="codearea fileman-editor" spellcheck="false" maxlength="%d">%s</textarea>
                <div class="actions" style="justify-content:flex-start;margin-top:14px">
                    <button class="button" type="submit">Save file</button>
                    <a class="secondary" href="%s">Cancel</a>
                </div>
            </form>
        </section>`, html.EscapeString(title), actionURL, fileHidden(operation, folder, ""), filenameField, checksumField, filemanager.MaxEditBytes, html.EscapeString(source), base)
    } else {
        var rows strings.Builder
        if folder != "" && folder != "." {
            parent := path.Dir(folder)
            if parent == "." { parent = "" }
            rows.WriteString(`<tr><td><a href="`+appFilesURL(app.ID, parent)+`">📁 ..</a></td><td>Parent folder</td><td>—</td><td></td></tr>`)
        }
        for _, item := range items {
            name := html.EscapeString(item.Name)
            entry := `<strong>`+name+`</strong>`
            kind := "File"
            size := fmt.Sprintf("%d B", item.Size)
            if item.Directory {
                kind = "Directory"
                size = "—"
                next := item.Name
                if folder != "" && folder != "." { next = folder + "/" + item.Name }
                entry = `<a href="`+appFilesURL(app.ID, next)+`">📁 `+name+`</a>`
            } else if item.Symlink {
                kind = "Symlink (not followed)"
                size = "—"
                entry = `🔗 `+name
            }
            var actions strings.Builder
            if !item.Directory && !item.Symlink {
                query := url.Values{}
                query.Set("dir", folder)
                query.Set("name", item.Name)
                actions.WriteString(`<a class="secondary compact-action" href="`+actionURL+`/download?`+html.EscapeString(query.Encode())+`">Download</a>`)
                query.Del("name")
                query.Set("edit", item.Name)
                actions.WriteString(`<a class="secondary compact-action" href="`+actionURL+`?`+html.EscapeString(query.Encode())+`">Edit</a>`)
                lower := strings.ToLower(item.Name)
                if strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
                    actions.WriteString(`<form method="post" action="`+actionURL+`">`+fileHidden("extract", folder, item.Name)+`<button class="secondary compact-action" onclick="return confirm('Extract this archive here? Existing files will not be overwritten.')">Extract</button></form>`)
                }
            }
            actions.WriteString(`<details class="fileman-more"><summary class="secondary compact-action">More</summary>
                <div class="fileman-popover">
                    <form method="post" action="`+actionURL+`">
                        `+fileHidden("move", folder, item.Name)+`
                        <label>Move / rename to (from app root)</label><input name="destination" required value="`+html.EscapeString(func() string { if folder == "" { return item.Name }; return folder+"/"+item.Name }())+`" placeholder="folder/new-name">
                        <button class="secondary compact-action">Move</button>
                    </form>`)
            if !item.Directory && !item.Symlink {
                actions.WriteString(`<form method="post" action="`+actionURL+`">
                    `+fileHidden("copy", folder, item.Name)+`
                    <label>Copy to</label><input name="destination" required placeholder="folder/copy-name">
                    <button class="secondary compact-action">Copy</button></form>`)
            }
            actions.WriteString(`<form method="post" action="`+actionURL+`" onsubmit="return confirm('Delete this item permanently?')">
                `+fileHidden("delete", folder, item.Name)+`<button class="danger compact-action">Delete</button></form></div></details>`)
            fmt.Fprintf(&rows, `<tr><td>%s</td><td>%s</td><td>%s</td><td><div class="actions fileman-actions">%s</div></td></tr>`,
                entry, html.EscapeString(kind), html.EscapeString(size), actions.String())
        }
        if len(items) == 0 { rows.WriteString(`<tr><td colspan="4" class="empty">This folder is empty.</td></tr>`) }
        if limited { alert += `<div class="alert">Displaying the first 400 entries. Use the terminal for larger directories.</div>` }
        fmt.Fprintf(&content, `<section class="panel panel-pad fileman-panel">
            <div class="fileman-tools">
                <form method="post" action="%s" enctype="multipart/form-data" class="fileman-tool">
                    %s<label class="fileman-tool-label" for="fileman-upload-file">Upload file <span>Max 1 GiB</span></label>
                    <div class="fileman-control-row">
                        <input type="hidden" name="client_size" value="">
                        <input id="fileman-upload-file" class="file-upload-input" type="file" name="file" required onchange="this.form.elements.client_size.value = this.files.length ? this.files[0].size : ''">
                        <button class="button" type="submit">Upload</button>
                    </div>
                </form>
                <form method="post" action="%s" class="fileman-tool">
                    %s<label class="fileman-tool-label" for="fileman-folder-name">New folder</label>
                    <div class="fileman-control-row">
                        <input id="fileman-folder-name" name="name" required maxlength="255" placeholder="Folder name">
                        <button class="secondary" type="submit">Create</button>
                    </div>
                </form>
                <div class="fileman-tool fileman-tool-new">
                    <span class="fileman-tool-label">Text editor</span>
                    <a class="secondary" href="%s">New file</a>
                </div>
            </div>
            <div class="fileman-table"><table><thead><tr><th>Name</th><th>Type</th><th>Size</th><th>Actions</th></tr></thead>
            <tbody>%s</tbody></table></div>
            <p class="note">Paths are relative to this application. Links cannot be followed. ZIP, TAR.GZ and TGZ extraction refuses overwrites.</p>
        </section>`, actionURL, fileHidden("upload", folder, ""), actionURL, fileHidden("mkdir", folder, ""), base+func()string{ if strings.Contains(base, "?") { return "&new=1" }; return "?new=1" }(), rows.String())
    }
    return header + `<style>
        .fileman-crumbs{display:flex;flex-wrap:wrap;align-items:center;gap:9px;margin:12px 0 18px;font-size:14px}
        .fileman-tools{display:grid;grid-template-columns:minmax(0,1.4fr) minmax(0,1fr) auto;gap:18px;align-items:end;margin-bottom:16px;padding-bottom:18px;border-bottom:1px solid var(--border)}
        .fileman-tool{display:flex;flex-direction:column;min-width:0;margin:0}
        .fileman-tool-label{display:flex;align-items:center;gap:9px;min-height:18px;margin:0 0 8px;color:#b8c9e2;font-size:12px;font-weight:700}
        .fileman-tool-label span{color:var(--muted-2);font-size:11px;font-weight:600}
        .fileman-control-row{display:flex;align-items:center;gap:8px;min-width:0}
        .fileman-control-row input{flex:1 1 auto;min-width:0;width:100%;height:42px}
        .fileman-control-row button,.fileman-tool-new>a{flex:0 0 auto;height:42px;white-space:nowrap}
        .fileman-tool-new>a{align-self:flex-start}
        .fileman-table{overflow-x:auto}
        .fileman-table td:first-child{overflow-wrap:anywhere}
        .fileman-table .empty{padding:42px 16px;text-align:center;color:var(--muted-2)}
        .fileman-actions{justify-content:flex-end;flex-wrap:nowrap}
        .fileman-more{position:relative}
        .fileman-more>summary{cursor:pointer;list-style:none;white-space:nowrap}
        .fileman-popover{position:static;min-width:min(300px,80vw);margin-top:9px;padding:14px;border:1px solid #425a78;border-radius:12px;background:#172a43}
        .fileman-popover form{display:grid;gap:8px;margin-bottom:14px}
        .fileman-popover form:last-child{margin-bottom:0}
        .fileman-popover input{min-width:0}
        .fileman-editor{display:block;width:100%;min-height:55vh;margin-top:14px;white-space:pre;tab-size:4;font-family:ui-monospace,SFMono-Regular,Consolas,monospace}
        @media(max-width:1120px){
            .fileman-tools{grid-template-columns:repeat(2,minmax(0,1fr))}
            .fileman-tool-new{grid-column:1/-1;flex-direction:row;align-items:center;gap:12px}
            .fileman-tool-new .fileman-tool-label{margin:0}
        }
        @media(max-width:740px){
            .fileman-tools{grid-template-columns:minmax(0,1fr)}
            .fileman-tool-new{grid-column:auto}
            .fileman-actions{flex-wrap:wrap;justify-content:flex-start}
        }
        @media(max-width:430px){
            .fileman-control-row{flex-wrap:wrap}
            .fileman-control-row button{width:100%}
        }
    </style>
    <main class="shell app-shell">
        <div class="app-page-head"><div>
            <a class="app-breadcrumb" href="/apps/`+strconv.FormatInt(app.ID,10)+`">`+html.EscapeString(app.Name)+`</a>
            <h1>Files</h1><p class="sub">`+html.EscapeString(app.Root)+`</p>
        </div><a class="secondary" href="/apps/`+strconv.FormatInt(app.ID,10)+`">Application</a></div>
        <nav class="fileman-crumbs" aria-label="Folder path">`+crumbs.String()+`</nav>
        `+alert+content.String()+`
    </main></body></html>`, nil
}
