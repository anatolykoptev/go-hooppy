package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// schedulePostsBody is a GET /posts/schedules/{id}/posts fixture: a
// schedule with 3 posts across 2 days. The keys are dd.mm.yyyy; the
// fixture is deliberately OUT OF lexicographic order (15.01.2027 before
// 31.01.2027 is fine, but 01.02.2027 must sort AFTER 31.01.2027
// chronologically, not before it as a raw-string sort would do).
const schedulePostsBody = `{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":101,"text":"a"},{"id":102,"text":"b"}]},"31.01.2027":{"day_name":"Вс","day_date":"31 Января","posts":[{"id":103,"text":"c"}]},"01.02.2027":{"day_name":"Пн","day_date":"1 Февраля","posts":[{"id":104,"text":"d"}]}},"total_rows":4,"rows_limit":1000,"is_has_more":false}`

// TestListSchedulePosts_IssuesExactlyOneRequest is THE one-request-contract
// guard for issue #106: the endpoint returns the whole calendar in one
// envelope, and the command MUST NOT page. A regression that adds a paged
// walk (issuing a second request when is_has_more is true, or walking
// offsets) fails here — the request count must be exactly 1.
//
// RED-on-revert: add a paged walk that issues a follow-up request when
// is_has_more is true (or any other condition) and calls==2 fails.
func TestListSchedulePosts_IssuesExactlyOneRequest(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/posts/schedules/55576/posts" {
			t.Errorf("GET /posts/schedules/55576/posts, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(schedulePostsBody))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{ScheduleID: 55576})
	if err != nil {
		t.Fatalf("ListSchedulePosts: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ListSchedulePosts issued %d requests, want exactly 1 — issue #106 forbids a paged walk; the endpoint returns the whole calendar in one envelope", got)
	}
	if resp.TotalRows != 4 {
		t.Errorf("TotalRows = %d, want 4", resp.TotalRows)
	}
	if len(resp.PostsByDays) != 3 {
		t.Errorf("len(PostsByDays) = %d, want 3", len(resp.PostsByDays))
	}
}

// TestListSchedulePosts_ZeroScheduleID_RefusesRequest is the input guard.
func TestListSchedulePosts_ZeroScheduleID_RefusesRequest(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{}); err == nil {
		t.Fatal("ListSchedulePosts with scheduleID=0: expected an error, got nil")
	}
	if reached {
		t.Fatal("ListSchedulePosts with scheduleID=0: a request was issued before the guard errored")
	}
}

// TestListSchedulePosts_EmptyQueue verifies an empty schedule (no
// posts_by_days keys) decodes cleanly to a non-nil map and TotalRows=0.
func TestListSchedulePosts_EmptyQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posts_by_days":[],"total_rows":0,"rows_limit":1000,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{ScheduleID: 55576})
	if err != nil {
		t.Fatalf("ListSchedulePosts: %v", err)
	}
	if resp.TotalRows != 0 {
		t.Errorf("TotalRows = %d, want 0", resp.TotalRows)
	}
	if resp.PostsByDays == nil {
		t.Fatal("PostsByDays = nil, want non-nil empty map (the server returned {})")
	}
	if len(resp.PostsByDays) != 0 {
		t.Errorf("len(PostsByDays) = %d, want 0", len(resp.PostsByDays))
	}
}

// TestListSchedulePosts_DecodesPostFields verifies the Post entries inside
// posts_by_days decode their fields (id, text) — the map value type is
// []Post, and a regression that changed it to []json.RawMessage or
// map[string]interface{} would lose typed access.
func TestListSchedulePosts_DecodesPostFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(schedulePostsBody))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{ScheduleID: 55576})
	if err != nil {
		t.Fatalf("ListSchedulePosts: %v", err)
	}
	day := resp.PostsByDays["15.01.2027"]
	if day.DayName == "" || day.DayDate == "" {
		t.Errorf("PostsByDays[\"15.01.2027\"] day labels = %q/%q, want both non-empty — the day value is an OBJECT carrying the server's display strings, not a bare post array",
			day.DayName, day.DayDate)
	}
	posts := day.Posts
	if len(posts) != 2 {
		t.Fatalf("PostsByDays[\"15.01.2027\"] = %d posts, want 2", len(posts))
	}
	if posts[0].ID != 101 {
		t.Errorf("PostsByDays[\"15.01.2027\"][0].ID = %d, want 101", posts[0].ID)
	}
	if posts[0].Text != "a" {
		t.Errorf("PostsByDays[\"15.01.2027\"][0].Text = %q, want \"a\"", posts[0].Text)
	}
}

// TestListSchedulePosts_MalformedDateFrom_RefusesRequest is the client-side
// date-validation guard (item C): the server answers HTTP 500 for an ISO date
// (2026-09-01) or garbage, not a silent ignore — the client validates first
// so the error names the expected format. Same shape as validateDDMMYYYY on
// StartParsing (issue #61); the validator is shared, not duplicated.
//
// RED-on-revert: drop the validateDDMMYYYY call and the malformed date
// reaches the server (reached=true) — the reached assertion fails.
func TestListSchedulePosts_MalformedDateFrom_RefusesRequest(t *testing.T) {
	for _, bad := range []string{"2026-09-01", "not-a-date", "01.09", "32.01.2027"} {
		reached := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			w.Write([]byte(`{}`))
		}))
		c := newTestClient(t, srv)
		_, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{
			ScheduleID: 55576,
			DateFrom:   bad,
		})
		if err == nil {
			t.Errorf("ListSchedulePosts with date_from=%q: expected an error, got nil — the server answers 500 for a malformed date; the client must validate first", bad)
		}
		if reached {
			t.Errorf("ListSchedulePosts with date_from=%q: a request was issued before the guard errored — the refusal MUST happen before any request", bad)
		}
		srv.Close()
	}
}

// TestListSchedulePosts_MalformedDateTo_RefusesRequest is the date_to side
// of the client-side date-validation guard (item C).
func TestListSchedulePosts_MalformedDateTo_RefusesRequest(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{
		ScheduleID: 55576,
		DateTo:     "2026-09-01",
	})
	if err == nil {
		t.Fatal("ListSchedulePosts with date_to=\"2026-09-01\": expected an error, got nil — the server answers 500 for an ISO date; the client must validate first")
	}
	if reached {
		t.Fatal("ListSchedulePosts with date_to=\"2026-09-01\": a request was issued before the guard errored")
	}
}

// TestListSchedulePosts_PagePassedToEndpoint verifies the page field is
// forwarded as the page query param to the endpoint — the only lever that
// walks a truncation without guessing dates (item B).
//
// RED-on-revert: drop the page param from the filter or the request and
// gotPage != "2" — the assertion fails.
func TestListSchedulePosts_PagePassedToEndpoint(t *testing.T) {
	var gotPage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPage = r.URL.Query().Get("page")
		w.Write([]byte(`{"posts_by_days":[],"total_rows":0,"rows_limit":200,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{
		ScheduleID: 55576,
		Page:       2,
	}); err != nil {
		t.Fatalf("ListSchedulePosts: %v", err)
	}
	if gotPage != "2" {
		t.Errorf("page query param = %q, want \"2\" — the page field must be forwarded to the endpoint", gotPage)
	}
}

// TestListSchedulePosts_EmptyCalendarArrivesAsAJSONList pins the PHP quirk.
// json_encode cannot tell an empty associative array from an empty list, so an
// empty calendar comes back as `[]` while a populated one comes back as `{}`.
//
// Measured live 2026-07-30, three ways, always `[]`: a page past the end
// (total_rows 96), a date window with no days in it (total_rows 0), and a
// schedule with an empty queue. Before ScheduleCalendar.UnmarshalJSON existed
// this aborted the WHOLE decode, so total_rows — the one number still worth
// having on an overrun — was lost with it, and the CLI's page-overrun guard
// could never be reached because the error fired first.
//
// Every test in this repo that covered the empty case used an invented `{}`.
func TestListSchedulePosts_EmptyCalendarArrivesAsAJSONList(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantRows   int
	}{
		{"page past the end", `{"posts_by_days":[],"total_rows":96,"rows_limit":200,"is_has_more":false}`, 96},
		{"empty date window", `{"posts_by_days":[],"total_rows":0,"rows_limit":200,"is_has_more":false}`, 0},
		{"object form still decodes", `{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":1}]}},"total_rows":1,"rows_limit":200,"is_has_more":false}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)

			resp, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{ScheduleID: 55576})
			if err != nil {
				t.Fatalf("ListSchedulePosts: %v — the server's own encoding of an empty calendar must not fail the decode", err)
			}
			if resp.PostsByDays == nil {
				t.Errorf("PostsByDays = nil, want a non-nil map — callers range over it and index it without a nil check")
			}
			if resp.TotalRows != tc.wantRows {
				t.Errorf("TotalRows = %d, want %d — the queue depth must survive an empty calendar; it is the whole answer on a page overrun", resp.TotalRows, tc.wantRows)
			}
		})
	}
}

// TestListSchedulePosts_PopulatedArrayIsStillAnError keeps the tolerance
// narrow. Accepting `[]` is a concession to one PHP encoding quirk; accepting
// a NON-empty array would be accepting a genuine shape change in silence,
// which is how the wrong shape shipped in the first place.
func TestListSchedulePosts_PopulatedArrayIsStillAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posts_by_days":[{"id":1}],"total_rows":1}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListSchedulePosts(context.Background(), ListSchedulePostsFilter{ScheduleID: 55576})
	if err == nil {
		t.Fatal("ListSchedulePosts returned nil error for a POPULATED array — only the empty array is PHP's quirk; a populated one is a real shape change and must be loud")
	}
	if !strings.Contains(err.Error(), "only the EMPTY array is accepted") {
		t.Errorf("error = %q, want it to explain that only the empty array is tolerated", err)
	}
}
