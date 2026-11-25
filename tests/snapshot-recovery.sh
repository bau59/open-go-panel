#!/usr/bin/env bash
set -euo pipefail
# This test destroys the disposable runner's panel state. Never run on a VPS.
test "${GITHUB_ACTIONS:-}" = true
test "${RUNNER_ENVIRONMENT:-}" = github-hosted
BASE=http://127.0.0.1:8443
sudo systemctl enable --now mysql.service
sudo mkdir -p /etc/systemd/system/open-go-panel.service.d
printf '[Service]\nEnvironment=MYSQL_PWD=root\n' | sudo tee /etc/systemd/system/open-go-panel.service.d/smoke-mysql.conf >/dev/null
sudo systemctl daemon-reload
sudo systemctl restart open-go-panel.service
for i in $(seq 1 30); do curl -fsS "$BASE/health" && break; sleep 1; done
PASS="$(sudo sed -n 's/^OGP_ADMIN_PASSWORD=//p' /etc/open-go-panel/open-go-panel.env | head -n1)"
login() {
  test "$(curl -sS -o /dev/null -w '%{http_code}' -c /tmp/recovery-cookies -d "username=admin&password=$PASS" "$BASE/login")" = 303
  CSRF="$(curl -fsS -b /tmp/recovery-cookies "$BASE/" | grep -o 'name="ogp_csrf" value="[^"]*"' | head -n1 | cut -d '"' -f4)"
  test -n "$CSRF"
}
post() {
  local path="$1"; shift
  local code
  code="$(curl -sS -o /dev/null -w '%{http_code}' -b /tmp/recovery-cookies --data-urlencode "ogp_csrf=$CSRF" "$@" "$BASE$path")"
  test "$code" = 303
}
login
post /users -d 'username=ogprecovery&password=SmokePass123!'
post /apps -d 'user=ogprecovery&name=recovery&type=static'
APP_ID="$(curl -fsS -b /tmp/recovery-cookies "$BASE/apps" | grep -oE 'href="/apps/[0-9]+"' | sed -n '1s/[^0-9]//gp')"
test -n "$APP_ID"
sudo -u ogprecovery sh -c 'printf "recovery-ok\n" > /home/ogprecovery/apps/recovery/proof.txt'
OLD_UID="$(id -u ogprecovery)"
post "/apps/$APP_ID/cron" --data-urlencode 'name=Recovery timer' --data-urlencode 'schedule=0 3 * * *' --data-urlencode 'command=/usr/bin/touch recovery-cron' -d 'enabled=1'
TASK_ID="$(curl -fsS -b /tmp/recovery-cookies "$BASE/apps/$APP_ID" | grep -oE "/apps/$APP_ID/cron/[0-9]+/run" | sed -n '1s#^.*/cron/\([0-9]\+\)/run#\1#p')"
test -n "$TASK_ID"
post /databases -d 'engine=mysql&name=ogprecoverydb&user=ogprecoverydbuser'
sudo env MYSQL_PWD=root mysql -uroot ogprecoverydb -e "CREATE TABLE proof (id INT); INSERT INTO proof VALUES (42);"
post /terminal/background/start -d 'user=ogprecovery' -d "app=$APP_ID" --data-urlencode 'command=sleep 600'
BG_ID="$(curl -fsS -b /tmp/recovery-cookies "$BASE/terminal/background" | grep -oE '/terminal/background/[0-9a-f]{24}/stop' | sed -n '1s#^.*/\([0-9a-f]\{24\}\)/stop#\1#p')"
test -n "$BG_ID"
sudo systemctl is-active --quiet "ogp-bg-$BG_ID.service"
post /backup/snapshots/create
ROOT=/var/lib/open-go-panel/backups/server
for i in $(seq 1 90); do
  SNAP="$(sudo find "$ROOT" -mindepth 2 -maxdepth 2 -name manifest.json -print 2>/dev/null | sed -n '1p' || true)"
  test -n "$SNAP" && break
  sleep 1
done
test -n "$SNAP"
ID="$(basename "$(dirname "$SNAP")")"
sudo test -s "$ROOT/$ID/files/ogprecovery.tar.gz"
sudo test -s "$ROOT/$ID/databases/mysql/ogprecoverydb.sql.gz"
# The original server is populated, so the recovery helper must refuse it.
if sudo env MYSQL_PWD=root /usr/local/bin/open-go-panel --restore-server-snapshot "$ID"; then
  echo 'Recovery unexpectedly accepted populated server' >&2
  exit 1
fi
# Simulate a clean panel on this disposable GitHub-hosted runner only.
sudo systemctl stop open-go-panel.service "ogp-bg-$BG_ID.service" "open-go-panel-cron-$TASK_ID.timer"
sudo systemctl disable "open-go-panel-cron-$TASK_ID.timer"
sudo rm -f "/etc/systemd/system/open-go-panel-cron-$TASK_ID.timer" "/etc/systemd/system/open-go-panel-cron-$TASK_ID.service"
sudo systemctl daemon-reload
sudo userdel -r ogprecovery
sudo env MYSQL_PWD=root mysql -uroot -e "DROP DATABASE ogprecoverydb; DROP USER 'ogprecoverydbuser'@'localhost';"
sudo mv /var/lib/open-go-panel/panel.db /tmp/ogp-recovery-original.db
sudo rm -f /var/lib/open-go-panel/panel.db-wal /var/lib/open-go-panel/panel.db-shm
sudo systemctl start open-go-panel.service
for i in $(seq 1 30); do curl -fsS "$BASE/health" && break; sleep 1; done
sudo env MYSQL_PWD=root /usr/local/bin/open-go-panel --restore-server-snapshot "$ID"
test "$(id -u ogprecovery)" = "$OLD_UID"
sudo -u ogprecovery cat /home/ogprecovery/apps/recovery/proof.txt | grep -qx recovery-ok
test "$(sudo env MYSQL_PWD=root mysql -uroot -N -e 'SELECT id FROM ogprecoverydb.proof')" = 42
sudo systemctl is-enabled --quiet "open-go-panel-cron-$TASK_ID.timer"
sudo systemctl is-active --quiet "ogp-bg-$BG_ID.service"
curl -fsS "$BASE/health" | grep -qx ok
login
curl -fsS -b /tmp/recovery-cookies "$BASE/apps/$APP_ID" | grep -q 'Recovery timer'
echo 'Managed-server snapshot recovery passed'
