#!/bin/bash
# Nightly backup of recall's SQLite database to Cloudflare R2 (Zakiego Lab).
#
#   1. snapshot /data/recall.db inside the running container (online backup API, gzipped)
#   2. docker compose cp it out to a temp dir
#   3. upload to r2://recall-backups/recall/YYYY-MM-DD/recall-<timestamp>.db.gz
#   4. delete the copy in the container and the local copy
#
# Credentials come from the Keychain through the cf-zakiego-lab wrapper. Nothing
# is written to disk except the snapshot itself (deleted on exit) and the log.
# Run by launchd (ops/com.zakiego.recall-backup.plist), or by hand.
set -euo pipefail

RECALL_DIR="${RECALL_DIR:-$HOME/selfhost/recall}"
LOG="${BACKUP_LOG:-$HOME/selfhost/recall-backup.log}"
BUCKET="${BACKUP_BUCKET:-recall-backups}"
CF="${CF_WRAPPER:-$HOME/.agents/skills/cf-zakiego-lab/scripts/cf-zakiego-lab}"
MIN_FREE_MB=500
SNAPSHOT_ATTEMPTS=3
SNAPSHOT_RETRY_DELAY="${SNAPSHOT_RETRY_DELAY:-60}"

# launchd starts with a bare PATH. docker lives in /usr/local/bin (OrbStack),
# cf and its node in an nvm version dir.
export PATH="/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
for d in "$HOME"/.nvm/versions/node/*/bin; do
  [ -x "$d/cf" ] && CF_NODE_BIN="$d"
done
[ -n "${CF_NODE_BIN:-}" ] && export PATH="$CF_NODE_BIN:$PATH"

# Keep the log small: trim to the last 2000 lines once it passes 1 MB.
mkdir -p "$(dirname "$LOG")"
if [ -f "$LOG" ] && [ "$(stat -f %z "$LOG")" -gt 1048576 ]; then
  tail -n 2000 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
fi
if [ -t 1 ]; then
  exec > >(tee -a "$LOG") 2>&1
else
  exec >>"$LOG" 2>&1
fi

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*"; }

day=$(date +%Y-%m-%d)
ts=$(date +%Y%m%d-%H%M%S)
name="recall-$ts.db.gz"
key="recall/$day/$name"
remote="/data/backups/$name"
tmpdir=""

cleanup() {
  status=$?
  [ -n "$tmpdir" ] && rm -rf "$tmpdir"
  (cd "$RECALL_DIR" && docker compose exec -T app sh -c 'rm -f "$1" "$1".tmp-*' _ "$remote") >/dev/null 2>&1 || true
  if [ "$status" -eq 0 ]; then log "done"; else log "FAILED (exit $status)"; fi
}
trap cleanup EXIT

log "start $key"
command -v docker >/dev/null || { log "docker not found"; exit 1; }
[ -x "$CF" ] || { log "cf wrapper not found at $CF"; exit 1; }
command -v cf >/dev/null || { log "cf CLI not found (looked in ~/.nvm/versions/node/*/bin)"; exit 1; }
free_mb=$(df -m / | awk 'NR==2 {print $4}')
[ "$free_mb" -ge "$MIN_FREE_MB" ] || { log "only ${free_mb} MB free, need ${MIN_FREE_MB}"; exit 1; }

cd "$RECALL_DIR"

# The same steps as src/server/backup.ts, for images built before the admin CLI
# was bundled into .output/server/cli.mjs.
INLINE_BACKUP='
const { createRequire } = require("node:module")
const fs = require("node:fs"), path = require("node:path"), zlib = require("node:zlib")
const { pipeline } = require("node:stream/promises")
const Database = createRequire("/app/.output/server/index.mjs")("better-sqlite3")
const dest = process.argv[1], tmp = dest + ".tmp-" + process.pid
;(async () => {
  fs.mkdirSync(path.dirname(dest), { recursive: true })
  const live = new Database(path.join(process.env.DATA_DIR || "/data", "recall.db"), { readonly: true, fileMustExist: true })
  try { live.pragma("busy_timeout = 5000"); await live.backup(tmp) } finally { live.close() }
  try {
    const snap = new Database(tmp)
    try {
      snap.pragma("journal_mode = DELETE")
      const check = snap.pragma("quick_check", { simple: true })
      if (check !== "ok") throw new Error("snapshot failed quick_check: " + check)
    } finally { snap.close() }
    const dbBytes = fs.statSync(tmp).size
    await pipeline(fs.createReadStream(tmp), zlib.createGzip({ level: 9 }), fs.createWriteStream(dest))
    console.log("backup " + dest + " " + fs.statSync(dest).size + " bytes (db " + dbBytes + " bytes)")
  } finally {
    for (const f of [tmp, tmp + "-wal", tmp + "-shm"]) fs.rmSync(f, { force: true })
  }
})().catch((e) => { console.error(e); process.exit(1) })
'

snapshot() {
  if docker compose exec -T app test -f .output/server/cli.mjs; then
    if docker compose exec -T app node .output/server/cli.mjs backup "$remote"; then
      return 0
    fi
    log "cli.mjs backup failed, falling back to the inline snapshot"
  fi
  docker compose exec -T app node -e "$INLINE_BACKUP" "$remote"
}

# The container can be mid-restart (a deploy, an OrbStack update) at 03:15.
# Give it a few minutes before calling the night a failure.
attempt=1
until snapshot; do
  if [ "$attempt" -ge "$SNAPSHOT_ATTEMPTS" ]; then
    log "snapshot failed $attempt times, giving up"
    exit 1
  fi
  log "snapshot attempt $attempt failed, retrying in ${SNAPSHOT_RETRY_DELAY}s"
  sleep "$SNAPSHOT_RETRY_DELAY"
  attempt=$((attempt + 1))
done

tmpdir=$(mktemp -d /tmp/recall-backup.XXXXXX)
local_file="$tmpdir/$name"
docker compose cp "app:$remote" "$local_file" >/dev/null
gzip -t "$local_file"
bytes=$(stat -f %z "$local_file")
log "copied out $bytes bytes, sha256 $(shasum -a 256 "$local_file" | cut -d' ' -f1)"

"$CF" r2 objects put "$key" --bucket-name "$BUCKET" --file "$local_file" \
  --content-type application/gzip >/dev/null

# Confirm the object landed with the right size.
remote_size=$("$CF" r2 objects list --bucket-name "$BUCKET" --prefix "$key" \
  | python3 -c 'import json,sys
d = json.load(sys.stdin)
items = d if isinstance(d, list) else d.get("objects", d.get("result", []))
print(next((o.get("size") for o in items if o.get("key") == sys.argv[1]), ""))' "$key")
if [ "$remote_size" != "$bytes" ]; then
  log "uploaded size mismatch: local $bytes, r2 '${remote_size}'"
  exit 1
fi
log "uploaded r2://$BUCKET/$key ($bytes bytes)"
