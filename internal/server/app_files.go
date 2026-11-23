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

    // Only raster images are displayed inline. SVG is a download to prevent
    // active same-origin content in the administrator's browser.
    mux.Handle("GET /apps/{id}/files/preview", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _, files, ok := openAppFiles(w, r, cfg)
        if !ok { return }
        name, folder := r.URL.Query().Get("name"), r.URL.Query().Get("dir")
        contentType := ""
        switch strings.ToLower(path.Ext(name)) {
        case ".png": contentType = "image/png"
        case ".jpg", ".jpeg": contentType = "image/jpeg"
        case ".gif": contentType = "image/gif"
        case ".webp": contentType = "image/webp"
        default:
            http.Error(w, "preview supports PNG, JPEG, GIF and WebP", http.StatusBadRequest)
            return
        }
        size, err := files.FileSize(folder, name)
        if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
        w.Header().Set("Content-Type", contentType)
        w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
        w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
        w.Header().Set("Cache-Control", "private, no-store")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
        if err := files.Download(folder, name, w); err != nil {
            cfg.Logger.Warn("app image preview interrupted", "err", err)
        }
    })))

    mux.Handle("POST /apps/{id}/files", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if action := r.FormValue("action"); action == "extract" || action == "move_contents" || action == "bulk_move" || action == "bulk_delete" || action == "archive" {
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
        uploadedNames := []string{}
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
        case "chmod":
            err = files.Chmod(folder, name, r.FormValue("mode"))
            if err == nil { notice = "Permissions updated" }
        case "archive":
            count, err = files.Archive(folder, r.PostForm["selected"], r.FormValue("archive_name"))
            if err == nil { notice = fmt.Sprintf("Created ZIP archive with %d entries", count) }
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
            // Parse all selected files while preserving the original single
            // file form and SHA-256 verification. Batch failures roll back
            // only files successfully created by this request.
            if r.MultipartForm == nil {
                err = errors.New("missing multipart file upload")
                break
            }
            headers := r.MultipartForm.File["file"]
            if len(headers) == 0 || len(headers) > 20 {
                err = errors.New("select between 1 and 20 files")
                break
            }
            sizeList := strings.TrimSpace(r.FormValue("client_sizes"))
            var selectedSizes []string
            if sizeList != "" { selectedSizes = strings.Split(sizeList, ",") }
            if len(selectedSizes) > 0 && len(selectedSizes) != len(headers) {
                err = errors.New("incomplete upload: selected file count changed")
                break
            }
            seen := make(map[string]bool, len(headers))
            var total int64
            for i, header := range headers {
                if seen[header.Filename] {
                    err = fmt.Errorf("duplicate upload filename %q", header.Filename)
                    break
                }
                seen[header.Filename] = true
                if header.Size < 0 || header.Size > filemanager.MaxUploadBytes || total > filemanager.MaxUploadBytes-header.Size {
                    err = errors.New("batch upload exceeds the 1 GiB limit")
                    break
                }
                total += header.Size
                expected := ""
                if len(selectedSizes) > 0 { expected = selectedSizes[i] }
                if len(headers) == 1 && expected == "" { expected = r.FormValue("client_size") }
                if expected != "" {
                    selected, parseErr := strconv.ParseInt(expected, 10, 64)
                    if parseErr != nil || selected != header.Size {
                        err = fmt.Errorf("incomplete upload: browser selected %s bytes, server received %d", expected, header.Size)
                        break
                    }
                }
            }
            if err != nil { break }
            for _, header := range headers {
                var input multipart.File
                input, err = header.Open()
                if err != nil { break }
                uploadErr := files.Upload(folder, header.Filename, input, header.Size)
                closeErr := input.Close()
                if uploadErr != nil { err = uploadErr; break }
                if closeErr != nil { err = closeErr; break }
                uploadedNames = append(uploadedNames, header.Filename)
            }
            if err != nil {
                for i := len(uploadedNames)-1; i >= 0; i-- {
                    if rollbackErr := files.Delete(folder, uploadedNames[i]); rollbackErr != nil {
                        err = fmt.Errorf("%w; rollback %q failed: %v", err, uploadedNames[i], rollbackErr)
                    }
                }
                uploadedNames = nil
            } else {
                notice = fmt.Sprintf("Uploaded %d file(s) · SHA-256 verified", len(uploadedNames))
            }
        default:
            err = fmt.Errorf("unsupported file operation")
        }

        // File-manager writes and moves can bypass a directory's default ACL,
        // especially when public/ is created after the domain was attached.
        // Refresh only the affected served path. PHP remains untouched.
        if err == nil && cfg.Caddy != nil && app.Type != "php" {
            rel := ""
            syncAccess := false
            switch action {
            case "upload":
                // A batch may include several files. Sync each one below.
                syncAccess = len(uploadedNames) > 0
            case "create", "save", "mkdir", "chmod":
                if action != "chmod" || (r.FormValue("mode") != "0600" && r.FormValue("mode") != "0700") {
                    rel = path.Join(folder, name)
                    syncAccess = true
                }
            case "move", "copy":
                rel = r.FormValue("destination")
                syncAccess = true
            case "archive":
                rel = path.Join(folder, r.FormValue("archive_name"))
                syncAccess = true
            case "bulk_move":
                rel = r.FormValue("destination")
                syncAccess = true
            case "move_contents":
                rel = folder
                if name == "" { rel = path.Dir(folder) }
                syncAccess = true
            case "extract":
                rel = folder
                if rel == "" && app.Type != "static" { rel = "public" }
                syncAccess = true
            }
            if syncAccess {
                targets := []string{rel}
                if action == "upload" {
                    targets = make([]string, 0, len(uploadedNames))
                    for _, filename := range uploadedNames { targets = append(targets, path.Join(folder, filename)) }
                }
                for _, target := range targets {
                    if accessErr := cfg.Caddy.EnsureManagedFileAccess(r.Context(), app.ID, app.Root, target); accessErr != nil {
                        err = fmt.Errorf("file operation completed, but Caddy read-access ACL failed: %w", accessErr)
                        break
                    }
                }
            }
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

// fileManagerIcon renders local, inline Lucide-style SVGs. No icon CDN or
// emoji fallback is required.
func fileManagerIcon(kind string) string {
    paths := map[string]string{
        "folder": `<path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`,
        "file": `<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/>`,
        "image": `<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="m21 15-5-5L5 21"/>`,
        "archive": `<rect x="4" y="2" width="16" height="20" rx="2"/><path d="M12 2v4m0 3v3m0 3v3m-3 0h6"/>`,
        "code": `<path d="m8 8-4 4 4 4m8-8 4 4-4 4m-3-11-2 14"/>`,
        "link": `<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`,
    }
    source, ok := paths[kind]
    if !ok { source = paths["file"] }
    return `<svg class="fileman-icon fileman-icon-` + kind + `" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + source + `</svg>`
}

func fileManagerEditable(name string, size int64) bool {
    if size > filemanager.MaxEditBytes { return false }
    switch strings.ToLower(path.Ext(name)) {
    case ".go", ".js", ".ts", ".html", ".htm", ".css", ".json", ".php", ".yaml", ".yml",
        ".txt", ".md", ".xml", ".svg", ".sql", ".sh", ".ini", ".toml", ".csv", ".env",
        ".conf", ".log", ".py", ".rb", ".gitignore", ".dockerignore":
        return true
    }
    if !strings.Contains(name, ".") {
        switch strings.ToLower(name) {
        case "dockerfile", "makefile", "caddyfile", "readme", "license":
            return true
        }
    }
    return strings.HasPrefix(name, ".env.")
}

func fileManagerSize(size int64) string {
    if size < 1024 { return fmt.Sprintf("%d B", size) }
    value := float64(size)
    for _, unit := range []string{"KiB", "MiB", "GiB", "TiB"} {
        value /= 1024
        if value < 1024 { return fmt.Sprintf("%.1f %s", value, unit) }
    }
    return fmt.Sprintf("%.1f PiB", value/1024)
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
            rows.WriteString(`<tr class="fileman-parent"><td></td><td><a class="fileman-entry" href="`+appFilesURL(app.ID, parent)+`">`+fileManagerIcon("folder")+`<span>..</span></a></td><td>Parent folder</td><td>—</td><td>—</td><td>—</td><td></td></tr>`)
        }
        for _, item := range items {
            name := html.EscapeString(item.Name)
            kind := "File"
            size := fileManagerSize(item.Size)
            mode := fmt.Sprintf("%04o", item.Mode.Perm())
            icon := "file"
            if item.Directory {
                kind = "Directory"
                size = "—"
                icon = "folder"
            } else if item.Symlink {
                kind = "Symlink (not followed)"
                size = "—"
                mode = "—"
                icon = "link"
            } else {
                lower := strings.ToLower(item.Name)
                switch {
                case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
                    icon = "archive"
                case strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"), strings.HasSuffix(lower, ".gif"), strings.HasSuffix(lower, ".webp"), strings.HasSuffix(lower, ".svg"):
                    icon = "image"
                case strings.HasSuffix(lower, ".go"), strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".css"), strings.HasSuffix(lower, ".json"), strings.HasSuffix(lower, ".php"), strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"), strings.HasSuffix(lower, ".sh"):
                    icon = "code"
                }
            }
            entry := `<span class="fileman-entry">`+fileManagerIcon(icon)+`<strong>`+name+`</strong></span>`
            if item.Directory {
                next := item.Name
                if folder != "" && folder != "." { next = folder + "/" + item.Name }
                entry = `<a class="fileman-entry" href="`+appFilesURL(app.ID, next)+`">`+fileManagerIcon(icon)+`<strong>`+name+`</strong></a>`
            }
            var actions strings.Builder
            if !item.Directory && !item.Symlink {
                query := url.Values{}
                query.Set("dir", folder)
                query.Set("name", item.Name)
                actions.WriteString(`<a class="secondary compact-action" href="`+actionURL+`/download?`+html.EscapeString(query.Encode())+`" hx-boost="false">Download</a>`)
                lower := strings.ToLower(item.Name)
                if strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".gif") || strings.HasSuffix(lower, ".webp") {
                    actions.WriteString(`<a class="secondary compact-action" href="`+actionURL+`/preview?`+html.EscapeString(query.Encode())+`" target="_blank" rel="noopener" hx-boost="false">Preview</a>`)
                }
                query.Del("name")
                query.Set("edit", item.Name)
                if fileManagerEditable(item.Name, item.Size) {
                    actions.WriteString(`<a class="secondary compact-action" href="`+actionURL+`?`+html.EscapeString(query.Encode())+`">Edit</a>`)
                }
                if strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
                    actions.WriteString(`<form method="post" action="`+actionURL+`">`+fileHidden("extract", folder, item.Name)+`<button class="secondary compact-action" onclick="return confirm('Extract this archive here? Existing files will not be overwritten.')">Extract</button></form>`)
                }
            }
            if !item.Symlink {
                archiveName := item.Name + ".zip"
                actions.WriteString(`<form method="post" action="`+actionURL+`" class="fileman-archive-action">`+fileHidden("archive", folder, "")+
                    `<input type="hidden" name="selected" value="`+name+`"><input type="hidden" name="archive_name" value="`+html.EscapeString(archiveName)+`"><button class="secondary compact-action" type="submit" title="Compress as ZIP">ZIP</button></form>`)
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
            if !item.Symlink {
                modes := []string{"0600", "0640", "0644", "0700", "0750", "0755"}
                if item.Directory { modes = []string{"0700", "0750", "0755"} }
                var selectModes strings.Builder
                known := false
                for _, value := range modes {
                    if value == mode { known = true; break }
                }
                if !known {
                    fmt.Fprintf(&selectModes, `<option value="" selected disabled>Current %s (ACL)</option>`, html.EscapeString(mode))
                }
                for _, value := range modes {
                    selected := ""
                    if value == mode { selected = " selected" }
                    fmt.Fprintf(&selectModes, `<option value="%s"%s>%s</option>`, value, selected, value)
                }
                actions.WriteString(`<form method="post" action="`+actionURL+`">`+
                    fileHidden("chmod", folder, item.Name)+
                    `<label>Permissions (octal)</label><div class="fileman-mode-row"><select name="mode" aria-label="File permissions" required>`+selectModes.String()+`</select><button class="secondary compact-action">Apply</button></div></form>`)
            }
            if item.Directory {
                actions.WriteString(`<form method="post" action="`+actionURL+`" onsubmit="return confirm('Move all contents of this folder here? Existing names will not be replaced.')">
                    `+fileHidden("move_contents", folder, item.Name)+`
                    <button class="secondary compact-action" type="submit">Move contents here</button></form>`)
            }
            actions.WriteString(`<form method="post" action="`+actionURL+`" onsubmit="return confirm('Delete this item permanently?')">
                `+fileHidden("delete", folder, item.Name)+`<button class="danger compact-action">Delete</button></form></div></details>`)
            modified := "—"
            if !item.ModifiedAt.IsZero() { modified = item.ModifiedAt.UTC().Format("2006-01-02 15:04") }
            fmt.Fprintf(&rows, `<tr class="fileman-row"><td><input class="fileman-select-item" type="checkbox" form="fileman-bulk-form" name="selected" value="%s" aria-label="Select %s"></td><td>%s</td><td>%s</td><td class="fileman-size">%s</td><td><code class="fileman-mode">%s</code></td><td class="fileman-modified">%s</td><td><div class="actions fileman-actions">%s</div></td></tr>`,
                name, name, entry, html.EscapeString(kind), html.EscapeString(size), html.EscapeString(mode), modified, actions.String())
        }
        if len(items) == 0 { rows.WriteString(`<tr><td colspan="7" class="empty">This folder is empty.</td></tr>`) }
        if limited { alert += `<div class="alert">Only the first 400 entries are shown and selectable. "Move all contents up" moves the entire folder, including entries not displayed.</div>` }
        parentDestination := ""
        if folder != "" && folder != "." {
            parentDestination = path.Dir(folder)
            if parentDestination == "." { parentDestination = "" }
        }
        defaultTarget := ""
        if folder != "" && folder != "." {
            defaultTarget = parentDestination
            if defaultTarget == "" { defaultTarget = "." }
        }
        var liftAction string
        if folder != "" && folder != "." {
            liftAction = `<form method="post" action="` + actionURL + `" class="fileman-lift" onsubmit="return confirm('Move the entire contents of this folder into its parent? Existing names will not be overwritten.')">` +
                fileHidden("move_contents", folder, "") +
                `<button type="submit" class="secondary">Move all contents up</button><span class="note">Moves subfolders as a whole; the current folder stays empty</span></form>`
        }
        bulkControls := fmt.Sprintf(`<form id="fileman-bulk-form" method="post" action="%s" class="fileman-bulk">
            <input type="hidden" name="dir" value="%s">
            <span id="fileman-selection-count" class="fileman-selection-count">0 selected</span>
            <label for="fileman-bulk-destination" class="fileman-bulk-target">Destination folder (from app root)
                <input id="fileman-bulk-destination" name="destination" placeholder="public/assets or . for root" value="%s">
            </label>
            <button class="secondary" type="submit" name="action" value="bulk_move">Move selected</button>
            <label for="fileman-bulk-archive-name" class="fileman-archive-target">ZIP filename
                <input id="fileman-bulk-archive-name" name="archive_name" maxlength="255" value="selection.zip">
            </label>
            <button class="secondary" type="submit" name="action" value="archive">Compress ZIP</button>
            <button class="danger" type="submit" name="action" value="bulk_delete" onclick="return confirm('Delete all selected files and folders permanently?')">Delete selected</button>
        </form>`, actionURL, html.EscapeString(folder), html.EscapeString(defaultTarget))

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
            %s
            %s
            <div class="fileman-table"><table><thead><tr><th class="fileman-check-column"><input id="fileman-select-all" type="checkbox" aria-label="Select all visible items"></th><th>Name</th><th>Type</th><th>Size</th><th>Permissions</th><th>Modified (UTC)</th><th>Actions</th></tr></thead>
            <tbody>%s</tbody></table></div>
            <p class="note">Paths are relative to the application root. Existing files are never overwritten by moves; linked directories are not followed. Select all affects only displayed entries.</p>
            
        </section>`, actionURL, fileHidden("upload", folder, ""), actionURL, fileHidden("mkdir", folder, ""), base+func()string{ if strings.Contains(base, "?") { return "&new=1" }; return "?new=1" }(), liftAction, bulkControls, rows.String())
    }
    return header + `

    <main class="shell app-shell">
        <div class="app-page-head"><div>
            <a class="app-breadcrumb" href="/apps/`+strconv.FormatInt(app.ID,10)+`">`+html.EscapeString(app.Name)+`</a>
            <h1>Files</h1><p class="sub">`+html.EscapeString(app.Root)+`</p>
        </div><a class="secondary" href="/apps/`+strconv.FormatInt(app.ID,10)+`">Application</a></div>
        <nav class="fileman-crumbs" aria-label="Folder path">`+crumbs.String()+`</nav>
        `+alert+content.String()+`
    </main></body></html>`, nil
}
