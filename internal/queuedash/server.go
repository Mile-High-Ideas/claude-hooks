package queuedash

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

// RecentLimit is how many finished runs the results list shows.
const RecentLimit = 25

// rescanEvery stops a page polled by several tabs from re-reading the logs
// more than once a second.
const rescanEvery = time.Second

// Dashboard answers the page and its polls from one collector.
type Dashboard struct {
	collector     *Collector
	history       *History
	slotCount     int
	defaultWindow int
	now           func() time.Time

	mu       sync.Mutex
	lastAt   time.Time
	lastScan Scan
}

// NewDashboard builds a dashboard over collector and history. window is
// how many runs the statistics cover unless the page asks otherwise.
func NewDashboard(collector *Collector, history *History, slotCount, window int) *Dashboard {
	return &Dashboard{
		collector:     collector,
		history:       history,
		slotCount:     slotCount,
		defaultWindow: window,
		now:           time.Now,
	}
}

// State scans the queue (at most once a second) and assembles the page's
// JSON over the newest window runs. It also returns how many runs that
// scan added to the history.
func (d *Dashboard) State(window int) (DashboardState, int, error) {
	scan, err := d.scan()
	if err != nil {
		return DashboardState{}, 0, err
	}
	now := d.now()
	state := BuildDashboardState(StateInput{
		Runs:          scan.Runs,
		Slots:         scan.Slots,
		Tickets:       scan.Tickets,
		Recent:        d.history.RecentRuns(RecentLimit),
		WindowRuns:    d.history.RecentSlotRuns(window),
		NowMs:         now.UnixMilli(),
		SlotCount:     d.slotCount,
		HistoryCounts: d.history.Counts(),
	})
	return state, scan.Ingested, nil
}

func (d *Dashboard) scan() (Scan, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if !d.lastAt.IsZero() && now.Sub(d.lastAt) < rescanEvery {
		return d.lastScan, nil
	}
	scan, err := d.collector.Collect(now)
	if err != nil {
		return Scan{}, err
	}
	d.lastAt, d.lastScan = now, scan
	return scan, nil
}

// Handler serves the page at / and its JSON at /api/state?window=N.
func (d *Dashboard) Handler() http.Handler {
	page := withDefaultWindow(dashboardHTML, d.defaultWindow)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		state, _, err := d.State(clampWindow(r.URL.Query().Get("window"), d.defaultWindow))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(state)
	})
	return mux
}

// clampWindow reads the page's window, falling back to the default and
// keeping it within 1..1000.
func clampWindow(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		n = fallback
	}
	return min(max(n, 1), 1000)
}

// withDefaultWindow stamps the served window on the page's run-window
// select as data-default, which the page starts from before any choice the
// browser saved. Without it --window would never take effect.
func withDefaultWindow(html string, window int) string {
	return strings.Replace(html, `<select id="window">`, fmt.Sprintf(`<select id="window" data-default="%d">`, window), 1)
}
