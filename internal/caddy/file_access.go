package caddy

import (
    "context"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

// EnsureManagedFileAccess keeps web-readable files readable after a file
// manager mutation. Only a configured static app root or a proxy app's public/
// receives Caddy ACLs. PHP site permission handling is unchanged.
func (m *Manager) EnsureManagedFileAccess(ctx context.Context, appID int64, appRoot, relative string) error {
    site, found, err := m.SiteForApp(appID)
    if err != nil { return err }
    if !found { return nil }

    root := ""
    switch site.Kind {
    case "static":
        root = site.Root
    case "proxy":
        root = filepath.Join(appRoot, "public")
    default:
        return nil
    }
    if !filepath.IsAbs(appRoot) || !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
        return errors.New("invalid web document root")
    }
    if relative == "" { relative = "." }
    if !filepath.IsLocal(relative) {
        return errors.New("invalid file path for Caddy ACL")
    }
    appRoot = filepath.Clean(appRoot)
    root = filepath.Clean(root)
    target := filepath.Join(appRoot, relative)
    subpath, err := filepath.Rel(root, target)
    if err != nil { return err }
    if subpath == ".." || strings.HasPrefix(subpath, ".."+string(filepath.Separator)) {
        return nil // Private application files are never exposed to Caddy.
    }

    entry, err := os.Lstat(target)
    if errors.Is(err, os.ErrNotExist) { return nil }
    if err != nil { return fmt.Errorf("inspect web file: %w", err) }
    if entry.Mode()&os.ModeSymlink != 0 { return nil }
    if !entry.Mode().IsRegular() && !entry.IsDir() { return nil }
    resolved, err := filepath.EvalSymlinks(target)
    if err != nil { return fmt.Errorf("resolve web file: %w", err) }
    if resolved != target { return errors.New("web path must not contain symlinks") }
    if _, err := exec.LookPath("setfacl"); err != nil { return errors.New("Caddy file access requires the acl package") }

    runACL := func(args ...string) error {
        output, err := exec.CommandContext(ctx, "setfacl", args...).CombinedOutput()
        if err != nil { return fmt.Errorf("set Caddy ACL on %s: %w: %s", args[len(args)-1], err, strings.TrimSpace(string(output))) }
        return nil
    }

    // Traverse protected /home/user/apps/app without listing or reading it.
    for parent := filepath.Dir(root); parent != "/" && parent != "."; parent = filepath.Dir(parent) {
        if err := runACL("-m", "u:caddy:--x", parent); err != nil { return err }
    }
    // Grant read and inherited read only along the served file's path.
    dirs := []string{}
    for dir := filepath.Dir(target); ; dir = filepath.Dir(dir) {
        dirs = append(dirs, dir)
        if dir == root { break }
        if dir == "/" || dir == "." { return errors.New("web file path escaped its root") }
    }
    for i := len(dirs)-1; i >= 0; i-- {
        if err := runACL("-m", "u:caddy:rX,d:u:caddy:rX", dirs[i]); err != nil { return err }
    }

    if entry.Mode().IsRegular() {
        return runACL("-m", "u:caddy:r--", target)
    }
    // A newly created or moved directory can contain preexisting files that
    // did not inherit an ACL. Apply it only to this subtree, not the app root.
    if err := runACL("-R", "-m", "u:caddy:rX", target); err != nil { return err }
    return filepath.WalkDir(target, func(path string, child os.DirEntry, walkErr error) error {
        if walkErr != nil { return walkErr }
        if !child.IsDir() { return nil }
        return runACL("-m", "d:u:caddy:rX", path)
    })
}
