package embed

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// No test may reach the Hugging Face Hub: parent and children download from
// a closed port unless a test serves the files itself.
const closedHub = "http://127.0.0.1:1/"

func TestMain(m *testing.M) {
	if os.Getenv("RECALL_TEST_EMBED_GARBAGE") == "1" {
		// A broken child: answers the first request with a garbled frame.
		r := bufio.NewReader(os.Stdin)
		if _, err := readFrame(r); err == nil {
			os.Stdout.Write([]byte{0, 0, 0, 5, 'x', 'x', 'x', 'x', 'x'})
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if os.Getenv("RECALL_TEST_EMBED_CHILD") == "1" {
		if v := os.Getenv("RECALL_TEST_HUB_URL"); v != "" {
			HubURL = v
		}
		os.Exit(RunChild(os.Stdin, os.Stdout))
	}
	HubURL = closedHub
	ChildCommand = func() (*exec.Cmd, error) {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(exe, "-test.run=^$")
		cmd.Env = append(os.Environ(), "RECALL_TEST_EMBED_CHILD=1", "RECALL_TEST_HUB_URL="+closedHub)
		return cmd, nil
	}
	os.Exit(m.Run())
}

var serviceTexts = []string{"Zaki suka sepatu Adidas", "Notes from the planning call about the Q4 roadmap.", "a", "b", "c"}

func setupService(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("MODELS_DIR", testModelsDir())
	t.Setenv("ONNXRUNTIME_LIB", testLibPath(t))
	t.Setenv("RECALL_EMBED", mode)
	t.Cleanup(Stop)
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(end) {
			t.Fatal("timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func equalVecs(a, b []float32) bool {
	if len(a) != len(b) || len(a) != Dims {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type reference struct {
	inline [][]float32
	single [][]float32
}

var (
	refOnce sync.Once
	ref     reference
)

func inlineReference(t *testing.T) reference {
	t.Helper()
	refOnce.Do(func() {
		setupService(t, "inline")
		var err error
		if ref.inline, err = Embed(serviceTexts, Passage); err != nil {
			t.Fatal(err)
		}
		for _, s := range serviceTexts[:2] {
			v, err := Embed([]string{s}, Passage)
			if err != nil {
				t.Fatal(err)
			}
			ref.single = append(ref.single, v[0])
		}
	})
	if ref.inline == nil {
		t.Fatal("no inline reference")
	}
	return ref
}

// embed.test.ts "returns the same vectors as the in-process model".
func TestChildReturnsTheInlineVectors(t *testing.T) {
	r := inlineReference(t)
	setupService(t, "child")
	vecs, err := Embed(serviceTexts, Passage)
	if err != nil {
		t.Fatal(err)
	}
	if ChildPID() == 0 {
		t.Fatal("no child")
	}
	if ChildPID() == os.Getpid() {
		t.Fatal("ran in-process")
	}
	for i := range serviceTexts {
		if !equalVecs(vecs[i], r.inline[i]) {
			t.Fatalf("text %d differs from the in-process vector", i)
		}
	}
}

// embed.test.ts "answers concurrent calls with their own vectors".
func TestChildAnswersConcurrentCalls(t *testing.T) {
	r := inlineReference(t)
	setupService(t, "child")
	var wg sync.WaitGroup
	got := make([][][]float32, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = Embed([]string{serviceTexts[i]}, Passage)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		if errs[i] != nil || !equalVecs(got[i][0], r.single[i]) {
			t.Fatalf("call %d: %v", i, errs[i])
		}
	}
}

// embed.test.ts "stops the child after the idle spell and starts a new one on
// the next call".
func TestChildStopsWhenIdle(t *testing.T) {
	r := inlineReference(t)
	setupService(t, "child")
	t.Setenv("RECALL_EMBED_IDLE_MS", "200")
	var logged atomic.Value
	old := logf
	logf = func(format string, args ...any) {
		if strings.Contains(format, "stopped after") {
			logged.Store(true)
		}
		old(format, args...)
	}
	defer func() { logf = old }()
	if _, err := Embed([]string{"warmup"}, Query); err != nil {
		t.Fatal(err)
	}
	first := ChildPID()
	waitFor(t, func() bool { return ChildPID() == 0 && !alive(first) })
	if logged.Load() == nil {
		t.Fatal("no idle log line")
	}
	v, err := Embed([]string{serviceTexts[0]}, Passage)
	if err != nil || !equalVecs(v[0], r.single[0]) {
		t.Fatal(err)
	}
	if ChildPID() == first || ChildPID() == 0 {
		t.Fatalf("child %d after %d", ChildPID(), first)
	}
}

// embed.test.ts "fails pending calls when the child dies, then recovers".
func TestChildDeathFailsPendingThenRecovers(t *testing.T) {
	r := inlineReference(t)
	setupService(t, "child")
	done := make(chan error, 1)
	go func() {
		_, err := Embed(serviceTexts, Passage)
		done <- err
	}()
	waitFor(t, func() bool { return ChildPID() != 0 })
	if err := syscall.Kill(ChildPID(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	err := <-done
	if err == nil || err.Error() != "embedder exited (SIGKILL)" {
		t.Fatalf("pending call: %v", err)
	}
	v, err := Embed([]string{serviceTexts[0]}, Passage)
	if err != nil || !equalVecs(v[0], r.single[0]) {
		t.Fatal(err)
	}
}

// A child that can't load the model answers with "embedder: <error>".
func TestChildReportsLoadErrors(t *testing.T) {
	setupService(t, "child")
	t.Setenv("MODELS_DIR", t.TempDir())
	_, err := Embed([]string{"x"}, Query)
	if err == nil || !strings.HasPrefix(err.Error(), "embedder: download config.json") {
		t.Fatalf("%v", err)
	}
	if ChildPID() == 0 {
		t.Fatal("child exited on a load error")
	}
}

// Missing files come from the Hub URLs transformers.js uses, into the TS
// cache layout; files already there are not fetched again.
func TestEnsureModelFilesDownloadsMissingFiles(t *testing.T) {
	src := filepath.Join(testModelsDir(), ModelID)
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("User-Agent") != "transformers.js/4.3.2; is_ci/false;" {
			w.WriteHeader(400)
			return
		}
		prefix := "/" + ModelID + "/resolve/main/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(404)
			return
		}
		http.ServeFile(w, r, filepath.Join(src, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, prefix))))
	}))
	defer srv.Close()
	old := HubURL
	HubURL = srv.URL + "/"
	defer func() { HubURL = old }()

	dir := t.TempDir()
	if err := EnsureModelFiles(dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range ModelFiles {
		a, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, ModelID, f))
		if err != nil || string(a) != string(b) {
			t.Fatalf("%s differs after download: %v", f, err)
		}
	}
	if len(paths) != len(ModelFiles) {
		t.Fatalf("requests %v", paths)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ModelID, "onnx"))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
	if err := EnsureModelFiles(dir); err != nil || len(paths) != len(ModelFiles) {
		t.Fatalf("second call fetched again: %v %v", paths, err)
	}
	m, err := LoadModel(dir, testLibPath(t))
	if err != nil {
		t.Fatal(err)
	}
	m.Close()

	HubURL = srv.URL + "/nothing/"
	err = EnsureModelFiles(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing file: %v", err)
	}
}
