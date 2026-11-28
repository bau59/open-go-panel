# Open Go Panel

**Self-hosted Linux server management and explicitly authorized AI terminals.**

Open Go Panel is a lightweight control panel written in Go for developers who manage their own Ubuntu VPS. It manages native Linux services, application deployments, databases, Docker workloads and disaster recovery from one interface. An independent Model Context Protocol (MCP) service lets ChatGPT work directly with an explicitly authorized application on the server.

The administrative panel is designed for a single administrator and normally stays behind an SSH tunnel. You can close the panel without stopping websites or the separate MCP service.

[Download releases](https://github.com/bau59/open-go-panel/releases) · [Build and test status](https://github.com/bau59/open-go-panel/actions) · [Report an issue](https://github.com/bau59/open-go-panel/issues)

> This is a privileged server-management tool for trusted administrators, not a public multi-tenant hosting platform. MCP file helpers isolate direct project operations. Terminal access uses normal host permissions, not a project sandbox; an authorized root terminal can administer the entire server.

## Features at a glance

| Area | Capabilities |
| --- | --- |
| Applications | Go, Node.js, PHP and static sites; Linux accounts, SSH keys, systemd lifecycle, environment, resource limits, health checks, journal logs |
| Deployment | Git deploy, configurable auto-deploy intervals, staged atomic directory exchange, rollback, Go Air development → prebuilt production binary |
| Files | Project-scoped editor, uploads/downloads, bulk move/delete, archive extraction, collision checks |
| AI / MCP | Separate OAuth connection per app, nine scoped file tools, host command execution, persistent PTY terminals, user/system-sudo/root access levels, token revocation |
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

Open Go Panel exposes a remote MCP service with independent OAuth grants per application. The nine direct file tools remain project-scoped. The terminal tools deliberately use normal host filesystem and network permissions, as if the selected Linux account were working on the server.

**Upgrade warning:** existing MCP grants now receive host terminal access as the ordinary project owner by default. Previous isolated command execution and per-grant MySQL/Redis relay settings are no longer used for terminal commands. Revoke existing grants before upgrading if you do not want that expanded access. Root is never selected automatically.

### Connect an application

1. Open **Infrastructure → ChatGPT access** in the private panel and configure a dedicated HTTPS domain with DNS pointing to the VPS.
2. Open **Applications → ChatGPT access → Enable connection**. Select the intended terminal access level before approving OAuth. Copy the plugin name and MCP URL.
3. In ChatGPT on the web, add a custom MCP server using the displayed URL and OAuth with dynamic registration (availability and UI labels depend on the account).
4. Match the OAuth request ID with the private panel request and approve only the intended connection. Check its displayed terminal level. Continue the authorization flow.
5. Select the installed plugin with `@` in a chat.

Each application has its own connection. Change the terminal level only in the private panel: this atomically invalidates all access tokens, refresh tokens, pending requests and registered clients for that grant. The URL stays the same, but the AI client must register and authorize again. Existing sessions are stopped. **Revoke access** invalidates the grant and stops associated managed sessions.

### Terminal access levels

| Level | Linux identity and permissions |
| --- | --- |
| Project user (default) | The application owner, with all permissions already available to that user |
| Project user with system sudo | The same application owner; sudo is controlled by the server's existing sudoers configuration and can prompt for authentication |
| Server administrator (root) | UID 0; unrestricted server administration |

Selecting sudo does not add the account to a sudo group, edit sudoers, bypass authentication, or elevate the file-helper identity. If the project account already has sudo privileges, those are also available in the ordinary user mode because the terminal does not impose an additional security boundary. Do not select either user mode for an account whose existing Linux permissions are inappropriate for the AI client.

All levels use the host's network, installed utilities, home directory and normal filesystem permissions. No MCP command allowlist, database-only relay or execution time limit is applied. Installations, downloads, Git operations and database access depend on ordinary OS rights, credentials and network configuration. A root or sudo-capable AI connection can make production-impacting changes.

### File tools

File tool paths are relative to the application root mounted at `/workspace` inside the existing file sandbox. Use `.` for the root. The file helper still runs as the project owner even when the terminal is configured as root.

| Tool | Arguments | Behavior |
| --- | --- | --- |
| `list_directory` | `path?` | List directory entries; reports truncation |
| `search_files` | `query`, `path?`, `context_lines?` | Literal text search with line numbers |
| `read_file` | `path`, `start_line?`, `end_line?` | UTF-8 text or 1-based inclusive lines; returns a version |
| `view_image` | `path`, `max_width?`, `max_height?`, `background?` | Native MCP `ImageContent` for PNG, JPEG, GIF or WebP; WebP and transformed images return PNG |
| `view_images` | `paths`, `max_width?`, `max_height?`, `background?`, `columns?` | Ordered contact sheet of 1–12 images with filenames and per-cell errors |
| `create_file` | `path`, `content` | Create text file without overwriting |
| `edit_file` | `path`, `content`, `version` | Replace file contents only if the version matches |
| `create_directory` | `path` | Create one directory |
| `delete_path` | `path`, `recursive?` | Delete file or empty directory; recursion requires explicit opt-in |

For design review, generate a screenshot with a terminal tool into the project directory (for example `screenshots/page.png`), then call `view_image` with that relative path. The result is a native MCP image content block with `type: image`, `mimeType` and base64 `data`, not text containing base64. Image contents are not executed. Client support for native MCP image rendering is required. WebP is decoded in Go and returned as PNG for compatibility; transparency is preserved unless an opaque `#RRGGBB` background is requested. Use `max_width` and/or `max_height` to reduce dimensions proportionally without upscaling. Original PNG/JPEG/GIF bytes are preserved when no transformation is needed; transformed GIFs use the first frame. Source files are limited to 2 MiB / 16 megapixels; converted PNGs are also bounded to 2 MiB and proportionally reduced if needed. No temporary files or OS image packages are used.

Example: `view_image` with `{"path":"public/icon.webp","max_width":300,"background":"#ffffff"}`. For multiple icons use `view_images` with `{"paths":["public/a.webp","public/b.webp"],"max_width":300,"max_height":300,"background":"#ffffff","columns":2}`. Contact sheets default to 300×300 image bounds, three columns, and white backgrounds. Equal cells are automatically reduced to fit the 1.5-megapixel sheet and 2 MiB PNG budget. Long labels may be abbreviated; unavailable/invalid files get error cells without exposing host paths.

File tools still reject paths outside the project, individual hardlinked files, and invalid input. Text editing is limited to 2 MiB. Concurrent file operations remain bounded. Those restrictions do not apply to the separately authorized host terminal.

### Terminal tools

MCP advertises 17 tools: the nine file tools above, `run_command` and seven terminal tools. Tool arguments are JSON objects; `?` denotes an optional argument.

| Tool | Arguments | Behavior |
| --- | --- | --- |
| `run_command` | `command`, `workdir?`, `timeout_seconds?`, `max_output_bytes?`, `output_encoding?`, `response_format?` | Start a host command; return initial output and session ID. The wait argument is now an initial read window (0–10 seconds), not a process deadline |
| `terminal_start` | `command?`, `workdir?` | Start a PTY shell or a single shell command |
| `terminal_write` | `session_id`, `input`, `response_format?` | Queue input, including a newline or terminal control characters |
| `terminal_read` | `session_id`, `offset?`, `max_output_bytes?`, `output_encoding?`, `response_format?` | Read the next output fragment by byte offset |
| `terminal_list` | none | List sessions for the authenticated OAuth client and current permission generation |
| `terminal_status` | `session_id`, `response_format?` | Status, start/end timestamps and exit code when available |
| `terminal_resize` | `session_id`, `rows`, `cols` | Change PTY size |
| `terminal_stop` | `session_id` | Stop the managed unit and its processes |

For terminals, the default working directory is the actual host project path (e.g. `/home/user/apps/example`), not `/workspace`. Absolute or project-relative host paths may be used. The existing `run_command` name remains available, but its sandbox/timeout behavior has changed. Refresh the MCP tool definitions after updating.

Example workflow:

1. Call `terminal_start` with `{}` and retain the returned `session_id`.
2. Call `terminal_write` with that ID and `{"input":"pwd\\n"}` (send an actual newline in the JSON string).
3. Call `terminal_read` with `offset: 0`; use the returned `next_offset` in the following read.
4. Send more shell commands, install a user-local dependency or launch a long-running process. Reconnect in a subsequent MCP request using the same session ID. OAuth refresh rotation does not change session ownership.
5. Use `terminal_stop` when done.

PTY output combines stdout and stderr. By default `run_command`, `terminal_read`, `terminal_status` and `terminal_write` return one compact JSON object in `structuredContent` with an empty `content` array. Read results include `session_id`, `status`, `exit_code`, `offset`, `next_offset`, `oldest_offset`, `end_offset`, `has_more`, `truncated`, `merged_streams` and either `output` (UTF-8 text) or `output_base64` (binary, split UTF-8, or explicitly requested `output_encoding: "base64"`). Set `max_output_bytes` to 1–65536 bytes (default 16384). Continue reading using `next_offset` until the session is exited and `has_more` is false. Nonzero shell exit codes are ordinary results with `isError: false`; actual tool failures remain errors.

Existing OAuth connections remain valid. Clients which only consume text can set `response_format: "text"` (single JSON text block, no structured duplicate). Integrations that depend on the old v0.1.58 fields or duplicated text/structured output can explicitly request `response_format: "legacy"` (old 64 KiB default and aliases); this intentionally uses more context. Refresh tool definitions to discover the new optional arguments and image tool.

The last 2 MiB of output is kept in memory per session. `truncated` indicates that unread bytes were evicted; no guarantee is made for output exceeding the retained history if the reader falls behind. Up to 128 retained/active sessions are managed; completed records are evicted as needed and after one hour on subsequent starts. Input fragments can be split into 64 KiB chunks. These limits bound transport and memory usage, not shell execution time.

### Service lifecycle and control

The installer runs `open-go-panel-mcp.service` independently on `127.0.0.1:18443`. Caddy exposes only the intended public MCP and OAuth routes. OAuth uses authorization code + S256 PKCE, dynamic registration, short-lived access tokens and rotating refresh tokens. The administrative control socket is root-only and is never exposed by Caddy.

Each terminal runs in a transient systemd service with its own PTY and managed cgroup. Disconnecting from an MCP request or closing the admin panel does not terminate the session. Restarting/stopping the MCP service, rebooting, revoking the grant, changing its mode, deleting the app, or expiry of the registered client's refresh authorization stops managed sessions. Output and session IDs are not persisted across MCP service restarts. The panel lists sessions and can stop them independently of the AI client.

A root-capable shell can deliberately create processes or services outside its managed cgroup or change the panel itself. Revocation cannot undo earlier system changes or guarantee control of processes intentionally moved outside the managed unit. Treat root access as equivalent to handing over a root SSH session. Use the lowest suitable level and revoke access when finished.

When panel audit logging is enabled, audit records include the application, OAuth grant/client identifier, session, Linux user, access level, action, input byte count and exit status; raw command/input contents and output are not stored in the panel audit log. Ordinary OS logs, shell history and process metadata may still contain command text. Never send secrets in command arguments unless required and authorized. OAuth grants are excluded from server snapshots; reauthorize on a recovered host.

```bash
systemctl status open-go-panel-mcp.service
journalctl -u open-go-panel-mcp.service -n 100 --no-pager
```

### Suggested AI project instructions

> Inspect the current project before changing anything. Use the version-checked file tools for edits; keep unrelated files unchanged. Terminal access uses the configured Linux identity. Do not expose credentials or .env values. Start database diagnostics with read-only queries. Ask before destructive operations, privilege changes, system package installations and production-impacting commands. Retain the terminal session ID and output offset for long-running work. Report actual checks and failures.

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

- browsing directories and editing UTF-8 text files (up to 2 MiB) with the embedded CodeMirror editor: automatic syntax mode, line numbers, search/replace, undo/redo, line wrapping, JSON formatting and source indentation;
- uploading a file (up to 1 GiB), downloading files, creating files or folders;
- moving/renaming files and folders, copying individual files, deleting files or directories;
- selecting multiple files/folders for a bulk move or delete (up to 400 visible entries at once), with no overwrite on name collisions;
- moving **all contents of a folder one level up** in a single action, including entries not visible on the page (up to 100,000 direct children); subfolders move as trees, and the source folder remains empty for optional deletion;
- extracting ZIP, TAR.GZ and TGZ archives without overwriting existing files.

The code editor is loaded only on file edit/create pages and needs no CDN or runtime npm. JSON formatting validates syntax and changes whitespace only, preserving numeric precision, duplicate keys and string escapes. Other modes reindent existing source lines on demand; HTML/SVG are never rendered or executed by the editor. Use Ctrl/Cmd+S to save. The native textarea fallback, original revision digest and application file permissions remain in place. LF and CRLF are preserved by enhanced saves; mixed line endings are normalized to LF.

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

Release builds are gated by Go tests/vet, browser regression checks for section tabs and checkbox geometry, Ubuntu 24.04 production smoke tests, managed-server snapshot recovery tests and MCP integration tests. The production smoke exercises installation, authentication, managed users/apps, terminal assets, background commands, PHP extension checks, SQL imports and automatic binary rollback. MCP tests cover OAuth, file-helper isolation, large projects, host terminals, downloads, privilege levels and session revocation. These checks do not replace a restore drill or testing your own application.

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


## Application dependencies and readiness

Open **Applications → Settings → System service dependencies** after configuring a managed Go, Node.js or worker service. Search installed systemd services by unit name, select one or more services and choose **Required** (default: `Requires` + `After`) or **Optional** (`Wants` + `After`).

The list comes from systemd, not a fixed allowlist. Settings are stored in the application's existing service JSON. The panel validates existing units, resolves aliases, rejects self-dependencies and dependency/ordering cycles, and writes only `/etc/systemd/system/open-go-panel-app-ID.service.d/50-ogp-dependencies.conf`. Saving verifies the unit, reloads systemd and rolls back on error; it does not restart the application. Removing all dependencies removes only this file. Server snapshots include the managed drop-in; other drop-ins are not modified.

The application runtime card separates the systemd process state from local TCP port availability. Active services with configured ports are **Starting** during a 90-second grace period if the port has not opened, then **Unavailable** while the port remains closed. A listening port is **Running**. Inactive and failed services retain their normal status. This works independently of Air or a compiled binary; the panel does not automatically restart an unavailable application. Public websites are not probed for readiness. Services without dedicated app units (static/PHP) are unchanged.

`After` guarantees ordering, not universal service readiness. A local dependency that signals readiness via systemd (such as a correctly configured `Type=notify` MySQL/Redis service) can be awaited; an arbitrary `Type=simple` process may still need its own readiness contract. No sleeps are inserted into app startup.

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
