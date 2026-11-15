package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/zakiego/recall/apps/server/internal/web"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zakiego/recall/apps/server/internal/apps"
	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/httpapi"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/reminders"
	"github.com/zakiego/recall/apps/server/internal/users"
)

// shutdownTimeout is srvx's graceful shutdown: SERVER_SHUTDOWN_TIMEOUT
// seconds (parseInt), 5 when unset, zero or not a number.
func shutdownTimeout() time.Duration {
	if n, ok := parseIntJS(os.Getenv("SERVER_SHUTDOWN_TIMEOUT")); ok && n != 0 {
		return time.Duration(n) * time.Second
	}
	return 5 * time.Second
}

// listenAddr is Nitro's node-server address: NITRO_HOST or HOST (all
// interfaces when unset or empty), and parseInt(NITRO_PORT ?? PORT), 3000
// when that is NaN.
func listenAddr() string {
	host := firstEnv("NITRO_HOST", "HOST")
	raw, ok := os.LookupEnv("NITRO_PORT")
	if !ok {
		raw = os.Getenv("PORT")
	}
	port := 3000
	if n, ok := parseIntJS(raw); ok {
		port = n
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// parseIntJS is Number.parseInt(s) base 10: optional whitespace and sign,
// then the leading digits; ok false is NaN.
func parseIntJS(s string) (int, bool) {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// serve is `recall serve`: plugins/jobs.ts's boot (the database and its
// migrations, the first user from ADMIN_PASSWORD_HASH, the embedder warmed
// in the background, the job loop and the hourly reminder and app sync
// jobs), then the HTTP server until SIGTERM or SIGINT.
func serve(stdout, stderr io.Writer) int {
	c := config.Load()
	d, err := db.Open(c.DBPath)
	if err != nil {
		fmt.Fprintf(stderr, "[recall] cannot open the database: %v\n", err)
		return 1
	}
	defer d.Close()
	boot, err := users.BootstrapUsers(d, nil)
	if err != nil {
		fmt.Fprintf(stderr, "[recall] bootstrap failed: %v\n", err)
		return 1
	}
	if boot.Created {
		fmt.Fprintf(stdout, "[recall] created user zaki as owner of workspace %s from ADMIN_PASSWORD_HASH\n", boot.WorkspaceID)
	} else if boot.Reason == "no_admin_hash" {
		// Migrated data lives in workspace "zaki"; a fresh database has no workspace yet.
		cmd := "create-user <username>"
		if _, found, _ := users.FindWorkspace(d, users.LegacyWorkspaceID); found {
			cmd = "create-user <username> --workspace zaki --role owner"
		}
		fmt.Fprintf(stdout, "[recall] no users yet: run `recall %s`\n", cmd)
	}
	embed.Warm()
	defer embed.Stop()
	var runner *jobs.Runner
	if c.JobsEnabled {
		runner = jobs.NewRunner(d, pipeline.Handlers(d, pipeline.DefaultDeps()), nil)
		runner.Start()
		now := time.Now().UnixMilli()
		// The hourly reminder check re-queues itself; this covers the first boot.
		if _, _, err := reminders.Schedule(d, now); err != nil {
			fmt.Fprintf(stderr, "[recall] scheduling reminders: %v\n", err)
		}
		// The hourly app feed pull, only while a connector is on.
		if _, _, err := apps.Schedule(d, now); err != nil {
			fmt.Fprintf(stderr, "[recall] scheduling app sync: %v\n", err)
		}
	}
	// The dashboard is the build compiled in, or RECALL_WEB_DIR's when set
	// (a `vite build` on disk, for development).
	srv := &http.Server{Addr: listenAddr(), Handler: (&httpapi.Server{DB: d, Web: web.Handler(os.Getenv("RECALL_WEB_DIR"))}).Handler()}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		fmt.Fprintf(stderr, "[recall] cannot listen on %s: %v\n", srv.Addr, err)
		if runner != nil {
			runner.Stop()
		}
		return 1
	}
	fmt.Fprintf(stdout, "[recall] listening on %s\n", ln.Addr())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	code := 0
	select {
	case sig := <-stop:
		fmt.Fprintf(stdout, "[recall] %s: shutting down\n", sig)
		// srvx: wait for in-flight requests up to the timeout, then close
		// every connection; a second signal forces it at once.
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout())
		go func() {
			select {
			case <-stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
		}
		cancel()
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "[recall] server: %v\n", err)
			code = 1
		}
	}
	if runner != nil {
		runner.Stop()
	}
	return code
}
