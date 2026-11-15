// fetch-model downloads the e5 model files the embedder needs into a models
// directory (CI fills apps/app/data/models with it before the tests, which
// never reach the Hub themselves).
//
//	go run -tags sqlite_fts5 ./tools/fetch-model <models-dir>
package main

import (
	"fmt"
	"os"

	"github.com/zakiego/recall/apps/server/internal/embed"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fetch-model <models-dir>")
		os.Exit(2)
	}
	if err := embed.EnsureModelFiles(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
