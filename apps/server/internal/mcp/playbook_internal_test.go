package mcp

import (
	"regexp"
	"testing"
)

// The version is a promise to every client holding a cached playbook: change
// the algorithm and all of them read as stale at once. These are the published
// FNV-1a 32-bit vectors, the same ones the other playbook servers test against.
func TestContentVersion(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "811c9dc5",
		"a":                      "e40c292c",
		"foobar":                 "bf9cf968",
		"http://www.google.com/": "365f1661",
	} {
		if got := contentVersion(in); got != want {
			t.Errorf("contentVersion(%q) = %s, want %s", in, got, want)
		}
	}
	a, b := contentVersion("newer facts supersede older ones"), contentVersion("newer facts supersede older ones.")
	if a == b {
		t.Fatal("a one-character edit kept the version")
	}
	hex := regexp.MustCompile(`^[0-9a-f]{8}$`)
	for _, v := range []string{a, b, contentVersion(""), PlaybookVersion} {
		if !hex.MatchString(v) {
			t.Errorf("version %q isn't 8 hex characters", v)
		}
	}
}

func TestPlaybookVersionIsOfServedText(t *testing.T) {
	if PlaybookVersion != contentVersion(playbookMarkdown) {
		t.Fatal("PlaybookVersion isn't the hash of the served markdown")
	}
	if playbookDocument() != "Playbook version: "+PlaybookVersion+"\n\n"+playbookMarkdown {
		t.Fatal("the document should be the version line, a blank line, then the markdown")
	}
}
