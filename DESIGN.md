# Recall design

The build contracts live in `docs/ui-contract.md` (dashboard) and `docs/site-contract.md` (public site). This file records design calls about Recall as they're made.

## Decisions

- 2026-11-10, landing hero visual. Variants: Pipeline (four abstract stations on a filling track), Story (one example told in four big lines), Graph (a spatial graph like the dashboard's Graph page: sources into Recall, facts out, a "replaces" edge). Kept: Graph. Reason: "I love graph!"
- 2026-11-10, landing page: no dashboard screenshot. The hero graph is the only product visual. Owner: "Remove dashboard image".
- 2026-11-10, Connect page: the key button is general ("Create API key"), not "Create a key for <client>". One key works for every client. Owner: "Button to create API key must be naming general".
- 2026-11-10, Connect page: "Where does your agent run?" and "Agent instructions" are two separate sidebar pages, not one screen. A side-by-side layout was tried first and rejected. Owner: "Split agen instruction and where does agent run, dont in one screen", then picked two sidebar pages.
- 2026-11-10, Connect page: agent instructions are a setup prompt the agent runs itself (connect to Recall, then add the memory rules to its own instructions), not only a rules block to paste by hand. Owner: "instruct agent to setup agent and add instruction memory of course".
- 2026-11-13, landing look: the landing and the legal pages follow Linear's system: always dark on `#08090a`, Inter at 510, 32px pill buttons with 44px pills only on the closing call to action, a sticky blurred header, full-width hairlines between sections, a bento frame for the three points, and the graph inside a top-lit hairline frame. The docs keep Geist and follow the system theme. Owner: "Polish landing page as well like [another project's] new landing page".
