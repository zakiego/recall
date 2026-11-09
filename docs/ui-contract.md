# Recall dashboard: design contract

Written before building phase 6 (anti-ui-slop step 1). The finish gate checks against this.

## Audience and task

One admin (Zaki) checking what agents have remembered about a container (a person, project or
team), correcting wrong facts, and wiring new agents up with keys. Visits are short and
purposeful: "what does Recall think about X right now, and why?"

Primary task: read the current facts for a container and fix the wrong ones.
Secondary: search like an agent would, add content by hand, watch it get processed, manage keys.

## Category default and its opposite

- Default: a SaaS analytics dashboard. KPI cards across the top, a chart, then a table.
- Predictable opposite: a chat-style "ask your memory" box as the home screen.
- Neither. Recall's own idea is that facts have versions. The home screen is the list of current
  facts, and every fact that changed shows its version count. One click opens the chain
  (Adidas, then Puma, then Nike) with the dates and the source document of each version. No
  other memory tool shows this up front, and it's the thing you need when an agent says
  something stale.

## Shell

Same look as the ai-gateway dashboard: inset sidebar, sticky translucent header, zinc ramp,
Geist and Geist Mono, base-nova shadcn components. The container switcher sits in the header
because every page is scoped to one container. Pages: Memories, Search, Graph, Add, Keys.

## Rules (counted at the finish gate)

- One accent hue: none beyond the zinc primary. Status colors (green, amber, red) only carry
  status: job state, revoked keys, destructive actions.
- Radius scale: the theme's `--radius` steps only (rounded-sm, -md, -lg, -xl, full for pills).
- 0 emoji, 0 gradients, 0 decorative cards around single elements.
- Machine-written values (ids, keys, scores, timestamps) in Geist Mono with tabular numbers.
- Every data view has loading (Skeleton), empty (Empty with a next step) and error (Alert with
  retry) states.
- Destructive actions (delete memory, revoke key) sit in a row menu or behind AlertDialog,
  never as a red button in every row.
- Long values (facts up to 500 chars, container names up to 128, URLs) wrap or truncate with
  the full value reachable (tooltip or expanded row). Nothing overflows at 390px.
- The new API key is shown once, with copy, and a clear "you won't see this again".

## States per page

- Memories: loading, empty container (points to Add and the API docs), list, filtered to
  nothing, expanded chain, editing inline, saving, error.
- Search: idle with example queries, searching, results with score bars and source, no
  results with suggestions (try chunks, try another container), error.
- Graph: loading, empty, rendered, too many nodes (capped with a note).
- Add: idle, submitting, queued, processing, done (with extracted fact count), failed (with
  the error and a retry).
- Keys: loading, none yet, list, created (key shown once), revoked rows dimmed.
- Login: idle, wrong password, locked out, misconfigured server (no hash or secret set).

## Limitations

No image generation tool and no Uizze MCP in this session, so there is no visual target image.
The incumbent ai-gateway dashboard is the reference instead.
