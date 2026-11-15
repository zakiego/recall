package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || strings.TrimSpace(out.String()) != Version {
		t.Fatalf("%d %q", code, out.String())
	}
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("usage exit %d", code)
	}
	if code := run([]string{"nope"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "unknown command: nope\nusage: cli <command>") {
		t.Fatalf("%d %q", code, errb.String())
	}
}

// An admin command opens the database at RECALL_DB_PATH, migrated.
func TestAdminCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "recall.db")
	t.Setenv("RECALL_DB_PATH", file)
	var out, errb bytes.Buffer
	if code := run([]string{"list-users"}, &out, &errb); code != 0 || out.String() != "no users\n" {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}

func TestHealthcheck(t *testing.T) {
	status := 200
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(status)
	}))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener = l
	srv.Start()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	t.Setenv("PORT", port)
	var errb bytes.Buffer
	if code := run([]string{"healthcheck"}, &errb, &errb); code != 0 {
		t.Fatalf("200: exit %d %s", code, errb.String())
	}
	status = 503
	if code := run([]string{"healthcheck"}, &errb, &errb); code != 1 {
		t.Fatalf("503: exit %d", code)
	}
	srv.Close()
	if code := run([]string{"healthcheck"}, &errb, &errb); code != 1 {
		t.Fatalf("down: exit %d", code)
	}
}

// Nitro's address and srvx's shutdown timeout.
func TestListenAddrAndShutdown(t *testing.T) {
	t.Setenv("HOST", "")
	t.Setenv("NITRO_HOST", "")
	for _, c := range []struct{ nitro, port, want string }{
		{"", "", ":3000"}, {"-", "4871", ":4871"}, {"4872abc", "4871", ":4872"}, {"x", "4871", ":3000"},
	} {
		t.Setenv("PORT", c.port)
		if c.nitro == "-" {
			os.Unsetenv("NITRO_PORT")
		} else {
			t.Setenv("NITRO_PORT", c.nitro)
		}
		if got := listenAddr(); got != c.want {
			t.Errorf("NITRO_PORT %q PORT %q: %s, want %s", c.nitro, c.port, got, c.want)
		}
	}
	for v, want := range map[string]time.Duration{"": 5 * time.Second, "0": 5 * time.Second, "12": 12 * time.Second, "x": 5 * time.Second} {
		t.Setenv("SERVER_SHUTDOWN_TIMEOUT", v)
		if got := shutdownTimeout(); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}
