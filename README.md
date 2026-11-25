# Open Go Panel

**Self-hosted Linux server management and project-scoped AI access.**

Open Go Panel is a lightweight control panel written in Go for developers who manage their own Ubuntu VPS. It manages native Linux services, application deployments, databases, Docker workloads and disaster recovery from one interface. An independent Model Context Protocol (MCP) service lets ChatGPT work directly with an explicitly authorized application on the server.

The administrative panel is designed for a single administrator and normally stays behind an SSH tunnel. You can close the panel without stopping websites or the separate MCP service.

[Download releases](https://github.com/bau59/open-go-panel/releases) · [Build and test status](https://github.com/bau59/open-go-panel/actions) · [Report an issue](https://github.com/bau59/open-go-panel/issues)

> This is a privileged server-management tool for trusted administrators, not a public multi-tenant hosting platform. MCP isolates direct project tools; it is not a replacement for isolating deployed applications from one another.

## Features at a glance

| Area | Capabilities |
| --- | --- |
| Applications | Go, Node.js, PHP and static sites; Linux accounts, SSH keys, systemd lifecycle, environment, resource limits, health checks, journal logs |
| Deployment | Git deploy, configurable auto-deploy intervals, staged atomic directory exchange, rollback, Go Air development → prebuilt production binary |
| Files | Project-scoped editor, uploads/downloads, bulk move/delete, archive extraction, collision checks |
| AI / MCP | Separate OAuth connection per app, eight project tools, version-checked editing, isolated shell commands, optional MySQL and Redis TCP access |
| Automation | Per-app cron expressions backed by systemd timers, manual runs and task logs; independent background terminal commands |
| Web infrastructure | Caddy domains, automatic TLS, static files, PHP FastCGI, reverse proxy, per-domain configuration and global defaults |
| Data | MySQL and PostgreSQL provisioning, app attachments, SQL imports, safety backups, Adminer; Redis metrics, key browser and slow commands |
| Containers | Docker image/registry launch, private GitHub builds with deploy keys, rebuilds, rollback containers, environment and persistent mounts |
| Backups | SQL backup schedules, local and cloud application/server snapshots, Yandex Disk, rclone off-site SQL backups, fresh-server recovery and exports |
| Observability | CPU/RAM/task counters, app and Caddy journals, on-demand HTTP latency reports, database slow logs, configurable optional logging and retention |
| Security | CrowdSec, nftables bouncer, UFW, trusted addresses, CSRF protection, authenticated terminals, audit history |
| Runtime management | Go, Air, Node.js LTS, PHP-FPM, PHP extensions and ionCube, isolated legacy PHP 7.4, Tailwind CLI, Git and build utilities |
| Operations | One Go executable, SQLite state, Linux amd64/arm64 binaries, SHA-256 verified updates, service health checks and automatic binary rollback |

The server-rendered web UI uses HTMX for navigation and updates, shared CSS and small vanilla JavaScript helpers. Applications, databases, Redis, Caddy, Docker, Software, Security, Performance and Log retention use section tabs; the Backups page separates server/application snapshots, SQL backups and cloud storage. Native forms remain available without JavaScript.

## Contents

- [Install and connect](#install)
- [AI access: ChatGPT and MCP](#ai-access-chatgpt-and-mcp)
- [Applications and PHP runtimes](#php-74-for-legacy-ioncube-sites-per-application)
- [Application files](#application-files)
- [Configuration and managed infrastructure](#configuration)
- [Git deployment safety](#git-deployment-safety)
- [Database and off-site backups](#off-site-database-backup-storage)
- [Server and application recovery](#yandex-disk-and-server-recovery)
- [Docker builds and updates](#build-docker-containers-from-private-github-repositories)
- [Go development to production](#go-development-air--production-binary)
- [Scheduled tasks and background commands](#application-scheduled-tasks-cron)
- [Observability](#observability-and-log-retention)
- [Service, update and uninstall](#service)
- [Security model and release safety](#security-model)

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

For PHP websites, install **Software → PHP + PHP-FPM** first, create a **PHP web app**, then deploy files and connect a domain. The domain form lets you choose the `public/` directory (Laravel/frameworks) or the application root (WordPress/plain PHP). Each app runs in an on-demand PHP-FPM pool under its Linux user, with a dedicated Unix socket for Caddy; no application HTTP port or per-app systemd service is created. Configure PHP application secrets in its project configuration or `.env`, not via the systemd environment editor. PHP-FPM daemon CPU/memory use is shared across sites.

To install additional PHP extensions, open **Software → PHP runtimes → PHP + PHP-FPM → Manage PHP extensions**. The panel offers an allowlist of Ubuntu extensions (GD, Intl, BCMath, SOAP, SQLite3, Redis, Imagick, APCu, OPcache) and the third-party **ionCube Loader**. Packages are installed for the detected PHP-FPM major/minor version, not arbitrary system PHP versions. Extensions affect **every application using that PHP-FPM runtime**; this is not a per-app toggle. Installation is asynchronous and PHP-FPM is reloaded only after a configuration test.

ionCube is a proprietary PHP Zend extension downloaded via HTTPS from the [official ionCube Linux Loaders](https://www.ioncube.com/loaders.php) for the panel's AMD64 or ARM64 architecture. The panel selects only the library matching the active PHP version, verifies its ELF architecture, checks that PHP can load it, and validates PHP-FPM before reload. It restores the previous managed configuration on test/reload failure. It does not bypass ionCube licensing or PHP-version compatibility: protected files must be compatible with the installed PHP runtime. If the official download stalls or its connection closes mid-transfer, the panel retries up to three times with a short backoff; partial archives are discarded and never installed. Network failures leave the PHP-FPM configuration unchanged.

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

## AI access: ChatGPT and MCP

Open Go Panel exposes a real remote MCP server, not an SSH command wrapper that gives an AI unrestricted server access. Each connection grants access to **one application**. The assistant can inspect a live project, read source files, make controlled edits, run local commands and, when separately permitted, work with local databases using the project's existing credentials.

### Connect an application

1. Open **Infrastructure → ChatGPT access** in the private panel. Set a dedicated HTTPS hostname (for example, `mcp.example.com`) with DNS pointing to the VPS. The panel configures Caddy; use **Test HTTPS connection** to verify it.
2. Open the target application → **ChatGPT access** tab → **Enable connection**. Copy its plugin name and MCP URL.
3. In ChatGPT on the web, open **Plugins → + → Add custom MCP server** (availability and UI labels depend on the account). Use the supplied MCP URL and OAuth with dynamic registration.
4. Match the request ID on the OAuth browser page with the pending request shown in the private application panel. Approve that request, then click **Continue after approval** in the OAuth browser.
5. Select the installed plugin with `@` in a chat. Start with a read-only inspection, and approve changes deliberately.

Each additional project requires its own connection. The application's **Revoke access** action invalidates its authorization. Existing authorizations survive panel restarts and in-place updates.

### MCP tools: machine-readable contract

The service advertises these eight tools through MCP `tools/list`. Tool arguments are JSON objects. Paths are relative to the selected application root, which is mounted at `/workspace` inside the sandbox. Use `.` for the root directory.

| Tool | Arguments | Behavior |
| --- | --- | --- |
| `list_directory` | `path?` | List directory entries; reports truncation (up to 400 entries) |
| `search_files` | `query`, `path?`, `context_lines?` | Literal text search with paths, line numbers and up to three context lines; bounded scan and output |
| `read_file` | `path`, `start_line?`, `end_line?` | Read UTF-8 text or a 1-based inclusive line range; returns a version for editing |
| `create_file` | `path`, `content` | Create a new text file; never overwrite an existing file |
| `edit_file` | `path`, `content`, `version` | Replace complete file content only if its read version still matches |
| `create_directory` | `path` | Create one directory; parent must exist |
| `delete_path` | `path`, `recursive?` | Delete a file or empty directory; recursive deletion requires explicit opt-in |
| `run_command` | `command`, `workdir?`, `timeout_seconds?` | Run a noninteractive shell command as the Linux app owner; returns output, exit code, timeout and truncation flags |

`?` denotes an optional argument. The server rejects unknown parameters and paths that escape the project. Read/edit text files are limited to 2 MiB. Commands have a maximum 120-second runtime and 256 KiB output. Search results are bounded; a truncated result is not proof that there are no additional matches. There is no fixed total project-file limit. Shell startup still checks for preexisting hardlinks, and that check counts toward the timeout.

The HTTP transport uses JSON-RPC and JSON responses. The public resource URL is generated by the panel; it is not a generic root-shell endpoint. OAuth uses authorization code with S256 PKCE, dynamic registration, short-lived access tokens and rotating refresh tokens. The current implementation validates the ChatGPT callback rather than advertising unrestricted OAuth-client compatibility.

### Optional database network access

Open the application → **ChatGPT access → Database network access**. Enable only the required service, then save.

| Permission | Endpoint inside sandbox | Scope |
| --- | --- | --- |
| MySQL | `127.0.0.1:3306` | Host-local MySQL TCP service; database permissions depend on supplied credentials |
| Redis | `127.0.0.1:6379` | Host-local Redis TCP service; actual key permissions depend on Redis ACLs |

Both permissions are disabled by default. Settings are associated with the individual MCP grant and apply to subsequent commands. The sandbox uses a private network namespace and relays only the selected IPv4 TCP endpoints. There is no general host network, arbitrary Unix-socket access, Internet access or PostgreSQL relay.

Example **inside an authorized MCP command**, assuming the required CLI is installed:

```bash
mysql --protocol=TCP -h 127.0.0.1 -u APP_DB_USER -p
redis-cli -h 127.0.0.1 -p 6379 PING
```

The MySQL example requires interactive password input and is intended as a connection reference: for actual noninteractive MCP commands, use the application's existing authorized credential configuration without printing passwords or passing them in the prompt. Prefer read-only SQL for diagnosis. For authenticated Redis, use the application's configured credentials.

**Redis may be shared by multiple applications.** Enabling access does not provide key-level isolation by itself. Use Redis ACLs and least-privilege SQL accounts; selecting a Redis database number is not a security boundary. Do not enable these permissions if access to the shared service is unacceptable.

### Suggested instructions for an AI working on a project

Copy or adapt this prompt after selecting the appropriate project plugin:

> Work only in the connected application. Inspect the existing files and project conventions first. Use relative paths, read a file before editing, pass its current version, and preserve unrelated code. Do not expose .env values, credentials or private keys in responses. Start database diagnostics with read-only queries. Ask before destructive file operations, schema changes, data deletion or production-impacting commands. Run available local tests and report the files changed, checks performed and any failures. Do not claim success when tools return an error or truncated output.

Useful first requests include inspecting `go.mod` or `package.json`, mapping application modules, diagnosing a build failure, reviewing a query, making a small version-checked change, or running an existing local test. Dependencies must already be available inside the project or read-only system runtime; ordinary network downloads and host-user caches are intentionally unavailable. The AI receives no panel-wide deployment, root terminal or administrator API through this plugin.

### Isolation and service lifecycle

The installer runs `open-go-panel-mcp.service` independently using the same Go binary in MCP mode, listening on `127.0.0.1:18443`. Caddy exposes only MCP, OAuth discovery/authorization and health routes on the dedicated HTTPS domain. The administrative panel remains private.

Each command or file helper executes under the app's Linux UID/GID in separate mount, PID, network and IPC namespaces. Only the project, read-only system runtimes and private temporary files are exposed. File tools reject individual hardlinked files; shell commands check the project tree for hardlinks. There is no unisolated fallback. Concurrent operations are bounded (one operation per app, up to four globally). Audit records include operation, path, timing and result when audit logging is enabled, not command text or file contents.

The plugin grants real read/write/execute access to project files, including secrets accessible to the owner. Use only trusted AI clients and grant access only to projects you intend to expose. Direct tool isolation is not isolation of the separately deployed application itself. Closing the panel does not stop MCP. OAuth grants are excluded from server snapshots so restoring a backup cannot reactivate revoked access; authorize again on a recovered server.

```bash
systemctl status open-go-panel-mcp.service
journalctl -u open-go-panel-mcp.service -n 100 --no-pager
```

Use the panel to configure and revoke connections. Do not expose the local listener directly or disable sandbox checks to work around a connection failure.

## PHP 7.4 for legacy ionCube sites (per application)

PHP files encoded for PHP 7.4 cannot execute under PHP 8.3, even when the latest ionCube Loader is correctly installed. Open Go Panel can run such sites in **separate PHP 7.4 Docker containers** while leaving modern PHP-FPM sites unchanged.

1. Install **Software → Docker Engine**, if it is not already installed.
2. In **Software → PHP runtimes → PHP 7.4 (isolated / legacy)**, choose **Install** to build the local `open-go-panel/php74:local` image. This is an asynchronous Docker build using the archived official `php:7.4-fpm-bullseye` base image, with `mysqli`, `pdo_mysql`, `gd`, `zip`, `mbstring`, `intl`, `imagick` and other common extensions. Errors appear in the Software task status.
3. Download the matching official Linux [ionCube Loader archive](https://www.ioncube.com/loaders.php) onto your computer. Upload that archive via **Software → PHP runtimes → PHP 7.4 → Upload PHP 7.4 Loader**. The panel extracts only the PHP 7.4 ELF library; **no direct ionCube download from the VPS is performed**.
4. Open the PHP app under **Applications → Settings → PHP runtime**, choose **PHP 7.4 + ionCube (isolated Docker)** and click **Apply PHP version**. The panel checks the Loader inside the PHP 7.4 image, starts an app-owned container and switches only that domain's Caddy FastCGI upstream.
5. Revisit the site and check **PHP errors** if the application still fails. To return to the current system PHP-FPM, choose **System PHP-FPM** in the same app settings.

For an already copied ionCube archive on the host, the panel also accepts an admin-prepared loader at `/usr/local/lib/open-go-panel/ioncube/amd64/ioncube_loader_lin_7.4.so` (or corresponding ARM64 path); use only the official vendor archive.

**Security and compatibility:** PHP 7.4 and the Bullseye base image have reached end-of-life. The Docker runtime runs as the site's unprivileged UID/GID, mounts only the site's files, its private PHP error log, the Loader and (when present) the host MySQL socket directory, and publishes its FastCGI listener **only to 127.0.0.1**. Containers have dropped Linux capabilities, read-only root filesystems and memory/CPU/PID limits. They still have bridge-network access for application HTTP requests, so this is not a security substitute for upgrading old code.

When MySQL runs on the host via the default `/run/mysqld/mysqld.sock`, that socket is passed into the container for ordinary `localhost` MySQL connections. An application explicitly configured to use `127.0.0.1:3306` or other host-local TCP services must be reconfigured: inside a container, `127.0.0.1` is the container itself. The Docker host gateway is available as `host.docker.internal` if the host database allows bridge connections.

The image build requires reachable Docker image/package registries. Unavailable old Debian repositories or incompatible legacy dependencies are reported as build errors; Open Go Panel does not downgrade or install PHP 7.4 into the host operating system.

## Application files

Open **Applications → Files** (or **Files** in an application's header). Every filesystem operation runs in a short-lived process as the application's Linux user, using that user's normal filesystem permissions; the root-owned panel does not read or modify app files directly. The manager remains confined to the selected application's root. It supports:

- browsing directories and editing UTF-8 text files (up to 2 MiB);
- uploading a file (up to 1 GiB), downloading files, creating files or folders;
- moving/renaming files and folders, copying individual files, deleting files or directories;
- selecting multiple files/folders for a bulk move or delete (up to 400 visible entries at once), with no overwrite on name collisions;
- moving **all contents of a folder one level up** in a single action, including entries not visible on the page (up to 100,000 direct children); subfolders move as trees, and the source folder remains empty for optional deletion;
- extracting ZIP, TAR.GZ and TGZ archives without overwriting existing files.

Uploads are streamed to temporary disk storage with a maximum of 1 GiB per file. Make sure the server has enough free disk space for the temporary upload, the destination file, and any database safety backup. Editing and archive extraction are one-time operations, not background tasks. Bulk moves preflight every target name and refuse collisions; if a filesystem error interrupts the batch, the panel attempts to roll back moved entries. Bulk deletions are permanent and may be partially completed if an error occurs. A destination folder is entered relative to the application root (`.` means the root). Archive extraction is limited to 100,000 entries and 4 GiB total uncompressed data. Unpacking may leave already-extracted files in place if a later entry fails; existing files are never replaced. Symbolic links can be seen and moved/deleted as entries, but are never followed. Paths are relative to the app's root; server configuration outside that root is deliberately inaccessible. On upload, the panel compares the browser-selected file size with the HTTP multipart payload, then verifies SHA-256 of the streamed bytes against the file read back from disk under the application's Linux user. A mismatch fails the upload and deletes the unverified destination. ZIP extraction accepts valid ZIP indexes even when non-ZIP bytes were appended after the ZIP end record. Unsupported formats (including RAR/7z renamed to .zip) and damaged ZIP indexes are rejected with a format-specific diagnostic. If the source is invalid, verify it with `unzip -t` and compare its SHA-256 to the original; the panel never attempts unsafe extraction or modifies a corrupt archive.

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

Open Go Panel manages native Linux components directly. Docker is available for container workloads and isolated legacy PHP runtimes:

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

To import an SQL file, open **Databases → the selected database → Import SQL**. Upload a `.sql` or `.sql.gz` file (max 1 GiB uploaded; compressed SQL expands to at most 4 GiB). Choose **Apply to existing database** to execute the script without first clearing existing tables, or **Replace database contents** to drop and recreate only the selected managed database before applying the script. Imports execute asynchronously and display success/failure on the Databases page. A safety backup is created before any modifications; if the SQL command fails, the panel attempts to restore the original database from that backup. Imported SQL runs under the managed database user and an unprivileged Linux process. SQL is executable code that may alter or remove data; verify the file's origin before importing.

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

In **Backups → Cloud storage**, enter a
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
Your websites, managed services, containers, Caddy, databases, independent MCP service, scheduled app timers and already started background commands are not stopped. Panel-process tasks, including Git auto-deploy checks and ongoing Docker builds, do not continue while the panel is closed.

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

In **Docker → Create container → Build from GitHub**, enter your `owner/repository` and click
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

The Docker page opens with the Containers tab and samples CPU and memory
from Docker every ten seconds. Use **Create container** for Run container
and Build from GitHub, and **Previous versions** to inspect rollback containers
when available. Closing the panel interrupts an in-progress rebuild, so wait for the
build task to finish before using **Close panel**.

## Per-app Git auto-deploy intervals

On an application's **Deploy** tab, enable **Auto deploy** and enter the
**Check interval (seconds)**. Supported values are **30–86400 seconds**:
`30` checks every 30 seconds, `60` every minute and `300` every
5 minutes (the default for existing installations). You can set a different
value for each application. Save with **Save Git settings**.

The scheduler evaluates which applications are due every five seconds.
Per-application last-check timestamps and interval settings are persisted
in SQLite across panel restarts. A failed remote Git check counts as a check,
so it is retried at the next configured interval rather than continuously.
Only changed commits trigger deployment. Checks may be delayed during a
running deployment; the panel serializes deployment switches to prevent
overlapping updates. Scheduled database backups run independently and no
longer hold up Git checking.

A manually promoted **Go prebuilt production binary** application is
excluded from Git Auto deploy; releases for that mode use **Build →
Production** from a separate development application.

## Go development (Air) → production binary

Create two **Go** applications with separate roots and ports:

1. Set your development application's Settings → Service settings → **Go: Air live
   reload (development)**. It runs `air` directly and reloads the local
   source code. Git updates to an Air app no longer run a separate `go mod
   download` staging step; Air is responsible for its own runtime build.
2. Create a separate production application. Set Settings → Service settings →
   **Go: prebuilt production binary**. Keep its own environment variables,
   Caddy domain, port, persistent data and autostart settings.
3. Open the **development** application → **Deploy → Dev → Production**. Select the
   production application and the Go main-package directory (normally `.`,
   or `./cmd/server`). Click **Build → Production**.

The panel copies the current development tree to a temporary directory and
runs `go build` there as the development Linux user. The live Air working
tree is never modified or stopped. Once compilation succeeds, **only the
production `.ogp-app` executable is atomically replaced**; the production
directory, databases, uploads and session data remain in place. The previous
executable is saved as `.ogp-app.previous` for manual rollback. The panel
then restarts the existing production systemd service and verifies that it
is active and listening. If activation fails, it automatically restores
the previous executable and restarts the service. A **Rollback production
binary** button is available on the production app page after the first
successful replacement.

The production environment, ports, domains and data are **not copied from
development**. Do not place production secrets in a development Git checkout.
The build task runs within the panel process: wait for completion before
closing the panel or restarting it. Go is required on the server to compile
the production executable, but is **not required at runtime** for the
prebuilt production mode.

Production binaries cannot be changed by the panel's ordinary Git Deploy
or Auto Deploy actions. Create a separate production app and use
**Build → Production** instead. The release switch is not a database
transaction: manage database schema changes separately. In a production
app with no listener yet, ensure the new program binds to the target
application's `PORT` environment variable for readiness checks.

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

Release builds are gated by Go tests/vet, browser regression checks for section tabs and checkbox geometry, Ubuntu 24.04 production smoke tests, managed-server snapshot recovery tests and MCP integration tests. The production smoke exercises installation, authentication, managed users/apps, terminal assets, background commands, PHP extension checks, SQL imports and automatic binary rollback. MCP tests cover OAuth, filesystem isolation, large projects, revocation and permitted database TCP relays. These checks do not replace a restore drill or testing your own application.

## Application scheduled tasks (cron)

Open an application → **Scheduled tasks**. Each application may have
multiple jobs with a name, command, five-field cron schedule and enable switch.
Presets such as `@hourly`, `@daily`, `@weekly` and `@monthly` are supported.
Examples:

- `*/5 * * * *` — every five minutes;
- `0 3 * * *` — every day at 03:00 server-local time;
- `0 9 * * 1-5` — weekdays at 09:00.

The panel translates cron syntax (including cron's day-of-month/weekday OR
semantics) into a managed systemd `.timer` and `.service` for each job. Timers
continue working while the panel is closed and after reboot. Jobs of the same
ID do not overlap. There is no Go polling loop, no crontab modification and no
missed-run catch-up after downtime (`Persistent=false`). Jobs time out after
15 minutes; use the page's **Run now** and **Logs** actions for inspection.
Each task runs in its application's root directory with the application's
Linux user and inherited managed service environment where available.
Disabled jobs can still be run manually. Deleting an app removes its timers.

PHP 7.4 jobs run using `docker exec` inside the app's *existing* isolated PHP
container; the runner first checks the panel-specific Docker labels. PHP 8.x
and other app types execute as the managed Linux user on the host. Switching
the PHP runtime refreshes scheduled command runners.


## Background commands

Use **Background commands** in the panel to launch a noninteractive command under a chosen Linux account and working directory, without leaving a browser terminal open. The panel stores the command definition and starts a separate transient systemd service. View status and journal output, stop the service and its child processes, or remove its saved record after it stops.

Already-started commands survive browser closure and panel restarts. They do not restart automatically after a normal server reboot. For recurring work use per-app scheduled tasks; for always-on workloads use a managed application service. Interactive terminal sessions and MCP shell commands are separate features.

## Observability and log retention

The dashboard shows managed application and Docker resource use. Application health checks, live/historical journals, Caddy access logs, database slow-query views and Redis SLOWLOG provide diagnostics without requiring a separate monitoring stack.

**Performance** generates reports on demand from existing Caddy logs, with duration trends, slow-route percentiles and request details. It does not continuously import requests into SQLite. Caddy durations are not isolated application CPU time or browser latency.

**Logs & retention** manages journal retention, optional Caddy access logs, managed app stdout/stderr, supported database slow-query logging, Redis SLOWLOG and the panel audit trail. Turning off optional logging does not disable essential system or security messages. App output logging changes take effect on the next service restart; existing records remain subject to retention. Custom web-server configuration or database-specific overrides may still log.

## Yandex Disk and server recovery

In **Backups → Cloud storage**, select **Yandex Disk**, enter the OAuth token with access to the chosen Disk scope, and specify a dedicated remote directory. Prefer application-folder access (`app:/`). The token is kept in a root-only credential file, not in the panel database.

In **Backups**, configure the server and each application's local and cloud policies independently. Intervals are in days, hours are UTC, zero disables a schedule. Cloud retention of zero means unlimited. Existing database backup settings are independent of these policies.

Completed snapshots are published under:

```text
<remote>/server/<UTC timestamp>/manifest.json
<remote>/server/<UTC timestamp>/config.tar.gz/manifest.json
<remote>/server/<UTC timestamp>/files/<user>.tar.gz/...
<remote>/server/<UTC timestamp>/databases/mysql/<database>.sql.gz/...
<remote>/projects/<user>/<app>/<UTC timestamp>/...
```

File archives preserve ACLs, extended attributes and symbolic links. A changing live tree is retried up to three times on tar exit code 1; a failed partial archive is discarded and diagnostics are reported. Archives are transferred in 900 MiB parts with SHA-256 verification. Interrupted cloud jobs retain the local pending snapshot for retry; verified remote parts are reused. The final snapshot manifest is published only after all archives complete. Splitting files does not bypass overall Disk quotas.

For managed-server recovery, install the panel on a clean Ubuntu host with the required database engines and application runtimes. Reconnect the same Disk folder, find cloud restore points in Backups, download and verify the desired snapshot, then confirm **Restore on fresh server**. Recovery refuses existing managed resources and conflicting Linux or SQL accounts. The panel stops and restarts during recovery; inspect `journalctl -u open-go-panel-restore.service`.

The server snapshot includes consistent SQLite state, Linux accounts and home directories, managed SQL databases and credentials, managed Caddy and service configuration, application cron jobs and previously running background command definitions. Active commands are restarted, not resumed from process memory. This is not a full operating-system image: Docker inspect metadata is exported, but Docker containers/volumes, arbitrary external mounts, unmanaged databases and operating-system packages are not automatically restored. Install required runtimes first. File and SQL snapshots are taken online and are not a single cross-service atomic point in time. A failed recovery may require a fresh host; the operation is not transactional across Linux and database engines.

Archives contain passwords, environment files and other secrets in their original form. They are not encrypted at rest by the panel. Restrict access to the cloud folder and only recover from trusted snapshots. Keep a separate copy of critical data.

Database backup schedules and history are under **Backups → Databases**. The Databases page retains quick local actions. Open **Cloud copies** for a database to list its remote SQL archives and retrieve a copy to the server; retrieval never changes the live database. Restore explicitly from local history after retrieval.
