#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
node --check internal/server/assets/panel.js
browser="$(command -v google-chrome || command -v chromium || command -v chromium-browser)"
python3 -m http.server 18888 --bind 127.0.0.1 >/tmp/ogp-tabs-http.log 2>&1 &
server=$!
work="$(mktemp -d)"
trap 'kill "$server" 2>/dev/null || true; rm -rf "$work"' EXIT
for attempt in $(seq 1 20); do
  if curl -fsS http://127.0.0.1:18888/tests/panel-tabs.html >/dev/null; then break; fi
  sleep 1
done
for viewport in 1280,800 390,844; do
  "$browser" --headless --no-sandbox --disable-gpu --disable-dev-shm-usage --no-first-run \
    --user-data-dir="$work/profile-$viewport" --window-size="$viewport" --virtual-time-budget=4000 \
    --dump-dom http://127.0.0.1:18888/tests/panel-tabs.html >"$work/result" 2>"$work/browser.log"
  if ! grep -q 'data-test-result="pass"' "$work/result"; then
    cat "$work/result" >&2
    cat "$work/browser.log" >&2
    exit 1
  fi
done
echo "Panel tabs and checkbox smoke passed"
