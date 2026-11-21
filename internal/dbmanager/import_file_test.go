package dbmanager

import (
    "bytes"
    "compress/gzip"
    "context"
    "errors"
    "io"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestSQLUploadFormat(t *testing.T) {
    for _, name := range []string{"dump.sql", "dump.SQL", "site.sql.gz", "site.SQL.GZ"} {
        compressed, err := sqlUploadFormat(name)
        if err != nil { t.Fatalf("%q: %v", name, err) }
        if compressed != strings.HasSuffix(strings.ToLower(name), ".sql.gz") {
            t.Fatalf("wrong format for %q", name)
        }
    }
    for _, name := range []string{"dump.zip", "dump.gz", "dump.sql.txt", "", "dump.sql.gz.bak"} {
        if _, err := sqlUploadFormat(name); err == nil { t.Fatalf("accepted %q", name) }
    }
}

func TestStageAndValidateSQLUpload(t *testing.T) {
    text := "CREATE TABLE example (id int);\n"
    path, err := stageSQLFile(strings.NewReader(text))
    if err != nil { t.Fatal(err) }
    defer os.Remove(path)
    info, err := os.Stat(path)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0600 || info.Size() != int64(len(text)) {
        t.Fatalf("bad staged upload: %o %d", info.Mode().Perm(), info.Size())
    }
    if err := validateSQLFile(path, false); err != nil { t.Fatal(err) }
    if _, err := stageSQLFile(strings.NewReader("")); err == nil {
        t.Fatal("empty upload accepted")
    }

    var buf bytes.Buffer
    zw := gzip.NewWriter(&buf)
    if _, err := io.WriteString(zw, text); err != nil { t.Fatal(err) }
    if err := zw.Close(); err != nil { t.Fatal(err) }
    compressed, err := stageSQLFile(bytes.NewReader(buf.Bytes()))
    if err != nil { t.Fatal(err) }
    defer os.Remove(compressed)
    if err := validateSQLFile(compressed, true); err != nil { t.Fatal(err) }
    data, err := os.ReadFile(compressed)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(compressed, data[:len(data)-5], 0600); err != nil { t.Fatal(err) }
    if err := validateSQLFile(compressed, true); err == nil {
        t.Fatal("truncated gzip accepted")
    }

    empty := filepath.Join(t.TempDir(), "empty.sql")
    if err := os.WriteFile(empty, nil, 0600); err != nil { t.Fatal(err) }
    if err := validateSQLFile(empty, false); err == nil { t.Fatal("empty SQL accepted") }
}

func TestSQLCommandUsesManagedDBUserAndUnprivilegedOSUser(t *testing.T) {
    t.Setenv("OGP_ADMIN_PASSWORD", "panel-secret-not-for-child")
    for _, tc := range []struct {
        engine string
        required []string
        passwordName string
    }{
        {"mysql", []string{"runuser", "-u", "nobody", "mysql", "--binary-mode", "--database=appdb"}, "MYSQL_PWD=db-secret"},
        {"postgres", []string{"runuser", "-u", "nobody", "psql", "-X", "ON_ERROR_STOP=1"}, "PGPASSWORD=db-secret"},
    } {
        cmd, err := sqlFileCommand(context.Background(), Database{
            Engine: tc.engine, Name: "appdb", User: "siteuser", Password: "db-secret",
        })
        if err != nil { t.Fatal(err) }
        args := strings.Join(cmd.Args, " ")
        for _, part := range tc.required {
            if !strings.Contains(args, part) { t.Errorf("%s missing %q from %q", tc.engine, part, args) }
        }
        env := strings.Join(cmd.Env, "\n")
        if !strings.Contains(env, tc.passwordName) {
            t.Errorf("%s missing its database credentials", tc.engine)
        }
        if strings.Contains(args, "db-secret") || strings.Contains(env, "panel-secret-not-for-child") {
            t.Errorf("%s exposed credentials in argv or inherited panel secrets", tc.engine)
        }
    }
}

func TestSQLFileImportRejectsConcurrentOperation(t *testing.T) {
    manager := &Manager{}
    manager.operationMu.Lock()
    defer manager.operationMu.Unlock()
    err := manager.StartFileImport(1, "data.sql", strings.NewReader("select 1;"), false)
    if !errors.Is(err, errOperationInProgress) { t.Fatalf("want busy error, got %v", err) }
}
