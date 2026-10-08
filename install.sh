#!/usr/bin/env bash
set -euo pipefail

REPO="bau59/open-go-panel"
INSTALL_DIR="/usr/local/bin"
BIN_PATH="${INSTALL_DIR}/open-go-panel"
CONFIG_DIR="/etc/open-go-panel"
STATE_DIR="/var/lib/open-go-panel"
ENV_FILE="${CONFIG_DIR}/open-go-panel.env"
SERVICE_FILE="/etc/systemd/system/open-go-panel.service"

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

DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
TMP_BIN="$(mktemp "${INSTALL_DIR}/.open-go-panel.XXXXXX")"
trap 'rm -f "${TMP_BIN}"' EXIT

echo "Downloading Open Go Panel..."
if ! curl -fL "${DOWNLOAD_URL}" -o "${TMP_BIN}"; then
  echo
  echo "Failed to download the latest release binary."
  echo "The release may still be building. Try again in a minute."
  exit 1
fi

chmod 0755 "${TMP_BIN}"
mv -f "${TMP_BIN}" "${BIN_PATH}"
trap - EXIT

mkdir -p "${CONFIG_DIR}" "${STATE_DIR}"
chmod 0700 "${CONFIG_DIR}"
chmod 0755 "${STATE_DIR}"
mkdir -p "${STATE_DIR}/runners" "${STATE_DIR}/env"
chmod 0755 "${STATE_DIR}/runners"
chmod 0700 "${STATE_DIR}/env"

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
systemctl restart open-go-panel

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
if [ -z "${IP}" ]; then
  IP="<server-ip>"
fi

echo
echo "Open Go Panel installed/updated."
echo "Persistent panel data was preserved."
LISTEN_ADDR="$(sed -n 's/^OGP_LISTEN_ADDR=//p' "${ENV_FILE}" | head -n1)"
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
