package server

import (
    "bytes"
    "io"
    "html"
    "os/user"
    "log/slog"
    "mime/multipart"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    panelapp "github.com/bau59/open-go-panel/internal/app"
    "github.com/bau59/open-go-panel/internal/filemanager"
)

func multipartCSRFRequest(t *testing.T, token string) (*bytes.Buffer, string) {
    t.Helper()
    var body bytes.Buffer
    writer := multipart.NewWriter(&body)
    if err := writer.WriteField(csrfFormField, token); err != nil { t.Fatal(err) }
    if err := writer.WriteField("action", "upload"); err != nil { t.Fatal(err) }
    file, err := writer.CreateFormFile("file", "text.txt")
    if err != nil { t.Fatal(err) }
    if _, err := io.Copy(file, strings.NewReader("small upload")); err != nil { t.Fatal(err) }
    if err := writer.Close(); err != nil { t.Fatal(err) }
    return &body, writer.FormDataContentType()
}

func TestMultipartMutationRequiresCSRF(t *testing.T) {
    handler := New(Config{
        Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
        AdminUser: "admin",
        AdminPassword: "secret",
    })
    for _, tc := range []struct{token string; want int}{
        {token: "valid", want: http.StatusSeeOther},
        {token: "wrong", want: http.StatusForbidden},
        {token: "", want: http.StatusForbidden},
    } {
        // Logout invalidates its session; use a fresh login per case.
        cookie, token := loginAndGetCSRF(t, handler)
        if tc.token != "valid" { token = tc.token }
        body, mediaType := multipartCSRFRequest(t, token)
        req := httptest.NewRequest(http.MethodPost, "/logout", body)
        req.Header.Set("Content-Type", mediaType)
        req.AddCookie(cookie)
        rec := httptest.NewRecorder()
        handler.ServeHTTP(rec, req)
        if rec.Code != tc.want { t.Fatalf("multipart POST status: got %d, want %d", rec.Code, tc.want) }
    }
}

func TestFileManagerHTMLForms(t *testing.T) {
    actual := fileHidden("create", "public", "")
    if strings.Contains(actual, `name="name"`) {
        t.Fatal("hidden empty filename overrides new file input")
    }
    if !strings.Contains(actual, `name="dir" value="public"`) {
        t.Fatal("folder not preserved")
    }
    path := appFilesURL(21, "public/my files")
    if path != "/apps/21/files?dir=public%2Fmy+files" {
        t.Fatalf("file browser path not encoded: %q", path)
    }
}

func TestFileEditorPreservesDraftAndRevision(t *testing.T) {
    root := t.TempDir()
    account, err := user.Current()
    if err != nil { t.Fatal(err) }
    files, err := filemanager.Open(root)
    if err != nil { t.Fatal(err) }
    defer files.Close()
    if err := files.Save("", "index.php", "<?php echo 'old';", true); err != nil { t.Fatal(err) }
    app := panelapp.App{ID: 12, Name: "site", Root: root, User: account.Username}
    page, err := fileManagerPage(app, files, "", "index.php", false, "", "")
    if err != nil { t.Fatal(err) }
    if !strings.Contains(page, `name="base_sha256"`) { t.Fatal("original file digest missing from editor") }

    draft := "<?php echo 'new';"
    staleHash := strings.Repeat("a", 64)
    page, err = fileManagerPage(app, files, "", "index.php", false, "Conflict", "", draft, staleHash)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(page, "Conflict") || !strings.Contains(page, html.EscapeString(draft)) {
        t.Fatal("draft editor content disappeared after an error")
    }
    if !strings.Contains(page, `name="base_sha256" value="`+staleHash+`"`) {
        t.Fatal("stale digest was silently replaced")
    }
}

func TestFileManagerToolbarControlsStayAligned(t *testing.T) {
    root := t.TempDir()
    account, err := user.Current()
    if err != nil { t.Fatal(err) }
    files, err := filemanager.Open(root)
    if err != nil { t.Fatal(err) }
    defer files.Close()

    app := panelapp.App{ID: 29, Name: "site", Root: root, User: account.Username}
    page, err := fileManagerPage(app, files, "", "", false, "", "")
    if err != nil { t.Fatal(err) }

    for _, expected := range []string{
        `class="fileman-tools"`,
        `class="fileman-control-row"`,
        `class="file-upload-input"`,
        `name="client_size" value=""`,
        `onchange="this.form.elements.client_size.value`,
        `for="fileman-upload-file"`,
        `id="fileman-upload-file"`,
        `for="fileman-folder-name"`,
        `id="fileman-folder-name"`,
        `class="fileman-tool fileman-tool-new"`,
        `file-upload-input::file-selector-button`,
        `Upload file <span>Max 1 GiB</span>`,
    } {
        if !strings.Contains(page, expected) {
            t.Errorf("toolbar is missing %q", expected)
        }
    }
    if strings.Count(page, `class="fileman-control-row"`) != 2 {
        t.Fatal("upload and folder-create forms must use the same input/button layout")
    }
    for _, path := range []string{
        `method="post" action="/apps/29/files" enctype="multipart/form-data"`,
        `name="file" required`,
        `name="name" required maxlength="255"`,
    } {
        if !strings.Contains(page, path) { t.Errorf("existing file operation missing: %q", path) }
    }
}

func TestFileManagerBulkControlsAndFolderLift(t *testing.T) {
    root := t.TempDir()
    files, err := filemanager.Open(root)
    if err != nil { t.Fatal(err) }
    defer files.Close()
    if err := files.Mkdir("", "kirbi.ru"); err != nil { t.Fatal(err) }
    if err := files.Save("", "test.zip", "zip contents", true); err != nil { t.Fatal(err) }
    app := panelapp.App{ID: 7, Name: "kirbi", Root: root}
    page, err := fileManagerPage(app, files, "", "", false, "", "")
    if err != nil { t.Fatal(err) }
    for _, want := range []string{
        `id="fileman-bulk-form"`,
        `name="action" value="bulk_move"`,
        `name="action" value="bulk_delete"`,
        `class="fileman-select-item"`,
        `form="fileman-bulk-form" name="selected" value="kirbi.ru"`,
        `id="fileman-select-all"`,
        `name="destination"`,
        `Move contents here`,
        `name="action" value="move_contents"`,
        `Select all affects only displayed entries`,
    } {
        if !strings.Contains(page, want) {
            t.Errorf("bulk file manager is missing %q", want)
        }
    }
    if strings.Count(page, `id="fileman-bulk-form"`) != 1 {
        t.Fatal("bulk form must exist exactly once")
    }
    if err := files.Save("kirbi.ru", "index.php", "<?php", true); err != nil { t.Fatal(err) }
    nested, err := fileManagerPage(app, files, "kirbi.ru", "", false, "", "")
    if err != nil { t.Fatal(err) }
    if !strings.Contains(nested, "Move all contents up") {
        t.Fatal("missing one-click promotion from inside extracted folder")
    }
    if !strings.Contains(nested, `name="destination" placeholder="public/assets or . for root" value="."`) {
        t.Fatal("bulk destination must default to app root from top-level folder")
    }
}
