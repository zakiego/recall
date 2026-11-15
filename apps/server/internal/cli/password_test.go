package cli

// password-prompt.test.ts, and testdata/password.json from
// testdata/gen/password-goldens.ts: keystrokes fed to the real TS
// readPassword on a fake terminal, with the password or error each gives.

import (
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/testutil"
)

func TestTerminalReadsWithoutEcho(t *testing.T) {
	pw, err := readTerminal(strings.NewReader("correct horse battery\r"))
	if err != nil || pw != "correct horse battery" {
		t.Fatal(pw, err)
	}
}

func TestPipedStdinReadWhole(t *testing.T) {
	pw, err := readPiped(strings.NewReader("piped password value\n"))
	if err != nil || pw != "piped password value" {
		t.Fatal(pw, err)
	}
}

func TestCtrlCCancels(t *testing.T) {
	if _, err := readTerminal(strings.NewReader("\x03")); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatal(err)
	}
}

func TestKeystrokesMatchReadline(t *testing.T) {
	var cases []struct {
		Input    string  `json:"input"`
		Password *string `json:"password"`
		Error    *string `json:"error"`
		// TS kept waiting for Enter even after the input ended.
		PendingUntilEOF bool `json:"pendingUntilEof"`
	}
	testutil.LoadJSON(t, "password.json", &cases)
	for _, c := range cases {
		pw, err := readTerminal(strings.NewReader(c.Input))
		switch {
		case c.PendingUntilEOF:
			if err != errEOF {
				t.Errorf("%q: got %q, %v; TS waits for Enter", c.Input, pw, err)
			}
		case c.Error != nil:
			if err == nil || err.Error() != *c.Error {
				t.Errorf("%q: got %q, %v; want error %q", c.Input, pw, err, *c.Error)
			}
		case err != nil || pw != *c.Password:
			t.Errorf("%q: got %q, %v; want %q", c.Input, pw, err, *c.Password)
		}
	}
}
