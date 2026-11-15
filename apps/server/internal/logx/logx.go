// Package logx writes log lines the way the TS build's console calls did:
// console.log to stdout, console.error and console.warn to stderr, no
// timestamp (docker adds one).
package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	mu  sync.Mutex
	out io.Writer = os.Stdout
	err io.Writer = os.Stderr
)

// SetOutput redirects both streams (tests, and the embedding child, whose
// stdout is its protocol pipe).
func SetOutput(stdout, stderr io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	out, err = stdout, stderr
}

// Info is console.log.
func Info(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(out, format+"\n", args...)
}

// Error is console.error and console.warn.
func Error(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(err, format+"\n", args...)
}
