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
Geist and Geist Mono, ui-sdk components styled with the same tokens. The container switcher sits in the header
because every page is scoped to one container. Pages: Memories, Search, Ask, Graph, Add, Keys.

## Rules (counted at the finish gate)

- One accent hue: none beyond the zinc primary. Status colors (green, amber, red) only carry
  status: job state, revoked keys, destructive actions.
- Radius scale: the theme's `--radius` steps only (rounded-sm, -md, -lg, -xl, full for pills).
- 0 emoji, 0 decorative gradients (the one gradient is the Skeleton shimmer band, which shows loading), 0 decorative cards around single elements.
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
- Ask: idle with example questions, asking (waits on the job queue, so show elapsed seconds and
  say why it can take a while), answered with citations, partly or not covered by memory (says
  so, points to Add), empty container, error (timeout, gateway down, job loop off) with retry.
- Graph: loading, empty, rendered, too many nodes (capped with a note).
- Add: idle, submitting, queued, processing, done (with extracted fact count), failed (with
  the error and a retry).
- Keys: loading, none yet, list, created (key shown once), revoked rows dimmed.
- Login: idle, wrong password, locked out, misconfigured server (no hash or secret set).

## Ask page (added with feat/ask)

Audience and task: Zaki checking what an agent would be told, and whether to trust it. Primary
task: ask a question, read a short answer, and check each claim against the fact behind it.

- Default: a chat thread (bubbles, typing dots, a running conversation).
- Predictable opposite: another ranked results list, which Search already is.
- Recall's own idea: an answer you can audit. Every claim carries a numbered citation chip;
  hovering or focusing it shows the exact fact, and it links to that fact on the Memories page,
  where its version chain is. Below the answer, the sources in citation order. "Not in memory"
  is a first-class result, not an error, with a way to add the missing fact.
- One question, one answer. No thread history: each ask is a fresh grounded call.
- Machine values (model, timing, ids) in Geist Mono. Citation chips are small mono pills, not
  links in running text, so they don't break the reading line.
- Long answers wrap; facts in previews and sources wrap in full. Nothing overflows at 390px.

## Limitations

No image generation tool and no Uizze MCP in this session, so there is no visual target image.
The incumbent ai-gateway dashboard is the reference instead.

## Users and workspaces (added with multi-user)

- Header: workspace switcher (DropdownMenu, role next to each name, "New workspace") left of the
  container switcher. Settings pages hide the container switcher, they aren't scoped to one.
- Sidebar groups: Recall (the four pages), Workspace (Members, API keys), You (Account). The
  footer shows who is signed in next to Log out.
- Login: username, password, then "Sign in with a passkey". States: idle, wrong credentials,
  rate limited, passkey cancelled (silent) or failed, server not configured, no accounts yet.
- Invite page: invalid, used, revoked or expired link (Empty with a way out); new account form;
  signed in, join with one click; already a member.
- Members: table with role select for those who may change it, row menu for remove or leave,
  open invite links with revoke. The invite link is shown once, like an API key.
- Account: passkeys (empty state, add dialog, remove behind AlertDialog, unsupported browser) and
  change password (signs out other sessions).
- Members see API keys read-only, with a note saying who can manage them.

## Connect page (added with R1)

Audience and task: someone in a workspace wiring a new agent to Recall. Primary task: copy one
config that works for their client, with a real key and the right container already in it, in
under a minute. Secondary: give the agent instructions so it actually uses its memory.

- Default: a docs page, a wall of code blocks with `YOUR_API_KEY` in each.
- Predictable opposite: a multi-step wizard that hides the config behind Next buttons.
- Recall's own idea: a live config generator for this workspace. One key action at the top fills
  the real key into every snippet on the page, the origin comes from the server's config, and the
  container is the one in the header switcher, so the agent instructions name the exact container
  the agent should read and write. Nothing to edit after copying.
- Two pages in the sidebar's Connect group: Connect ("Where does your agent run?" and the config) and
  Agent instructions (the setup prompt, with its own "Which agent is it for?" picker). Each links to
  the other with the picked client and container, and each has its own key card.
- Clients are choice cards (Claude Code, Claude Desktop, Other MCP clients, REST): an icon, the name,
  one line of hint and a radio. The picked one shows one primary snippet and a short note on where it
  goes. Claude Desktop says plainly that its config file only starts local programs, so
  `npx mcp-remote` bridges it.
- The key action is general ("Create API key", named `agent-<username>-MMDD`): one key works for
  every client.
- The key is shown once, kept only in page state, and gone when you leave the page. No storage, no
  URL. Without a key, snippets show `rcl_YOUR_KEY` and a line saying so.
- Members can't create keys: they see every snippet with the placeholder and a note to ask an admin.
- Agent instructions: a setup prompt for the picked client. The agent connects itself to Recall,
  then appends the memory rules (for the current container) to its own instructions file. The
  prompt carries the key; the rules never do.

States: loading (Skeleton), loader error (AppError with retry), no key (placeholder), creating key,
create failed (inline error), key created (shown once with warning), member (no create button),
long container names and keys wrap at 390px.

## Dated facts, Upcoming and Reminders (added with R4)

Audience and task: Zaki checking what is coming due (a contract ending, a renewal, a deadline)
and trusting that Recall will email him before it does. Primary task: see the next thing due and
how far away it is. Secondary: fix a wrong date, and set when reminders go out.

- Default: a month calendar grid. Predictable opposite: one more sorted table.
- Neither. Recall's idea: dates belong to facts, and facts have versions. Upcoming is a short
  timeline grouped by how soon (Past due, Within a week, Within a month, Later). Each row leads with the
  date in mono and how many days are left, then the fact, and one click opens its version chain,
  so a date that changed shows what it used to be. A status line above the list says exactly
  what the reminder email will do ("Email 30 and 1 days before, to you@x"), or why it won't.
- The window (7, 30, 90, 365 days) is a pick-one choice that changes the page, so it is a row of
  labelled choice cards with the count in each, not a segmented control (TASTE.md).
- Dates show as "Nov 5, 2026", "Dec 2027" or "2027" by precision; days left as "in 6 days",
  "tomorrow", "3 days ago". Machine values in Geist Mono with tabular numbers.
- Memory rows get small date badges (Happened, Due). Due within 7 days or past due uses the
  warning badge; that is status, so it doesn't break the one-accent rule.
- Edit fact: Happened and Due fields, each a precision choice (Day, Month, Year) plus the matching
  input, and Clear. Empty means no date.

States per page:

- Upcoming: groups are Past due, Within a week, Within a month, Later. Loading, nothing due in the window (Empty that says where dates come from: extraction,
  editing a fact, the backfill command), list, past due group, all containers, error with retry.
  Reminder status: on, off (link to settings), email not configured on the server.
- Reminders (Settings, You): email not configured (Alert naming RESEND_API_KEY and
  RECALL_EMAIL_FROM; the form still saves, test send is disabled), off, on, on without an email
  (field error), saving, saved, sending a test, test sent (to which address), test failed (the
  error). Lead times are a checkbox set (90, 30, 14, 7, 3, 1 days, on the day). Time zone is a
  select that defaults to the browser's zone. Email goes out once a day after 08:00 in that zone.

## Connectors page (added with the Gmail connector)

Audience and task: a workspace member pulling a few emails into a container by hand. Primary task:
connect your own Gmail read-only, search with Gmail's syntax, tick messages, import them, and watch
them get processed.

- Default: an integrations marketplace grid of logo cards. Predictable opposite: a full mail client.
- Recall's own idea: a search box that speaks Gmail's query language, a result list you tick like a
  checklist, and one button that names the container it imports into. Messages already imported are
  marked per container; failed ones say so and can be imported again.
- States: not configured (members: ask an admin; admins: the missing key names and the docs), not
  connected, connected, access ended (connect again), search idle, searching, error with retry, no
  results, results with load more, selection with a dash for a partial "select all" and a cap of 50,
  importing, recent imports with queued, processing, done and failed (with retry), and for admins a
  list of who has connected (no addresses, no mail).
- The query stays out of the URL so mail searches never land in browser history or proxy logs.
- Disconnect sits in the card menu behind an AlertDialog. Long subjects clamp at two lines, sender
  addresses break anywhere, dates never truncate. Nothing overflows at 390px.
- Feed cards sit under the Gmail card, one per feed, workspace-wide, with Add a feed below them.
  Owners and admins get a form (Sync every hour switch, name, address, write-only API key), Save,
  Sync now and Remove (behind an AlertDialog). Add a feed asks for a name, an id (from the name,
  editable, fixed once saved), the address and the key. Members get the status only. States: off,
  syncing hourly, needs attention (the last error in a destructive Alert, which says the cursor
  stayed put), never synced, CONNECTOR_SECRET missing (warning Alert, key field disabled), saving,
  syncing, and a footer line naming the container the notes go into (or the move from the old one).

## People (added with R5)

Audience and task: Zaki, before a call or after an agent names someone, checking who a person is
and what Recall knows about them. Fixing the extractor's mistakes: one person split in two, a
wrong name or role. Primary task: find a person and read the facts that mention them.

- Default: a CRM contact grid (avatar cards, company logos, follow-up reminders).
- Predictable opposite: a bare alphabetical address book.
- Recall's own idea: a person is a lens over facts. Each list row shows the newest fact that
  mentions them, so you see why they're there without a click. The person page is a dated
  timeline of those facts with their sources, and each one links back to its row on Memories
  where its version chain is. Merge is a first-class action, because extraction can split a
  person ("Clara" and "Clara Simmons").
- List: one table (name with role and organization, newest fact, fact count, last mentioned),
  a search field and a sort toggle (Recent, A to Z). No avatars, no initials circles.
- Person page: header with name, role at organization, aliases, contact (emails and handles in
  Geist Mono, each copyable); the summary; the timeline. Edit, Merge and Delete sit in one
  actions menu. Delete and Merge confirm in a dialog; delete says the facts stay.
- Possible duplicates (added with fix/people-dedupe): above the list, a card of likely pairs
  ("Rizky Kurn into Rizky Kurniawan", with the reason). Each row has "Not the same" (kept
  per browser) and Merge, which confirms in an AlertDialog. It loads after the page, since the
  check runs the embedding model, and stays hidden while loading or when there are no pairs. A
  failed check shows an Alert with retry.
- Memory rows, search results and Ask show linked people as small links, never as a column
  that is empty for most facts.
- States. List: loading, no people yet (says they come from facts and how to backfill),
  filtered to nothing, error. Person: loading, not found (merged or deleted, back to People),
  no current facts, editing, merging (searchable picker of others in the container), deleting.
- Long names, emails and organizations wrap or truncate with the full value reachable. Nothing
  overflows at 390px.
