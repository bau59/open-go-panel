package phpfpm

import (
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"
)

func TestPHPErrorLogPathIsPerApplication(t *testing.T) {
    for _, id := range []int64{1, 42, 999} {
        want := filepath.Join(phpErrorLogDir, "app-"+strconv.FormatInt(id, 10)+".log")
        if got := ErrorLogPath(id); got != want {
            t.Fatalf("ID %d: got %q, want %q", id, got, want)
        }
    }
}

func TestReadErrorLogRejectsInvalidID(t *testing.T) {
    if _, err := ReadErrorLog(0); err == nil { t.Fatal("invalid ID accepted") }
    if _, err := ReadErrorLog(-1); err == nil { t.Fatal("negative ID accepted") }
}

func TestBoundedLogReadRetainsLatestLines(t *testing.T) {
    filename := filepath.Join(t.TempDir(), "php.log")
    var b strings.Builder
    for i := 0; i < 500; i++ {
        b.WriteString("PHP Fatal error ")
        b.WriteString(strconv.Itoa(i))
        b.WriteByte('\n')
    }
    if err := os.WriteFile(filename, []byte(b.String()), 0600); err != nil { t.Fatal(err) }
    lines, err := readErrorLogFile(filename)
    if err != nil { t.Fatal(err) }
    if strings.Contains(lines, "PHP Fatal error 1\n") || !strings.Contains(lines, "PHP Fatal error 499") {
        t.Fatalf("unexpected log tail: %s", lines)
    }
    if n := len(strings.Split(lines, "\n")); n != phpLogReadMaxLines {
        t.Fatalf("expected %d lines, got %d", phpLogReadMaxLines, n)
    }
}

func TestReadErrorLogFileRejectsDirectory(t *testing.T) {
    if _, err := readErrorLogFile(t.TempDir()); err == nil {
        t.Fatal("directory was accepted as a log file")
    }
}

func TestReadErrorLogFileEmpty(t *testing.T) {
    filename := filepath.Join(t.TempDir(), "empty")
    if err := os.WriteFile(filename, nil, 0600); err != nil { t.Fatal(err) }
    text, err := readErrorLogFile(filename)
    if err != nil || text != "" { t.Fatalf("empty log: %q, %v", text, err) }
}

func TestReadErrorLogFileIsBoundedByBytes(t *testing.T) {
    filename := filepath.Join(t.TempDir(), "big.log")
    var b strings.Builder
    b.WriteString(strings.Repeat("x", phpLogReadMaxBytes))
    b.WriteString("\nPHP Fatal error: newest\n")
    if err := os.WriteFile(filename, []byte(b.String()), 0600); err != nil { t.Fatal(err) }
    text, err := readErrorLogFile(filename)
    if err != nil || text != "PHP Fatal error: newest" {
        t.Fatalf("unbounded or malformed log tail: %q, %v", text, err)
    }
}
