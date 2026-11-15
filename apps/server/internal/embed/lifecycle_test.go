package embed

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func captureLogs(t *testing.T) func() []string {
	var mu sync.Mutex
	var lines []string
	old := logf
	logf = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, sprintf(format, args...))
		mu.Unlock()
	}
	t.Cleanup(func() { logf = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

// A call inside the idle spell reuses the running child.
func TestCallInsideIdleSpellKeepsTheChild(t *testing.T) {
	setupService(t, "child")
	t.Setenv("RECALL_EMBED_IDLE_MS", "5000")
	if _, err := Embed([]string{"a"}, Query); err != nil {
		t.Fatal(err)
	}
	pid := ChildPID()
	if _, err := Embed([]string{"b"}, Query); err != nil {
		t.Fatal(err)
	}
	if ChildPID() != pid {
		t.Fatalf("child changed: %d -> %d", pid, ChildPID())
	}
}

// The idle line is the TS one: whole seconds, Math.round.
func TestIdleLogLine(t *testing.T) {
	logs := captureLogs(t)
	setupService(t, "child")
	t.Setenv("RECALL_EMBED_IDLE_MS", "1600")
	if _, err := Embed([]string{"a"}, Query); err != nil {
		t.Fatal(err)
	}
	first := ChildPID()
	waitFor(t, func() bool { return ChildPID() == 0 && !alive(first) })
	found := false
	for _, l := range logs() {
		if l == "[recall] embedder stopped after 2s idle" {
			found = true
		}
	}
	if !found {
		t.Fatalf("logs: %v", logs())
	}
}

// Stop with a call in flight fails it the way proc.kill() (SIGTERM) does.
func TestStopFailsPendingWithSIGTERM(t *testing.T) {
	setupService(t, "child")
	if _, err := Embed([]string{"warm"}, Query); err != nil {
		t.Fatal(err)
	}
	long := make([]string, 200)
	for i := range long {
		long[i] = strings.Repeat("long text ", 300)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Embed(long, Passage)
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	Stop()
	if err := <-done; err == nil || err.Error() != "embedder exited (SIGTERM)" {
		t.Fatalf("%v", err)
	}
}

// A child whose model failed to load works on the next call once the files
// are there (the loading promise is reset on error).
func TestChildRetriesAFailedLoad(t *testing.T) {
	setupService(t, "child")
	src := filepath.Join(testModelsDir(), ModelID)
	dir := t.TempDir()
	t.Setenv("MODELS_DIR", dir)
	if _, err := Embed([]string{"x"}, Query); err == nil {
		t.Fatal("expected a load error")
	}
	pid := ChildPID()
	for _, f := range ModelFiles {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, ModelID, f)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v, err := Embed([]string{"x"}, Query)
	if err != nil || len(v) != 1 || ChildPID() != pid {
		t.Fatalf("%v %d %d", err, ChildPID(), pid)
	}
}

// Without a child command, child mode embeds in-process and says so once.
func TestFallsBackInProcessWithoutAChild(t *testing.T) {
	logs := captureLogs(t)
	setupService(t, "child")
	oldCmd := ChildCommand
	ChildCommand = func() (*exec.Cmd, error) { return nil, errors.New("no executable") }
	mu.Lock()
	noChild = false
	childLog = sync.Once{}
	mu.Unlock()
	defer func() {
		ChildCommand = oldCmd
		mu.Lock()
		noChild = false
		mu.Unlock()
	}()
	old := errorf
	var errs []string
	errorf = func(format string, args ...any) { errs = append(errs, sprintf(format, args...)) }
	defer func() { errorf = old }()
	for i := 0; i < 2; i++ {
		if _, err := Embed([]string{"x"}, Query); err != nil {
			t.Fatal(err)
		}
	}
	if ChildPID() != 0 || len(errs) != 1 || errs[0] != "[recall] embed-child entry not found, embedding in-process instead" {
		t.Fatalf("pid %d, errors %v, logs %v", ChildPID(), errs, logs())
	}
}

// Warm embeds once in the background and logs the in-process load.
func TestWarm(t *testing.T) {
	logs := captureLogs(t)
	setupService(t, "inline")
	Warm()
	waitFor(t, func() bool {
		for _, l := range logs() {
			if strings.HasPrefix(l, "[recall] embedder ready, memory: rss=") {
				return true
			}
		}
		return false
	})
}

// A garbled response fails the pending call and the next call gets a new
// child, instead of everything waiting forever.
func TestGarbledFrameFailsAndRecovers(t *testing.T) {
	setupService(t, "child")
	old := ChildCommand
	ChildCommand = func() (*exec.Cmd, error) {
		cmd, err := old()
		if err != nil {
			return nil, err
		}
		cmd.Env = append(cmd.Env, "RECALL_TEST_EMBED_GARBAGE=1")
		return cmd, nil
	}
	_, err := Embed([]string{"x"}, Query)
	ChildCommand = old
	if err == nil || !strings.HasPrefix(err.Error(), "embedder exited (") {
		t.Fatalf("%v", err)
	}
	if _, err := Embed([]string{"x"}, Query); err != nil {
		t.Fatal(err)
	}
}

// The child exits as soon as its stdin closes, even in the middle of a
// request (the parent went away).
func TestChildExitsWhenStdinCloses(t *testing.T) {
	t.Setenv("MODELS_DIR", testModelsDir())
	t.Setenv("ONNXRUNTIME_LIB", testLibPath(t))
	cmd, err := ChildCommand()
	if err != nil {
		t.Fatal(err)
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	texts := make([]string, 400)
	for i := range texts {
		texts[i] = strings.Repeat("long text ", 300)
	}
	// Two long requests: one running, one queued.
	for id := uint64(1); id <= 2; id++ {
		if err := writeRequest(stdin, id, texts, Passage); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	stdin.Close()
	go io.Copy(io.Discard, bufio.NewReader(stdout))
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("child took %s to exit", el)
	}
}

// Downloads: a cut-off body leaves no temp file, and a token goes only to
// the Hub's own hosts.
func TestDownloadCleansUpAndKeepsTokensOnTheHub(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		w.Write([]byte("partial"))
	}))
	defer srv.Close()
	old := HubURL
	HubURL = srv.URL + "/"
	defer func() { HubURL = old }()
	t.Setenv("HF_TOKEN", "hf_secret")
	dir := t.TempDir()
	if err := EnsureModelFiles(dir); err == nil {
		t.Fatal("a cut-off body was accepted")
	}
	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 0 {
		t.Fatalf("left behind: %v", files)
	}
	if len(auth) != 1 || auth[0] != "" {
		t.Fatalf("authorization sent to a non-Hub host: %v", auth)
	}
}

// A send that fails (the child's stdin is gone) fails that call, and the next
// call gets a working child.
func TestFailedSendFailsTheCall(t *testing.T) {
	setupService(t, "child")
	if _, err := Embed([]string{"a"}, Query); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	c := current
	mu.Unlock()
	c.stdin.Close()
	if _, err := Embed([]string{"b"}, Query); err == nil {
		t.Fatal("a call through a closed pipe succeeded")
	}
	Stop()
	if _, err := Embed([]string{"c"}, Query); err != nil {
		t.Fatal(err)
	}
}

// A call that arrives as the idle timer fires either reuses the child or gets
// a new one; it never fails or hangs.
func TestIdleStopRacingACall(t *testing.T) {
	setupService(t, "child")
	t.Setenv("RECALL_EMBED_IDLE_MS", "1")
	for i := 0; i < 20; i++ {
		if _, err := Embed([]string{"x"}, Query); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		time.Sleep(time.Duration(i%3) * time.Millisecond)
	}
}

// A child response with an empty error message is still an error.
func TestEmptyChildErrorIsAnError(t *testing.T) {
	var buf strings.Builder
	if err := writeResponse(&buf, 7, nil, true, ""); err != nil {
		t.Fatal(err)
	}
	id, vecs, failed, msg, err := readResponse(bufio.NewReader(strings.NewReader(buf.String())))
	if err != nil || id != 7 || !failed || msg != "" || vecs != nil {
		t.Fatalf("%d %v %v %q %v", id, vecs, failed, msg, err)
	}
}
