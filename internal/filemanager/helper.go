package filemanager

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
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

func (c *Client) Upload(folder, name string, source io.Reader) error {
    _, err := c.call("upload", folder, name, "", io.LimitReader(source, MaxUploadBytes+1))
    return err
}

func (c *Client) Mkdir(folder, name string) error {
    _, err := c.call("mkdir", folder, name, "", nil)
    return err
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
            return manager.Upload(folder, name, io.LimitReader(os.Stdin, MaxUploadBytes+1))
        }
        data, err := io.ReadAll(io.LimitReader(os.Stdin, MaxEditBytes+1))
        if err != nil { return err }
        return manager.Save(folder, name, string(data), operation == "create")
    case "mkdir":
        return manager.Mkdir(folder, name)
    case "move":
        return manager.Move(folder, name, destination)
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
