package filemanager

import (
    "archive/zip"
    "io"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestFilePermissionsAndPublicDefaults(t *testing.T) {
    root := t.TempDir()
    files, err := Open(root)
    if err != nil { t.Fatal(err) }
    defer files.Close()

    if err := files.Mkdir("", "public"); err != nil { t.Fatal(err) }
    if err := files.Save("public", "picture.png", "png-data", true); err != nil { t.Fatal(err) }
    publicFile := filepath.Join(root, "public", "picture.png")
    info, err := os.Stat(publicFile)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0640 { t.Fatalf("public asset mode=%04o, want 0640 before ACL grant", info.Mode().Perm()) }
    if err := files.Save("", "secrets.env", "secret", true); err != nil { t.Fatal(err) }
    privateInfo, err := os.Stat(filepath.Join(root, "secrets.env"))
    if err != nil { t.Fatal(err) }
    if privateInfo.Mode().Perm() != 0640 { t.Fatalf("private mode=%04o, want 0640", privateInfo.Mode().Perm()) }

    if err := files.Chmod("public", "picture.png", 0600); err != nil { t.Fatal(err) }
    info, err = os.Stat(publicFile)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0600 { t.Fatalf("chmod mode=%04o", info.Mode().Perm()) }
    if err := files.Chmod("public", "picture.png", 0777); err == nil { t.Fatal("world-writable mode accepted") }
    if err := files.Chmod("", "public", 0644); err == nil { t.Fatal("directory with no execute permission accepted") }

    outside := filepath.Join(t.TempDir(), "outside")
    if err := os.WriteFile(outside, []byte("private"), 0600); err != nil { t.Fatal(err) }
    if err := os.Symlink(outside, filepath.Join(root, "shortcut")); err != nil { t.Fatal(err) }
    if err := files.Chmod("", "shortcut", 0644); err == nil { t.Fatal("chmod followed a symlink") }
    untouched, err := os.Stat(outside)
    if err != nil { t.Fatal(err) }
    if untouched.Mode().Perm() != 0600 { t.Fatal("outside target mode changed") }
}

func TestZIPCompressionAndBoundaries(t *testing.T) {
    root := t.TempDir()
    files, err := Open(root)
    if err != nil { t.Fatal(err) }
    defer files.Close()
    if err := files.Mkdir("", "assets"); err != nil { t.Fatal(err) }
    if err := files.Save("assets", "style.css", "h1 { color: blue }", true); err != nil { t.Fatal(err) }
    if err := files.Save("", "readme.txt", "hello", true); err != nil { t.Fatal(err) }

    count, err := files.Archive("", []string{"assets", "readme.txt"}, "bundle.zip")
    if err != nil { t.Fatal(err) }
    if count != 3 { t.Fatalf("ZIP entries=%d, want 3", count) }
    zr, err := zip.OpenReader(filepath.Join(root, "bundle.zip"))
    if err != nil { t.Fatal(err) }
    defer zr.Close()
    got := make(map[string]string)
    for _, item := range zr.File {
        if item.FileInfo().IsDir() { continue }
        input, err := item.Open()
        if err != nil { t.Fatal(err) }
        data, err := io.ReadAll(input)
        _ = input.Close()
        if err != nil { t.Fatal(err) }
        got[item.Name] = string(data)
    }
    if got["assets/style.css"] != "h1 { color: blue }" || got["readme.txt"] != "hello" {
        t.Fatalf("archive contents mismatch: %+v", got)
    }
    if _, err := files.Archive("", []string{"readme.txt"}, "bundle.zip"); err == nil {
        t.Fatal("existing archive was overwritten")
    }
    if _, err := files.Archive("", []string{"readme.txt"}, "readme.txt"); err == nil {
        t.Fatal("archive without .zip accepted")
    }
    if err := os.Symlink(t.TempDir(), filepath.Join(root, "external")); err != nil { t.Fatal(err) }
    if _, err := files.Archive("", []string{"external"}, "should-not-exist.zip"); err == nil || !strings.Contains(err.Error(), "symlink") {
        t.Fatalf("expected symlink rejection, got %v", err)
    }
    if _, err := os.Stat(filepath.Join(root, "should-not-exist.zip")); !os.IsNotExist(err) {
        t.Fatal("partial archive left behind after failure")
    }
}
