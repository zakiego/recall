// Command membench measures the resident memory of the Go embedder the way
// docs/GO-PORT.md M0 asks: after loading the model, after one batch of 4 long
// texts and after 20 batches. testdata/gen/membench.ts does the same for the
// TS embedder. Both read RSS with ps, so the numbers compare.
//
//	go run -tags sqlite_fts5 ./tools/membench <modelsDir> <libonnxruntime>
package main

import (
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/zakiego/recall/apps/server/internal/embed"
)

func heap() string {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	return fmt.Sprintf("heapInuse=%.1f heapSys=%.1f heapReleased=%.1f MiB", float64(ms.HeapInuse)/1048576, float64(ms.HeapSys)/1048576, float64(ms.HeapReleased)/1048576)
}

// rssMiB reads VmRSS on Linux (no ps in the image) and asks ps elsewhere.
func rssMiB() float64 {
	if status, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if f := strings.Fields(line); len(f) >= 2 && f[0] == "VmRSS:" {
				kb, _ := strconv.ParseFloat(f[1], 64)
				return kb / 1024
			}
		}
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		panic(err)
	}
	kb, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return kb / 1024
}

func longTexts() []string {
	seeds := []string{
		"The memory engine stores facts about people places and plans so later questions find them quickly",
		"Pemerintah daerah mengumumkan jadwal baru untuk pelayanan publik di kantor kecamatan setiap hari kerja",
		"Alice is a software engineer based in Lisbon who likes Go TypeScript and self hosting",
		"Rapat koordinasi membahas jadwal rilis versi baru dan pembagian tugas tim secara rinci",
	}
	var out []string
	for _, s := range seeds {
		words := strings.Fields(s)
		var b []string
		for i := 0; i < 700; i++ {
			b = append(b, words[i%len(words)])
		}
		out = append(out, strings.Join(b, " "))
	}
	return out
}

func main() {
	modelsDir, lib := os.Args[1], os.Args[2]
	fmt.Printf("start rss=%.1f MiB %s\n", rssMiB(), heap())
	m, err := embed.LoadModel(modelsDir, lib)
	if err != nil {
		panic(err)
	}
	if _, err := m.Embed([]string{"warmup"}, embed.Query); err != nil {
		panic(err)
	}
	fmt.Printf("loaded rss=%.1f MiB %s\n", rssMiB(), heap())
	texts := longTexts()
	if _, err := m.Embed(texts, embed.Passage); err != nil {
		panic(err)
	}
	fmt.Printf("after 1 batch rss=%.1f MiB %s\n", rssMiB(), heap())
	for i := 1; i < 20; i++ {
		if i%5 == 0 {
			fmt.Printf("  after %d batches rss=%.1f MiB\n", i, rssMiB())
		}
		if _, err := m.Embed(texts, embed.Passage); err != nil {
			panic(err)
		}
	}
	fmt.Printf("after 20 batches rss=%.1f MiB %s\n", rssMiB(), heap())
}
