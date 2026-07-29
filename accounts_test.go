package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListAllPages_TwoPages verifies the walk starts at page 1, accumulates
// both pages, and produces no duplicate IDs. See TestListAllPosts_TwoPages
// for the off-by-one this guards against (page 0 double-fetch).
func TestListAllPages_TwoPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`{"list":[{"id":3,"social_page_name":"C"}],"total_rows":3,"is_has_more":false,"rows_limit":20}`))
		default:
			w.Write([]byte(`{"list":[{"id":1,"social_page_name":"A"},{"id":2,"social_page_name":"B"}],"total_rows":3,"is_has_more":true,"rows_limit":20}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, err := c.ListAllPages(context.Background(), ListPagesFilter{})
	if err != nil {
		t.Fatalf("ListAllPages: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}
	seen := map[int]bool{}
	for _, p := range all {
		if seen[p.ID] {
			t.Errorf("duplicate page ID %d", p.ID)
		}
		seen[p.ID] = true
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("page params = %v, want [1 2]", pages)
	}
}

// TestListAllPages_SanityCap verifies the walk errors when is_has_more
// never goes false.
func TestListAllPages_SanityCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"list":[{"id":1}],"total_rows":1000000,"is_has_more":true,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListAllPages(context.Background(), ListPagesFilter{})
	if err == nil {
		t.Fatal("expected error when is_has_more never goes false, got nil")
	}
	if !contains(err.Error(), "exceeded") {
		t.Errorf("expected cap error mentioning 'exceeded', got: %v", err)
	}
}

// TestListAccounts_NegativeRejected covers issue #65 item 1: the
// ListAccounts SourceID/Page filters were gated on `> 0` — the same
// silent-negative hole this PR closed across the search/posts filters. A
// negative took neither branch: no error, no parameter, an unfiltered
// result that looks filtered. The guard now rejects negatives before any
// request; zero stays the unset sentinel.
func TestListAccounts_NegativeRejected(t *testing.T) {
	cases := []struct {
		name string
		f    ListAccountsFilter
	}{
		{"SourceID negative", ListAccountsFilter{SourceID: -1}},
		{"Page negative", ListAccountsFilter{Page: -1}},
	}
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			_, err := c.ListAccounts(context.Background(), tc.f)
			if err == nil {
				t.Fatalf("ListAccounts with %s: expected an error, got nil — a negative ID/page value must be rejected before any request (issue #65 item 1)", tc.name)
			}
			if reached {
				t.Fatalf("ListAccounts with %s: the guard issued a request before erroring — rejection MUST happen before any request is issued", tc.name)
			}
		})
	}
}

// TestListPages_NegativeRejected covers issue #65 item 1: the ListPages
// SourceID/AccountID/Page filters were gated on `> 0` — the same
// silent-negative hole this PR closed across the search/posts filters.
func TestListPages_NegativeRejected(t *testing.T) {
	cases := []struct {
		name string
		f    ListPagesFilter
	}{
		{"SourceID negative", ListPagesFilter{SourceID: -1}},
		{"AccountID negative", ListPagesFilter{AccountID: -1}},
		{"Page negative", ListPagesFilter{Page: -1}},
	}
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			_, err := c.ListPages(context.Background(), tc.f)
			if err == nil {
				t.Fatalf("ListPages with %s: expected an error, got nil — a negative ID/page value must be rejected before any request (issue #65 item 1)", tc.name)
			}
			if reached {
				t.Fatalf("ListPages with %s: the guard issued a request before erroring — rejection MUST happen before any request is issued", tc.name)
			}
		})
	}
}
