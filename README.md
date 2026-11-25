# Open Go Panel

Lightweight open-source Linux server control panel written in Go.

> Production baseline for single-administrator, developer-managed Ubuntu servers. The panel runs privileged server-management operations and is intended to be accessed through an SSH tunnel, not exposed as a public multi-tenant hosting panel.

## Included

- single Go binary;
- SQLite panel state;
- Linux users and SSH keys;
- applications with systemd services, resource limits and live journal logs;
- PHP web applications through per-user PHP-FPM pools and Caddy FastCGI routing;
- per-application file browser/editor with upload, download, mkdir, rename/move, copy, delete and ZIP/TAR.GZ extraction;
- application health checks;
- Git deploy, one-step rollback and optional automatic deployment when the configured branch changes;
- domains, automatic TLS and per-domain Caddy configuration;
- MySQL and PostgreSQL provisioning;
- Redis installation, metrics, key browser and cache controls;
- one-time remote MySQL/PostgreSQL snapshot import with automatic pre-import safety backup;
- application ↔ database attachments;
- MySQL, PostgreSQL and Redis runtime metrics plus resource-aware MySQL tuning preset;
- database backups, restore and daily retention schedule;
- upload and import `.sql` / `.sql.gz` files into managed MySQL and PostgreSQL databases, with safety snapshots;
- Adminer behind the panel session;
- CrowdSec Security Engine and nftables firewall bouncer;
- UFW recommended firewall policy;
- structured CrowdSec decisions, allowlist and alerts;
- audit log for successful panel changes;
- system-wide software management for Docker, Node.js LTS, PHP-FPM, Tailwind CLI, Go, Air, Git, build tools and common CLI utilities;
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

For PHP websites, install **Software → PHP + PHP-FPM** first, create a **PHP web app**, then deploy files and connect a domain. The domain form lets you choose the `public/` directory (Laravel/frameworks) or the application root (WordPress/plain PHP). Each app runs in an on-demand PHP-FPM pool under its Linux user, with a dedicated Unix socket for Caddy; no application HTTP port or per-app systemd service is created. Configure PHP application secrets in its project configuration or `.env`, not via the systemd environment editor. PHP-FPM daemon CPU/memory use is shared across sites.

To install additional PHP extensions, open **Software → PHP + PHP-FPM → Manage PHP extensions**. The panel offers an allowlist of Ubuntu extensions (GD, Intl, BCMath, SOAP, SQLite3, Redis, Imagick, APCu, OPcache) and the third-party **ionCube Loader**. Packages are installed for the detected PHP-FPM major/minor version, not arbitrary system PHP versions. Extensions affect **every application using that PHP-FPM runtime**; this is not a per-app toggle. Installation is asynchronous and PHP-FPM is reloaded only after a configuration test.

ionCube is a proprietary PHP Zend extension downloaded via HTTPS from the [official ionCube Linux Loaders](https://www.ioncube.com/loaders.php) for the panel's AMD64 or ARM64 architecture. The panel selects only the library matching the active PHP version, verifies its ELF architecture, checks that PHP can load it, and validates PHP-FPM before reload. It restores the previous managed configuration on test/reload failure. It does not bypass ionCube licensing or PHP-version compatibility: protected files must be compatible with the installed PHP runtime. If the official download stalls or its connection closes mid-transfer, the panel retries up to three times with a short backoff; partial archives are discarded and never installed. Network failures leave the PHP-FPM configuration unchanged.

## PHP 7.4 for legacy ionCube sites (per application)

PHP files encoded for PHP 7.4 cannot execute under PHP 8.3, even when the latest ionCube Loader is correctly installed. Open Go Panel can run such sites in **separate PHP 7.4 Docker containers** while leaving modern PHP-FPM sites unchanged.

1. Install **Software → Docker Engine**, if it is not already installed.
2. In **Software → PHP 7.4 (isolated / legacy)**, choose **Install** to build the local `open-go-panel/php74:local` image. This is an asynchronous Docker build using the archived official `php:7.4-fpm-bullseye` base image, with `mysqli`, `pdo_mysql`, `gd`, `zip`, `mbstring`, `intl`, `imagick` and other common extensions. Errors appear in the Software task status.
3. Download the matching official Linux [ionCube Loader archive](https://www.ioncube.com/loaders.php) onto your computer. Upload that archive via **Software → PHP 7.4 → Upload PHP 7.4 Loader**. The panel extracts only the PHP 7.4 ELF library; **no direct ionCube download from the VPS is performed**.
4. Open the PHP app under **Applications → PHP runtime**, choose **PHP 7.4 + ionCube (isolated Docker)** and click **Apply PHP version**. The panel checks the Loader inside the PHP 7.4 image, starts an app-owned container and switches only that domain's Caddy FastCGI upstream.
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

## Per-app Git auto-deploy intervals

On an application's **Deploy** card, enable **Auto deploy** and enter the
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

1. Set your development application's Service settings → **Go: Air live
   reload (development)**. It runs `air` directly and reloads the local
   source code. Git updates to an Air app no longer run a separate `go mod
   download` staging step; Air is responsible for its own runtime build.
2. Create a separate production application. Set Service settings →
   **Go: prebuilt production binary**. Keep its own environment variables,
   Caddy domain, port, persistent data and autostart settings.
3. Open the **development** application → **Dev → Production**. Select the
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

Release builds are gated by Go tests/vet plus an Ubuntu 24.04 production smoke test that installs the panel, authenticates through the web UI, creates a Linux user and app, checks self-hosted terminal assets, exercises Redis access, and verifies automatic rollback with an intentionally broken update.

## Next

- persistent background-job history and richer progress UI;
- richer deployment history;
- optional file manager;
- broader real-server integration coverage for MySQL/PostgreSQL/CrowdSec/Docker.


## Application scheduled tasks (cron)

Open an application and expand **Scheduled tasks**. Each application may have
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


### Yandex Disk and server recovery

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

Archives are transferred in 900 MiB parts with SHA-256 verification. Interrupted cloud jobs retain the local pending snapshot for retry; verified remote parts are reused. The final snapshot manifest is published only after all archives complete. Splitting files does not bypass overall Disk quotas.

For managed-server recovery, install the panel on a clean Ubuntu host with the required database engines and application runtimes. Reconnect the same Disk folder, find cloud restore points in Backups, download and verify the desired snapshot, then confirm **Restore on fresh server**. Recovery refuses existing managed resources and conflicting Linux or SQL accounts. The panel stops and restarts during recovery; inspect `journalctl -u open-go-panel-restore.service`.

The server snapshot includes consistent SQLite state, Linux accounts and home directories, managed SQL databases and credentials, managed Caddy and service configuration, application cron jobs and previously running background command definitions. Active commands are restarted, not resumed from process memory. This is not a full operating-system image: Docker inspect metadata is exported, but Docker containers/volumes, arbitrary external mounts, unmanaged databases and operating-system packages are not automatically restored. Install required runtimes first. File and SQL snapshots are taken online and are not a single cross-service atomic point in time. A failed recovery may require a fresh host; the operation is not transactional across Linux and database engines.

Archives contain passwords, environment files and other secrets in their original form. They are not encrypted at rest by the panel. Restrict access to the cloud folder and only recover from trusted snapshots. Keep a separate copy of critical data.

Database backup schedules and history are under **Backups → Databases**. The Databases page retains quick local actions. Open **Cloud copies** for a database to list its remote SQL archives and retrieve a copy to the server; retrieval never changes the live database. Restore explicitly from local history after retrieval.


### ChatGPT access (MCP)

The installer delivers one independent `open-go-panel-mcp.service` using the
same panel binary in `mcp` mode. It listens on `127.0.0.1:18443`. Closing the
administrative panel does not stop MCP.

1. In **Infrastructure → ChatGPT access**, set a dedicated HTTPS hostname
   (for example, `mcp.example.com`) with DNS pointing to this VPS. The panel
   configures Caddy; test HTTPS before connecting an application.
2. In an application, open **ChatGPT access**, enable the connection and copy
   its plugin name and MCP URL.
3. In ChatGPT on the web, open **Plugins → + → Add custom MCP server**.
   Use the displayed URL, OAuth with dynamic registration, and install the
   personal plugin. Match the authorization request ID shown in the OAuth
   browser against the private panel request; approve it there, then continue
   in the OAuth browser.
4. In a new chat, select that plugin with `@`. A different application needs
   a different connection. Revoke access from the application's page.

The MCP service uses per-application OAuth authorization-code + S256 PKCE,
short-lived access tokens and rotating refresh tokens. Tokens are stored as
hashes, and deleting an application removes its grants. The public Caddy
site exposes only OAuth, MCP and health routes; no administrative route is
forwarded.

Commands and file helpers execute under the application owner inside a
separate Linux mount/PID/network/IPC namespace. Only the selected project
(`/workspace`), read-only system runtimes and private temporary files are
visible. Network sockets are disabled; dependency downloads and host-user
caches are intentionally unavailable. File tools reject access to individual hardlinked files because their inode
may also be visible in another project. Shell commands check the entire
project for hardlinks, streaming directory entries without a fixed total
file-count limit; this check counts toward the command timeout. Use vendored
dependencies or prepare dependencies through the normal deployment process.
There is no unisolated fallback. File edits require the version returned by
`read_file`; conflicting edits are rejected. Commands time out within 120
seconds and output is bounded to 256 KiB. This is direct tool isolation, not
isolation of the separately deployed application itself.

OAuth authorizations survive service restarts and panel updates. They are
excluded from full-server backups to prevent a restore from reviving revoked
access; authorize again on a recovered VPS. Real ChatGPT compatibility
requires completing the OAuth flow from the user's own web account.
