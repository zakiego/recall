# Operations

Running Recall as one container behind a Cloudflare tunnel, with snapshots before each deploy and a nightly backup to Cloudflare R2. The admin CLI and restore steps apply to any Docker install. The backup agent is an example for a macOS host with launchd; its paths are set at the top of `scripts/backup.sh` and in the plist.

## Deploy

Pull the new commit in the compose project, run `scripts/pre-deploy.sh`, then
`docker compose up -d --build`. Port 4800, volume `recall_data`.

`scripts/pre-deploy.sh` runs from the checkout before the build. With the app
running, it snapshots the database with the admin CLI (`recall backup`) to
`/data/backups/pre-deploy-<UTC timestamp>.db.gz` inside the volume and keeps the newest 3. A failed
backup stops the deploy; on a first deploy (no running app) there's nothing to back up. It also
reruns `ops/install-backup.sh` when `scripts/backup.sh` differs from the installed copy.

`/health` returns 503 with `"error": "db_newer_than_app"` when the database has migrations the
running build doesn't ship. That happens when a new image migrated the database and the deploy
script then rolled back to `recall:prev`. Restore the newest pre-deploy backup (see Restore) or
deploy the newer build again.

The image's entrypoint is the admin CLI, `recall` (the server is `recall serve`). It opens
`$DATA_DIR/recall.db` (`/data` in the container) and runs migrations like the app does. The image
has no shell, so run each command on its own from the compose project directory:

```bash
docker compose exec app recall create-user eva --workspace home --role member   # prompts for the password
docker compose exec app recall create-user rina --workspace rina --new-workspace   # new workspace, rina owns it
docker compose exec app recall list-users                  # every user and their workspaces
docker compose exec app recall create-key <name> --workspace home   # id, name and key
docker compose exec -T app recall create-key <name> --workspace home --quiet > key.txt   # key only
docker compose exec app recall create-key <name> --workspace home --json   # {"id","name","prefix","workspace","key"}
docker compose exec app recall list-keys --workspace home  # add --json for JSON
docker compose exec app recall revoke-key <id>
docker compose exec app recall hash-password             # prompts for the password
docker compose exec -T app recall backup /data/backups/x.db.gz   # gzipped online snapshot
docker compose exec -T app recall backup - > x.db.gz     # the same, streamed to the host
docker compose exec -T app recall prune-backups /data/backups pre-deploy- 3   # keep the newest 3
```

`create-user --workspace <slug>` fails when the slug doesn't exist, unless `--new-workspace` is
passed; a new workspace needs `--role owner` (the default).
`--workspace` can be left out of `create-key` and `list-keys` while there is only one workspace.
`-T` turns off the TTY so the redirected output holds only the key. Outside Docker, after the
build in Develop: `cd apps/server && DATA_DIR=../app/data bin/recall list-keys`.

## Backups

Every night at 03:15 a launchd agent (`com.zakiego.recall-backup`) runs
`scripts/backup.sh`. It takes a consistent snapshot of `/data/recall.db` inside the running
container with SQLite's online backup API, gzips it, streams it out (`recall backup -`),
uploads it to the private R2 bucket `recall-backups` (`BACKUP_BUCKET`) as
`recall/YYYY-MM-DD/recall-<timestamp>.db.gz`, and deletes both local copies. A lifecycle rule
deletes objects after 30 days. `/data/models` isn't backed up, since the app downloads it again. If the
container is restarting at 03:15, the snapshot gets 3 tries, 60 s apart, before the run fails.

- Log: `~/selfhost/recall-backup.log` (each run ends with `done` or `FAILED`)
- Credentials: a Cloudflare CLI wrapper (`CF_WRAPPER`) that reads them from the Keychain.
  Nothing is written to disk.
- Snapshot command: `recall backup -` in the container, streamed straight to the host (the
  image has no shell). A failed try (the container mid-restart) is retried twice, a minute apart.

```bash
./ops/install-backup.sh                                          # install or update the agent
launchctl kickstart gui/$(id -u)/com.zakiego.recall-backup       # run a backup now
launchctl print gui/$(id -u)/com.zakiego.recall-backup | grep -E 'runs|last exit'
```

`install-backup.sh` copies the script to `~/selfhost/bin/recall-backup.sh`, so rerun it after
changing `scripts/backup.sh` or the plist.

### Restore

On the host. Pick an object, download it, and check it before touching the live data:

```bash
CF="$CF_WRAPPER"   # your Cloudflare CLI wrapper
"$CF" r2 objects list --bucket-name recall-backups --prefix recall/ --per-page 100
mkdir -p /tmp/recall-restore
"$CF" r2 objects get recall/2026-11-09/recall-20261109-142633.db.gz \
  --bucket-name recall-backups > /tmp/recall-restore/recall.db.gz
gunzip /tmp/recall-restore/recall.db.gz
sqlite3 /tmp/recall-restore/recall.db 'pragma integrity_check; select count(*) from memories'
```

Then swap it into the volume. The old files (including `-wal` and `-shm`, which must not sit
next to the restored file) move to `/data/pre-restore/`, and the copy runs as uid 1000 (the
app's user) so the app can write to it. The app image has no
shell, so the file steps run in `node:24-bookworm-slim` (the image the build already uses):

```bash
cd ~/recall   # the compose project
docker compose stop app
docker run --rm --user 1000:1000 -v recall_data:/data -v /tmp/recall-restore:/restore:ro node:24-bookworm-slim \
  sh -c 'rm -rf /data/pre-restore && mkdir /data/pre-restore && mv /data/recall.db* /data/pre-restore/ && cp /restore/recall.db /data/recall.db'
docker compose start app
curl -s http://127.0.0.1:4800/health
rm -r /tmp/recall-restore
```

Once the app looks right, `docker run --rm -v recall_data:/data node:24-bookworm-slim rm -rf /data/pre-restore`.

To restore a pre-deploy backup instead of an R2 object, copy it out of the volume first, then
check and swap it the same way:

```bash
cd ~/recall   # the compose project
docker run --rm -v recall_data:/data:ro node:24-bookworm-slim ls -1 /data/backups/   # pre-deploy-<UTC timestamp>.db.gz, newest 3
mkdir -p /tmp/recall-restore
docker compose cp app:/data/backups/pre-deploy-20261109T081500Z.db.gz /tmp/recall-restore/recall.db.gz
gunzip /tmp/recall-restore/recall.db.gz
```

What a restore brings back with it:

- Logins are whatever the snapshot holds. Anyone who changed their password after it signs in with
  the old one, and passkeys added after it are gone (add them again in Settings, Account).
- A snapshot from before multi-user (migration 0004) has no users. On boot the app creates `admin`
  from `ADMIN_PASSWORD_HASH`; if that's no longer set, nobody can sign in. Recreate the owner with
  `docker compose exec app recall create-user <username> --workspace <slug> --role owner`
  (it prompts for a new password). The boot log names the migrated workspace's slug.
