# Recall hooks for Claude Code

Two user-level Claude Code hooks that let every session on Zaki's Macs remember him through Recall.

- `SessionStart` fetches `GET /v1/profile?container=zaki&q=<project>` and injects a short block (at most 1,500 characters) as additional context. It has a 1.8 s budget. On a timeout, an error or a missing key it injects nothing, and the session starts as usual.
- `SessionEnd` hands the session to a detached worker and exits right away. The worker reads the transcript and keeps only your prompts and Claude's prose replies. Tool calls, tool output, thinking and code blocks are dropped. It scrubs anything that looks like a secret and sends one chat document with `source_id` `claude-code:<session_id>`. Recall's extractor then keeps only the lasting facts. The hook makes no LLM call of its own.

Sessions with fewer than 2 prompts are skipped. That covers one-shot `claude -p` jobs like the scheduled agents.

## Install (Mac mini)

```bash
node integrations/claude-code/install.mjs
```

The installer:

1. Copies `bin/` and `lib/` to `~/.local/share/recall-hooks/`, so the hooks don't depend on this checkout or worktree. It also records the current `node` path.
2. Creates a dedicated key named `claude-code-hooks` in workspace `zaki` if `~/.config/recall/hooks-key` doesn't exist yet. It runs the bundled CLI in the production container and writes the key straight to the file with mode 600. The key is never printed. Pass `--no-key` to skip this.
3. Backs up `~/.claude/settings.json` to `settings.json.recall-backup-<timestamp>`. It then appends one `SessionStart` and one `SessionEnd` entry and leaves every other hook alone. Running it again replaces only its own entries.

The hooks read `~/.config/recall/hooks-key` and fall back to `~/.config/recall/api-key`.

## Second Mac (MacBook Air)

The commands use `$HOME`, so the same install works under `/home/dev`. The Air has no production container, so copy the key over instead of creating one.

```bash
# on the Air, from a checkout of this repo
mkdir -p ~/.config/recall && chmod 700 ~/.config/recall
scp zakiego@<mac-mini-host>:.config/recall/hooks-key ~/.config/recall/hooks-key
chmod 600 ~/.config/recall/hooks-key
node integrations/claude-code/install.mjs --no-key
node integrations/claude-code/install.mjs --status
```

You can also create a separate key for the Air on the Mac mini, e.g. `create-key claude-code-hooks-air --workspace zaki --quiet`. Then each machine can be revoked on its own.

## Turn it off

| What | How |
|---|---|
| Kill switch, both hooks | `touch ~/.config/recall/hooks-disabled` (delete the file to turn it back on) |
| Kill switch, one session | `RECALL_HOOKS_DISABLED=1 claude` |
| Only the injection | `RECALL_HOOKS_INJECT=0` |
| Only the capture | `RECALL_HOOKS_CAPTURE=0` |
| Uninstall | `node integrations/claude-code/install.mjs --uninstall` |

Uninstall removes only the Recall entries from `settings.json` (after another backup) and deletes `~/.local/share/recall-hooks`. It keeps the key file and the log. To revoke the key: `pnpm cli list-keys --workspace zaki`, then `pnpm cli revoke-key <id>`. In production, run the same commands through `docker compose exec -T app node .output/server/cli.mjs`.

## Settings

All optional, read from the environment Claude Code runs in.

| Variable | Default |
|---|---|
| `RECALL_HOOKS_URL` | `https://recall.zakiego.com` |
| `RECALL_HOOKS_CONTAINER` | `zaki` |
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
