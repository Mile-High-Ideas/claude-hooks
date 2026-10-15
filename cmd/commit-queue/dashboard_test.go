package main

import (
	"bytes"
	"testing"
)

func TestParseDashboardFlags(t *testing.T) {
	isolate(t)
	var errOut bytes.Buffer

	o, err := parseDashboardFlags(nil, &errOut)
	if err != nil || o.port != 4180 || o.window != 50 || o.host != "127.0.0.1" || o.once {
		t.Errorf("defaults = %+v, %v", o, err)
	}

	t.Setenv("COMMIT_QUEUE_DASHBOARD_PORT", "4999")
	o, err = parseDashboardFlags([]string{"--window", "0", "--once"}, &errOut)
	if err != nil || o.port != 4999 || o.window != 1 || !o.once {
		t.Errorf("env port, clamped window, once = %+v, %v", o, err)
	}
	o, err = parseDashboardFlags([]string{"--port", "5000"}, &errOut)
	if err != nil || o.port != 5000 {
		t.Errorf("--port beats the env: %+v, %v", o, err)
	}

	t.Setenv("COMMIT_QUEUE_DASHBOARD_PORT", "http")
	if _, err := parseDashboardFlags(nil, &errOut); err == nil {
		t.Error("accepted COMMIT_QUEUE_DASHBOARD_PORT=http")
	}
}
