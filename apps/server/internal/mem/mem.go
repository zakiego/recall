// Package mem is mem.ts: one line of process memory for the logs.
package mem

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func mb(bytes uint64) int64 { return int64(math.Round(float64(bytes) / 1_048_576)) }

// RSS is the resident set size in bytes (VmRSS on Linux, peak RSS elsewhere).
func RSS() uint64 {
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "VmRSS:" {
				kb, _ := strconv.ParseUint(f[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return peakRSS()
}

// Line is "rss=… heapInuse=… heapSys=… MB". rss is what the container is
// charged for; the heap numbers are Go's. The rest of rss is native memory,
// mostly SQLite and, in the embedding child, ONNX Runtime.
func Line() string {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return fmt.Sprintf("rss=%d heapInuse=%d heapSys=%d MB", mb(RSS()), mb(ms.HeapInuse), mb(ms.HeapSys))
}
