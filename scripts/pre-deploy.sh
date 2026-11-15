#!/bin/bash
# Run by the zaki-selfhost deploy script from the checkout dir, after `git pull`
# and before `docker compose up`. A non-zero exit stops the deploy.
#
#   1. If the app is running, snapshot its database with its own CLI
#      (`recall backup`; the image has no shell) to
#      /data/backups/pre-deploy-<UTC timestamp>.db.gz (inside the volume), so
#      a migration in the new build can always be undone. Keeps the newest 3.
#      No running app (first deploy): nothing to back up, carry on.
#   2. If scripts/backup.sh changed, reinstall the nightly backup agent so the
#      installed copy in ~/selfhost/bin doesn't drift from the repo.
set -euo pipefail

cd "$(dirname "$0")/.."
KEEP=3
INSTALLED_BACKUP="$HOME/selfhost/bin/recall-backup.sh"
# Overridable so tests never touch the real launchd agent.
INSTALL_BACKUP="${RECALL_INSTALL_BACKUP:-./ops/install-backup.sh}"

log() { echo "[pre-deploy] $*"; }

if [ -z "$(docker compose ps --status running --quiet app 2>/dev/null)" ]; then
  log "app isn't running (first deploy?), no backup needed"
else
  dest="/data/backups/pre-deploy-$(date -u +%Y%m%dT%H%M%SZ).db.gz"
  log "backing up to $dest"
  if ! docker compose exec -T app recall backup "$dest"; then
    log "backup FAILED, not deploying"
    exit 1
  fi
  docker compose exec -T app recall prune-backups /data/backups pre-deploy- "$KEEP" ||
    log "couldn't prune old pre-deploy backups (the new one is fine)"
fi

if command -v launchctl >/dev/null && ! cmp -s scripts/backup.sh "$INSTALLED_BACKUP"; then
  log "scripts/backup.sh differs from $INSTALLED_BACKUP, reinstalling the nightly backup"
  # A launchd hiccup shouldn't block the deploy, but it must be loud.
  "$INSTALL_BACKUP" || log "WARNING: install-backup.sh failed; the nightly backup still runs the old script"
fi
