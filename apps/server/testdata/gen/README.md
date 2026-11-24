# Golden generators

The scripts here wrote the golden files in `apps/server/testdata` by running
the TypeScript build that the Go server replaced, so the Go tests hold the
server to what that build did. They import the TS backend
(`apps/app/src/server`), which this tree no longer has, so they don't run here.

To regenerate a golden, check out the tree from before the TS backend was
removed (the Go port's first PR, before the cleanup; the M10a state in
`docs/GO-PORT.md`: the backend in `apps/app/src/server`, its
routes in `src/server/routes-legacy`, the server functions in
`src/server/fns`) in a separate worktree, install it, copy the model files
into its `apps/app/data/models`, and run the script from its `apps/app`:

```bash
npx tsx ../server/testdata/gen/<name>.ts
```

Each script's header says what it records and how the Go test replays it.
`docs/GO-PORT.md` (Golden files, and the Decisions log per milestone) lists
which script wrote which file. The generators replace `globalThis.fetch` with
one that throws, so nothing reaches the Hugging Face Hub, the gateway or
Google.

Comments in the Go code that name `src/server/<file>.ts` or a `*.test.ts`
point at that same TS source.

## Edited by hand since

- 2026-11-17, the move from ai-gateway to Simpang Router. The client now
  posts OpenAI-style bodies to `/v1/chat/completions` and reads a
  chat-completions stream. Every gateway request in `gateway.json`,
  `pipeline.json` and `domains.json` was rewritten to the new body, the
  replies and `testdata/gateway/*.sse` to the new stream with the same text,
  and `gateway.json`'s `streams` replaced with cases for the new reader.
  `m1.json`'s config defaults moved to the Simpang URL, and the fallback
  model to `claude-haiku-5-5` (the same as the extract model). The generators still record the old protocol,
  so a regenerated `gateway.json`, `pipeline.json` or `domains.json` needs the
  same rewrite.
- 2026-11-23, neutral test data. The maintainer's names, home town and
  private project names were swapped for neutral words in every golden, in
  `search/seeded.db`, in these generators and in the Go tests. Each word maps
  to one word of the same length that appeared nowhere in the corpus before,
  applied the same way everywhere, so byte offsets, chunk boundaries and
  FTS5/BM25 statistics stay as they were, and no expected order changed.
  Literals that come from shipped migrations (the legacy workspace that 0004
  creates) were left alone.
  - `seeded.db`: the text of the affected memories, chunks and documents and
    their FTS rows were rewritten with sqlite3, the FTS indexes optimized and
    the file vacuumed. Its vectors, the query vectors in `seeded.json`, and
    the replayed vectors in `domains.json`, `pipeline.json`, `rpc.json` and
    `rest.json` keep the TS run's values under the new text: no test embeds
    those texts, and the recorded similarities depend on them.
  - `embed.json`: the token ids of the changed texts, and the vectors of every
    batch of four that holds one, were recomputed with the Go embedder, which
    matches the TS run bit for bit on darwin/arm64. The same batches in
    `embed-linux-arm64.json` and `embed-linux-amd64.json` were recomputed with
    the Go embedder in a `golang` Linux container (amd64 under emulation),
    which reproduced every unchanged recorded vector bit for bit.
  - `domains.json`: the reminder idempotency keys hash the user id, so the
    owner's keys were recomputed.
  - `apps-goldens.ts` still imports the old TS tree's module and export
    names, and `rpc-goldens.ts` uses the renamed connector provider ids, which
    the old tree doesn't know. A regenerated `rpc.json` or `apps.json` needs
    the provider ids and the feed documents renamed by hand again.
- 2026-11-24, neutral places. The place names around the maintainer's home
  town (cities, the province, the regional dish and language) were swapped the
  same way: same-length words that appeared nowhere before, in the goldens,
  `seeded.db`, the content HTML fixtures, these generators and the Go tests.
  `seeded.db` and the replayed vectors were handled as in the entry above.
  `embed.json` and both Linux goldens got new token ids and vectors for the
  12 batches that hold a changed text; every unchanged vector came out bit
  for bit on all three platforms.
