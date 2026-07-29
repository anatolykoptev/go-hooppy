package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
