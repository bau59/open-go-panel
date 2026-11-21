package filemanager

import (
    "archive/tar"
    "archive/zip"
    "bytes"
    "compress/gzip"
    "encoding/binary"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "os"
    "path"
    "path/filepath"
    "sort"
    "strings"
    "unicode/utf8"

    "golang.org/x/sys/unix"
    "github.com/bau59/open-go-panel/internal/uploadlimits"
)

const (
    MaxEditBytes = 2 << 20
    MaxUploadBytes = uploadlimits.FileBytes
    MaxArchiveBytes = uploadlimits.FileBytes
    MaxExtractBytes = uploadlimits.ExpandedBytes
    // Typical PHP sites with Composer vendor trees regularly exceed 10k.
    // Keep an explicit bound against excessive inode creation.
    MaxArchiveEntries = 100000
    MaxDirectoryEntries = 400
    maxDepth = 32
)

type Entry struct {
    Name string
    Directory bool
    Symlink bool
    Size int64
    Mode os.FileMode
}

type Manager struct {
    root *os.File
}

func validName(name string) bool {
    return name != "" && name != "." && name != ".." && len(name) <= 255 &&
        !strings.ContainsAny(name, "/\\\x00\r\n")
}

func validPath(name string) bool {
    if name == "" || name == "." { return true }
    if filepath.IsAbs(name) || strings.ContainsAny(name, "\\\x00\r\n") { return false }
    for _, part := range strings.Split(name, "/") {
        if !validName(part) { return false }
    }
    return len(name) <= 4096
}

func Open(root string) (*Manager, error) {
    if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
        return nil, errors.New("invalid application root")
    }
    base, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
    if err != nil { return nil, err }
    defer unix.Close(base)
    fd, err := unix.Openat2(base, strings.TrimPrefix(filepath.Clean(root), "/"), &unix.OpenHow{
        Flags: uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),
        Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS,
    })
    if err != nil { return nil, fmt.Errorf("open application root: %w", err) }
    return &Manager{root: os.NewFile(uintptr(fd), root)}, nil
}

func (m *Manager) Close() error { return m.root.Close() }

func (m *Manager) directory(folder string) (int, error) {
    if !validPath(folder) { return -1, errors.New("invalid directory path") }
    if folder == "" { folder = "." }
    fd, err := unix.Openat2(int(m.root.Fd()), folder, &unix.OpenHow{
        Flags: uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),
        Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV,
    })
    if err != nil { return -1, fmt.Errorf("open directory: %w", err) }
    return fd, nil
}

func (m *Manager) List(folder string) ([]Entry, bool, error) {
    fd, err := m.directory(folder)
    if err != nil { return nil, false, err }
    dir := os.NewFile(uintptr(fd), folder)
    defer dir.Close()
    items, readErr := dir.ReadDir(MaxDirectoryEntries+1)
    if readErr != nil && !errors.Is(readErr, io.EOF) { return nil, false, readErr }
    limited := len(items) > MaxDirectoryEntries
    if limited { items = items[:MaxDirectoryEntries] }
    result := make([]Entry, 0, len(items))
    for _, item := range items {
        // Read metadata relative to the already-validated directory fd.
        // DirEntry.Info would resolve its path relative to the panel's cwd.
        var info unix.Stat_t
        if err := unix.Fstatat(fd, item.Name(), &info, unix.AT_SYMLINK_NOFOLLOW); err != nil { continue }
        kind := info.Mode & unix.S_IFMT
        result = append(result, Entry{
            Name: item.Name(), Directory: kind == unix.S_IFDIR,
            Symlink: kind == unix.S_IFLNK,
            Size: info.Size, Mode: os.FileMode(info.Mode & 0777),
        })
    }
    sort.Slice(result, func(i, j int) bool {
        if result[i].Directory != result[j].Directory { return result[i].Directory }
        return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
    })
    return result, limited, nil
}

func (m *Manager) openFile(folder, name string) (*os.File, os.FileInfo, error) {
    if !validName(name) { return nil, nil, errors.New("invalid file name") }
    fd, err := m.directory(folder)
    if err != nil { return nil, nil, err }
    defer unix.Close(fd)
    fileFD, err := unix.Openat2(fd, name, &unix.OpenHow{
        Flags: uint64(unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW),
        Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV,
    })
    if err != nil { return nil, nil, fmt.Errorf("open file: %w", err) }
    file := os.NewFile(uintptr(fileFD), name)
    info, err := file.Stat()
    if err != nil { file.Close(); return nil, nil, err }
    if !info.Mode().IsRegular() { file.Close(); return nil, nil, errors.New("only regular files can be opened") }
    return file, info, nil
}

func (m *Manager) Download(folder, name string) (*os.File, os.FileInfo, error) {
    return m.openFile(folder, name)
}

func (m *Manager) ReadText(folder, name string) (string, error) {
    file, info, err := m.openFile(folder, name)
    if err != nil { return "", err }
    defer file.Close()
    if info.Size() > MaxEditBytes { return "", errors.New("file exceeds 2 MiB editor limit") }
    data, err := io.ReadAll(io.LimitReader(file, MaxEditBytes+1))
    if err != nil { return "", err }
    if len(data) > MaxEditBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
        return "", errors.New("file is too large or not UTF-8 text")
    }
    return string(data), nil
}

func (m *Manager) write(folder, name string, source io.Reader, limit int64, overwrite bool) error {
    if !validName(name) { return errors.New("invalid file name") }
    fd, err := m.directory(folder)
    if err != nil { return err }
    defer unix.Close(fd)

    var stat unix.Stat_t
    statErr := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
    if statErr != nil && !errors.Is(statErr, unix.ENOENT) { return statErr }
    if statErr == nil && (stat.Mode&unix.S_IFMT) != unix.S_IFREG {
        return errors.New("refusing to replace a directory or symlink")
    }
    if overwrite && errors.Is(statErr, unix.ENOENT) { return errors.New("file no longer exists") }
    if !overwrite && statErr == nil { return errors.New("destination already exists") }

    random := make([]byte, 12)
    if _, err := rand.Read(random); err != nil { return err }
    temp := ".ogp-" + hex.EncodeToString(random) + ".tmp"
    temporaryFD, err := unix.Openat(fd, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
    if err != nil { return err }
    defer unix.Unlinkat(fd, temp, 0)
    output := os.NewFile(uintptr(temporaryFD), temp)
    written, copyErr := io.Copy(output, io.LimitReader(source, limit+1))
    if copyErr == nil && written > limit { copyErr = fmt.Errorf("file exceeds %d MiB limit", limit>>20) }
    mode := uint32(0640)
    if overwrite { mode = uint32(stat.Mode & 0777) }
    if copyErr == nil { copyErr = unix.Fchmod(temporaryFD, mode) }
    if copyErr == nil { copyErr = output.Sync() }
    closeErr := output.Close()
    if copyErr != nil { return copyErr }
    if closeErr != nil { return closeErr }
    if overwrite {
        return unix.Renameat(fd, temp, fd, name)
    }
    if err := unix.Renameat2(fd, temp, fd, name, unix.RENAME_NOREPLACE); err != nil {
        return fmt.Errorf("store file: %w", err)
    }
    return nil
}

func (m *Manager) Save(folder, name, content string, create bool) error {
    if len(content) > MaxEditBytes || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
        return errors.New("editor content must be valid UTF-8 and <= 2 MiB")
    }
    return m.write(folder, name, strings.NewReader(content), MaxEditBytes, !create)
}

func (m *Manager) Upload(folder, name string, source io.Reader) error {
    return m.write(folder, name, source, MaxUploadBytes, false)
}

func (m *Manager) Mkdir(folder, name string) error {
    if !validName(name) { return errors.New("invalid directory name") }
    fd, err := m.directory(folder)
    if err != nil { return err }
    defer unix.Close(fd)
    return unix.Mkdirat(fd, name, 0750)
}

func splitTarget(destination string) (string, string, error) {
    if !validPath(destination) || destination == "" || destination == "." {
        return "", "", errors.New("invalid destination; use a path relative to the application")
    }
    parent := path.Dir(destination)
    name := path.Base(destination)
    if !validName(name) { return "", "", errors.New("invalid destination filename") }
    if parent == "." { parent = "" }
    return parent, name, nil
}

func (m *Manager) Move(folder, name, destination string) error {
    if !validName(name) { return errors.New("invalid source name") }
    targetDir, targetName, err := splitTarget(destination)
    if err != nil { return err }
    from, err := m.directory(folder)
    if err != nil { return err }
    defer unix.Close(from)
    to, err := m.directory(targetDir)
    if err != nil { return err }
    defer unix.Close(to)
    if err := unix.Renameat2(from, name, to, targetName, unix.RENAME_NOREPLACE); err != nil {
        return fmt.Errorf("move file: %w", err)
    }
    return nil
}

func (m *Manager) Copy(folder, name, destination string) error {
    targetDir, targetName, err := splitTarget(destination)
    if err != nil { return err }
    file, info, err := m.openFile(folder, name)
    if err != nil { return err }
    defer file.Close()
    if info.Size() > MaxUploadBytes { return errors.New("copy source exceeds 1 GiB") }
    return m.write(targetDir, targetName, file, MaxUploadBytes, false)
}

func (m *Manager) Delete(folder, name string) error {
    if !validName(name) { return errors.New("invalid item name") }
    fd, err := m.directory(folder)
    if err != nil { return err }
    defer unix.Close(fd)
    remaining := 10000
    return removeTree(fd, name, 0, &remaining)
}

func removeTree(parent int, name string, depth int, remaining *int) error {
    if !validName(name) || depth > maxDepth || *remaining <= 0 {
        return errors.New("delete depth/item limit reached")
    }
    *remaining--
    var info unix.Stat_t
    if err := unix.Fstatat(parent, name, &info, unix.AT_SYMLINK_NOFOLLOW); err != nil { return err }
    if (info.Mode & unix.S_IFMT) != unix.S_IFDIR { return unix.Unlinkat(parent, name, 0) }
    fd, err := unix.Openat2(parent, name, &unix.OpenHow{
        Flags: uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),
        Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV,
    })
    if err != nil { return err }
    dir := os.NewFile(uintptr(fd), name)
    names, readErr := dir.Readdirnames(*remaining+1)
    if readErr != nil && !errors.Is(readErr, io.EOF) { dir.Close(); return readErr }
    if len(names) > *remaining { dir.Close(); return errors.New("directory exceeds 10000-item delete limit") }
    for _, child := range names {
        if err := removeTree(fd, child, depth+1, remaining); err != nil { dir.Close(); return err }
    }
    if err := dir.Close(); err != nil { return err }
    return unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
}

func (m *Manager) makeDirectories(base, relative string) (string, error) {
    current := base
    for _, name := range strings.Split(relative, "/") {
        if !validName(name) { return "", errors.New("invalid archive directory") }
        fd, err := m.directory(current)
        if err != nil { return "", err }
        err = unix.Mkdirat(fd, name, 0750)
        if errors.Is(err, unix.EEXIST) { err = nil }
        if err == nil {
            childFD, openErr := unix.Openat2(fd, name, &unix.OpenHow{
                Flags: uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),
                Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV,
            })
            if openErr != nil { err = openErr } else { unix.Close(childFD) }
        }
        unix.Close(fd)
        if err != nil { return "", err }
        if current == "" || current == "." { current = name } else { current += "/" + name }
    }
    return current, nil
}

func validateArchiveName(name string) (string, bool, error) {
    directory := strings.HasSuffix(name, "/")
    for strings.HasPrefix(name, "./") { name = strings.TrimPrefix(name, "./") }
    cleaned := strings.TrimSuffix(name, "/")
    if strings.Count(cleaned, "/") >= maxDepth || !validPath(cleaned) || cleaned == "" || cleaned == "." {
        return "", false, errors.New("archive contains invalid or escaping path")
    }
    return cleaned, directory, nil
}

func (m *Manager) extractEntry(folder, name string, directory bool, size int64, source io.Reader, remaining *int64) error {
    cleaned, _, err := validateArchiveName(name)
    if err != nil { return err }
    if directory {
        _, err := m.makeDirectories(folder, cleaned)
        return err
    }
    if size < 0 || size > MaxUploadBytes || size > *remaining { return errors.New("archive entry exceeds extraction limit") }
    parent := path.Dir(cleaned)
    target := folder
    if parent != "." {
        target, err = m.makeDirectories(folder, parent)
        if err != nil { return err }
    }
    if err := m.write(target, path.Base(cleaned), source, size, false); err != nil { return err }
    *remaining -= size
    return nil
}

// openZIPArchive first uses Go's standard ZIP reader. Some ZIP creators append
// a non-ZIP payload after the end-of-central-directory record. That is not
// part of the archive and must not prevent extracting its valid ZIP entries.
// Only recover when a shorter view can be validated by archive/zip itself;
// never invoke an unconfined external extractor.
func openZIPArchive(file *os.File, size int64) (*zip.Reader, error) {
    archive, err := zip.NewReader(file, size)
    if err == nil { return archive, nil }
    if !errors.Is(err, zip.ErrFormat) {
        return nil, fmt.Errorf("read ZIP archive: %w", err)
    }
    const maxTrailingScan int64 = 8 << 20
    const maxZIPComment int64 = 65535
    const endRecordBytes int64 = 22
    scan := maxTrailingScan + maxZIPComment + endRecordBytes
    if size < scan { scan = size }
    if scan >= endRecordBytes {
        tail := make([]byte, int(scan))
        start := size - scan
        if _, readErr := file.ReadAt(tail, start); readErr == nil {
            // Scan backwards; candidate offsets are validated with zip.NewReader
            // before accepting any archive data. Bound attempts for malformed
            // or adversarial files.
            attempts := 0
            for i := len(tail) - int(endRecordBytes); i >= 0 && attempts < 64; i-- {
                if !bytes.Equal(tail[i:i+4], []byte{'P', 'K', 5, 6}) { continue }
                commentLen := int(binary.LittleEndian.Uint16(tail[i+20 : i+22]))
                end := i + int(endRecordBytes) + commentLen
                if end >= len(tail) { continue }
                attempts++
                candidate, candidateErr := zip.NewReader(file, start+int64(end))
                if candidateErr == nil { return candidate, nil }
            }
        }
    }

    var header [8]byte
    n, _ := file.ReadAt(header[:], 0)
    prefix := header[:n]
    switch {
    case bytes.HasPrefix(prefix, []byte{'R','a','r','!',0x1a,0x07}):
        return nil, errors.New("file contains RAR data, not ZIP; upload a ZIP archive")
    case bytes.HasPrefix(prefix, []byte{'7','z',0xbc,0xaf,0x27,0x1c}):
        return nil, errors.New("file contains 7z data, not ZIP; upload a ZIP archive")
    case bytes.HasPrefix(prefix, []byte{0x1f,0x8b}):
        return nil, errors.New("file contains gzip data, not ZIP; use .tar.gz only for a tar archive")
    case bytes.HasPrefix(bytes.TrimSpace(prefix), []byte{'<'}):
        return nil, errors.New("file appears to be HTML/XML rather than a ZIP archive")
    default:
        return nil, fmt.Errorf("ZIP structure is missing or damaged (%d bytes). Check the source archive with unzip -t and compare its SHA-256 checksum; upload again if incomplete: %w", size, err)
    }
}

func (m *Manager) Extract(folder, name string) (int, error) {
    lower := strings.ToLower(name)
    if !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".tgz") {
        return 0, errors.New("supported archives: .zip, .tar.gz, .tgz")
    }
    file, info, err := m.openFile(folder, name)
    if err != nil { return 0, err }
    defer file.Close()
    if info.Size() > MaxArchiveBytes { return 0, errors.New("archive exceeds 1 GiB") }
    remaining := int64(MaxExtractBytes)
    count := 0

    if strings.HasSuffix(lower, ".zip") {
        archive, err := openZIPArchive(file, info.Size())
        if err != nil { return 0, err }
        if len(archive.File) > MaxArchiveEntries { return 0, errors.New("archive contains too many entries") }
        for _, item := range archive.File {
            if (item.Name == "./" || item.Name == ".") && item.FileInfo().IsDir() { continue }
            if item.Mode() & os.ModeType != 0 && !item.FileInfo().IsDir() {
                return count, errors.New("archive contains an unsupported file type")
            }
            cleaned, directory, err := validateArchiveName(item.Name)
            if err != nil { return count, err }
            size := int64(item.UncompressedSize64)
            if item.UncompressedSize64 > uint64(MaxUploadBytes) { return count, errors.New("archive entry too large") }
            if directory {
                if err := m.extractEntry(folder, cleaned, true, 0, nil, &remaining); err != nil { return count, err }
            } else {
                input, err := item.Open()
                if err != nil { return count, err }
                err = m.extractEntry(folder, cleaned, false, size, input, &remaining)
                closeErr := input.Close()
                if err != nil { return count, err }
                if closeErr != nil { return count, closeErr }
            }
            count++
        }
        return count, nil
    }

    stream, err := gzip.NewReader(file)
    if err != nil { return 0, err }
    defer stream.Close()
    archive := tar.NewReader(stream)
    for {
        item, err := archive.Next()
        if errors.Is(err, io.EOF) { break }
        if err != nil { return count, err }
        count++
        if count > MaxArchiveEntries { return count-1, errors.New("archive contains too many entries") }
        if item.Typeflag == tar.TypeDir && (item.Name == "." || item.Name == "./") { continue }
        if item.Typeflag != tar.TypeDir && item.Typeflag != tar.TypeReg && item.Typeflag != tar.TypeRegA {
            return count-1, errors.New("archive contains links or unsupported entries")
        }
        cleaned, _, err := validateArchiveName(item.Name)
        if err != nil { return count-1, err }
        if err := m.extractEntry(folder, cleaned, item.Typeflag == tar.TypeDir, item.Size, archive, &remaining); err != nil {
            return count-1, err
        }
    }
    return count, nil
}
