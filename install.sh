#!/usr/bin/env bash
set -euo pipefail

REPO="bau59/open-go-panel"
INSTALL_DIR="/usr/local/bin"
BIN_PATH="${INSTALL_DIR}/open-go-panel"
CONFIG_DIR="/etc/open-go-panel"
STATE_DIR="/var/lib/open-go-panel"
ENV_FILE="${CONFIG_DIR}/open-go-panel.env"
SERVICE_FILE="/etc/systemd/system/open-go-panel.service"
PREVIOUS_BIN="${BIN_PATH}.previous"
WEB_ASSET_DIR="${STATE_DIR}/web-assets/xterm"
RELEASE_BASE_URL="${OGP_RELEASE_BASE_URL:-https://github.com/${REPO}/releases/latest/download}"

# install.sh is intentionally non-destructive.
# It installs or updates the panel binary and service while preserving:
#   /etc/open-go-panel
#   /var/lib/open-go-panel
#   Linux users and home directories
#   MySQL/PostgreSQL data
#   Caddy state/certificates
#   CrowdSec state
#   UFW rules

if [ "${EUID}" -ne 0 ]; then
  echo "Run this installer as root."
  exit 1
fi

# Make an already-installed Go toolchain usable by managed Linux users.
# Open Go Panel itself is precompiled and does not require Go to run.
# Never overwrite administrator-managed binaries or symlinks.
ensure_managed_go_links() {
  local go_root="${1:-/usr/local/go}"
  local bin_dir="${2:-/usr/local/bin}"
  local tool source_path link_path

  for tool in go gofmt; do
    source_path="${go_root}/bin/${tool}"
    link_path="${bin_dir}/${tool}"

    # Go is optional; installing the panel must not download or replace it.
    if [ ! -x "${source_path}" ]; then
      continue
    fi
    # -L also catches dangling symlinks: never overwrite existing links.
    if [ -e "${link_path}" ] || [ -L "${link_path}" ]; then
      continue
    fi

    ln -s "${source_path}" "${link_path}"
    echo "Exposed existing Go tool: ${link_path} -> ${source_path}"
  done
}

ensure_managed_go_links /usr/local/go /usr/local/bin

if ! command -v curl >/dev/null 2>&1; then
  apt-get update
  apt-get install -y curl
fi

if ! command -v sshd >/dev/null 2>&1; then
  echo "Installing OpenSSH server..."
  apt-get update
  apt-get install -y openssh-server
fi

systemctl enable --now ssh

if ! command -v git >/dev/null 2>&1 || ! command -v ssh-keyscan >/dev/null 2>&1; then
  echo "Installing Git and SSH client tools..."
  apt-get update
  apt-get install -y git openssh-client
fi

if ! command -v caddy >/dev/null 2>&1; then
  echo "Installing Caddy..."
  apt-get update
  apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' -o /etc/apt/sources.list.d/caddy-stable.list
  chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg /etc/apt/sources.list.d/caddy-stable.list
  apt-get update
  apt-get install -y caddy
fi

systemctl enable --now caddy

if ! command -v setfacl >/dev/null 2>&1; then
  echo "Installing ACL tools for static sites..."
  apt-get update
  apt-get install -y acl
fi


ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64)
    ASSET="open-go-panel-linux-amd64"
    ;;
  aarch64|arm64)
    ASSET="open-go-panel-linux-arm64"
    ;;
  *)
    echo "Unsupported architecture: ${ARCH}"
    exit 1
    ;;
esac

DOWNLOAD_URL="${OGP_BINARY_URL:-${RELEASE_BASE_URL}/${ASSET}}"
CHECKSUMS_URL="${OGP_CHECKSUMS_URL:-${RELEASE_BASE_URL}/SHA256SUMS}"
ASSET_BASE_URL="${OGP_ASSET_BASE_URL:-${RELEASE_BASE_URL}}"
TMP_DIR="$(mktemp -d)"
TMP_BIN="${TMP_DIR}/${ASSET}"
TMP_SUMS="${TMP_DIR}/SHA256SUMS"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "Downloading Open Go Panel..."
if ! curl -fL "${DOWNLOAD_URL}" -o "${TMP_BIN}"; then
  echo "Failed to download the release binary."
  exit 1
fi
if ! curl -fL "${CHECKSUMS_URL}" -o "${TMP_SUMS}"; then
  echo "Failed to download release checksums."
  exit 1
fi

EXPECTED_SHA="$(awk -v file="${ASSET}" '$2 == file {print $1; exit}' "${TMP_SUMS}")"
ACTUAL_SHA="$(sha256sum "${TMP_BIN}" | awk '{print $1}')"
if [ -z "${EXPECTED_SHA}" ] || [ "${EXPECTED_SHA}" != "${ACTUAL_SHA}" ]; then
  echo "Binary checksum verification failed."
  exit 1
fi
chmod 0755 "${TMP_BIN}"

echo "Downloading terminal frontend assets..."
for asset in xterm.css xterm.js xterm-addon-fit.js; do
  if ! curl -fL "${ASSET_BASE_URL}/${asset}" -o "${TMP_DIR}/${asset}"; then
    echo "Failed to download ${asset}."
    exit 1
  fi
  expected="$(awk -v file="${asset}" '$2 == file {print $1; exit}' "${TMP_SUMS}")"
  actual="$(sha256sum "${TMP_DIR}/${asset}" | awk '{print $1}')"
  if [ -z "${expected}" ] || [ "${expected}" != "${actual}" ]; then
    echo "Checksum verification failed for ${asset}."
    exit 1
  fi
done

if [ -f "${BIN_PATH}" ]; then
  cp -a "${BIN_PATH}" "${PREVIOUS_BIN}.tmp"
  mv -f "${PREVIOUS_BIN}.tmp" "${PREVIOUS_BIN}"
fi
mv -f "${TMP_BIN}" "${BIN_PATH}"

mkdir -p "${CONFIG_DIR}" "${STATE_DIR}"
chmod 0700 "${CONFIG_DIR}"
chmod 0755 "${STATE_DIR}"
mkdir -p "${STATE_DIR}/runners" "${STATE_DIR}/env" "${WEB_ASSET_DIR}"
chmod 0755 "${STATE_DIR}/runners"
chmod 0700 "${STATE_DIR}/env"
chmod 0755 "${STATE_DIR}/web-assets" "${WEB_ASSET_DIR}"
install -m 0644 "${TMP_DIR}/xterm.css" "${WEB_ASSET_DIR}/xterm.css"
install -m 0644 "${TMP_DIR}/xterm.js" "${WEB_ASSET_DIR}/xterm.js"
install -m 0644 "${TMP_DIR}/xterm-addon-fit.js" "${WEB_ASSET_DIR}/xterm-addon-fit.js"

if [ ! -f "${ENV_FILE}" ]; then
  ADMIN_PASSWORD="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"

  cat > "${ENV_FILE}" <<EOF
OGP_LISTEN_ADDR=127.0.0.1:8443
OGP_ADMIN_USER=admin
OGP_ADMIN_PASSWORD=${ADMIN_PASSWORD}
EOF

  chmod 0600 "${ENV_FILE}"
else
  ADMIN_PASSWORD="$(sed -n 's/^OGP_ADMIN_PASSWORD=//p' "${ENV_FILE}" | head -n1)"
fi

cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=Open Go Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
EnvironmentFile=${ENV_FILE}
ExecStart=${BIN_PATH}
Restart=always
RestartSec=3
NoNewPrivileges=false

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable open-go-panel

rollback_binary() {
  echo "Open Go Panel failed its post-update health check. Rolling back..."
  if [ -f "${PREVIOUS_BIN}" ]; then
    cp -f "${PREVIOUS_BIN}" "${BIN_PATH}"
    chmod 0755 "${BIN_PATH}"
    systemctl restart open-go-panel.service || true
    echo "Previous binary restored."
  else
    systemctl stop open-go-panel.service 2>/dev/null || true
    rm -f "${BIN_PATH}"
    echo "No previous binary existed; the failed installation was removed."
  fi
}

if ! systemctl restart open-go-panel.service; then
  rollback_binary
  exit 1
fi

LISTEN_ADDR="$(sed -n 's/^OGP_LISTEN_ADDR=//p' "${ENV_FILE}" | head -n1)"
HEALTH_PORT="$(printf '%s' "${LISTEN_ADDR}" | sed -nE 's/.*:([0-9]+)$/\1/p')"
if [ -z "${HEALTH_PORT}" ]; then
  HEALTH_PORT="8443"
fi
HEALTH_URL="http://127.0.0.1:${HEALTH_PORT}/health"
HEALTH_OK=false
for _ in $(seq 1 20); do
  if curl -fsS --max-time 2 "${HEALTH_URL}" | grep -qx "ok"; then
    HEALTH_OK=true
    break
  fi
  sleep 0.5
done
if [ "${HEALTH_OK}" != true ]; then
  rollback_binary
  exit 1
fi

rm -rf "${TMP_DIR}"
trap - EXIT

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
if [ -z "${IP}" ]; then
  IP="<server-ip>"
fi

echo
echo "Open Go Panel installed/updated."
echo "Persistent panel data was preserved."
echo "Release checksum verified and post-update health check passed."
if [ -f "${PREVIOUS_BIN}" ]; then
  echo "Previous binary kept at: ${PREVIOUS_BIN}"
fi
if [[ "${LISTEN_ADDR}" == 127.0.0.1:* || "${LISTEN_ADDR}" == localhost:* ]]; then
  echo "Panel listens on localhost only."
  echo "SSH tunnel:"
  echo "  ssh -L 8443:127.0.0.1:8443 root@${IP}"
  echo "Then open: http://127.0.0.1:8443"
else
  echo "URL: http://${IP}:8443"
fi
echo "Username: admin"
echo "Password: ${ADMIN_PASSWORD}"
echo
echo "SSH/SFTP: enabled through OpenSSH on the server."
echo "Credentials are stored in: ${ENV_FILE}"
