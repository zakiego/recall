package embed

// Two ways to run the model (config embedMode), as in embed.ts:
//
//   - "inline": in this process, loaded on first use and kept. Tests, the CLI.
//   - "child": in a child process (`recall embed-child`), started on first use
//     and killed after embedIdleMs without a request. Production. The model and
//     what ONNX Runtime keeps after inference are most of the memory, and none
//     of it goes back to the OS until the process ends. The cost is a model
//     load on the first request after an idle spell.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"syscall"
	"time"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/mem"
)

// LibPath finds the ONNX Runtime library: ONNXRUNTIME_LIB, then the image's
// /usr/lib/recall, then lib/onnxruntime/<os>-<arch> next to the executable or
// one folder up (a checkout of apps/server).
func LibPath() (string, error) {
	if p := os.Getenv("ONNXRUNTIME_LIB"); p != "" {
		return p, nil
	}
	name := "libonnxruntime.so." + RuntimeVersion
	if goruntime.GOOS == "darwin" {
		name = "libonnxruntime." + RuntimeVersion + ".dylib"
	}
	candidates := []string{filepath.Join("/usr/lib/recall", name)}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, root := range []string{dir, filepath.Dir(dir)} {
			candidates = append(candidates, filepath.Join(root, "lib", "onnxruntime", goruntime.GOOS+"-"+goruntime.GOARCH, name))
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("onnxruntime library not found (set ONNXRUNTIME_LIB): looked in %v", candidates)
}

// loader loads the model on first use; a failed load is tried again on the
// next call (embed-model.ts resets its promise on error).
type loader struct {
	mu        sync.Mutex
	modelsDir string
	m         *Model
}

func (l *loader) embed(texts []string, kind Kind) ([][]float32, error) {
	l.mu.Lock()
	if l.m == nil {
		lib, err := LibPath()
		if err == nil {
			if err = EnsureModelFiles(l.modelsDir); err == nil {
				l.m, err = LoadModel(l.modelsDir, lib)
			}
		}
		if err != nil {
			l.mu.Unlock()
			return nil, err
		}
	}
	m := l.m
	l.mu.Unlock()
	return m.Embed(texts, kind)
}

var (
	inlineMu sync.Mutex
	inline   *loader
)

func inlineEmbed(texts []string, kind Kind) ([][]float32, error) {
	inlineMu.Lock()
	if inline == nil {
		inline = &loader{modelsDir: config.Load().ModelsDir}
	}
	l := inline
	inlineMu.Unlock()
	return l.embed(texts, kind)
}

// ChildCommand builds the command that runs the child. The default is this
// executable with "embed-child"; tests point it at the test binary.
var ChildCommand = func() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(exe, "embed-child"), nil
}

type result struct {
	vecs [][]float32
	err  error
}

type child struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	pending map[uint64]chan result
	idle    *time.Timer
	done    bool
}

var (
	mu       sync.Mutex
	current  *child
	nextID   uint64 = 1
	noChild  bool
	logf     = logx.Info
	errorf   = logx.Error
	childLog sync.Once
)

func startChild() (*child, error) {
	cmd, err := ChildCommand()
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Environ(), "MODELS_DIR="+config.Load().ModelsDir)
	cmd.Stderr = os.Stderr
	setChildAttrs(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &child{cmd: cmd, stdin: stdin, pending: map[uint64]chan result{}}
	go c.read(bufio.NewReader(stdout))
	return c, nil
}

// read delivers responses until the pipe closes, then waits for the exit and
// fails whatever is still pending.
func (c *child) read(r *bufio.Reader) {
	var readErr error
	for {
		id, vecs, failed, msg, err := readResponse(r)
		if err != nil {
			readErr = err
			break
		}
		mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		if len(c.pending) == 0 && !c.done {
			c.armIdle()
		}
		mu.Unlock()
		if !ok {
			continue
		}
		if failed {
			ch <- result{err: errors.New("embedder: " + msg)}
		} else {
			ch <- result{vecs: vecs}
		}
	}
	// A garbled pipe (not the end of a dying child's output): make sure the
	// child is gone, so Wait returns and every pending call fails instead of
	// waiting forever.
	if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		_ = c.cmd.Process.Kill()
	}
	waitErr := c.cmd.Wait()
	c.fail(fmt.Errorf("embedder exited (%s)", exitReason(c.cmd, waitErr)))
}

func (c *child) fail(err error) {
	mu.Lock()
	defer mu.Unlock()
	if current == c {
		current = nil
	}
	c.done = true
	if c.idle != nil {
		c.idle.Stop()
	}
	for id, ch := range c.pending {
		ch <- result{err: err}
		delete(c.pending, id)
	}
}

// armIdle (with mu held) stops the child once the idle spell is over.
func (c *child) armIdle() {
	if c.idle != nil {
		c.idle.Stop()
	}
	ms := float64(config.Load().EmbedIdleMs)
	// setTimeout: a delay over 2^31-1 ms, under 1 or NaN becomes 1 ms.
	delay := ms
	if math.IsNaN(delay) || delay < 1 || delay > 2147483647 {
		delay = 1
	}
	c.idle = time.AfterFunc(time.Duration(delay*float64(time.Millisecond)), func() {
		mu.Lock()
		if current != c || len(c.pending) > 0 {
			mu.Unlock()
			return
		}
		current = nil
		mu.Unlock()
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
		logf("[recall] embedder stopped after %ss idle", jsonx.NumberString(jsRound(ms/1000)))
	})
}

func childEmbed(texts []string, kind Kind) ([][]float32, error) {
	mu.Lock()
	if current == nil {
		c, err := startChild()
		if err != nil {
			mu.Unlock()
			return nil, err
		}
		current = c
	}
	c := current
	if c.idle != nil {
		c.idle.Stop()
	}
	id := nextID
	nextID++
	ch := make(chan result, 1)
	c.pending[id] = ch
	mu.Unlock()

	c.writeMu.Lock()
	err := writeRequest(c.stdin, id, texts, kind)
	c.writeMu.Unlock()
	if err != nil {
		mu.Lock()
		_, still := c.pending[id]
		delete(c.pending, id)
		if len(c.pending) == 0 && !c.done {
			c.armIdle()
		}
		mu.Unlock()
		if still {
			return nil, err
		}
	}
	r := <-ch
	if r.err == nil && len(r.vecs) != len(texts) {
		return nil, fmt.Errorf("embedder: %d vectors for %d texts", len(r.vecs), len(texts))
	}
	return r.vecs, r.err
}

// useChild is config embedMode "child" with a child command available.
func useChild() bool {
	if config.Load().EmbedMode != "child" {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	if noChild {
		return false
	}
	if _, err := ChildCommand(); err != nil {
		noChild = true
		childLog.Do(func() { errorf("[recall] embed-child entry not found, embedding in-process instead") })
		return false
	}
	return true
}

// Override, when set, answers Embed instead of the model. Golden tests use it
// to replay the vectors the TS build computed, so they run the same on every
// platform.
var Override func(texts []string, kind Kind) ([][]float32, error)

// Embed returns L2-normalized vectors, one per input.
func Embed(texts []string, kind Kind) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	if Override != nil {
		return Override(texts, kind)
	}
	if useChild() {
		return childEmbed(texts, kind)
	}
	return inlineEmbed(texts, kind)
}

// EmbedOne embeds one text.
func EmbedOne(text string, kind Kind) ([]float32, error) {
	v, err := Embed([]string{text}, kind)
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// ChildPID is the running child's pid, 0 when there is none (tests).
func ChildPID() int {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return 0
	}
	return current.cmd.Process.Pid
}

// Stop kills the child if one is running. Pending requests fail.
func Stop() {
	mu.Lock()
	c := current
	current = nil
	if c != nil && c.idle != nil {
		c.idle.Stop()
	}
	mu.Unlock()
	if c != nil {
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
	}
}

// Warm loads the model in the background so the first search isn't slow, with
// one real inference (ONNX Runtime prepares kernels on the first run).
func Warm() {
	inChild := useChild()
	go func() {
		if _, err := Embed([]string{"warmup"}, Query); err != nil {
			errorf("[recall] embedder warmup failed: %v", err)
			return
		}
		if !inChild {
			logf("[recall] embedder ready, memory: %s", mem.Line())
		}
	}()
}

// jsRound is Math.round: halves round up, and -0 prints as 0.
func jsRound(x float64) float64 {
	r := math.Floor(x + 0.5)
	if r == 0 {
		return 0
	}
	return r
}
