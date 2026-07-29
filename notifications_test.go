package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListAllNotifications_TwoPages verifies the walk starts at page 1,
// accumulates both pages, and produces no duplicate IDs. Without the fix
// (walk starting at page 0) the first page is fetched twice because page=0
// and page=1 are byte-identical on the server, yielding duplicates and a
// length that exceeds total_rows.
func TestListAllNotifications_TwoPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`{"list":[{"id":3}],"total_rows":3,"is_has_more":false,"rows_limit":12}`))
		default: // page 1 (and the buggy page 0 which omits the param)
			w.Write([]byte(`{"list":[{"id":1},{"id":2}],"total_rows":3,"is_has_more":true,"rows_limit":12}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, err := c.ListAllNotifications(context.Background())
	if err != nil {
		t.Fatalf("ListAllNotifications: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3 (server total_rows)", len(all))
	}
	seen := map[int]bool{}
	for _, n := range all {
		if seen[n.ID] {
			t.Errorf("duplicate notification ID %d in accumulated result", n.ID)
		}
		seen[n.ID] = true
	}
	if len(pages) != 2 {
		t.Fatalf("handler received %d requests, want 2 (pages=%v)", len(pages), pages)
	}
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want \"1\"", pages[0])
	}
	if pages[1] != "2" {
		t.Errorf("second request page = %q, want \"2\"", pages[1])
	}
}

// TestListAllNotifications_SanityCap verifies the walk returns an error
// instead of looping forever when is_has_more never goes false.
func TestListAllNotifications_SanityCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"list":[{"id":1}],"total_rows":1000000,"is_has_more":true,"rows_limit":12}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListAllNotifications(context.Background())
	if err == nil {
		t.Fatal("expected error when is_has_more never goes false, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("expected cap error mentioning 'exceeded', got: %v", err)
	}
}

// TestListAllNotifications_DistinctPageParams is the RED-on-revert test:
// consecutive requests in the walk must emit DISTINCT page= values. If the
// walk start index goes back to 0 (the off-by-one this PR exists to fix),
// page 0 and page 1 both hit server page 1, the distinctness assertion fails.
func TestListAllNotifications_DistinctPageParams(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "3":
			w.Write([]byte(`{"list":[{"id":5}],"total_rows":5,"is_has_more":false,"rows_limit":12}`))
		case "2":
			w.Write([]byte(`{"list":[{"id":3},{"id":4}],"total_rows":5,"is_has_more":true,"rows_limit":12}`))
		default: // page 1 (and the buggy page 0 which omits the param)
			w.Write([]byte(`{"list":[{"id":1},{"id":2}],"total_rows":5,"is_has_more":true,"rows_limit":12}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.ListAllNotifications(context.Background()); err != nil {
		t.Fatalf("ListAllNotifications: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("handler received %d requests, want 3 (pages=%v)", len(pages), pages)
	}
	// Distinctness: every page= value must be distinct — a walk starting at
	// page 0 fetches page 1 twice (page=0 and page=1 both hit server page 1).
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p] {
			t.Errorf("page param %q emitted twice — double-fetch (walk start index reverted to 0?)", p)
		}
		seen[p] = true
	}
	// First request must be page=1, not page="" (the buggy page 0).
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want \"1\" (walk must start at page 1, not 0)", pages[0])
	}
}
