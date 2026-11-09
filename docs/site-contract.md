# Recall site: design contract

Written before building `apps/site` (anti-ui-slop step 1). The finish gate checks against this.

## Audience and task

Engineers who land on recall.zakiego.com: people curious about what Zaki built, people who build agents and want to read how Recall works, and the few who use it through an invite. Nobody here is buying anything.

Primary task: understand in one screen what Recall does and how, then read the docs.
Secondary: see the real dashboard, the architecture and the stack, and the decisions behind them.

## Category default and its opposite

- Default: a SaaS product landing. A big claim, "Get started free", a logo wall, three feature cards with icons, a pricing table, testimonials.
- Predictable opposite: a terminal-themed "hacker" page, or a README rendered as a page.
- Neither. Recall's own idea is that a fact has a history: new content writes a new version and the old one gets an end date. The first screen shows exactly that, with the real request and the real response shape from the API, next to the version chain it produced. Then the page explains the pipeline, shows the dashboard, and says how it was built.

## Content rules

- No signup, no pricing, no customers, no testimonials, no invented numbers. The only call to action is the docs.
- Every claim comes from the repo: README, PLAN.md, docs/, and the code. Numbers that appear (384 dimensions, 800-word chunks, 12 facts for Ask, 20 MB uploads) are the code's own values.
- Screenshots come from a local build with sample data, never from the live instance.
- No secrets, home-directory paths, internal hostnames, keys or IPs, here or in the docs.

## Look

- Same family as the dashboard: zinc ramp, Geist and Geist Mono, shadcn base-nova components. The site is the dashboard's front door, not a new brand.
- One accent hue: none beyond the zinc primary. Status colors (green, amber) only where they carry status, as in the version chain.
- Radius scale: the theme's `--radius` steps only (rounded-md, -lg, -xl, full for pills).
- 0 emoji, 0 gradients, 0 decorative blobs, 0 cards around single elements.
- Machine values (ids, timestamps, routes, JSON) in Geist Mono with tabular numbers.
- Light and dark both, following the system, with a toggle in the docs.

## States and sizes

- Static page, so no loading or error states of its own. Images carry width and height so nothing shifts, and have real alt text.
- Works at 390, 768, 1024 and 1440 wide. Code samples scroll inside their block, never the page.
- Motion is subtle and optional: nothing important is hidden until it animates, and `prefers-reduced-motion` gets the final state.
