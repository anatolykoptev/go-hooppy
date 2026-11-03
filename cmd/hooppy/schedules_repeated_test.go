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

// stubSchedulesListCountingEdits serves the schedules list (one page) and
// counts every GET /posts/schedules/{id}/edit read, answering each with a
// 200 {} — the exact response class from issue #158: a valid empty object
// that decodes is_posts_repeated=0 and silently drops the schedule when the
// filter reads /edit per row.
func stubSchedulesListCountingEdits(t *testing.T, listBody string, editCalls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules":
			w.Write([]byte(listBody))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts/schedules/") && strings.HasSuffix(r.URL.Path, "/edit"):
			*editCalls++
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestRunListSchedulesRepeated_FiltersOnListRows is the F1 journey guard for
// issue #158: `schedules list --repeated` must filter on the LIST response,
// which already carries is_posts_repeated, delete_posts_day and
// delete_posts_hour per row (see testdata/live/schedules.json). The stub
// answers EVERY /edit read with a 200 {} and counts the reads — under the
// old /edit-based filter the repeated schedule would silently drop out.
// The command must emit the repeated row (with its delete_posts_* values)
// and issue ZERO /edit requests.
//
// F1 RED-on-revert (mutation: cmd/hooppy/list.go — restore the per-schedule
// GetScheduleEdit filter): the {} edit body decodes is_posts_repeated=0 →
// the repeated schedule is dropped → len(rows)=0 fails "want 1".
func TestRunListSchedulesRepeated_FiltersOnListRows(t *testing.T) {
	listBody := `{"list":[
		{"id":1,"name":"R","state":1,"is_posts_repeated":1,"delete_posts_day":30,"delete_posts_hour":5},
		{"id":2,"name":"N","state":1,"is_posts_repeated":0,"delete_posts_day":0,"delete_posts_hour":0}
	],"total_rows":2,"is_has_more":false,"rows_limit":20}`
	editCalls := 0
	srv := stubSchedulesListCountingEdits(t, listBody, &editCalls)
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runListSchedulesRepeated(context.Background(), c, &out, &errOut)
	if code != 0 {
		t.Fatalf("runListSchedulesRepeated exit %d, want 0; stderr=%s", code, errOut.String())
	}
	if editCalls != 0 {
		t.Errorf("GET /posts/schedules/{id}/edit issued %d time(s), want 0 — the list response already carries is_posts_repeated/delete_posts_*; each read costs one request per schedule and reintroduces the issue-#158 empty-{} hole", editCalls)
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
		t.Fatalf("stdout has %d rows, want 1 — only the is_posts_repeated=1 list row may be emitted (rows=%v)", len(rows), rows)
	}
	r := rows[0]
	if r.ID != 1 || r.IsPostsRepeated != 1 || r.DeletePostsDay != 30 || r.DeletePostsHour != 5 {
		t.Errorf("row = %+v, want id=1 is_posts_repeated=1 delete_posts_day=30 delete_posts_hour=5 — the values must come from the list row, not an /edit read", r)
	}
}

// TestRunListSchedulesRepeated_NoneRepeating guards the filter direction:
// a list where every row carries is_posts_repeated=0 must yield an EMPTY
// array (exit 0), not the whole list.
//
// RED-on-revert: invert or drop the is_posts_repeated==1 filter → all rows
// are emitted and the stdout assertion fails.
func TestRunListSchedulesRepeated_NoneRepeating(t *testing.T) {
	listBody := `{"list":[
		{"id":1,"name":"A","state":1,"is_posts_repeated":0,"delete_posts_day":0,"delete_posts_hour":0},
		{"id":2,"name":"B","state":1,"is_posts_repeated":0,"delete_posts_day":0,"delete_posts_hour":0}
	],"total_rows":2,"is_has_more":false,"rows_limit":20}`
	editCalls := 0
	srv := stubSchedulesListCountingEdits(t, listBody, &editCalls)
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runListSchedulesRepeated(context.Background(), c, &out, &errOut)
	if code != 0 {
		t.Fatalf("runListSchedulesRepeated exit %d, want 0; stderr=%s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("stdout = %q, want \"[]\" — no list row is repeating", out.String())
	}
}

// TestRunListSchedulesRepeated_ListErrorFailsLoud keeps the loud error path:
// a failed list walk must exit 1 naming the failure (the /edit failure mode
// is gone by construction — no /edit reads are issued at all).
func TestRunListSchedulesRepeated_ListErrorFailsLoud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message":"boom","status":500}`))
	}))
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runListSchedulesRepeated(context.Background(), c, &out, &errOut)
	if code == 0 {
		t.Fatalf("exit code = 0, want 1 — a failed list walk must not silently produce a report; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "error") {
		t.Errorf("stderr = %q, want it to name the failed list walk", errOut.String())
	}
}
