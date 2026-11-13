# Recall

A self-hosted memory engine for AI agents. Agents send it chats, text, URLs or files; it
extracts facts per container (a person, project or team), keeps them current (newer facts
supersede older ones, with history kept), and serves them back through a REST API, an MCP
server and this dashboard. Runs on Zaki's Mac mini at https://recall.zakiego.com.

## Users

Zaki, an engineer, plus the people he invites. Each person signs in with a username and password
or a passkey, and belongs to one or more workspaces (owner, admin or member). Mostly desktop,
sometimes a phone.
Visits are short: check what Recall currently believes about a container, fix a wrong fact,
see why an agent said something stale, add something by hand, issue a key for a new agent.

## Surfaces

- Dashboard (Operate mode, behind a login, under `/app`): Memories, Search, Ask, Graph, Add,
  Connectors (manual Gmail import), Settings (Members, API keys, Account), Login, and the invite page. Every dashboard page is `noindex`.
- Public site (`apps/site`, static on Cloudflare Pages): the landing page at `/` (a personal
  showcase, Persuade mode, docs as the only call to action) and the docs under `/docs` (Read mode).
  See `docs/site-contract.md`.

## Product truth

- Facts are short English statements with English and Indonesian keywords.
- Each fact is static (stable trait) or dynamic (changes over time).
- A changed fact is a new version; the old one gets an end date. Deletes are soft.
- Extraction runs in a background job through the ai-gateway, a few seconds per document.
- Search is local (FTS5 + vectors), no LLM call, under 100 ms when warm.
- Ask is one LLM call over the top facts, answered only from them, with citations.

## Visual authority

The ai-gateway dashboard (`~/Code/JS/Node/2026/ai-gateway/dashboard`): zinc ramps, Geist and
Geist Mono, ui-sdk components styled to match, inset sidebar, sticky translucent header. Recall
extends it; it doesn't get its own identity. See `docs/ui-contract.md` for the build contract.
