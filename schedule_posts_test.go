package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// schedulePostsBody is a GET /posts/schedules/{id}/posts fixture: a
// schedule with 3 posts across 2 days. The keys are dd.mm.yyyy; the
// fixture is deliberately OUT OF lexicographic order (15.01.2027 before
// 31.01.2027 is fine, but 01.02.2027 must sort AFTER 31.01.2027
// chronologically, not before it as a raw-string sort would do).
const schedulePostsBody = `{
	"posts_by_days": {
		"15.01.2027": [{"id":101,"text":"a"},{"id":102,"text":"b"}],
		"31.01.2027": [{"id":103,"text":"c"}],
		"01.02.2027": [{"id":104,"text":"d"}]
	},
	"total_rows": 4,
	"rows_limit": 1000,
	"is_has_more": false
}`

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

	resp, err := c.ListSchedulePosts(context.Background(), 55576, "", "", 0)
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

	if _, err := c.ListSchedulePosts(context.Background(), 0, "", "", 0); err == nil {
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
		w.Write([]byte(`{"posts_by_days":{},"total_rows":0,"rows_limit":1000,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSchedulePosts(context.Background(), 55576, "", "", 0)
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

	resp, err := c.ListSchedulePosts(context.Background(), 55576, "", "", 0)
	if err != nil {
		t.Fatalf("ListSchedulePosts: %v", err)
	}
	posts := resp.PostsByDays["15.01.2027"]
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
