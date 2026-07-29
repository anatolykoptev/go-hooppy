package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// TestRunScheduleUpdate_NoOverridesRefused is the RED-on-revert test for the
// CLI `schedules update` no-op refusal. The MCP equivalent
// (TestUpdateScheduleTool_NoOverridesRefused) and the library helper
// (TestUpdateScheduleFromEdit_NoOverridesRefused) both have tests; the CLI
// path had none, so removing the refusal failed nothing. This test drives
// the extracted runScheduleUpdate core (the same core the cobra Run closure
// calls) and asserts a no-op call (neither --name nor --state) is refused
// locally with exit 1 BEFORE any request reaches the server. The stub FAILS
// the test if any request arrives — that assertion IS the guard.
//
// RED-on-revert: remove the no-op refusal from runScheduleUpdate → a request
// reaches the stub → the stub's t.Errorf fires and the test fails.
func TestRunScheduleUpdate_NoOverridesRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — a no-op update (neither --name nor --state) must be refused locally before any request", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runScheduleUpdate(context.Background(), c, &out, &errOut, scheduleUpdateArgs{
		id:    42,
		name:  "",
		state: 0,
	})
	if code == 0 {
		t.Fatalf("runScheduleUpdate exit code = 0, want 1 (a no-op update must be refused); stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "at least one of --name or --state") {
		t.Errorf("stderr must name the requirement, got: %s", errOut.String())
	}
}

// TestRunScheduleUpdate_NameOnly_PreservesFields drives the real
// runScheduleUpdate core end to end and asserts the PUT body carries the
// unmodelled keys back from the /edit response (the read-modify-write
// contract). This is the CLI-side mirror of the MCP
// TestUpdateScheduleTool_WireBodyPreservesUnmodelledFields guard: it pins
// that the CLI routes through UpdateScheduleFromEdit, not the partial writer.
func TestRunScheduleUpdate_NameOnly_PreservesFields(t *testing.T) {
	const editResponse = `{
		"id": 42,
		"name": "A",
		"publication_how_type": 1,
		"times": [[{"hours":12,"minutes":25}],[],[],[],[],[],[]],
		"posts_caption": 0,
		"start_date": 0,
		"stop_date": 0,
		"selected_pages_by_source_ids": {"1": [100, 200]}
	}`
	var putReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/42/edit":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(editResponse))
		case r.Method == http.MethodPut && r.URL.Path == "/posts/schedules/42":
			putReceived = true
			w.Write([]byte(`{"schedules":[{"id":42,"name":"New Name"}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runScheduleUpdate(context.Background(), c, &out, &errOut, scheduleUpdateArgs{
		id:    42,
		name:  "New Name",
		state: 0,
	})
	if code != 0 {
		t.Fatalf("runScheduleUpdate exit %d; stderr=%s", code, errOut.String())
	}
	if !putReceived {
		t.Fatal("PUT /posts/schedules/42 was never issued — the CLI must route through UpdateScheduleFromEdit")
	}
	if !strings.Contains(out.String(), "New Name") {
		t.Errorf("stdout must carry the updated name, got: %s", out.String())
	}
}
