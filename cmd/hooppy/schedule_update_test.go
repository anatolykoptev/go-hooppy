package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
)

// pint/pstr build explicit-presence pointers for ops input structs.
func pint(v int) *int       { return &v }
func pstr(v string) *string { return &v }

// TestRunScheduleUpdate_NoOverridesRefused is the RED-on-revert test for the
// CLI `schedules update` no-op refusal. The MCP equivalent
// (TestUpdateScheduleTool_NoOverridesRefused) and the library helper
// (TestUpdateScheduleFromEdit_NoOverridesRefused) both have tests; the CLI
// path had none, so removing the refusal failed nothing. This test drives
// the extracted runScheduleUpdate core (the same core the cobra Run closure
// calls) and asserts a no-op call (no override flag given) is refused
// locally with exit 1 BEFORE any request reaches the server. The stub FAILS
// the test if any request arrives — that assertion IS the guard.
//
// RED-on-revert: remove the no-op refusal from runScheduleUpdate → a request
// reaches the stub → the stub's t.Errorf fires and the test fails.
func TestRunScheduleUpdate_NoOverridesRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — a no-op update (no override flag given) must be refused locally before any request", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runScheduleUpdate(context.Background(), c, &out, &errOut, 42, ops.UpdateScheduleInput{})
	if code == 0 {
		t.Fatalf("runScheduleUpdate exit code = 0, want 1 (a no-op update must be refused); stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "at least one of --name, --state, --delete-after-days or --delete-after-hours") {
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
	code := runScheduleUpdate(context.Background(), c, &out, &errOut, 42, ops.UpdateScheduleInput{Name: pstr("New Name")})
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

// scheduleEditDeleteAfterFixture is a repeating schedule's /edit response for
// the delete-after flags tests: is_posts_repeated=1, delete_posts_hour=5 and
// the structural keys UpdateScheduleFromEdit's recognisable-schedule guard
// requires (id, name, publication_how_type), plus a few of the ~50 unmodelled
// keys a full-state PUT must carry back.
const scheduleEditDeleteAfterFixture = `{
	"id": 57793,
	"name": "Repeating",
	"state": 1,
	"publication_how_type": 1,
	"is_posts_repeated": 1,
	"delete_posts_day": 0,
	"delete_posts_hour": 5,
	"posts_caption": 0,
	"times": [[{"hours":12,"minutes":25}],[],[],[],[],[],[]],
	"selected_pages_by_source_ids": {"1": [100]}
}`

// stubScheduleEditPUT serves GET /posts/schedules/{id}/edit with the given
// fixture and captures the PUT body on /posts/schedules/{id}.
func stubScheduleEditPUT(t *testing.T, id int, editFixture string, putBody *[]byte, putReceived *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/"+itoa(id)+"/edit":
			w.Write([]byte(editFixture))
		case r.Method == http.MethodPut && r.URL.Path == "/posts/schedules/"+itoa(id):
			*putReceived = true
			body, _ := io.ReadAll(r.Body)
			*putBody = body
			w.Write([]byte(`{"schedules":[{"id":` + itoa(id) + `}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// itoa is shared from crossposting_helpers_test.go.

// TestRunScheduleUpdate_DeleteAfterDays_PreservesAllFields is the F1/F2
// journey test: `schedules update --id 57793 --delete-after-days 30` on a
// repeating schedule must (a) send delete_posts_day=30 through
// UpdateScheduleFromEdit's read-modify-write so every other field is
// preserved byte-for-byte, and (b) leave delete_posts_hour at its existing
// value 5 — hours is only sent when --delete-after-hours was passed.
//
// F1 RED-on-revert: bypassing UpdateScheduleFromEdit (PUT only
// {delete_posts_day}) drops is_posts_repeated/name → assertions fail.
// F2 RED-on-revert: always sending delete_posts_hour=0 when the flag was not
// given → the hour assertion fails.
func TestRunScheduleUpdate_DeleteAfterDays_PreservesAllFields(t *testing.T) {
	var putBody []byte
	var putReceived bool
	srv := stubScheduleEditPUT(t, 57793, scheduleEditDeleteAfterFixture, &putBody, &putReceived)
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runScheduleUpdate(context.Background(), c, &out, &errOut, 57793, ops.UpdateScheduleInput{DeletePostsDay: pint(30)})
	if code != 0 {
		t.Fatalf("runScheduleUpdate exit %d; stderr=%s", code, errOut.String())
	}
	if !putReceived {
		t.Fatal("PUT /posts/schedules/57793 was never issued — the delete-after flag must route through UpdateScheduleFromEdit")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("decode PUT body: %v", err)
	}
	assertJSONNumber := func(key string, want float64) {
		t.Helper()
		raw, ok := body[key]
		if !ok {
			t.Errorf("PUT body is missing %q — the read-modify-write must carry every /edit key back", key)
			return
		}
		var got float64
		if err := json.Unmarshal(raw, &got); err != nil || got != want {
			t.Errorf("PUT body %s = %s, want %v", key, string(raw), want)
		}
	}
	assertJSONNumber("delete_posts_day", 30)
	assertJSONNumber("is_posts_repeated", 1)
	assertJSONNumber("delete_posts_hour", 5)
	assertJSONNumber("posts_caption", 0)
	var name string
	if err := json.Unmarshal(body["name"], &name); err != nil || name != "Repeating" {
		t.Errorf("PUT body name = %s, want \"Repeating\" — the update must preserve the schedule's name", string(body["name"]))
	}
}

// TestRunScheduleUpdate_DeleteAfterHours_SetsHour verifies --delete-after-hours
// sends delete_posts_hour while delete_posts_day keeps its existing value.
func TestRunScheduleUpdate_DeleteAfterHours_SetsHour(t *testing.T) {
	var putBody []byte
	var putReceived bool
	srv := stubScheduleEditPUT(t, 57793, scheduleEditDeleteAfterFixture, &putBody, &putReceived)
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runScheduleUpdate(context.Background(), c, &out, &errOut, 57793, ops.UpdateScheduleInput{DeletePostsHour: pint(9)})
	if code != 0 {
		t.Fatalf("runScheduleUpdate exit %d; stderr=%s", code, errOut.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("decode PUT body: %v", err)
	}
	var hour, day float64
	if err := json.Unmarshal(body["delete_posts_hour"], &hour); err != nil || hour != 9 {
		t.Errorf("PUT body delete_posts_hour = %s, want 9", string(body["delete_posts_hour"]))
	}
	if err := json.Unmarshal(body["delete_posts_day"], &day); err != nil || day != 0 {
		t.Errorf("PUT body delete_posts_day = %s, want 0 (untouched — --delete-after-days was not passed)", string(body["delete_posts_day"]))
	}
}

// TestRunScheduleUpdate_EmptyEditResponse_NoPUT is the F2 journey test for
// issue #158: `schedules update 57793 --delete-after-days 30` when GET
// /posts/schedules/57793/edit answers a 200 {} (or a full-looking state for a
// DIFFERENT schedule) must NOT send the PUT — a read-modify-write body built
// on it would wipe or overwrite the schedule. The command refuses with exit 1.
//
// F2 RED-on-revert (mutation: projects.go — drop the id check in
// UpdateScheduleFromEdit): the different_id case issues the PUT → putCalls=1
// fails the "want 0" assertion. (The {} case is additionally covered by the
// older structural-key guard.)
func TestRunScheduleUpdate_EmptyEditResponse_NoPUT(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty_object", `{}`},
		{"different_id", `{"id":43,"name":"Other","publication_how_type":1,"times":[[{"hours":1,"minutes":2}]]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/57793/edit":
					w.Write([]byte(tc.body))
				case r.Method == http.MethodPut && r.URL.Path == "/posts/schedules/57793":
					putCalls++
					w.Write([]byte(`{"schedules":[{}]}`))
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
			code := runScheduleUpdate(context.Background(), c, &out, &errOut, 57793, ops.UpdateScheduleInput{DeletePostsDay: pint(30)})
			if code != 1 {
				t.Fatalf("runScheduleUpdate exit %d, want 1 — a bad /edit state must refuse before the PUT; stdout=%s", code, out.String())
			}
			if putCalls != 0 {
				t.Errorf("PUT /posts/schedules/57793 issued %d time(s), want 0 — a read-modify-write on a bad /edit state would wipe the schedule", putCalls)
			}
		})
	}
}

// TestRunScheduleUpdate_DeleteAfterValidationRefused checks the flag-range
// guards fire locally, before any request: days >= 0 and 0 <= hours <= 23.
// The stub FAILS the test if any request arrives — the refusal must precede
// the network. --delete-after-days 0 is valid (disables auto-delete) and
// must NOT be refused.
func TestRunScheduleUpdate_DeleteAfterValidationRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      ops.UpdateScheduleInput
		wantMsg string
	}{
		{"negative days", ops.UpdateScheduleInput{DeletePostsDay: pint(-1)}, "--delete-after-days"},
		{"hours 24", ops.UpdateScheduleInput{DeletePostsHour: pint(24)}, "--delete-after-hours"},
		{"negative hours", ops.UpdateScheduleInput{DeletePostsHour: pint(-1)}, "--delete-after-hours"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s — an out-of-range delete-after flag must be refused locally", r.Method, r.URL.Path)
			}))
			defer srv.Close()
			c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			var out, errOut bytes.Buffer
			code := runScheduleUpdate(context.Background(), c, &out, &errOut, 42, tc.in)
			if code == 0 {
				t.Fatalf("exit code = 0, want 1; stdout=%s stderr=%s", out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.wantMsg) {
				t.Errorf("stderr = %q, want it to name %s", errOut.String(), tc.wantMsg)
			}
		})
	}
}
