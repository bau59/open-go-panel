package filemanager

import (
    "archive/zip"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "os"
    "path"
    "strings"

    "golang.org/x/sys/unix"
)

// Archive creates a ZIP containing selected files and folders in the current
// directory, as the application owner. It never follows links or overwrites
// existing files. Extraction and archiving share the same resource budgets.
func (m *Manager) Archive(folder string, names []string, archiveName string) (int, error) {
    if err := checkSelectedNames(names, MaxBulkSelection); err != nil { return 0, err }
    if !validName(archiveName) || !strings.HasSuffix(strings.ToLower(archiveName), ".zip") {
        return 0, errors.New("archive filename must end with .zip")
    }
    for _, name := range names {
        if name == archiveName { return 0, errors.New("archive cannot include itself") }
    }
    fd, err := m.directory(folder)
    if err != nil { return 0, err }
    defer unix.Close(fd)
    var existing unix.Stat_t
    if err := unix.Fstatat(fd, archiveName, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
        return 0, errors.New("archive destination already exists")
    } else if !errors.Is(err, unix.ENOENT) {
        return 0, fmt.Errorf("inspect archive destination: %w", err)
    }

    random := make([]byte, 12)
    if _, err := rand.Read(random); err != nil { return 0, err }
    tempName := ".ogp-" + hex.EncodeToString(random) + ".tmp"
    tmpFD, err := unix.Openat(fd, tempName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
    if err != nil { return 0, fmt.Errorf("create temporary archive: %w", err) }
    defer unix.Unlinkat(fd, tempName, 0)
    output := os.NewFile(uintptr(tmpFD), tempName)
    closed := false
    defer func() { if !closed { _ = output.Close() } }()

    writer := zip.NewWriter(&archiveLimitedWriter{target: output, remaining: MaxArchiveBytes})
    bytes := int64(0)
    count := 0
    for _, name := range names {
        if err := archiveZIPEntry(fd, name, name, writer, &bytes, &count, 0); err != nil {
            _ = writer.Close()
            return count, err
        }
    }
    if err := writer.Close(); err != nil { return count, fmt.Errorf("finish ZIP archive: %w", err) }
    mode := uint32(0640)
    if folder == "public" || strings.HasPrefix(folder, "public/") { mode = 0644 }
    if err := unix.Fchmod(tmpFD, mode); err != nil { return count, err }
    if err := output.Sync(); err != nil { return count, err }
    if err := output.Close(); err != nil { closed = true; return count, err }
    closed = true
    if err := unix.Renameat2(fd, tempName, fd, archiveName, unix.RENAME_NOREPLACE); err != nil {
        return count, fmt.Errorf("store archive: %w", err)
    }
    return count, nil
}

type archiveLimitedWriter struct {
    target io.Writer
    remaining int64
}

func (w *archiveLimitedWriter) Write(p []byte) (int, error) {
    if int64(len(p)) > w.remaining { return 0, errors.New("archive exceeds the 1 GiB output limit") }
    n, err := w.target.Write(p)
    w.remaining -= int64(n)
    return n, err
}

func archiveZIPEntry(parentFD int, name, entryName string, writer *zip.Writer, bytes *int64, count *int, depth int) error {
    if !validName(name) || depth > maxDepth || *count >= MaxArchiveEntries {
        return errors.New("archive depth or entry limit reached")
    }
    var info unix.Stat_t
    if err := unix.Fstatat(parentFD, name, &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
        return fmt.Errorf("inspect %q: %w", entryName, err)
    }
    switch info.Mode & unix.S_IFMT {
    case unix.S_IFDIR:
        childFD, err := unix.Openat2(parentFD, name, &unix.OpenHow{
            Flags: uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),
            Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV,
        })
        if err != nil { return fmt.Errorf("open directory %q: %w", entryName, err) }
        defer unix.Close(childFD)
        header := &zip.FileHeader{Name: entryName + "/", Method: zip.Store}
        header.SetMode(os.ModeDir | os.FileMode(info.Mode&0777))
        if _, err := writer.CreateHeader(header); err != nil { return err }
        *count++
        children, err := readAllNames(childFD, MaxArchiveEntries-*count)
        if err != nil { return fmt.Errorf("read directory %q: %w", entryName, err) }
        for _, child := range children {
            if err := archiveZIPEntry(childFD, child, path.Join(entryName, child), writer, bytes, count, depth+1); err != nil {
                return err
            }
        }
        return nil
    case unix.S_IFREG:
        fileFD, err := unix.Openat2(parentFD, name, &unix.OpenHow{
            Flags: uint64(unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK),
            Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV,
        })
        if err != nil { return fmt.Errorf("open file %q: %w", entryName, err) }
        input := os.NewFile(uintptr(fileFD), name)
        defer input.Close()
        var current unix.Stat_t
        if err := unix.Fstat(fileFD, &current); err != nil { return err }
        if current.Mode&unix.S_IFMT != unix.S_IFREG { return errors.New("archive source is not a regular file") }
        if current.Size < 0 || current.Size > MaxUploadBytes || current.Size > MaxExtractBytes-*bytes {
            return fmt.Errorf("archive source %q exceeds input size limit", entryName)
        }
        header := &zip.FileHeader{Name: entryName, Method: zip.Deflate}
        header.SetMode(os.FileMode(current.Mode&0777))
        output, err := writer.CreateHeader(header)
        if err != nil { return err }
        copied, err := io.Copy(output, io.LimitReader(input, current.Size+1))
        if err != nil { return fmt.Errorf("compress %q: %w", entryName, err) }
        if copied != current.Size { return fmt.Errorf("source %q changed during archiving", entryName) }
        *bytes += copied
        *count++
        return nil
    default:
        return fmt.Errorf("archive source %q is a symlink or unsupported file type", entryName)
    }
}
