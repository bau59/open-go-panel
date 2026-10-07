# Open Go Panel

Lightweight open-source Linux server control panel written in Go.

> Technical preview. Open Go Panel runs privileged server-management operations and is intended for developer-managed Ubuntu servers.

## Included

- single Go binary;
- SQLite panel state;
- Linux users and SSH keys;
- applications with systemd services, resource limits and live journal logs;
- application health checks;
- Git deploy and one-step rollback;
- domains, automatic TLS and per-domain Caddy configuration;
- MySQL and PostgreSQL provisioning;
- application ↔ database attachments;
- MySQL server metrics and resource-aware tuning preset;
- database backups, restore and daily retention schedule;
- Adminer behind the panel session;
- CrowdSec Security Engine and nftables firewall bouncer;
- UFW recommended firewall policy;
- structured CrowdSec decisions, allowlist and alerts;
- audit log for successful panel changes;
- Linux amd64 and arm64 release builds.

## Install

Run as root on Ubuntu:

```bash
curl -fsSL https://raw.githubusercontent.com/bau59/open-go-panel/main/install.sh | bash
```

New installations bind the panel to localhost:

```text
127.0.0.1:8443
```

Connect through SSH:

```bash
ssh -L 8443:127.0.0.1:8443 root@SERVER_IP
```

Then open:

```text
http://127.0.0.1:8443
```

The installer enables OpenSSH, installs Caddy when needed, downloads the latest release binary, creates the panel service and generates the initial admin password.

Existing installations keep their current `OGP_LISTEN_ADDR` during updates.

## Configuration

The installer stores panel configuration in:

```text
/etc/open-go-panel/open-go-panel.env
```

Supported variables:

```text
OGP_LISTEN_ADDR=127.0.0.1:8443
OGP_ADMIN_USER=admin
OGP_ADMIN_PASSWORD=...
```

Persistent panel state is stored under:

```text
/var/lib/open-go-panel/
```

The primary state database is:

```text
/var/lib/open-go-panel/panel.db
```

Legacy JSON state is migrated into SQLite automatically and is not deleted during migration.

## Managed infrastructure

Open Go Panel uses native Linux components instead of containers:

```text
OpenSSH
systemd
Caddy
MySQL / PostgreSQL
CrowdSec
nftables firewall bouncer
UFW
```

Application files remain under the Linux owner's home directory. Removing an app from the panel preserves its application files.

Caddy-managed sites are isolated in:

```text
/etc/caddy/open-go-panel/*.caddy
```

The root Caddyfile imports that directory, so unrelated manual Caddy configuration can coexist with Open Go Panel.

Database backups are stored in:

```text
/var/lib/open-go-panel/backups/databases/
```

## Service

```bash
systemctl status open-go-panel
systemctl restart open-go-panel
journalctl -u open-go-panel -f
```

## Update

Running the installer again performs an in-place update:

```bash
curl -fsSL https://raw.githubusercontent.com/bau59/open-go-panel/main/install.sh | bash
```

Updates preserve:

```text
/etc/open-go-panel
/var/lib/open-go-panel
Linux users and home directories
application files
MySQL/PostgreSQL data
Caddy certificates/configuration
CrowdSec state
UFW rules
```

## Uninstall

Remove the panel binary and service while preserving state:

```bash
curl -fsSL https://raw.githubusercontent.com/bau59/open-go-panel/main/uninstall.sh | bash
```

Purge Open Go Panel's own configuration and state:

```bash
curl -fsSL https://raw.githubusercontent.com/bau59/open-go-panel/main/uninstall.sh | bash -s -- --purge
```

Even purge mode intentionally leaves Linux users, application files, actual MySQL/PostgreSQL databases, Caddy, CrowdSec and UFW untouched.

## Security model

The panel is intended to be reached through an SSH tunnel and binds to localhost on new installations. Application traffic is exposed through Caddy. CrowdSec provides behavioral detection and dynamic bans; the official firewall bouncer enforces decisions through nftables; UFW provides the static inbound policy.

The panel currently uses a single administrator account and in-memory web sessions. It remains a technical preview and should not be treated as a multi-tenant hosting control panel.

## Next

- background jobs and progress for long-running deploy/backup/install operations;
- richer deployment history;
- runtime/package management;
- optional terminal and file management.
