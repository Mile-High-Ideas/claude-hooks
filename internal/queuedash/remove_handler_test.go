package queuedash

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

func TestDashboardRemovesAnAbandonedRun(t *testing.T) {
	f, c := newRemoveFixture(t)
	dash := NewDashboard(c, c.history, 2, 20)
	srv := httptest.NewServer(dash.Handler())
	defer srv.Close()

	before := dashState(t, srv.URL)
	if names := abandonedNames(before); names != "gone,stuck" {
		t.Fatalf("abandoned before = %q, want gone,stuck", names)
	}
	body := `{"repoKey":"` + commitqueue.RepoKey(testRepo) + `","name":"gone"}`
	if status := postRemove(t, srv.URL, "application/json", "", body); status != http.StatusNoContent {
		t.Fatalf("POST remove = %d, want 204", status)
	}
	if f.logExists("gone") {
		t.Error("gone.log still exists")
	}
	// The scan cache is dropped, so the very next poll no longer lists it.
	if names := abandonedNames(dashState(t, srv.URL)); names != "stuck" {
		t.Errorf("abandoned after = %q, want stuck", names)
	}
}

func TestDashboardRemoveRefusals(t *testing.T) {
	key := commitqueue.RepoKey(testRepo)
	good := `{"repoKey":"` + key + `","name":"gone"}`
	tests := []struct {
		name        string
		method      string
		contentType string
		origin      string
		body        string
		want        int
	}{
		{"GET", http.MethodGet, "application/json", "", good, http.StatusMethodNotAllowed},
		{"form post", http.MethodPost, "text/plain", "", good, http.StatusUnsupportedMediaType},
		{"cross-origin", http.MethodPost, "application/json", "https://evil.example", good, http.StatusForbidden},
		{"bad body", http.MethodPost, "application/json", "", `{"run":"gone"}`, http.StatusBadRequest},
		{"bad name", http.MethodPost, "application/json", "", `{"repoKey":"` + key + `","name":"../x"}`, http.StatusBadRequest},
		{"missing run", http.MethodPost, "application/json", "", `{"repoKey":"` + key + `","name":"nope"}`, http.StatusNotFound},
		{"finished run", http.MethodPost, "application/json", "", `{"repoKey":"` + key + `","name":"done"}`, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c := newRemoveFixture(t)
			srv := httptest.NewServer(NewDashboard(c, c.history, 2, 20).Handler())
			defer srv.Close()
			if status := sendRemove(t, tt.method, srv.URL, tt.contentType, tt.origin, tt.body); status != tt.want {
				t.Errorf("status = %d, want %d", status, tt.want)
			}
			if !f.logExists("gone") {
				t.Error("gone.log was removed by a refused request")
			}
		})
	}
}

func TestDashboardRemoveAcceptsItsOwnOrigin(t *testing.T) {
	_, c := newRemoveFixture(t)
	srv := httptest.NewServer(NewDashboard(c, c.history, 2, 20).Handler())
	defer srv.Close()
	body := `{"repoKey":"` + commitqueue.RepoKey(testRepo) + `","name":"gone"}`
	if status := postRemove(t, srv.URL, "application/json; charset=utf-8", srv.URL, body); status != http.StatusNoContent {
		t.Errorf("same-origin POST = %d, want 204", status)
	}
}

func dashState(t *testing.T, base string) DashboardState {
	t.Helper()
	var state DashboardState
	if err := json.Unmarshal([]byte(dashGet(t, base+"/api/state")), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	return state
}

func abandonedNames(state DashboardState) string {
	names := make([]string, 0, len(state.Abandoned))
	for _, r := range state.Abandoned {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return strings.Join(names, ",")
}

func postRemove(t *testing.T, base, contentType, origin, body string) int {
	t.Helper()
	return sendRemove(t, http.MethodPost, base, contentType, origin, body)
}

func sendRemove(t *testing.T, method, base, contentType, origin, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, base+"/api/abandoned/remove", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s remove: %v", method, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
