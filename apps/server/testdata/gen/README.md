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
