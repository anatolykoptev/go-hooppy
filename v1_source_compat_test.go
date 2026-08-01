package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The v1 call form must keep compiling. ListProxies and ListSourceResources
// took only a context in v1.1.2 and gained a required page parameter in #126;
// in a Go module that is a compile break for every consumer, which post-1.0
// semver calls a major, which means a /v2 module path. There is no /v2, so a
// v2.0.0 tag is not fetchable and the release would reach nobody.
//
// This test is the compile-time half of the guarantee: the two-argument-free
// call below simply must build. Reverting either signature to a required
// `page int` fails compilation here — which is not a falsification on its own,
// so the sub-tests below assert runtime behaviour that a wrong variadic
// implementation would break while still compiling.
func TestV1SourceCompat_ListersAcceptTheV1CallForm(t *testing.T) {
	var reqs atomic.Int64
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		lastQuery = r.URL.RawQuery
		w.Write([]byte(`{"list":[],"total_rows":0}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	t.Run("v1 form sends no page parameter", func(t *testing.T) {
		lastQuery = ""
		if _, err := c.ListProxies(ctx); err != nil {
			t.Fatalf("ListProxies(ctx): %v", err)
		}
		if strings.Contains(lastQuery, "page") {
			t.Errorf("query = %q, want no page — omitting the argument must leave the parameter unset, not send page=0", lastQuery)
		}
		if _, err := c.ListSourceResources(ctx); err != nil {
			t.Fatalf("ListSourceResources(ctx): %v", err)
		}
	})

	t.Run("one page argument reaches the wire", func(t *testing.T) {
		lastQuery = ""
		if _, err := c.ListProxies(ctx, 3); err != nil {
			t.Fatalf("ListProxies(ctx, 3): %v", err)
		}
		if !strings.Contains(lastQuery, "page=3") {
			t.Errorf("query = %q, want page=3 — the variadic form must still be able to select a page, or restoring compatibility would have cost the capability", lastQuery)
		}
	})

	t.Run("more than one page is refused before any request", func(t *testing.T) {
		before := reqs.Load()
		if _, err := c.ListProxies(ctx, 1, 2); err == nil {
			t.Fatal("ListProxies(ctx, 1, 2) was accepted — variadic is a compatibility device, not a list of pages; silently using the first would make a caller's mistake invisible")
		}
		if got := reqs.Load() - before; got != 0 {
			t.Errorf("requests issued = %d, want 0 — the arity error must be caught before the call", got)
		}
	})

	t.Run("a negative page is refused before any request", func(t *testing.T) {
		before := reqs.Load()
		if _, err := c.ListSourceResources(ctx, -1); err == nil {
			t.Fatal("a negative page was accepted")
		}
		if got := reqs.Load() - before; got != 0 {
			t.Errorf("requests issued = %d, want 0", got)
		}
	})
}

// The arity error must name the method the caller actually invoked. The
// helper serves both listers, and an error is a corrective instruction — one
// that names a different method than the one the caller typed sends them to
// the wrong place. Same principle as errSchedulesWithoutWhenType3 naming each
// surface's own flag.
//
// RED-on-revert: hardcode "ListProxies(ctx)" back into the message and a
// ListSourceResources caller reads an example naming ListProxies.
func TestV1SourceCompat_ArityErrorNamesTheCallersMethod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"list":[],"total_rows":0}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListSourceResources(context.Background(), 1, 2)
	if err == nil {
		t.Fatal("expected an arity error")
	}
	if strings.Contains(err.Error(), "ListProxies") {
		t.Fatalf("the error names ListProxies while the caller used ListSourceResources — a corrective instruction must name what the caller can fix: %v", err)
	}
	if !strings.Contains(err.Error(), "ListSourceResources(ctx)") {
		t.Errorf("the example call form should name this method, got: %v", err)
	}
}
