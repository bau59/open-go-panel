# Open Go Panel

Lightweight open-source Linux server control panel written in Go.

## Current status

Early technical preview.

Included now:

- single Go binary;
- starts on port `8443`;
- admin login;
- protected dashboard;
- public healthcheck at `/health`;
- systemd service;
- one-command installer;
- Linux amd64 and arm64 release builds.

## Install

Run as root on Ubuntu:

```bash
curl -fsSL https://raw.githubusercontent.com/bau59/open-go-panel/main/install.sh | bash
```

The installer:

1. detects amd64 or arm64;
2. downloads the latest GitHub Release binary;
3. installs it to `/usr/local/bin/open-go-panel`;
4. generates an admin password;
5. creates `/etc/open-go-panel/open-go-panel.env`;
6. creates and starts `open-go-panel.service`;
7. prints the panel URL and credentials.

Default URL:

```text
http://SERVER_IP:8443
```

Default username:

```text
admin
```

## Configuration

Configuration is read from environment variables:

```text
OGP_LISTEN_ADDR=:8443
OGP_ADMIN_USER=admin
OGP_ADMIN_PASSWORD=...
```

The installer stores them in:

```text
/etc/open-go-panel/open-go-panel.env
```

## Service

```bash
systemctl status open-go-panel
systemctl restart open-go-panel
journalctl -u open-go-panel -f
```

## Security

The current preview serves plain HTTP on port `8443`.

Do not expose it as a production root control panel on an untrusted network yet. HTTPS and additional hardening are planned before the first stable release.

## Planned

- server overview and metrics;
- Linux users;
- web terminal;
- systemd services;
- Caddy site management;
- databases;
- runtime/package management.
