package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/milehighideas/claude-hooks/internal/queuedash"
)

const (
	defaultDashboardPort   = 4180
	defaultDashboardWindow = 50
)

// dashboardOptions are the dashboard command's flags.
type dashboardOptions struct {
	host   string
	port   int
	window int
	once   bool
}

func parseDashboardFlags(args []string, errOut io.Writer) (dashboardOptions, error) {
	port := defaultDashboardPort
	if env := os.Getenv("COMMIT_QUEUE_DASHBOARD_PORT"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n <= 0 {
			// flag reports its own errors; this one is ours to print.
			err := fmt.Errorf("COMMIT_QUEUE_DASHBOARD_PORT must be a port number, not %q", env)
			say(errOut, "%v\n", err)
			return dashboardOptions{}, err
		}
		port = n
	}
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var o dashboardOptions
	fs.StringVar(&o.host, "host", "127.0.0.1", "address to listen on")
	fs.IntVar(&o.port, "port", port, "port to listen on")
	fs.IntVar(&o.window, "window", defaultDashboardWindow, "runs the statistics cover by default")
	fs.BoolVar(&o.once, "once", false, "scan once, print a summary and exit")
	if err := fs.Parse(args); err != nil {
		return dashboardOptions{}, err
	}
	if o.window < 1 {
		o.window = 1
	}
	return o, nil
}

// runDashboard serves the commit queue dashboard, or with --once prints its
// summary. Every scan stores newly finished runs in <state>/history.jsonl;
// the first one backfills every log on disk.
func runDashboard(args []string, slotsFlag int, out, errOut io.Writer) int {
	o, err := parseDashboardFlags(args, errOut)
	if err != nil {
		return 2
	}
	q, _, _, err := openQueue(".", slotsFlag)
	if err != nil {
		say(errOut, "%v\n", err)
		return 1
	}
	history, err := queuedash.OpenHistory(filepath.Join(q.Dir(), "history.jsonl"))
	if err != nil {
		say(errOut, "%v\n", err)
		return 1
	}
	dash := queuedash.NewDashboard(queuedash.NewCollector(q, history, nil), history, q.SlotCount(), o.window)
	state, ingested, err := dash.State(o.window)
	if err != nil {
		say(errOut, "%v\n", err)
		return 1
	}
	if o.once {
		say(out, "%s", queuedash.FormatSummary(state, ingested))
		return 0
	}
	addr := net.JoinHostPort(o.host, strconv.Itoa(o.port))
	say(out, "Commit queue dashboard on http://%s (state: %s, %d runs backfilled)\n", addr, q.Dir(), ingested)
	server := &http.Server{Addr: addr, Handler: dash.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		say(errOut, "%v\n", err)
		return 1
	}
	return 0
}
