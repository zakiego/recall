package mcp

// The MCP playbook: the operating rules the tool schemas can't express,
// served by the get_playbook tool so they ship with the server that enforces
// them. A schema says what an argument is, not when to search before
// answering, what is worth saving, or how a newer fact supersedes an older
// one. A copy pasted into each client goes stale; this one changes with the
// deploy.

import (
	_ "embed"
	"fmt"
	"hash/fnv"
	"strings"
)

//go:embed playbook.md
var playbookSource string

// playbookMarkdown is the document get_playbook returns after its version line.
var playbookMarkdown = strings.TrimSpace(playbookSource)

// PlaybookVersion stamps the playbook with a hash of its text, so it moves
// whenever the rules do and nobody has to bump it by hand. The server
// instructions carry it too, which lets a client that kept the rules from an
// earlier conversation see they are stale without spending a tool call.
var PlaybookVersion = contentVersion(playbookMarkdown)

// contentVersion is FNV-1a (32-bit) over s, as 8 hex characters: the same
// stamp the other MCP servers with a get_playbook tool use. Changing the
// algorithm makes every cached copy in every client read as stale at once,
// on a day nobody edited the rules.
func contentVersion(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// Instructions is what initialize returns. It is read in every conversation
// whether or not a tool is called, so it holds what an agent needs before its
// first call (what Recall is, that saved facts reach every agent, and where
// the rules are) and points at get_playbook for the rest.
var Instructions = strings.Join([]string{
	"Recall is a long-term memory of the user, their work and the people they know. Look things up here before answering about them.",
	"Every agent on this workspace reads what add_memory saves, and newer facts supersede older ones on their own.",
	"",
	"Call get_playbook before your first tool call. It returns the operating rules (playbook version " + PlaybookVersion + "):",
	"when to search and when to ask, what is worth saving and what never is, containers, how supersession decides,",
	"event_date versus due_date and the time zone, people records, the limits, and the errors.",
	"None of that is inferable from the tool schemas, so do not guess it.",
	"",
	"Once per conversation is enough. If you are carrying rules from an earlier conversation and the version",
	"above does not match the one they came with, they are stale: call get_playbook again and use the new text.",
}, "\n")

// playbookDocument is what get_playbook returns: the version, then the rules.
func playbookDocument() string {
	return "Playbook version: " + PlaybookVersion + "\n\n" + playbookMarkdown
}
