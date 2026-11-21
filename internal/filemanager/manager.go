package filemanager

import (
    "archive/tar"
    "archive/zip"
    "compress/gzip"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "os"
    "os/user"
    "path"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
    "unicode/utf8"

    "golang.org/x/sys/unix"
)

const (
    MaxEditBytes = 2 << 20
    MaxUploadBytes = 128 << 20
    MaxArchiveBytes = 128 << 20
    MaxExtractBytes = 256 << 20
    MaxArchiveEntries = 10000
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
    uid int
    gid int
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

func Open(root, username string) (*Manager, error) {
    if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
        return nil, errors.New("invalid application root")
    }
    account, err := user.Lookup(username)
    if err != nil { return nil, fmt.Errorf("lookup application owner: %w", err) }
    uid, err := strconv.Atoi(account.Uid)
    if err != nil { return nil, err }
    gid, err := strconv.Atoi(account.Gid)
    if err != nil { return nil, err }

    base, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
    if err != nil { return nil, err }
    defer unix.Close(base)
    fd, err := unix.Openat2(base, strings.TrimPrefix(filepath.Clean(root), "/"), &unix.OpenHow{
        Flags: uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),
        Resolve: unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS,
    })
    if err != nil { return nil, fmt.Errorf("open application root: %w", err) }
    return &Manager{root: os.NewFile(uintptr(fd), root), uid: uid, gid: gid}, nil
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
    if copyErr == nil { copyErr = unix.Fchown(temporaryFD, m.uid, m.gid) }
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
    if err := unix.Mkdirat(fd, name, 0750); err != nil { return err }
    if err := unix.Fchownat(fd, name, m.uid, m.gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
        _ = unix.Unlinkat(fd, name, unix.AT_REMOVEDIR)
        return err
    }
    return nil
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
    if info.Size() > MaxUploadBytes { return errors.New("copy source exceeds 128 MiB") }
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
        if errors.Is(err, unix.EEXIST) { err = nil } else if err == nil {
            err = unix.Fchownat(fd, name, m.uid, m.gid, unix.AT_SYMLINK_NOFOLLOW)
        }
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
    if size < 0 || size > 64<<20 || size > *remaining { return errors.New("archive entry exceeds extraction limit") }
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

func (m *Manager) Extract(folder, name string) (int, error) {
    lower := strings.ToLower(name)
    if !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".tgz") {
        return 0, errors.New("supported archives: .zip, .tar.gz, .tgz")
    }
    file, info, err := m.openFile(folder, name)
    if err != nil { return 0, err }
    defer file.Close()
    if info.Size() > MaxArchiveBytes { return 0, errors.New("archive exceeds 128 MiB") }
    remaining := int64(MaxExtractBytes)
    count := 0

    if strings.HasSuffix(lower, ".zip") {
        archive, err := zip.NewReader(file, info.Size())
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
            if item.UncompressedSize64 > 64<<20 { return count, errors.New("archive entry too large") }
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
