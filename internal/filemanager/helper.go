package filemanager

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "hash"
    "io"
    "os"
    "os/exec"
    "os/user"
    "path/filepath"
    "strconv"
    "strings"
)

// Client invokes the file helper with the application's Linux identity.
// The panel never opens or writes application files as root.
type Client struct {
    ctx context.Context
    root string
    username string
    home string
    binary string
}

func NewClient(ctx context.Context, root, username string) (*Client, error) {
    if ctx == nil { return nil, errors.New("context required") }
    if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
        return nil, errors.New("invalid application root")
    }
    account, err := user.Lookup(username)
    if err != nil { return nil, fmt.Errorf("lookup application user: %w", err) }
    if account.Uid == "0" {
        return nil, errors.New("file manager cannot run as root")
    }
    binary, err := os.Executable()
    if err != nil { return nil, fmt.Errorf("resolve panel binary: %w", err) }
    return &Client{ctx:ctx, root:root, username:username, home:account.HomeDir, binary:binary}, nil
}

func (c *Client) command(operation, folder, name, destination string) *exec.Cmd {
    // No shell or privileged environment from the panel is inherited.
    cmd := exec.CommandContext(c.ctx, "runuser",
        "-u", c.username, "--", c.binary, "--app-file-helper",
        c.root, operation, folder, name, destination)
    cmd.Dir = "/"
    cmd.Env = []string{
        "PATH=/usr/local/bin:/usr/bin:/bin",
        "LANG=C.UTF-8", "HOME="+c.home,
        "USER="+c.username, "LOGNAME="+c.username,
    }
    return cmd
}

func (c *Client) execute(operation, folder, name, destination string, input io.Reader, output io.Writer) error {
    cmd := c.command(operation, folder, name, destination)
    cmd.Stdin = input
    cmd.Stdout = output
    var stderr strings.Builder
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        message := strings.TrimSpace(stderr.String())
        if message == "" { message = err.Error() }
        if len(message) > 1500 { message = message[:1500] }
        return fmt.Errorf("file operation %s: %s", operation, message)
    }
    return nil
}

func (c *Client) call(operation, folder, name, destination string, input io.Reader) ([]byte, error) {
    var output strings.Builder
    if err := c.execute(operation, folder, name, destination, input, &output); err != nil {
        return nil, err
    }
    return []byte(output.String()), nil
}

func (c *Client) List(folder string) ([]Entry, bool, error) {
    output, err := c.call("list", folder, "", "", nil)
    if err != nil { return nil, false, err }
    var result struct {
        Entries []Entry
        Limited bool
    }
    if err := json.Unmarshal(output, &result); err != nil { return nil, false, err }
    return result.Entries, result.Limited, nil
}

func (c *Client) ReadText(folder, name string) (string, error) {
    output, err := c.call("read", folder, name, "", nil)
    if err != nil { return "", err }
    return string(output), nil
}

func (c *Client) FileSize(folder, name string) (int64, error) {
    output, err := c.call("stat", folder, name, "", nil)
    if err != nil { return 0, err }
    size, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
    if err != nil { return 0, err }
    return size, nil
}

func (c *Client) Download(folder, name string, output io.Writer) error {
    return c.execute("download", folder, name, "", nil, output)
}

func (c *Client) Save(folder, name, content string, create bool) error {
    if len(content) > MaxEditBytes || strings.ContainsRune(content, 0) {
        return errors.New("editor content exceeds limit or contains binary data")
    }
    operation := "save"
    if create { operation = "create" }
    _, err := c.call(operation, folder, name, "", strings.NewReader(content))
    return err
}

// Upload checks every byte transferred to the unprivileged helper against the
// file read back from its final location. The size supplied by the browser is
// also verified, so a truncated multipart request is not silently accepted.
func (c *Client) Upload(folder, name string, source io.Reader, expectedSize int64) error {
    if expectedSize < 0 || expectedSize > MaxUploadBytes {
        return errors.New("file upload exceeds the 1 GiB limit")
    }
    reader := &digestReader{source: io.LimitReader(source, MaxUploadBytes+1), digest: sha256.New()}
    output, err := c.call("upload", folder, name, "", reader)
    if err != nil { return err }

    var saved uploadDigest
    if err := json.Unmarshal(output, &saved); err != nil {
        // A successful upload can only be returned with a verified digest.
        cleanupErr := c.Delete(folder, name)
        return fmt.Errorf("invalid upload verification response: %w (cleanup: %v)", err, cleanupErr)
    }
    if err := verifyUploadDigest(expectedSize, reader, saved); err != nil {
        cleanupErr := c.Delete(folder, name)
        return fmt.Errorf("%w (cleanup: %v)", err, cleanupErr)
    }
    return nil
}

type uploadDigest struct {
    Size int64 `json:"size"`
    SHA256 string `json:"sha256"`
}

type digestReader struct {
    source io.Reader
    digest hash.Hash
    bytes int64
}
func (r *digestReader) Read(p []byte) (int, error) {
    n, err := r.source.Read(p)
    if n > 0 {
        _, _ = r.digest.Write(p[:n])
        r.bytes += int64(n)
    }
    return n, err
}

func verifyUploadDigest(expectedSize int64, reader *digestReader, saved uploadDigest) error {
    if reader.bytes != expectedSize || saved.Size != expectedSize {
        return fmt.Errorf("upload is incomplete: browser sent %d bytes, helper received %d, saved %d", expectedSize, reader.bytes, saved.Size)
    }
    originalSHA256 := hex.EncodeToString(reader.digest.Sum(nil))
    if !strings.EqualFold(originalSHA256, saved.SHA256) {
        return fmt.Errorf("upload checksum mismatch: source SHA-256 %s, saved SHA-256 %s", originalSHA256, saved.SHA256)
    }
    return nil
}

// helperUpload atomically saves a file as the Linux app owner, then reads
// it back from disk and reports its SHA-256 and length to the parent process.
func helperUpload(manager *Manager, folder, name string, input io.Reader, output io.Writer) error {
    if err := manager.Upload(folder, name, io.LimitReader(input, MaxUploadBytes+1)); err != nil {
        return err
    }
    complete := false
    defer func() {
        if !complete { _ = manager.Delete(folder, name) }
    }()

    file, info, err := manager.Download(folder, name)
    if err != nil { return fmt.Errorf("verify uploaded file: %w", err) }
    defer file.Close()
    digest := sha256.New()
    copied, err := io.Copy(digest, file)
    if err != nil { return fmt.Errorf("read uploaded file for verification: %w", err) }
    if copied != info.Size() { return errors.New("uploaded file changed during verification") }
    result := uploadDigest{Size: copied, SHA256: hex.EncodeToString(digest.Sum(nil))}
    if err := json.NewEncoder(output).Encode(result); err != nil {
        return fmt.Errorf("write upload verification: %w", err)
    }
    complete = true
    return nil
}

func (c *Client) Mkdir(folder, name string) error {
    _, err := c.call("mkdir", folder, name, "", nil)
    return err
}

func (c *Client) Chmod(folder, name, mode string) error {
    _, err := c.call("chmod", folder, name, mode, nil)
    return err
}

// Archive performs compression in the unprivileged app-owner helper.
func (c *Client) Archive(folder string, names []string, outputName string) (int, error) {
    if err := checkSelectedNames(names, MaxBulkSelection); err != nil { return 0, err }
    if !validName(outputName) { return 0, errors.New("invalid archive filename") }
    data, err := json.Marshal(names)
    if err != nil { return 0, err }
    result, err := c.call("archive", folder, "", outputName, strings.NewReader(string(data)))
    if err != nil { return 0, err }
    return strconv.Atoi(strings.TrimSpace(string(result)))
}

func (c *Client) Move(folder, name, destination string) error {
    _, err := c.call("move", folder, name, destination, nil)
    return err
}

func (c *Client) Copy(folder, name, destination string) error {
    _, err := c.call("copy", folder, name, destination, nil)
    return err
}

func (c *Client) Delete(folder, name string) error {
    _, err := c.call("delete", folder, name, "", nil)
    return err
}

func (c *Client) MoveMany(folder string, names []string, destination string) (int, error) {
    if err := checkSelectedNames(names, MaxBulkSelection); err != nil { return 0, err }
    data, err := json.Marshal(names)
    if err != nil { return 0, err }
    result, err := c.call("bulk-move", folder, "", destination, strings.NewReader(string(data)))
    if err != nil { return 0, err }
    return strconv.Atoi(strings.TrimSpace(string(result)))
}

func (c *Client) DeleteMany(folder string, names []string) (int, error) {
    if err := checkSelectedNames(names, MaxBulkSelection); err != nil { return 0, err }
    data, err := json.Marshal(names)
    if err != nil { return 0, err }
    result, err := c.call("bulk-delete", folder, "", "", strings.NewReader(string(data)))
    if err != nil { return 0, err }
    return strconv.Atoi(strings.TrimSpace(string(result)))
}

func (c *Client) MoveContentsUp(folder, child string) (int, error) {
    result, err := c.call("move-contents", folder, child, "", nil)
    if err != nil { return 0, err }
    return strconv.Atoi(strings.TrimSpace(string(result)))
}

// Decode only bounded lists: the helper is unprivileged, but must never read
// arbitrary amounts of stdin in response to a bulk action.
func readBulkNames(input io.Reader) ([]string, error) {
    const maxJSON = MaxBulkSelection*(255+8) + 256
    raw, err := io.ReadAll(io.LimitReader(input, maxJSON+1))
    if err != nil { return nil, err }
    if len(raw) > maxJSON { return nil, errors.New("bulk selection exceeds request limit") }
    var names []string
    if err := json.Unmarshal(raw, &names); err != nil { return nil, err }
    if err := checkSelectedNames(names, MaxBulkSelection); err != nil { return nil, err }
    return names, nil
}

func (c *Client) Extract(folder, name string) (int, error) {
    output, err := c.call("extract", folder, name, "", nil)
    if err != nil { return 0, err }
    return strconv.Atoi(strings.TrimSpace(string(output)))
}

// RunHelper is entered before loading privileged panel configuration. It only
// runs after runuser has dropped root privileges to the application's owner.
func RunHelper(args []string) error {
    if os.Geteuid() == 0 {
        return errors.New("file helper refuses root execution")
    }
    if len(args) != 5 {
        return errors.New("invalid file helper arguments")
    }
    root, operation, folder, name, destination := args[0], args[1], args[2], args[3], args[4]
    manager, err := Open(root)
    if err != nil { return err }
    defer manager.Close()
    switch operation {
    case "list":
        entries, limited, err := manager.List(folder)
        if err != nil { return err }
        return json.NewEncoder(os.Stdout).Encode(struct {
            Entries []Entry
            Limited bool
        }{entries, limited})
    case "read":
        data, err := manager.ReadText(folder, name)
        if err != nil { return err }
        _, err = io.WriteString(os.Stdout, data)
        return err
    case "stat":
        file, info, err := manager.Download(folder, name)
        if err != nil { return err }
        defer file.Close()
        _, err = fmt.Fprintln(os.Stdout, info.Size())
        return err
    case "download":
        file, _, err := manager.Download(folder, name)
        if err != nil { return err }
        defer file.Close()
        _, err = io.Copy(os.Stdout, file)
        return err
    case "save", "create", "upload":
        if operation == "upload" {
            return helperUpload(manager, folder, name, os.Stdin, os.Stdout)
        }
        data, err := io.ReadAll(io.LimitReader(os.Stdin, MaxEditBytes+1))
        if err != nil { return err }
        return manager.Save(folder, name, string(data), operation == "create")
    case "mkdir":
        return manager.Mkdir(folder, name)
    case "chmod":
        bits, err := strconv.ParseUint(destination, 8, 32)
        if err != nil { return errors.New("invalid octal permissions") }
        return manager.Chmod(folder, name, os.FileMode(bits))
    case "archive":
        names, err := readBulkNames(os.Stdin)
        if err != nil { return err }
        count, err := manager.Archive(folder, names, destination)
        if err != nil { return err }
        _, err = fmt.Fprintln(os.Stdout, count)
        return err
    case "move":
        return manager.Move(folder, name, destination)
    case "bulk-move", "bulk-delete":
        names, err := readBulkNames(os.Stdin)
        if err != nil { return err }
        var count int
        if operation == "bulk-move" {
            count, err = manager.MoveMany(folder, names, destination)
        } else {
            count, err = manager.DeleteMany(folder, names)
        }
        if err != nil { return err }
        _, err = fmt.Fprintln(os.Stdout, count)
        return err
    case "move-contents":
        count, err := manager.MoveContentsUp(folder, name)
        if err != nil { return err }
        _, err = fmt.Fprintln(os.Stdout, count)
        return err
    case "copy":
        return manager.Copy(folder, name, destination)
    case "delete":
        return manager.Delete(folder, name)
    case "extract":
        count, err := manager.Extract(folder, name)
        if err != nil { return err }
        _, err = fmt.Fprintln(os.Stdout, count)
        return err
    default:
        return errors.New("unknown file helper operation")
    }
}
