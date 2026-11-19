# Security

Recall stores personal memories, so security reports get priority.

## Reporting a vulnerability

Please don't open a public issue. Report it privately through GitHub instead: open the repository's Security tab and choose "Report a vulnerability". Only the maintainer sees the report.

Include what you found, how to reproduce it, and what an attacker could do with it. A proof of concept helps. Please don't test against https://recall.zakiego.com beyond what's needed to show the problem, and never read or change data that isn't yours.

You'll get a reply within 7 days. Once a fix ships, you'll be credited in the release notes unless you'd rather not be.

## Supported versions

Only the latest commit on `main` gets fixes. There are no versioned releases yet.

## Scope

In scope: the Go server (`apps/server`), the dashboard (`apps/app`), the Claude Code hooks (`integrations/claude-code`) and the edge worker (`apps/edge`).

Out of scope: the LLM gateway you point `GATEWAY_URL` at, and reports that need a compromised server or a malicious admin of the same instance.
