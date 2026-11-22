#!/bin/bash
# Install (or reinstall) the nightly recall backup on a macOS host (launchd).
#   - copies scripts/backup.sh to ~/selfhost/bin/recall-backup.sh, so the job
#     doesn't depend on any checkout staying in place
#   - renders ops/com.zakiego.recall-backup.plist into ~/Library/LaunchAgents
#   - (re)loads it with launchctl
# Rerun after changing either file. Run once now with:
#   launchctl kickstart gui/$(id -u)/com.zakiego.recall-backup
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
label=com.zakiego.recall-backup
plist="$HOME/Library/LaunchAgents/$label.plist"
domain="gui/$(id -u)"

mkdir -p "$HOME/selfhost/bin" "$HOME/Library/LaunchAgents"
install -m 755 "$repo/scripts/backup.sh" "$HOME/selfhost/bin/recall-backup.sh"
sed "s|__HOME__|$HOME|g" "$repo/ops/$label.plist" > "$plist"
plutil -lint "$plist" >/dev/null

launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
echo "installed $label: daily at 03:15, log ~/selfhost/recall-backup.log"
