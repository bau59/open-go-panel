package server

import (
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
        defer files.Close()
        folder := r.URL.Query().Get("dir")
        page, err := fileManagerPage(app, files, folder, r.URL.Query().Get("edit"), r.URL.Query().Get("new") == "1", "", r.URL.Query().Get("notice"))
        if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
        writeHTML(w, cfg.Logger, http.StatusOK, page)
    })))

    mux.Handle("GET /apps/{id}/files/download", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _, files, ok := openAppFiles(w, r, cfg)
        if !ok { return }
        defer files.Close()
        name := r.URL.Query().Get("name")
        file, info, err := files.Download(r.URL.Query().Get("dir"), name)
        if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
        defer file.Close()
        disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
        w.Header().Set("Content-Disposition", disposition)
        w.Header().Set("Content-Type", "application/octet-stream")
        w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
        w.Header().Set("Cache-Control", "private, no-store")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        if _, err := io.Copy(w, file); err != nil {
            cfg.Logger.Warn("app file download interrupted", "err", err)
        }
    })))

    mux.Handle("POST /apps/{id}/files", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        app, files, ok := openAppFiles(w, r, cfg)
        if !ok { return }
        defer files.Close()
        folder := r.FormValue("dir")
        name := r.FormValue("name")
        action := r.FormValue("action")
        var err error
        switch action {
        case "mkdir":
            err = files.Mkdir(folder, name)
        case "create":
            err = files.Save(folder, name, r.FormValue("content"), true)
        case "save":
            err = files.Save(folder, name, r.FormValue("content"), false)
        case "move":
            err = files.Move(folder, name, r.FormValue("destination"))
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
                    err = fmt.Errorf("upload exceeds 128 MiB limit")
                } else {
                    err = files.Upload(folder, header.Filename, input)
                }
            }
        default:
            err = fmt.Errorf("unsupported file operation")
        }
        if err != nil {
            page, renderErr := fileManagerPage(app, files, folder, "", false, err.Error(), "")
            if renderErr != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
            writeHTML(w, cfg.Logger, http.StatusBadRequest, page)
            return
        }
        destination := appFilesURL(app.ID, folder)
        delimiter := "?"
        if strings.Contains(destination, "?") { delimiter = "&" }
        http.Redirect(w, r, destination+delimiter+"notice="+url.QueryEscape(action+" completed"), http.StatusSeeOther)
    })))
}

func openAppFiles(w http.ResponseWriter, r *http.Request, cfg Config) (panelapp.App, *filemanager.Manager, bool) {
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
    files, err := filemanager.Open(app.Root, app.User)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return panelapp.App{}, nil, false
    }
    return app, files, true
}

func fileHidden(action, folder, name string) string {
    return `<input type="hidden" name="action" value="`+html.EscapeString(action)+`">`+
        `<input type="hidden" name="dir" value="`+html.EscapeString(folder)+`">`+
        `<input type="hidden" name="name" value="`+html.EscapeString(name)+`">`
}

func fileManagerPage(app panelapp.App, files *filemanager.Manager, folder, editName string, create bool, message, notice string) (string, error) {
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
        source := ""
        if editName != "" {
            source, err = files.ReadText(folder, editName)
            if err != nil { return "", err }
        }
        operation := "save"
        filenameField := `<input type="hidden" name="name" value="`+html.EscapeString(editName)+`">`
        title := "Edit " + editName
        if create {
            operation = "create"
            filenameField = `<label>Filename</label><input name="name" maxlength="255" required placeholder="index.php">`
            title = "New text file"
        }
        fmt.Fprintf(&content, `<section class="panel panel-pad fileman-panel">
            <h2>%s</h2>
            <form method="post" action="%s">
                %s%s
                <textarea name="content" class="codearea fileman-editor" spellcheck="false" maxlength="%d">%s</textarea>
                <div class="actions" style="justify-content:flex-start;margin-top:14px">
                    <button class="button" type="submit">Save file</button>
                    <a class="secondary" href="%s">Cancel</a>
                </div>
            </form>
        </section>`, html.EscapeString(title), actionURL, fileHidden(operation, folder, ""), filenameField, filemanager.MaxEditBytes, html.EscapeString(source), base)
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
                        <label>Move / rename to</label><input name="destination" required placeholder="folder/new-name">
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
                    %s<label>Upload file (max 128 MiB)</label>
                    <input type="file" name="file" required>
                    <button class="button">Upload</button>
                </form>
                <form method="post" action="%s" class="fileman-tool">
                    %s<label>New folder</label>
                    <input name="name" required maxlength="255" placeholder="folder-name">
                    <button class="secondary">Create</button>
                </form>
                <div class="fileman-tool">
                    <label>Text editor</label>
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
        .fileman-tools{display:grid;grid-template-columns:repeat(2,minmax(0,1fr)) auto;gap:15px;align-items:end;margin-bottom:20px}
        .fileman-tool{display:flex;flex-direction:column;gap:9px;min-width:0}
        .fileman-tool label{font-size:12px;font-weight:650}
        .fileman-tool button,.fileman-tool>a{align-self:flex-start}
        .fileman-table{overflow-x:auto}
        .fileman-actions{justify-content:flex-end;flex-wrap:nowrap}
        .fileman-more{position:relative}
        .fileman-more>summary{cursor:pointer;list-style:none;white-space:nowrap}
        .fileman-popover{position:absolute;right:0;top:100%;z-index:15;min-width:min(300px,80vw);padding:14px;border:1px solid #425a78;border-radius:12px;background:#172a43;box-shadow:0 16px 36px rgba(0,0,0,.35)}
        .fileman-popover form{display:grid;gap:8px;margin-bottom:14px}
        .fileman-popover form:last-child{margin-bottom:0}
        .fileman-popover input{min-width:0}
        .fileman-editor{display:block;width:100%;min-height:55vh;margin-top:14px;white-space:pre;tab-size:4;font-family:ui-monospace,SFMono-Regular,Consolas,monospace}
        @media(max-width:800px){.fileman-tools{grid-template-columns:1fr}.fileman-actions{flex-wrap:wrap}}
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
