package queuedash

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"time"
)

// maxRemoveBody caps the request body; the real one is a few dozen bytes.
const maxRemoveBody = 4 << 10

// removeRequest names the abandoned run to remove.
type removeRequest struct {
	RepoKey string `json:"repoKey"`
	Name    string `json:"name"`
}

// handleRemoveAbandoned answers POST /api/abandoned/remove with 204 once the
// run's log is gone. It accepts only a same-origin JSON POST: the server
// listens on loopback, but any page open in the browser can reach it, and
// a JSON content type forces a CORS preflight this server never approves.
func (d *Dashboard) handleRemoveAbandoned(w http.ResponseWriter, r *http.Request) {
	if status, msg := checkRemoveRequest(r); status != 0 {
		http.Error(w, msg, status)
		return
	}
	var req removeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRemoveBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "body must be {\"repoKey\", \"name\"}", http.StatusBadRequest)
		return
	}
	if err := d.removeAbandoned(req); err != nil {
		http.Error(w, err.Error(), removeErrorStatus(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkRemoveRequest returns a status and message for a request that is not
// a same-origin JSON POST, or 0 when it is one.
func checkRemoveRequest(r *http.Request) (int, string) {
	if r.Method != http.MethodPost {
		return http.StatusMethodNotAllowed, "POST only"
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return http.StatusUnsupportedMediaType, "Content-Type must be application/json"
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		return http.StatusForbidden, "cross-origin request refused"
	}
	return 0, ""
}

// removeAbandoned removes the run under the scan lock, then drops the
// cached scan so the next poll no longer lists it.
func (d *Dashboard) removeAbandoned(req removeRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.collector.RemoveAbandoned(req.RepoKey, req.Name, d.now()); err != nil {
		return err
	}
	d.lastAt = time.Time{}
	return nil
}

// removeErrorStatus maps a RemoveAbandoned error to an HTTP status.
func removeErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrInvalidRunName):
		return http.StatusBadRequest
	case errors.Is(err, ErrRunNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrNotAbandoned), errors.Is(err, ErrRunHoldsSlot):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
