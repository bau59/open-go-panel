package filemanager

import (
    "errors"
    "fmt"
    "io"
    "os"
    "path"
    "strings"

    "golang.org/x/sys/unix"
)

const (
    MaxBulkSelection = 400
    MaxMoveContents = 100000
)

func checkSelectedNames(names []string, limit int) error {
    if len(names) == 0 { return errors.New("select at least one file or folder") }
    if len(names) > limit { return fmt.Errorf("too many selected items (limit %d)", limit) }
    seen := make(map[string]struct{}, len(names))
    for _, name := range names {
        if !validName(name) { return fmt.Errorf("invalid selected filename %q", name) }
        if _, exists := seen[name]; exists { return fmt.Errorf("duplicate selected filename %q", name) }
        seen[name] = struct{}{}
    }
    return nil
}

// checkEntries preflights the complete move before changing anything.
// All parent directories are opened through openat2 without following links.
func checkEntries(sourceFD, targetFD int, names []string) error {
    for _, name := range names {
        var stat unix.Stat_t
        if err := unix.Fstatat(sourceFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
            return fmt.Errorf("source %q: %w", name, err)
        }
        if err := unix.Fstatat(targetFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
            return fmt.Errorf("destination already contains %q; nothing was moved", name)
        } else if !errors.Is(err, unix.ENOENT) {
            return fmt.Errorf("inspect destination %q: %w", name, err)
        }
    }
    return nil
}

func moveEntries(sourceFD, targetFD int, names []string) (int, error) {
    if err := checkEntries(sourceFD, targetFD, names); err != nil { return 0, err }
    for index, name := range names {
        if err := unix.Renameat2(sourceFD, name, targetFD, name, unix.RENAME_NOREPLACE); err != nil {
            rollbackFailures := 0
            for j := index - 1; j >= 0; j-- {
                if undoErr := unix.Renameat2(targetFD, names[j], sourceFD, names[j], unix.RENAME_NOREPLACE); undoErr != nil {
                    rollbackFailures++
                }
            }
            if rollbackFailures != 0 {
                return index, fmt.Errorf("moving %q: %w; rollback of %d items also failed; inspect both folders", name, err, rollbackFailures)
            }
            return 0, fmt.Errorf("moving %q: %w; preceding changes were rolled back", name, err)
        }
    }
    return len(names), nil
}

func normalizeFolder(folder string) (string, error) {
    if folder == "." { folder = "" }
    if !validPath(folder) { return "", errors.New("invalid folder path; use a path relative to the application root") }
    return folder, nil
}

func (m *Manager) MoveMany(folder string, names []string, destination string) (int, error) {
    if err := checkSelectedNames(names, MaxBulkSelection); err != nil { return 0, err }
    source, err := normalizeFolder(folder)
    if err != nil { return 0, err }
    target, err := normalizeFolder(destination)
    if err != nil { return 0, err }
    if source == target { return 0, errors.New("source and destination folders are the same") }
    for _, name := range names {
        sourcePath := name
        if source != "" { sourcePath = source + "/" + name }
        if target == sourcePath || strings.HasPrefix(target, sourcePath+"/") {
            return 0, fmt.Errorf("cannot move %q into itself or a subfolder", name)
        }
    }
    sourceFD, err := m.directory(source)
    if err != nil { return 0, err }
    defer unix.Close(sourceFD)
    targetFD, err := m.directory(target)
    if err != nil { return 0, err }
    defer unix.Close(targetFD)
    return moveEntries(sourceFD, targetFD, names)
}

func (m *Manager) DeleteMany(folder string, names []string) (int, error) {
    if err := checkSelectedNames(names, MaxBulkSelection); err != nil { return 0, err }
    fd, err := m.directory(folder)
    if err != nil { return 0, err }
    defer unix.Close(fd)
    // Missing items are detected before the first deletion.
    for _, name := range names {
        var stat unix.Stat_t
        if err := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
            return 0, fmt.Errorf("source %q: %w", name, err)
        }
    }
    for i, name := range names {
        remaining := MaxMoveContents
        if err := removeTree(fd, name, 0, &remaining); err != nil {
            return i, fmt.Errorf("deleted %d of %d items before %q failed: %w", i, len(names), name, err)
        }
    }
    return len(names), nil
}

func readAllNames(fd int, limit int) ([]string, error) {
    duplicateFD, err := unix.Dup(fd)
    if err != nil { return nil, err }
    directory := os.NewFile(uintptr(duplicateFD), "bulk-source")
    defer directory.Close()
    names, err := directory.Readdirnames(limit + 1)
    if err != nil && !errors.Is(err, io.EOF) { return nil, err }
    if len(names) > limit { return nil, fmt.Errorf("folder contains more than %d items", limit) }
    if len(names) == 0 { return names, nil }
    if err := checkSelectedNames(names, limit); err != nil { return nil, err }
    return names, nil
}

// MoveContentsUp moves every direct child of a folder to its parent. Subfolders
// move as entire trees. The source folder is kept empty for explicit deletion.
// If child is empty, folder is the source. Otherwise child names one of its
// direct subdirectories (used by "Move contents here" in the parent listing).
func (m *Manager) MoveContentsUp(folder, child string) (int, error) {
    current, err := normalizeFolder(folder)
    if err != nil { return 0, err }

    source := current
    target := ""
    if child != "" {
        if !validName(child) { return 0, errors.New("invalid source directory") }
        source = child
        if current != "" { source = current + "/" + child }
        target = current
    } else {
        if current == "" { return 0, errors.New("cannot move the application root into its parent") }
        target = path.Dir(current)
        if target == "." { target = "" }
    }

    sourceFD, err := m.directory(source)
    if err != nil { return 0, fmt.Errorf("open source folder: %w", err) }
    defer unix.Close(sourceFD)
    targetFD, err := m.directory(target)
    if err != nil { return 0, fmt.Errorf("open parent folder: %w", err) }
    defer unix.Close(targetFD)
    names, err := readAllNames(sourceFD, MaxMoveContents)
    if err != nil { return 0, err }
    if len(names) == 0 { return 0, nil }
    return moveEntries(sourceFD, targetFD, names)
}
