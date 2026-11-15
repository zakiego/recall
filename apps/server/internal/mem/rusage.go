package mem

import (
	"runtime"
	"syscall"
)

// peakRSS is getrusage's maxrss (bytes on macOS, kilobytes on Linux).
func peakRSS() uint64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return uint64(ru.Maxrss)
	}
	return uint64(ru.Maxrss) * 1024
}
