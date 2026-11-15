package embed

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/mem"
)

// The parent and the child talk over the child's stdin and stdout in frames:
// a 4-byte big-endian length, then that many bytes. A request frame is JSON
// {id, texts, kind}. A response frame is a 4-byte big-endian header length,
// the JSON header {id, n, error} and n vectors of Dims little-endian float32,
// so vectors cross as they are (the TS child used structured clone for that).

const maxFrame = 1 << 30

type request struct {
	ID    uint64   `json:"id"`
	Texts []string `json:"texts"`
	Kind  Kind     `json:"kind"`
}

type responseHeader struct {
	ID     uint64 `json:"id"`
	N      int    `json:"n"`
	Failed bool   `json:"failed,omitempty"`
	Error  string `json:"error,omitempty"`
}

func writeFrame(w io.Writer, b []byte) error {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	if _, err := w.Write(append(n[:], b...)); err != nil {
		return err
	}
	return nil
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > maxFrame {
		return nil, fmt.Errorf("frame of %d bytes", size)
	}
	b := make([]byte, size)
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeRequest(w io.Writer, id uint64, texts []string, kind Kind) error {
	b, err := json.Marshal(request{ID: id, Texts: texts, Kind: kind})
	if err != nil {
		return err
	}
	return writeFrame(w, b)
}

// readResponse returns the id, the vectors, and for a failed request
// failed=true with the child's message.
func readResponse(r *bufio.Reader) (uint64, [][]float32, bool, string, error) {
	b, err := readFrame(r)
	if err != nil {
		return 0, nil, false, "", err
	}
	if len(b) < 4 {
		return 0, nil, false, "", errors.New("short response")
	}
	hl := binary.BigEndian.Uint32(b[:4])
	if int(hl) > len(b)-4 {
		return 0, nil, false, "", errors.New("bad response header")
	}
	var h responseHeader
	if err := json.Unmarshal(b[4:4+hl], &h); err != nil {
		return 0, nil, false, "", err
	}
	body := b[4+hl:]
	if h.Failed {
		return h.ID, nil, true, h.Error, nil
	}
	if h.N < 0 || len(body) != h.N*Dims*4 {
		return 0, nil, false, "", errors.New("bad response body")
	}
	vecs := make([][]float32, h.N)
	for i := range vecs {
		v := make([]float32, Dims)
		for j := range v {
			v[j] = math.Float32frombits(binary.LittleEndian.Uint32(body[(i*Dims+j)*4:]))
		}
		vecs[i] = v
	}
	return h.ID, vecs, false, "", nil
}

func writeResponse(w io.Writer, id uint64, vecs [][]float32, failed bool, msg string) error {
	hb, err := json.Marshal(responseHeader{ID: id, N: len(vecs), Failed: failed, Error: msg})
	if err != nil {
		return err
	}
	b := make([]byte, 4, 4+len(hb)+len(vecs)*Dims*4)
	binary.BigEndian.PutUint32(b, uint32(len(hb)))
	b = append(b, hb...)
	for _, v := range vecs {
		for _, x := range v {
			b = binary.LittleEndian.AppendUint32(b, math.Float32bits(x))
		}
	}
	return writeFrame(w, b)
}

// RunChild is `recall embed-child`: it answers requests on in until in closes
// (the parent exited or stopped it), one at a time, in order (two at once
// would double the peak memory). The model loads on the first request.
func RunChild(in io.Reader, out io.Writer) int {
	// Its stdout is the protocol pipe, so the child's log lines go to stderr.
	logx.SetOutput(os.Stderr, os.Stderr)
	modelsDir, ok := os.LookupEnv("MODELS_DIR")
	if !ok {
		modelsDir = "data/models"
	}
	l := &loader{modelsDir: modelsDir}
	w := bufio.NewWriter(out)
	loaded := false
	// Requests are read on their own goroutine into an unbounded queue, so a
	// closed stdin (the parent went away) ends the process at once, even with a
	// long request running and more queued, as the TS child exits on
	// "disconnect".
	var qmu sync.Mutex
	var queue [][]byte
	closed := false
	ready := make(chan struct{}, 1)
	go func() {
		r := bufio.NewReader(in)
		for {
			b, err := readFrame(r)
			qmu.Lock()
			if err != nil {
				closed = true
			} else {
				queue = append(queue, b)
			}
			qmu.Unlock()
			select {
			case ready <- struct{}{}:
			default:
			}
			if err != nil {
				childExit(0)
				return
			}
		}
	}()
	next := func() ([]byte, bool) {
		for {
			qmu.Lock()
			if len(queue) > 0 {
				b := queue[0]
				queue = queue[1:]
				qmu.Unlock()
				return b, true
			}
			if closed {
				qmu.Unlock()
				return nil, false
			}
			qmu.Unlock()
			<-ready
		}
	}
	for {
		b, open := next()
		if !open {
			return 0
		}
		var req request
		if err := json.Unmarshal(b, &req); err != nil {
			logf("[recall] embed-child: bad request: %v", err)
			return 1
		}
		vecs, err := l.embed(req.Texts, req.Kind)
		msg, failed := "", false
		if err != nil {
			msg, failed = err.Error(), true
			vecs = nil
		} else if !loaded {
			loaded = true
			logf("[recall] embedder ready (pid %d), memory: %s", os.Getpid(), mem.Line())
		}
		if err := writeResponse(w, req.ID, vecs, failed, msg); err != nil {
			return 0
		}
		if err := w.Flush(); err != nil {
			return 0
		}
	}
}

// childExit ends the child when its stdin closes. Tests replace it.
var childExit = os.Exit

// exitReason is Node's `signal ?? "code " + code` for the child's exit.
func exitReason(cmd *exec.Cmd, waitErr error) string {
	st := cmd.ProcessState
	if st == nil {
		return "code null"
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return unix.SignalName(ws.Signal())
	}
	return fmt.Sprintf("code %d", st.ExitCode())
}
