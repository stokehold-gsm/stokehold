#!/usr/bin/env bash
# Stokehold uninstaller. Removes the panel and the helper. It never touches game
# servers or their Linux users. Panel data (users, settings) is kept unless you
# pass --purge.
set -euo pipefail

PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1
[ "$(id -u)" -eq 0 ] || { echo "error: run as root" >&2; exit 1; }

systemctl disable --now stokehold.service stokehold-helper.service 2>/dev/null || true
rm -f /etc/systemd/system/stokehold.service /etc/systemd/system/stokehold-helper.service
systemctl daemon-reload
rm -f /usr/local/bin/stokehold /usr/local/bin/stokehold-helper
rm -rf /usr/local/lib/stokehold

if [ "$PURGE" -eq 1 ]; then
  rm -rf /var/lib/stokehold /etc/stokehold
  userdel stokehold 2>/dev/null || true
  echo "removed, including panel data"
else
  echo "removed. Panel data is kept in /var/lib/stokehold and /etc/stokehold (use --purge to delete it)."
fi
echo "Game servers and their gs_* users were left as they are."
