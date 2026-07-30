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

// stubPagedServer serves one path from a per-page-body map keyed by the
// `page` query parameter (page "" or "1" → "1"). It is the list-command
// analogue of stubDoctorPaginatedAPIServer, used to drive the runList*
// cores end-to-end against a walker that fetches real pages.
func stubPagedServer(t *testing.T, path string, pages map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("unexpected request: %s %s, want %s", r.Method, r.URL.Path, path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		pg := r.URL.Query().Get("page")
		if pg == "" {
			pg = "1"
		}
		body, ok := pages[pg]
		if !ok {
			t.Errorf("unexpected %s page=%s", path, pg)
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
}

// pageRows builds a JSON page body for /accounts/pages with `count` rows of
// sequential ids starting at `start`, the given total_rows, and is_has_more.
func pageRows(start, count, total int, hasMore bool) string {
	type page struct {
		ID             int    `json:"id"`
		SourceID       int    `json:"source_id"`
		SocialPageName string `json:"social_page_name"`
	}
	list := make([]page, 0, count)
	for i := 0; i < count; i++ {
		list = append(list, page{ID: start + i, SourceID: 1, SocialPageName: "P"})
	}
	b, _ := json.Marshal(struct {
		List      []page `json:"list"`
		TotalRows int    `json:"total_rows"`
		IsHasMore bool   `json:"is_has_more"`
		RowsLimit int    `json:"rows_limit"`
	}{list, total, hasMore, 20})
	return string(b)
}

// TestRunListPages_TruncationWarning is the RED-then-GREEN guard for the
// issue #103 truncation warning. A 40-page account returns 20 rows on page
// 1 with is_has_more=true and total_rows=40. Without the fix the command
// printed the 20 rows to stdout, nothing to stderr, and exited 0 — pages
// 21-40 were unreachable and the user was not told. The fix prints a stderr
// warning naming the numbers and the exact remedy, keeps stdout valid JSON,
// and exits 0 (a truncated page is a complete answer to "give me page 1",
// not an error).
//
// RED-on-revert: remove the `!all && isHasMore` branch from emitList and the
// stderr assertion fails (warning absent); the stdout/exit-0 assertions pin
// that the warning is NOT promoted to an error or to stdout.
func TestRunListPages_TruncationWarning(t *testing.T) {
	srv := stubPagedServer(t, "/accounts/pages", map[string]string{
		"1": pageRows(1, 20, 40, true),
	})
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runListPages(context.Background(), c, &out, &errOut, hooppy.ListPagesFilter{}, false)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a truncated page is a complete answer, not an error; stderr=%s", code, errOut.String())
	}
	want := "40 pages total — showing 20. Use --all to fetch every page."
	if !strings.Contains(errOut.String(), want) {
		t.Fatalf("stderr = %q, want it to contain %q — the truncation warning must name total_rows, the rows shown, and the --all remedy (issue #103)", errOut.String(), want)
	}
	// Stdout must stay valid JSON (the warning is on stderr only).
	var env struct {
		List      []json.RawMessage `json:"list"`
		TotalRows int               `json:"total_rows"`
		IsHasMore bool              `json:"is_has_more"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON: %v — the warning must not corrupt stdout (stdout=%q)", err, out.String())
	}
	if len(env.List) != 20 {
		t.Errorf("stdout list len = %d, want 20 (the single page, unchanged)", len(env.List))
	}
	if !env.IsHasMore {
		t.Errorf("stdout is_has_more = false, want true — the non-truncated stdout shape must not be altered")
	}
}

// TestRunListPages_AllReturnsMoreRows is the RED-then-GREEN guard for --all
// returning more rows than one page. The account has 40 pages across two
// 20-row pages; --all walks both and emits an AllListEnvelope whose list
// holds all 40. Without the fix `pages list` had no --all flag and pages
// 21-40 were unreachable from the CLI by any invocation.
//
// RED-on-revert: break the ListAllPagesWithTotal walk (or drop the --all
// branch from runListPages) and len(list) < 40 fails. The "more than one
// page" assertion (40 > 20) is what makes this non-vacuous — a test that
// only checks the flag parses would pass with the walk broken.
func TestRunListPages_AllReturnsMoreRows(t *testing.T) {
	srv := stubPagedServer(t, "/accounts/pages", map[string]string{
		"1": pageRows(1, 20, 40, true),
		"2": pageRows(21, 20, 40, false),
	})
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runListPages(context.Background(), c, &out, &errOut, hooppy.ListPagesFilter{}, true)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if errOut.String() != "" {
		t.Errorf("stderr = %q, want empty — an --all walk must not emit a truncation warning", errOut.String())
	}
	var env hooppy.AllListEnvelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not a valid AllListEnvelope: %v (stdout=%q)", err, out.String())
	}
	if env.IsHasMore {
		t.Errorf("AllListEnvelope.is_has_more = true, want false — an --all walk pins is_has_more false")
	}
	if env.TotalRows != 40 {
		t.Errorf("AllListEnvelope.total_rows = %d, want 40 (the server's total, not len(list))", env.TotalRows)
	}
	// AllListEnvelope.List is interface{}; re-marshal to count rows.
	raw, _ := json.Marshal(env.List)
	var rows []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("AllListEnvelope.list did not decode as a page slice: %v", err)
	}
	if len(rows) != 40 {
		t.Fatalf("--all returned %d rows, want 40 — --all must return MORE rows than one page (20); pages 21-40 were unreachable before the fix", len(rows))
	}
}

// TestRunListPages_NoWarningWhenComplete verifies the warning does NOT fire
// when is_has_more is false (a complete single page) — the warning is gated
// on truncation, not emitted unconditionally.
func TestRunListPages_NoWarningWhenComplete(t *testing.T) {
	srv := stubPagedServer(t, "/accounts/pages", map[string]string{
		"1": pageRows(1, 5, 5, false),
	})
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runListPages(context.Background(), c, &out, &errOut, hooppy.ListPagesFilter{}, false)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if errOut.String() != "" {
		t.Errorf("stderr = %q, want empty — no warning when is_has_more is false (the list is complete)", errOut.String())
	}
}
