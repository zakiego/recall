# Recall hooks for Claude Code

Two user-level Claude Code hooks that let every session on Zaki's Macs remember him through Recall.

- `SessionStart` fetches `GET /v1/profile?container=zaki&q=<project>` and injects a short block (at most 1,500 characters) as additional context. The block holds the static profile, the facts that match the project name (each with the date it was stored), and only the sentences of the dynamic profile that name the project, stamped with the profile's date. Another repo's work status stays out. Facts can come from ingested email and web pages, so the block tells the agent it is data, not instructions, and every `<` and `>` in it becomes `‹` and `›`, so nothing inside can close the block or fake a tag. It has a 1.8 s budget. On a timeout, an error or a missing key it injects nothing, and the session starts as usual.
- `SessionEnd` hands the session to a detached worker and exits right away. The worker reads the transcript and keeps only your prompts and Claude's prose replies. Tool calls, tool output, thinking and code blocks are dropped. It scrubs anything that looks like a secret and sends one chat document with `source_id` `claude-code:<session_id>`. Recall's extractor then keeps only the lasting facts. The document's header note tells it to skip transient work status (branch names, PR numbers, what is committed or merged), and the branch and path go only in metadata, not in the text it reads. The hook makes no LLM call of its own.

Sessions with fewer than 2 prompts are skipped. That covers one-shot `claude -p` jobs like the scheduled agents.

## Install (Mac mini)

```bash
node integrations/claude-code/install.mjs
```

The installer:

1. Copies `bin/` and `lib/` to `~/.local/share/recall-hooks/`, so the hooks don't depend on this checkout or worktree. It also records the current `node` path.
2. Creates a dedicated key named `claude-code-hooks` in workspace `zaki` if `~/.config/recall/hooks-key` doesn't exist yet. It runs the bundled CLI in the production container and writes the key straight to the file with mode 600. The key is never printed. Pass `--no-key` to skip this.
3. Backs up `~/.claude/settings.json` to `settings.json.recall-backup-<timestamp>`. It then appends one `SessionStart` and one `SessionEnd` entry and leaves every other hook alone. Running it again replaces only its own entries.

The hooks read `~/.config/recall/hooks-key` and fall back to `~/.config/recall/api-key`. The first key file found must be mode 600. If group or others can read it, the hooks refuse it (they don't fall back to the other file), do nothing, and log the exact `chmod 600` command to run.

## Second Mac (MacBook Air)

The hook commands use `$HOME`, so the same entries work under `/home/dev`. The Air has no production container, so give it its own key from the Mac mini. Then each machine can be revoked on its own. Run this from a checkout on the Mac mini (`m1` is the Air's ssh alias). The Air doesn't need a checkout:

```bash
# 1. A key for the Air, streamed straight into a 600 file there and never printed
(cd ~/selfhost/recall && docker compose exec -T app recall create-key claude-code-hooks-air --workspace zaki --quiet) \
  | ssh m1 'umask 077; mkdir -p ~/.config/recall && cat > ~/.config/recall/hooks-key'

# 2. Copy the hook files over and run the installer there
tar -C integrations -cf - --exclude test claude-code \
  | ssh m1 'mkdir -p ~/.cache/recall-hooks-src && tar -C ~/.cache/recall-hooks-src -xf -'
ssh m1 'zsh -lic "node ~/.cache/recall-hooks-src/claude-code/install.mjs --no-key && node ~/.cache/recall-hooks-src/claude-code/install.mjs --status"'
```

To update the Air after a change, run step 2 again.

### settings.json is synced between the Macs

`~/.claude/settings.json` syncs between the two Macs, so the hook entries show up on the other Mac within minutes of an install. The scripts and the key don't sync. Until the other Mac has `~/.local/share/recall-hooks`, its hooks fail (Claude Code reports a non-blocking hook error), inject nothing and send nothing. Run the install on both Macs.

The same goes for uninstalling. `--uninstall` on one Mac removes the entries on both, but it deletes the scripts only on the Mac where it ran. For a full removal, run it on both, or delete `~/.local/share/recall-hooks` on the other Mac. To pause just one Mac, use the kill switch file. It doesn't sync.

## Turn it off

| What | How |
|---|---|
| Kill switch, both hooks | `touch ~/.config/recall/hooks-disabled` (delete the file to turn it back on) |
| Kill switch, one session | `RECALL_HOOKS_DISABLED=1 claude` |
| Only the injection | `RECALL_HOOKS_INJECT=0` |
| Only the capture | `RECALL_HOOKS_CAPTURE=0` |
| Uninstall | `node integrations/claude-code/install.mjs --uninstall` |

Uninstall removes only the Recall entries from `settings.json` (after another backup) and deletes `~/.local/share/recall-hooks`. It keeps the key file and the log. To revoke the key, from `~/selfhost/recall`: `docker compose exec app recall list-keys --workspace zaki`, then `docker compose exec app recall revoke-key <id>`.

## Settings

Read from the environment Claude Code runs in. Only `RECALL_HOOKS_URL` is required.

| Variable | Default |
|---|---|
| `RECALL_HOOKS_URL` | Required, no default. Your instance's origin, e.g. `https://recall.example.com`. Must be `https://`. Plain `http://` works only for `localhost`, `127.0.0.1` and `[::1]`, since the key would otherwise travel in cleartext. Any other URL makes both hooks log the reason and do nothing. |
| `RECALL_HOOKS_CONTAINER` | `default` |
| `RECALL_HOOKS_KEY_FILE` | `~/.config/recall/hooks-key`, then `~/.config/recall/api-key` |
| `RECALL_HOOKS_START_TIMEOUT_MS` | `1800` |
| `RECALL_HOOKS_MAX_CONTEXT_CHARS` | `1500` |
| `RECALL_HOOKS_MIN_PROMPTS` | `2` |
| `RECALL_HOOKS_MAX_SUMMARY_CHARS` | `12000` |
| `RECALL_HOOKS_LOG` | `~/.cache/recall-hooks.log` |

## Log

`~/.cache/recall-hooks.log` gets one JSON line per event: injected or not, the document id stored, and the reason a session was skipped. It never holds the key or session text. Past 512 KB it rotates to `recall-hooks.log.1`, and only one old file is kept.

```bash
tail -5 ~/.cache/recall-hooks.log
```

## Limits

- `SessionEnd` doesn't fire when the terminal is killed hard. That session isn't captured.
- `source_id` dedupe keeps the first document. If a session is resumed and ended again under the same id, the new part isn't sent. Delete the document to send it again.
- The transcript format is internal to Claude Code. The parser skips anything it doesn't recognise, so a format change means less gets captured, not a crash.

## Tests

```bash
node --test integrations/claude-code/test/*.test.mjs
```

The tests run the real hook scripts against a fake Recall server in a temp `HOME`.
