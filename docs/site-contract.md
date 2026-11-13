# Recall site: design contract

Written before building `apps/site` (anti-ui-slop step 1). The finish gate checks against this.

## Audience and task

Engineers who land on recall.zakiego.com: people curious about what Zaki built, people who build agents and want to read how Recall works, and the few who use it through an invite. Nobody here is buying anything.

Primary task: understand in one screen what Recall does, then read the docs.
Everything about internals (the pipeline, the stack, the decisions) lives in the docs, and the landing page links there instead of repeating it.

## Category default and its opposite

- Default: a SaaS product landing. A big claim, "Get started free", a logo wall, three feature cards with icons, a pricing table, testimonials.
- Predictable opposite: a terminal-themed "hacker" page, or a README rendered as a page.
- Neither. Recall's own idea is that a fact has a history: new content writes a new version and the old one gets an end date. The first screen shows how that happens as one big animated graph (a chat flows into Recall, the new fact lands, the old one is kept and marked replaced), under a headline, one sentence and the docs button.

## Landing page shape

- Hero: headline, one sentence, "Read the docs" (primary) and "How it works" (into the docs), then the graph with a caption that types out one line per step, before the graph acts on it. At 390 wide the caption and the graph fit in the first screen.
- Then a two-tone heading and at most three points, a title and one line each, each linking to the docs page that explains it.
- A closing line with the docs button and a self-hosting link, the one place a button is 44px tall.
- A small footer: the domain, who built it, and Docs.
- No paragraphs about internals, no code blocks, no stack table. Keep the visible copy around 130 words.

## Content rules

- No signup, no pricing, no customers, no testimonials, no invented numbers. The only call to action is the docs.
- Every claim comes from the repo: README, PLAN.md, docs/, and the code. Numbers that appear (384 dimensions, 800-word chunks, 12 facts for Ask, 20 MB uploads) are the code's own values.
- Screenshots come from a local build with sample data, never from the live instance.
- No secrets, home-directory paths, internal hostnames, keys or IPs, here or in the docs.

## Look

- The landing and legal pages (`components/landing/landing.css`): Linear's system, the same as Palui's landing. Always dark (`#08090a`, panels `#0f1011` and `#141516`), Inter at 510 with -0.022em tracking on headings, Geist Mono for machine values, pill buttons. The docs keep the dashboard's zinc ramp and Geist.
- One accent hue: Linear's indigo `#5e6ad2`, for the focus ring and text selection only. Status colors (green, amber) only where they carry status, as in the version chain.
- Radius scale: 4, 6, 8 and 12px for panels, full for buttons and nav pills.
- 0 emoji, 0 decorative blobs, 0 cards around single elements. No gradients, except low-alpha white on the hero frame's hairline and top light, the dot fields, and the masked rings behind the closing line.
- Machine values (ids, timestamps, routes, JSON) in Geist Mono with tabular numbers.
- The docs follow the system theme, with a toggle. The landing is dark only.

## States and sizes

- Static page, so no loading or error states of its own. Images carry width and height so nothing shifts, and have real alt text.
- Works at 390, 768, 1024 and 1440 wide. Code samples scroll inside their block, never the page.
- Motion is subtle and optional, with one exception: the hero graph opens on its first step and builds up in order, because the finished picture shown first was hard to follow. `prefers-reduced-motion` gets the finished graph, and it pauses on a button, offscreen and in a hidden tab.
