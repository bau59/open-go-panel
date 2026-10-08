package docker

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os/exec"
    "sort"
    "strings"
    "time"
)

type Status struct {
    Installed bool
    Active    bool
    Version   string
    Error     string
}

type Container struct {
    ID            string
    Name          string
    Image         string
    State         string
    Running       bool
    RestartPolicy string
    Ports         string
    Created       time.Time
}

type Manager struct{}

func New() *Manager { return &Manager{} }

func (m *Manager) Status(ctx context.Context) Status {
    if _, err := exec.LookPath("docker"); err != nil {
        return Status{}
    }
    status := Status{Installed: true}
    out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput()
    if err != nil {
        status.Error = strings.TrimSpace(string(out))
        return status
    }
    status.Active = true
    status.Version = strings.TrimSpace(string(out))
    return status
}

func (m *Manager) Containers(ctx context.Context) ([]Container, error) {
    if _, err := exec.LookPath("docker"); err != nil {
        return nil, errors.New("docker is not installed")
    }
    idsOut, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--no-trunc").CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("docker ps: %w: %s", err, strings.TrimSpace(string(idsOut)))
    }
    ids := strings.Fields(string(idsOut))
    if len(ids) == 0 {
        return nil, nil
    }

    args := append([]string{"inspect"}, ids...)
    out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("docker inspect: %w: %s", err, strings.TrimSpace(string(out)))
    }

    var inspected []struct {
        ID      string `json:"Id"`
        Name    string `json:"Name"`
        Created string `json:"Created"`
        Config  struct {
            Image string `json:"Image"`
        } `json:"Config"`
        State struct {
            Status  string `json:"Status"`
            Running bool   `json:"Running"`
        } `json:"State"`
        HostConfig struct {
            RestartPolicy struct {
                Name string `json:"Name"`
            } `json:"RestartPolicy"`
        } `json:"HostConfig"`
        NetworkSettings struct {
            Ports map[string][]struct {
                HostIP   string `json:"HostIp"`
                HostPort string `json:"HostPort"`
            } `json:"Ports"`
        } `json:"NetworkSettings"`
    }
    if err := json.Unmarshal(out, &inspected); err != nil {
        return nil, fmt.Errorf("decode docker inspect: %w", err)
    }

    containers := make([]Container, 0, len(inspected))
    for _, item := range inspected {
        id := item.ID
        if len(id) > 12 {
            id = id[:12]
        }
        created, _ := time.Parse(time.RFC3339Nano, item.Created)
        containers = append(containers, Container{
            ID: id,
            Name: strings.TrimPrefix(item.Name, "/"),
            Image: item.Config.Image,
            State: item.State.Status,
            Running: item.State.Running,
            RestartPolicy: defaultRestartPolicy(item.HostConfig.RestartPolicy.Name),
            Ports: formatPorts(item.NetworkSettings.Ports),
            Created: created,
        })
    }
    sort.Slice(containers, func(i, j int) bool {
        if containers[i].Running != containers[j].Running {
            return containers[i].Running
        }
        return containers[i].Name < containers[j].Name
    })
    return containers, nil
}

func (m *Manager) Start(ctx context.Context, id string) error { return dockerCommand(ctx, "start", id) }
func (m *Manager) Stop(ctx context.Context, id string) error { return dockerCommand(ctx, "stop", id) }
func (m *Manager) Restart(ctx context.Context, id string) error { return dockerCommand(ctx, "restart", id) }
func (m *Manager) Remove(ctx context.Context, id string) error { return dockerCommand(ctx, "rm", "-f", id) }

func (m *Manager) SetAutostart(ctx context.Context, id string, enabled bool) error {
    policy := "no"
    if enabled { policy = "unless-stopped" }
    return dockerCommand(ctx, "update", "--restart", policy, id)
}

func dockerCommand(ctx context.Context, args ...string) error {
    if _, err := exec.LookPath("docker"); err != nil {
        return errors.New("docker is not installed")
    }
    out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
    if err != nil {
        return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
    }
    return nil
}

func defaultRestartPolicy(value string) string {
    value = strings.TrimSpace(value)
    if value == "" { return "no" }
    return value
}

func formatPorts(ports map[string][]struct {
    HostIP string `json:"HostIp"`
    HostPort string `json:"HostPort"`
}) string {
    if len(ports) == 0 { return "—" }
    keys := make([]string, 0, len(ports))
    for containerPort := range ports { keys = append(keys, containerPort) }
    sort.Strings(keys)
    var parts []string
    for _, containerPort := range keys {
        bindings := ports[containerPort]
        if len(bindings) == 0 {
            parts = append(parts, containerPort)
            continue
        }
        for _, binding := range bindings {
            host := binding.HostIP
            if host == "" || host == "0.0.0.0" || host == "::" { host = "*" }
            parts = append(parts, host+":"+binding.HostPort+"→"+containerPort)
        }
    }
    return strings.Join(parts, ", ")
}
