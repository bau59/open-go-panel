package filemanager

import (
    "archive/zip"
    "bytes"
    "io"
    "os"
    "os/user"
    "path/filepath"
    "strings"
    "testing"
)

func openTestManager(t *testing.T) (*Manager, string) {
    t.Helper()
    root := t.TempDir()
    account, err := user.Current()
    if err != nil { t.Fatal(err) }
    manager, err := Open(root, account.Username)
    if err != nil { t.Fatal(err) }
    t.Cleanup(func(){ _ = manager.Close() })
    return manager, root
}

func TestValidPaths(t *testing.T) {
    for _, input := range []string{"", ".", "public", "public/img", ".env", "README.md"} {
        if !validPath(input) { t.Errorf("valid path rejected: %q", input) }
    }
    for _, input := range []string{"/etc", "../etc", "public/../../etc", "a//b", "a/./b", "a\\b", "a/..", "a/\x00"} {
        if validPath(input) { t.Errorf("invalid path accepted: %q", input) }
    }
    for _, input := range []string{"", ".", "..", "../bad", "a/b", "a\\b", "a\n"} {
        if validName(input) { t.Errorf("invalid basename accepted: %q", input) }
    }
}

func TestFileOperations(t *testing.T) {
    manager, _ := openTestManager(t)
    if err := manager.Mkdir("", "public"); err != nil { t.Fatal(err) }
    if err := manager.Save("public", "index.php", "<?php echo 1;\n", true); err != nil { t.Fatal(err) }
    if err := manager.Save("public", "index.php", "<?php echo 2;\n", true); err == nil {
        t.Fatal("create must not overwrite")
    }
    data, err := manager.ReadText("public", "index.php")
    if err != nil || data != "<?php echo 1;\n" { t.Fatalf("unexpected read: %q %v", data, err) }
    if err := manager.Save("public", "index.php", "<?php echo 2;\n", false); err != nil { t.Fatal(err) }
    if err := manager.Copy("public", "index.php", "copy.php"); err != nil { t.Fatal(err) }
    if err := manager.Move("public", "index.php", "public/main.php"); err != nil { t.Fatal(err) }
    if err := manager.Move("public", "main.php", "../escape"); err == nil {
        t.Fatal("path escaping should be rejected")
    }
    if err := manager.Upload("public", "asset.txt", strings.NewReader("hello")); err != nil { t.Fatal(err) }
    if err := manager.Upload("public", "asset.txt", strings.NewReader("overwrite")); err == nil {
        t.Fatal("uploads must not overwrite existing data")
    }
    file, info, err := manager.Download("public", "asset.txt")
    if err != nil { t.Fatal(err) }
    downloaded, err := io.ReadAll(file)
    file.Close()
    if err != nil || info.Size() != 5 || string(downloaded) != "hello" {
        t.Fatalf("download: %q %v %v", downloaded, info, err)
    }
    if err := manager.Delete("public", "asset.txt"); err != nil { t.Fatal(err) }
    if err := manager.Delete("", "public"); err != nil { t.Fatal(err) }
    if _, _, err := manager.List("public"); err == nil { t.Fatal("deleted directory still exists") }
}

func TestSymlinkCannotEscapeApplication(t *testing.T) {
    manager, root := openTestManager(t)
    outside := t.TempDir()
    if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("not yours"), 0600); err != nil { t.Fatal(err) }
    if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil { t.Fatal(err) }
    if err := os.Symlink(filepath.Join(outside,"secret"), filepath.Join(root, "secret")); err != nil { t.Fatal(err) }
    if _, _, err := manager.List("linked"); err == nil { t.Fatal("directory symlink followed") }
    if _, err := manager.ReadText("", "secret"); err == nil { t.Fatal("file symlink followed") }
    if err := manager.Save("", "secret", "overwrite", false); err == nil { t.Fatal("symlink overwritten through editor") }
    if err := manager.Move("", "secret", "secret-moved"); err != nil { t.Fatal(err) }
    data, err := os.ReadFile(filepath.Join(outside, "secret"))
    if err != nil || string(data) != "not yours" { t.Fatal("external file changed") }
}

func makeZip(t *testing.T, entries map[string]string) []byte {
    t.Helper()
    var buf bytes.Buffer
    archive := zip.NewWriter(&buf)
    for name, value := range entries {
        w, err := archive.Create(name)
        if err != nil { t.Fatal(err) }
        if _, err := io.WriteString(w, value); err != nil { t.Fatal(err) }
    }
    if err := archive.Close(); err != nil { t.Fatal(err) }
    return buf.Bytes()
}

func TestSafeArchiveExtraction(t *testing.T) {
    manager, root := openTestManager(t)
    good := makeZip(t, map[string]string{"public/index.php": "<?php echo 1;", "config.txt": "ok"})
    if err := manager.Upload("", "site.zip", bytes.NewReader(good)); err != nil { t.Fatal(err) }
    count, err := manager.Extract("", "site.zip")
    if err != nil || count != 2 { t.Fatalf("extract: %d %v", count, err) }
    text, err := manager.ReadText("public", "index.php")
    if err != nil || text != "<?php echo 1;" { t.Fatalf("archive data: %q %v", text, err) }
    if _, err := manager.Extract("", "site.zip"); err == nil {
        t.Fatal("extract must not overwrite existing files")
    }
    bad := makeZip(t, map[string]string{"../escape.php": "bad"})
    if err := manager.Upload("", "bad.zip", bytes.NewReader(bad)); err != nil { t.Fatal(err) }
    if _, err := manager.Extract("", "bad.zip"); err == nil { t.Fatal("zip slip should fail") }
    if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.php")); !os.IsNotExist(err) {
        t.Fatalf("zip slip created a file outside root: %v", err)
    }
}

func TestTextEditorRejectsBinary(t *testing.T) {
    manager, _ := openTestManager(t)
    if err := manager.Save("", "bad.bin", string([]byte{0,1,2}), true); err != nil {
        // A new file can be binary only via upload, never via text editor.
        return
    }
    t.Fatal("text editor accepted NUL bytes")
}
