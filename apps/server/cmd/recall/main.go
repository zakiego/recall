// Command recall is the whole backend in one binary:
//
//	recall serve          the HTTP server and the job loop
//	recall healthcheck    GET /health on this container, for the compose healthcheck
//	recall embed-child    the embedding model process (started by serve)
//	recall version
//	recall <admin command> the admin CLI (admin-cli.ts): create-user, list-users,
//	                      create-key, list-keys, revoke-key, hash-password, backup,
//	                      backfill-people, suggest-people-merges, backfill-dates
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/zakiego/recall/apps/server/internal/cli"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
)

// Version is stamped at build time with -ldflags "-X main.Version=...".
var Version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: recall serve | healthcheck | embed-child | version | <admin command>")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "healthcheck":
		return healthcheck(stderr)
	case "embed-child":
		return embed.RunChild(os.Stdin, stdout)
	case "serve":
		return serve(stdout, stderr)
	}
	return runCommand(args, stdout, stderr)
}

// runCommand runs an admin command (cli.ts): it opens the database with
// migrations, same as the app, on first use.
var runCommand = func(args []string, stdout, stderr io.Writer) int {
	var d *db.DB
	defer func() {
		if d != nil {
			d.Close()
		}
	}()
	return cli.Run(args, cli.IO{
		Stdout: stdout,
		DB: func() *db.DB {
			if d == nil {
				var err error
				if d, err = db.Open(config.Load().DBPath); err != nil {
					fmt.Fprintln(stderr, err)
					os.Exit(1)
				}
			}
			return d
		},
		Out:          func(line string) { fmt.Fprintln(stdout, line) },
		Err:          func(line string) { fmt.Fprintln(stderr, line) },
		ReadPassword: func() (string, error) { return cli.ReadPassword(os.Stdin, stderr) },
	})
}

// healthcheck asks this container's server for /health and exits 0 on a 2xx.
// It replaces the old image's `node -e "fetch(...)"` healthcheck.
func healthcheck(stderr io.Writer) int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	c := &http.Client{Timeout: 5 * time.Second}
	res, err := c.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		fmt.Fprintln(stderr, "health:", res.Status)
		return 1
	}
	return 0
}
