package server

import (
    "bytes"
    "io"
    "log/slog"
    "mime/multipart"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
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
    cookie, token := loginAndGetCSRF(t, handler)
    for _, tc := range []struct{token string; want int}{
        {token: token, want: http.StatusSeeOther},
        {token: "wrong", want: http.StatusForbidden},
        {token: "", want: http.StatusForbidden},
    } {
        body, mediaType := multipartCSRFRequest(t, tc.token)
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
