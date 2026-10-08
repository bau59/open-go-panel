# Open Go Panel

Lightweight open-source Linux server control panel written in Go.

> Production baseline for single-administrator, developer-managed Ubuntu servers. The panel runs privileged server-management operations and is intended to be accessed through an SSH tunnel, not exposed as a public multi-tenant hosting panel.

## Included

- single Go binary;
- SQLite panel state;
- Linux users and SSH keys;
- applications with systemd services, resource limits and live journal logs;
- application health checks;
- Git deploy, one-step rollback and optional automatic deployment when the configured branch changes;
- domains, automatic TLS and per-domain Caddy configuration;
- MySQL and PostgreSQL provisioning;
- Redis installation, metrics, key browser and cache controls;
- one-time remote MySQL/PostgreSQL snapshot import with automatic pre-import safety backup;
- application ↔ database attachments;
- MySQL, PostgreSQL and Redis runtime metrics plus resource-aware MySQL tuning preset;
- database backups, restore and daily retention schedule;
- Adminer behind the panel session;
- CrowdSec Security Engine and nftables firewall bouncer;
- UFW recommended firewall policy;
- structured CrowdSec decisions, allowlist and alerts;
- audit log for successful panel changes;
- system-wide software management for Docker, Node.js LTS, Tailwind CLI, Go, Air, Git, build tools and common CLI utilities;
- Docker container management: pull/run from image or registry URL, list, start, stop, restart, remove and autostart policy;
- authenticated web terminal with root and managed-user shells; SSH tunnel remains the recommended access method;
- release checksum verification, post-update health checks and automatic binary rollback;
- Linux amd64 and arm64 release builds.

## Install

Run as root on Ubuntu:

```bash
curl -fsSL https://raw.githubusercontent.com/bau59/open-go-panel/main/install.sh | bash
```

The panel is distributed as a precompiled binary; Go is optional and is
installed separately via **Software → Go** when needed for hosting Go apps.
If `/usr/local/go/bin/go` and `gofmt` are already installed when you run
`install.sh`, the installer automatically creates missing
`/usr/local/bin/go` and `/usr/local/bin/gofmt` symlinks. This also runs on
panel updates. Existing executables and symlinks are never overwritten, so
managed application users can find Go without manual PATH changes.

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
MySQL / PostgreSQL / Redis
Docker
Git / OpenSSH
CrowdSec
nftables firewall bouncer
UFW
```

Application files remain under the Linux owner's home directory. Removing an app from the panel preserves its application files.

### Git deployment safety

Git deployments and rollbacks now build a separate copy alongside the active
application directory. They prepare dependencies in that staging directory,
atomically exchange the prepared release with the active path on Linux
(`renameat2(RENAME_EXCHANGE)`), restart the service, and check the service
state/listener before recording the new commit. If activation fails, the panel
atomically switches back to the previous files and attempts to restart the
previous service. On filesystems that cannot exchange directories atomically,
the deploy is refused instead of falling back to unsafe in-place replacement.

**Store persistent application data outside the Git release directory.**
Staging copies the current directory at one moment in time; writes to files
inside it while a deployment is in progress can be lost when the new release
is activated. Use a separate data directory or object storage for uploads,
databases, and other mutable files. Filesystem activation is atomic, but a
systemd restart still entails a brief application interruption; it is not
zero-downtime blue/green traffic switching.

Caddy-managed sites are isolated in:

```text
/etc/caddy/open-go-panel/*.caddy
```

The root Caddyfile imports that directory, so unrelated manual Caddy configuration can coexist with Open Go Panel.

Database backups are stored in:

```text
/var/lib/open-go-panel/backups/databases/
```

### Off-site database backup storage

**Do not keep your only backup on the VPS.** Use a separate S3-compatible
bucket (Backblaze B2, Cloudflare R2, AWS S3) or an independent SFTP server.
Open Go Panel can upload each completed manual or scheduled database backup
through [rclone](https://rclone.org/), and keeps the backup destination in
panel settings. Provider access keys stay in rclone's root-owned config, not
in the panel database.

On the VPS:

```bash
sudo apt-get update && sudo apt-get install -y rclone
sudo rclone config
sudo chmod 600 /root/.config/rclone/rclone.conf
sudo rclone lsd s3:
```

Choose the backend and credentials for **your** off-server storage when
configuring the `s3` remote. The remote name can differ.

In **Databases → Automatic backups → Off-site database backups**, enter a
destination such as `s3:my-backup-bucket/open-go-panel` and save. New database
snapshots are uploaded under `mysql/<database>/<timestamp>.sql.gz` or
`postgres/<database>/<timestamp>.sql.gz`. An upload failure leaves the local
snapshot on disk and reports an error; the scheduled batch does not prune
that database's backups. The local retention limit does **not** delete remote
objects: configure a separate storage-provider lifecycle policy.

Run a real restore drill and enable provider-side encryption, credential
least privilege, and object versioning/immutability where available. When no
remote is configured, backups remain local only.

Docker-published container ports bind to `127.0.0.1` by default. To publish
ports on all interfaces, explicitly enable **Public ports** when creating a
container. Public Docker ports may bypass UFW policy, so protect them separately.

## Open the panel only when needed

The panel normally listens on `127.0.0.1:8443`, reachable through an SSH tunnel.
Use **Close panel** in the top navigation to schedule a stop of
`open-go-panel.service`. The action also disables its autostart on reboot.
Your websites, managed services, containers, Caddy, and databases are not stopped.

To reopen it on demand, connect with SSH and run:

```bash
sudo systemctl start open-go-panel.service
```

If you are accessing the server remotely, establish a local SSH tunnel:

```bash
ssh -L 8443:127.0.0.1:8443 root@YOUR_SERVER
```

Then open `http://127.0.0.1:8443`. To restore automatic startup at boot
explicitly, run `sudo systemctl enable open-go-panel.service`. Note that
running the panel installer/upgrade enables and starts the panel again.

## Build Docker containers from private GitHub repositories

In **Docker → Build from GitHub**, enter your `owner/repository` and click
**Generate SSH deploy key**. Copy the displayed **public key** into the
matching GitHub repository's **Settings → Deploy keys → Add deploy key**.
Leave write access **disabled**. GitHub deploy keys are repository-specific;
generate a distinct key for each private repository.

Provide the repository URL, optional branch (blank uses the default branch),
Dockerfile path relative to the repository, container name, ports, and
autostart setting. You can also configure container runtime settings:

- **Environment variables** (one `KEY=value` per line, with blank lines and
  `#` comments allowed), for example `BRIDGE_API_KEYS=...` and
  `STATE_ENCRYPTION_KEY=...`. These are passed through a restricted,
  temporary environment file to `docker run`, not to the image builder.
  Alternatively, specify an existing private **server .env file**, such as
  `/opt/deepseek-bridge/.env` (permissions 0600); the panel reuses that file
  without copying or overwriting its secrets. Enter variables OR a file path.
- **Persistent mounts** (one `/host/path:/container/path` or
  `named_volume:/container/path` per line). For a DeepSeek bridge,
  `/opt/deepseek-bridge/data:/app/data` preserves session state across
  container removal/recreation. Missing host data directories are created
  with 0700 permissions; existing contents are not deleted.
- **Init** and **shared memory** (e.g. `512m`) are optional runtime
  settings for browser-based/Playwright apps.

Click **Build & run**. The panel clones the source into a
temporary build context, runs `docker build`, creates the container, and
removes the temporary clone. By default Docker ports bind to `127.0.0.1`;
explicit public publishing is optional. GitHub SSH host keys are fetched
from GitHub's HTTPS metadata API and verified during SSH clone. The SSH
private key remains server-side under `/etc/open-go-panel/docker-git/`
with restricted permissions. The build never passes the private key into
Docker as an argument, layer or build context.

**Existing containers are not recreated by these forms.** If you have
already configured a live container manually, its environment and mounts
remain unchanged. Enter the same persistent mount and environment values
when deliberately creating a replacement; Docker will otherwise reject a
duplicate container name. Keep existing `STATE_ENCRYPTION_KEY` unchanged
for encrypted DeepSeek sessions. The UI does not show or retrieve existing
secret values.

Public GitHub repositories can be built without a deploy key. Git, an SSH
client and a running Docker daemon are required. The build runs as a
background operation in the **panel process**; do not close or restart the
panel before it finishes. Private clone and build require outbound access
to GitHub over SSH (port 22) and HTTPS (GitHub host-key metadata).

## Updating a Docker container built from GitHub

Open **Docker → Containers → Rebuild GitHub** on the corresponding row.
For builds created before source labels were recorded, supply the GitHub
repository (for example `bau59/deepseek-web-api-bridge`), branch and Dockerfile
manually the first time. New builds retain repository, branch, Dockerfile
and commit metadata in the image labels.

**Rebuild & deploy** is an asynchronous operation. It clones and builds a
fresh image first; a failed Git clone or Docker build does not touch the running
container. After a successful build, the panel inspects the existing container
and preserves its environment (including existing encryption keys), published
host/IP ports, bind/named volumes, restart policy, init, shared memory, user and
working directory. It stops the old container, renames it to an
`ogp-prev-...` backup, disables that backup's autostart, then creates the new
container using the new image. If the new process exits promptly or its Docker
HEALTHCHECK fails, the panel attempts to remove that replacement and return
the previous container to its original name and state.

**This is not a zero-downtime or database-transaction rollback.** There is a
short restart window. Without a Docker HEALTHCHECK only process survival is
verified for three seconds, not full application readiness. Back up application
data before deploying breaking schema updates. The previous Docker container
is retained but shares any attached persistent volumes with the new one;
an application data migration cannot automatically be undone. Special
networking, privileges and unsupported mount configurations are rejected
rather than silently lost.

The Docker landing page lists containers before creation controls. It samples
CPU and memory from Docker every ten seconds. **Run container** and
**Build from GitHub** creation forms are collapsed until their buttons are
clicked. Closing the panel interrupts an in-progress rebuild, so wait for the
build task to finish before using **Close panel**.

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
MySQL/PostgreSQL/Redis data
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

Even purge mode intentionally leaves Linux users, application files, actual MySQL/PostgreSQL databases, Redis data/packages, Caddy, Docker, CrowdSec and UFW untouched.

## Security model

The panel is intended to be reached through an SSH tunnel and binds to localhost on new installations. Application traffic is exposed through Caddy. CrowdSec provides behavioral detection and dynamic bans; the official firewall bouncer enforces decisions through nftables; UFW provides the static inbound policy.

The panel currently uses a single administrator account and in-memory web sessions. Login attempts are throttled, HTML responses carry restrictive browser security headers, and terminal WebSocket connections require the authenticated same-origin panel session. Because the terminal can open a root shell, SSH-tunneled or otherwise protected panel access is strongly recommended. It should not be treated as a public multi-tenant hosting control panel.

## Release safety

Release artifacts include a `SHA256SUMS` file. The installer verifies the selected binary and bundled terminal assets before replacing the running panel.

During an update the previous binary is kept at:

```text
/usr/local/bin/open-go-panel.previous
```

After replacement the installer restarts Open Go Panel and checks `/health`. If the new binary fails to start or does not become healthy, the previous binary is restored automatically.

Release builds are gated by Go tests/vet plus an Ubuntu 24.04 production smoke test that installs the panel, authenticates through the web UI, creates a Linux user and app, checks self-hosted terminal assets, exercises Redis access, and verifies automatic rollback with an intentionally broken update.

## Next

- persistent background-job history and richer progress UI;
- richer deployment history;
- optional file manager;
- broader real-server integration coverage for MySQL/PostgreSQL/CrowdSec/Docker.
