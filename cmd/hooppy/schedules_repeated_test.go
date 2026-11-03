package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// stubSchedulesListAndEdits serves the schedules list (one page) plus the
// per-schedule /edit responses keyed by path — the two-endpoint shape
// `schedules list --repeated` walks.
func stubSchedulesListAndEdits(t *testing.T, listBody string, edits map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules":
			w.Write([]byte(listBody))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts/schedules/") && strings.HasSuffix(r.URL.Path, "/edit"):
			if body, ok := edits[r.URL.Path]; ok {
				w.Write([]byte(body))
				return
			}
			t.Errorf("unexpected /edit read: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// repeatedEditFixture builds an /edit response body for one schedule.
func repeatedEditFixture(id, repeated, delDay, delHour int, name string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"id":                   id,
		"name":                 name,
		"state":                1,
		"publication_how_type": 1,
		"is_posts_repeated":    repeated,
		"delete_posts_day":     delDay,
		"delete_posts_hour":    delHour,
		"times":                [][]interface{}{},
		"posts_caption":        0,
	})
	return string(b)
}

// TestRunListSchedulesRepeated_OnlyRepeating is the F3 journey test:
// `schedules list --repeated` must emit ONLY the schedules whose /edit state
// carries is_posts_repeated=1, with their delete_posts_* values. The list
// response does not carry the flag, so the command does one /edit read per
// schedule.
//
// F3 RED-on-revert: drop the is_posts_repeated == 1 filter → both fixture
// schedules appear and the len/assertion fails.
func TestRunListSchedulesRepeated_OnlyRepeating(t *testing.T) {
	listBody := `{"list":[{"id":1,"name":"R"},{"id":2,"name":"N"}],"total_rows":2,"is_has_more":false,"rows_limit":20}`
	srv := stubSchedulesListAndEdits(t, listBody, map[string]string{
		"/posts/schedules/1/edit": repeatedEditFixture(1, 1, 30, 5, "R"),
		"/posts/schedules/2/edit": repeatedEditFixture(2, 0, 0, 0, "N"),
	})
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runListSchedulesRepeated(context.Background(), c, &out, &errOut)
	if code != 0 {
		t.Fatalf("runListSchedulesRepeated exit %d; stderr=%s", code, errOut.String())
	}
	var rows []struct {
		ID              int    `json:"id"`
		Name            string `json:"name"`
		State           int    `json:"state"`
		IsPostsRepeated int    `json:"is_posts_repeated"`
		DeletePostsDay  int    `json:"delete_posts_day"`
		DeletePostsHour int    `json:"delete_posts_hour"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("stdout is not a JSON array of schedule summaries: %v (stdout=%q)", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("stdout has %d rows, want 1 — only the is_posts_repeated=1 schedule may be listed (rows=%v)", len(rows), rows)
	}
	r := rows[0]
	if r.ID != 1 || r.IsPostsRepeated != 1 || r.DeletePostsDay != 30 || r.DeletePostsHour != 5 {
		t.Errorf("row = %+v, want id=1 is_posts_repeated=1 delete_posts_day=30 delete_posts_hour=5", r)
	}
}

// TestRunListSchedulesRepeated_EditReadFailsLoud guards the silent-drop
// failure: a schedule whose /edit read fails must NOT be silently excluded
// from the report — the command exits 1 and names the failure instead of
// printing a list that quietly lost rows.
//
// RED-on-revert: skip-on-error (append nothing, continue) → exit 0 and this
// test fails.
func TestRunListSchedulesRepeated_EditReadFailsLoud(t *testing.T) {
	listBody := `{"list":[{"id":1,"name":"R"},{"id":2,"name":"N"}],"total_rows":2,"is_has_more":false,"rows_limit":20}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules":
			w.Write([]byte(listBody))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/1/edit":
			w.Write([]byte(repeatedEditFixture(1, 1, 30, 5, "R")))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/2/edit":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"boom","status":500}`))
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
	code := runListSchedulesRepeated(context.Background(), c, &out, &errOut)
	if code == 0 {
		t.Fatalf("exit code = 0, want 1 — a failed /edit read must not silently drop the schedule from the report; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "error") {
		t.Errorf("stderr = %q, want it to name the failed /edit read", errOut.String())
	}
}
