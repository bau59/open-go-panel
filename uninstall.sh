#!/usr/bin/env bash
set -euo pipefail

BIN_PATH="/usr/local/bin/open-go-panel"
CONFIG_DIR="/etc/open-go-panel"
STATE_DIR="/var/lib/open-go-panel"
SERVICE_FILE="/etc/systemd/system/open-go-panel.service"

PURGE=false

usage() {
  cat <<'EOF'
Usage:
  uninstall.sh
  uninstall.sh --purge

Default uninstall:
  - stops and disables Open Go Panel
  - removes the panel binary and its systemd unit
  - preserves /etc/open-go-panel
  - preserves /var/lib/open-go-panel
  - preserves Linux users and their home directories
  - preserves MySQL/PostgreSQL data and packages
  - preserves Caddy, certificates and configuration
  - preserves CrowdSec and UFW state

--purge additionally removes:
  - /etc/open-go-panel
  - /var/lib/open-go-panel

Even --purge does NOT delete Linux users, app directories, databases,
Caddy packages/certificates, CrowdSec, UFW, MySQL or PostgreSQL.
EOF
}

case "${1:-}" in
  "")
    ;;
  --purge)
    PURGE=true
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage
    exit 2
    ;;
esac

if [ "${EUID}" -ne 0 ]; then
  echo "Run this uninstaller as root."
  exit 1
fi

if systemctl list-unit-files open-go-panel.service >/dev/null 2>&1; then
  systemctl disable --now open-go-panel.service 2>/dev/null || true
fi

rm -f "${SERVICE_FILE}"
rm -f "${BIN_PATH}"
systemctl daemon-reload

if [ "${PURGE}" = true ]; then
  echo
  echo "PURGE MODE"
  echo "This removes only Open Go Panel configuration/state."
  echo "Linux users, app files, databases and infrastructure packages are preserved."
  echo

  rm -rf "${CONFIG_DIR}"
  rm -rf "${STATE_DIR}"

  echo "Open Go Panel removed with panel state/config purged."
else
  echo "Open Go Panel removed."
  echo "Persistent data preserved:"
  echo "  ${CONFIG_DIR}"
  echo "  ${STATE_DIR}"
  echo
  echo "Reinstall later with:"
  echo "  curl -fsSL https://raw.githubusercontent.com/bau59/open-go-panel/main/install.sh | bash"
fi
