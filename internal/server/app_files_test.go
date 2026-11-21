package server

import (
    "bytes"
    "io"
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
    files, err := filemanager.Open(root, account.Username)
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
    if !strings.Contains(page, "Conflict") || !strings.Contains(page, draft) {
        t.Fatal("draft editor content disappeared after an error")
    }
    if !strings.Contains(page, `name="base_sha256" value="`+staleHash+`"`) {
        t.Fatal("stale digest was silently replaced")
    }
}
