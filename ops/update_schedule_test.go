package ops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// pint/pstr build explicit-presence pointers for the ops input structs.
func pint(v int) *int       { return &v }
func pstr(v string) *string { return &v }

// stubScheduleEditPUT serves GET /posts/schedules/{id}/edit with fixture and
// captures the PUT body on /posts/schedules/{id}.
func stubScheduleEditPUT(t *testing.T, id int, editFixture string, putBody *[]byte, putReceived *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/"+strconv.Itoa(id)+"/edit":
			w.Write([]byte(editFixture))
		case r.Method == http.MethodPut && r.URL.Path == "/posts/schedules/"+strconv.Itoa(id):
			*putReceived = true
			body, _ := io.ReadAll(r.Body)
			*putBody = body
			w.Write([]byte(`{"schedules":[{"id":` + strconv.Itoa(id) + `}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

const updateScheduleEditFixture = `{
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

func newTestClient(t *testing.T, baseURL string) *hooppy.Client {
	t.Helper()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: baseURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestUpdateSchedule_NoFieldsRefused: an input with every pointer nil is a
// no-op and must be refused locally — before any HTTP request. The stub
// FAILS the test if a request arrives.
//
// RED-on-revert: remove the no-fields check → UpdateScheduleFromEdit's own
// empty-overrides refusal fires instead, and the error no longer names the
// fields (and a future call path could send the PUT).
func TestUpdateSchedule_NoFieldsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — a no-op update must be refused locally", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	_, err := UpdateSchedule(context.Background(), newTestClient(t, srv.URL), 57793, UpdateScheduleInput{})
	if err == nil {
		t.Fatal("UpdateSchedule with no fields set returned nil error — a no-op update must be refused")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("error type = %T, want *ValidationError (input validation must be distinguishable from API errors)", err)
	}
	for _, name := range []string{"--name", "--state", "--delete-after-days", "--delete-after-hours"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("no-fields error must name %s, got: %s", name, err.Error())
		}
	}
}

// TestUpdateSchedule_StateValidation is the F4 guard: state 0 must be
// refused with a clear error naming the real values, not silently ignored
// (the 2026-11-03 audit F-1 defect: the MCP schema documented "0=paused"
// while the handler treated 0 as unset — a paused-looking update that kept
// the schedule active).
//
// Mutation: remove the state range check → state=0 flows into the overrides
// and the refusal assertions fail.
func TestUpdateSchedule_StateValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   int
		wantErr bool
	}{
		{"state 0 refused", 0, true},
		{"state 4 refused", 4, true},
		{"state -1 refused", -1, true},
		{"state 1 accepted", 1, false},
		{"state 2 accepted", 2, false},
		{"state 3 accepted", 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var putBody []byte
			var putReceived bool
			srv := stubScheduleEditPUT(t, 57793, updateScheduleEditFixture, &putBody, &putReceived)
			defer srv.Close()

			_, err := UpdateSchedule(context.Background(), newTestClient(t, srv.URL), 57793, UpdateScheduleInput{State: pint(tc.state)})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("state=%d returned nil error — out-of-range state must be refused", tc.state)
				}
				if putReceived {
					t.Errorf("state=%d issued a PUT — validation must fire before the network", tc.state)
				}
				if !strings.Contains(err.Error(), "state") {
					t.Errorf("error must name the state field, got: %s", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("state=%d must be accepted, got: %v", tc.state, err)
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(putBody, &body); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			var got float64
			if err := json.Unmarshal(body["state"], &got); err != nil || got != float64(tc.state) {
				t.Errorf("PUT body state = %s, want %d", string(body["state"]), tc.state)
			}
		})
	}
}

// TestUpdateSchedule_DeleteAfterValidation: day >= 0 and hour in 0..23 are
// enforced locally. Zero is a REAL value for both (day=0 disables
// auto-delete; hour=0 is midnight) and must be accepted — explicit presence
// means "set to zero", unlike the old int-sentinel input where 0 meant unset.
func TestUpdateSchedule_DeleteAfterValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      UpdateScheduleInput
		wantErr bool
	}{
		{"day -1 refused", UpdateScheduleInput{DeletePostsDay: pint(-1)}, true},
		{"hour 24 refused", UpdateScheduleInput{DeletePostsHour: pint(24)}, true},
		{"hour -1 refused", UpdateScheduleInput{DeletePostsHour: pint(-1)}, true},
		{"day 0 accepted", UpdateScheduleInput{DeletePostsDay: pint(0)}, false},
		{"hour 0 accepted", UpdateScheduleInput{DeletePostsHour: pint(0)}, false},
		{"hour 23 accepted", UpdateScheduleInput{DeletePostsHour: pint(23)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var putBody []byte
			var putReceived bool
			srv := stubScheduleEditPUT(t, 57793, updateScheduleEditFixture, &putBody, &putReceived)
			defer srv.Close()

			_, err := UpdateSchedule(context.Background(), newTestClient(t, srv.URL), 57793, tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("out-of-range delete-after value returned nil error")
				}
				if putReceived {
					t.Error("invalid input issued a PUT — validation must fire before the network")
				}
				return
			}
			if err != nil {
				t.Fatalf("valid input refused: %v", err)
			}
			if !putReceived {
				t.Fatal("valid input did not reach the PUT — the op must route through UpdateScheduleFromEdit")
			}
		})
	}
}

// TestUpdateSchedule_DeletePostsDay_PreservesAllFields is the F1 journey at
// the op layer: delete_posts_day=30 reaches the PUT body through
// UpdateScheduleFromEdit's read-modify-write, and every other schedule
// field is preserved — including delete_posts_hour=5, which is NOT sent
// because its pointer is nil (a key travels only when explicitly given).
//
// Mutation: drop the DeletePostsDay override → the delete_posts_day=30
// assertion fails. Always sending delete_posts_hour → the hour=5-preserved
// assertion fails.
func TestUpdateSchedule_DeletePostsDay_PreservesAllFields(t *testing.T) {
	var putBody []byte
	var putReceived bool
	srv := stubScheduleEditPUT(t, 57793, updateScheduleEditFixture, &putBody, &putReceived)
	defer srv.Close()

	resp, err := UpdateSchedule(context.Background(), newTestClient(t, srv.URL), 57793, UpdateScheduleInput{DeletePostsDay: pint(30)})
	if err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	if resp == nil {
		t.Fatal("UpdateSchedule returned nil response")
	}
	if !putReceived {
		t.Fatal("PUT /posts/schedules/57793 was never issued")
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
	assertJSONNumber("delete_posts_hour", 5)
	assertJSONNumber("is_posts_repeated", 1)
	assertJSONNumber("posts_caption", 0)
	var name string
	if err := json.Unmarshal(body["name"], &name); err != nil || name != "Repeating" {
		t.Errorf("PUT body name = %s, want \"Repeating\"", string(body["name"]))
	}
}

// TestUpdateSchedule_AbsentFieldsPreserved pins explicit presence from the
// other direction: with only State given, every other field must keep its
// /edit value in the PUT body. A nil pointer leaking through as a zero-value
// override shows up here as name="" or delete_posts_hour=0 — the exact
// silent-reset class explicit presence exists to prevent.
func TestUpdateSchedule_AbsentFieldsPreserved(t *testing.T) {
	var putBody []byte
	var putReceived bool
	srv := stubScheduleEditPUT(t, 57793, updateScheduleEditFixture, &putBody, &putReceived)
	defer srv.Close()

	_, err := UpdateSchedule(context.Background(), newTestClient(t, srv.URL), 57793, UpdateScheduleInput{State: pint(2)})
	if err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("decode PUT body: %v", err)
	}
	var state float64
	if err := json.Unmarshal(body["state"], &state); err != nil || state != 2 {
		t.Errorf("PUT body state = %s, want 2 (the override was not applied)", string(body["state"]))
	}
	var name string
	if err := json.Unmarshal(body["name"], &name); err != nil || name != "Repeating" {
		t.Errorf("PUT body name = %s, want \"Repeating\" — a nil Name must not produce an override", string(body["name"]))
	}
	var hour float64
	if err := json.Unmarshal(body["delete_posts_hour"], &hour); err != nil || hour != 5 {
		t.Errorf("PUT body delete_posts_hour = %s, want 5 — a nil DeletePostsHour must not produce an override", string(body["delete_posts_hour"]))
	}
	var day float64
	if err := json.Unmarshal(body["delete_posts_day"], &day); err != nil || day != 0 {
		t.Errorf("PUT body delete_posts_day = %s, want 0 — a nil DeletePostsDay must not produce an override", string(body["delete_posts_day"]))
	}
}

// TestUpdateSchedule_ZeroIDRefused: id is the required input; zero means
// "not given" and must be refused before any request.
func TestUpdateSchedule_ZeroIDRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — id=0 must be refused locally", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	_, err := UpdateSchedule(context.Background(), newTestClient(t, srv.URL), 0, UpdateScheduleInput{Name: pstr("x")})
	if err == nil {
		t.Fatal("id=0 returned nil error")
	}
}
