package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// newCLITestClient builds a hooppy.Client pointing at a httptest.Server,
// matching the CLI test convention (cmd/hooppy package).
func newCLITestClient(t *testing.T, srv *httptest.Server) *hooppy.Client {
	t.Helper()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// scheduleDrivenEditBodyCLI is a GET /posts/{id}/edit fixture for a
// schedule-driven post (when_type=3). The pre-move read recovers when_type
// for the guard; the post-move read recovers the server-assigned date.
const scheduleDrivenEditBodyCLI = `{"id":42,"publication_when_type":3,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[{"text":"old","source_id":0}],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":7,"project_id":0}`

// movedEditBodyCLI is the post-move read: schedule swapped to the target,
// server-assigned publication_date 15.01.2027.
const movedEditBodyCLI = `{"id":42,"publication_when_type":3,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[{"text":"old","source_id":0}],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":55576,"project_id":0,"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}}`

// TestRunMovePost_ReportsPublicationDate verifies the CLI core surfaces
// the server-assigned publication_date in its JSON output — the
// load-bearing output of a move (a months-long delay is silent without
// it). The stub returns a post-move edit with publication_date
// 15.01.2027; the CLI must encode that in stdout.
func TestRunMovePost_ReportsPublicationDate(t *testing.T) {
	var getCalls int
	var postCalled, putCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(scheduleDrivenEditBodyCLI))
			} else {
				w.Write([]byte(movedEditBodyCLI))
			}
		case http.MethodPost:
			if r.URL.Path == "/posts/batch/move" {
				postCalled = true
				w.Write([]byte(`{"success":true}`))
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodPut:
			putCalled = true
		}
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runMovePost(context.Background(), c, &out, &errOut, 42, 55576)
	if code != 0 {
		t.Fatalf("runMovePost exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued — MovePost must move via the batch endpoint")
	}
	if putCalled {
		t.Fatal("PUT /posts/{id} was issued — MovePost must NOT use the full-state PUT")
	}
	stdout := out.String()
	if !strings.Contains(stdout, "\"schedule_id\": 55576") {
		t.Errorf("stdout missing schedule_id 55576: %s", stdout)
	}
	if !strings.Contains(stdout, "15.01.2027") {
		t.Errorf("stdout missing the recovered publication_date 15.01.2027 — the CLI MUST surface the server-assigned slot (a months-long delay is silent without it): %s", stdout)
	}
}

// TestRunMovePost_ZeroTargetSchedule_Refuses verifies the CLI core refuses
// a zero target schedule before any request.
func TestRunMovePost_ZeroTargetSchedule_Refuses(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runMovePost(context.Background(), c, &out, &errOut, 42, 0)
	if code == 0 {
		t.Fatal("runMovePost with toScheduleID=0: exit 0, want non-zero")
	}
	if reached {
		t.Fatal("runMovePost with toScheduleID=0: a request was issued before the guard errored")
	}
}

// TestRunMovePost_EpochDate_WarnsOnStderr verifies the CLI core surfaces
// the stopped-schedule warning on stderr when the recovered date is
// 01.01.1970 — the operator must see the stopped-schedule trap.
func TestRunMovePost_EpochDate_WarnsOnStderr(t *testing.T) {
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(scheduleDrivenEditBodyCLI))
			} else {
				w.Write([]byte(`{"id":42,"publication_when_type":3,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[{"text":"old","source_id":0}],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":55576,"project_id":0,"publication_date":{"date":"01.01.1970","hours":"00","minutes":"00"}}`))
			}
		case http.MethodPost:
			if r.URL.Path == "/posts/batch/move" {
				w.Write([]byte(`{"success":true}`))
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runMovePost(context.Background(), c, &out, &errOut, 42, 55576)
	if code != 0 {
		t.Fatalf("runMovePost exit = %d, want 0 — an epoch date is a successful move into a stopped schedule, not an error; stderr: %s", code, errOut.String())
	}
	stderr := errOut.String()
	if !strings.Contains(stderr, "01.01.1970") {
		t.Errorf("stderr missing the epoch date in the warning — the operator must see the stopped-schedule trap: %s", stderr)
	}
	if !strings.Contains(stderr, "STOPPED") {
		t.Errorf("stderr missing \"STOPPED\" in the warning: %s", stderr)
	}
}

// TestRunBatchMove_ReportsPerPostDates verifies the CLI core surfaces the
// per-post publication_dates in its JSON output.
func TestRunBatchMove_ReportsPerPostDates(t *testing.T) {
	var postCalled bool
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			postCalled = true
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			getCalls++
			w.Write([]byte(`{"id":0,"publication_when_type":3,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":55576,"project_id":0,"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}}`))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runBatchMove(context.Background(), c, &out, &errOut, []int{10, 20}, 55576)
	if code != 0 {
		t.Fatalf("runBatchMove exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued")
	}
	stdout := out.String()
	if !strings.Contains(stdout, "\"schedule_id\": 55576") {
		t.Errorf("stdout missing schedule_id 55576: %s", stdout)
	}
	// Two moved entries, each carrying the date.
	if !strings.Contains(stdout, "15.01.2027") {
		t.Errorf("stdout missing the recovered publication_date 15.01.2027 — the CLI MUST surface the per-post server-assigned slots: %s", stdout)
	}
	if getCalls != 2 {
		t.Errorf("post-move GET issued %d times, want 2 (one per id)", getCalls)
	}
}

// TestRunScheduleQueue_SummarySurfacesTotalRowsAndBookedUntil verifies
// the default (non-JSON) output surfaces TotalRows and BookedUntil — the
// two values an operator needs before moving posts INTO a schedule.
func TestRunScheduleQueue_SummarySurfacesTotalRowsAndBookedUntil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":1,"text":"a"}]},"01.02.2027":{"day_name":"Пн","day_date":"1 Февраля","posts":[{"id":2,"text":"b"}]}},"total_rows":2,"rows_limit":1000,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runScheduleQueue(context.Background(), c, &out, &errOut, 55576, "", "", 0, false)
	if code != 0 {
		t.Fatalf("runScheduleQueue exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	stdout := out.String()
	if !strings.Contains(stdout, "\"total_rows\": 2") {
		t.Errorf("stdout missing total_rows 2 — the queue depth MUST appear in the default summary: %s", stdout)
	}
	if !strings.Contains(stdout, "01.02.2027") {
		t.Errorf("stdout missing the booked-until date 01.02.2027 — the LAST day with posts MUST appear in the default summary: %s", stdout)
	}
	if !strings.Contains(stdout, "15.01.2027") {
		t.Errorf("stdout missing the first-booked-day 15.01.2027: %s", stdout)
	}
}

// TestRunScheduleQueue_JSONOutput verifies --json prints the raw envelope
// (posts_by_days, total_rows, rows_limit, is_has_more) without
// transformation.
func TestRunScheduleQueue_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":1,"text":"a"}]}},"total_rows":1,"rows_limit":1000,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runScheduleQueue(context.Background(), c, &out, &errOut, 55576, "", "", 0, true)
	if code != 0 {
		t.Fatalf("runScheduleQueue --json exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	stdout := out.String()
	if !strings.Contains(stdout, "posts_by_days") {
		t.Errorf("--json stdout missing posts_by_days (raw envelope): %s", stdout)
	}
	if !strings.Contains(stdout, "rows_limit") {
		t.Errorf("--json stdout missing rows_limit (raw envelope): %s", stdout)
	}
	// --json must NOT carry the summary-only fields.
	if strings.Contains(stdout, "booked_until") {
		t.Errorf("--json stdout contains \"booked_until\" — that is a summary field, not in the raw envelope: %s", stdout)
	}
}

// TestRunScheduleQueue_IssuesExactlyOneRequest verifies the CLI core
// issues exactly ONE request (no paged walk) — the issue #106 contract.
// is_has_more=true returns exit 2 (truncation warning) but still only one
// request.
func TestRunScheduleQueue_IssuesExactlyOneRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// is_has_more=true would tempt a paged walk; the contract forbids it.
		// rows_limit=200 matches the measured live limit (issue #116).
		w.Write([]byte(`{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":1}]}},"total_rows":1,"rows_limit":200,"is_has_more":true}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runScheduleQueue(context.Background(), c, &out, &errOut, 55576, "", "", 0, false)
	// is_has_more=true → exit 2 (partial/truncated), NOT 0 — a partial
	// answer must not look complete to a script. Exit 2 distinguishes
	// partial from error (exit 1) so a script can branch (item F).
	if code != 2 {
		t.Fatalf("runScheduleQueue exit = %d, want 2 — is_has_more=true is a PARTIAL result; the exit code must signal incompleteness (2=partial, 1=error); stderr: %s", code, errOut.String())
	}
	if calls != 1 {
		t.Errorf("runScheduleQueue issued %d requests, want exactly 1 — issue #106 forbids a paged walk even when is_has_more is true", calls)
	}
	// The truncation warning MUST name --from/--to as the recovery levers.
	stderr := errOut.String()
	if !strings.Contains(stderr, "PARTIAL") {
		t.Errorf("stderr missing \"PARTIAL\" in the truncation warning: %s", stderr)
	}
	if !strings.Contains(stderr, "--from/--to") {
		t.Errorf("stderr missing \"--from/--to\" — the warning must name the recovery levers: %s", stderr)
	}
}

// TestRunScheduleQueue_TruncatedSuppressesBookedUntil verifies that when
// is_has_more=true the summary OMITS booked_until (the last day of page
// one is NOT the real booked-until date) and warns loudly. This is the
// silent-truncation defect #106 exists to remove.
func TestRunScheduleQueue_TruncatedSuppressesBookedUntil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// rows_limit=200 matches the measured live limit (issue #116).
		w.Write([]byte(`{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":1}]},"31.01.2027":{"day_name":"Вс","day_date":"31 Января","posts":[{"id":2}]}},"total_rows":500,"rows_limit":200,"is_has_more":true}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runScheduleQueue(context.Background(), c, &out, &errOut, 55576, "", "", 0, false)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 — a truncated result must exit 2 (partial), not 0 or 1; stderr: %s", code, errOut.String())
	}
	stdout := out.String()
	// booked_until MUST be omitted (omitempty + IsHasMore guard).
	if strings.Contains(stdout, "booked_until") {
		// Check it's not a non-empty value — the key may appear with an
		// empty string if omitempty is missing.
		if strings.Contains(stdout, `"booked_until": "31.01.2027"`) {
			t.Errorf("stdout carries booked_until 31.01.2027 — a truncated response's last day is the last day of page ONE, not the real booked-until date; it MUST be omitted: %s", stdout)
		}
	}
	// total_rows MUST still appear (it is the real depth regardless of
	// truncation).
	if !strings.Contains(stdout, `"total_rows": 500`) {
		t.Errorf("stdout missing total_rows 500 — the real depth must appear even when truncated: %s", stdout)
	}
	// The warning MUST name the truncation and the recovery levers.
	stderr := errOut.String()
	if !strings.Contains(stderr, "booked_until") {
		t.Errorf("stderr warning does not name booked_until: %s", stderr)
	}
}

// TestRunScheduleQueue_ZeroScheduleID_Refuses verifies the CLI core
// refuses a zero schedule id before any request.
func TestRunScheduleQueue_ZeroScheduleID_Refuses(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runScheduleQueue(context.Background(), c, &out, &errOut, 0, "", "", 0, false)
	if code == 0 {
		t.Fatal("runScheduleQueue with scheduleID=0: exit 0, want non-zero")
	}
	if reached {
		t.Fatal("runScheduleQueue with scheduleID=0: a request was issued before the guard errored")
	}
}

// TestRunScheduleQueue_DateFromPassedToEndpoint verifies the --from flag
// is forwarded as date_from to the endpoint — the recovery lever for a
// truncated result.
func TestRunScheduleQueue_DateFromPassedToEndpoint(t *testing.T) {
	var gotDateFrom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDateFrom = r.URL.Query().Get("date_from")
		w.Write([]byte(`{"posts_by_days":[],"total_rows":0,"rows_limit":200,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	_ = runScheduleQueue(context.Background(), c, &out, &errOut, 55576, "01.03.2027", "", 0, false)
	if gotDateFrom != "01.03.2027" {
		t.Errorf("date_from query param = %q, want \"01.03.2027\" — the --from flag must be forwarded to narrow a truncated calendar", gotDateFrom)
	}
}

// TestRunScheduleQueue_PagePassedToEndpoint verifies the page parameter is
// forwarded to the endpoint — the only lever that walks a truncation without
// guessing dates (item B). It was previously plumbed through three layers
// but both production callers passed a literal 0; now the CLI has a --page
// flag.
//
// RED-on-revert: drop the page param from runScheduleQueue's
// ListSchedulePostsFilter and gotPage != "2" — the assertion fails.
func TestRunScheduleQueue_PagePassedToEndpoint(t *testing.T) {
	var gotPage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPage = r.URL.Query().Get("page")
		w.Write([]byte(`{"posts_by_days":[],"total_rows":0,"rows_limit":200,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	_ = runScheduleQueue(context.Background(), c, &out, &errOut, 55576, "", "", 2, false)
	if gotPage != "2" {
		t.Errorf("page query param = %q, want \"2\" — the page parameter must be forwarded to the endpoint (item B)", gotPage)
	}
}

// TestRunScheduleQueue_PagePastEnd_WarnsAndExits2 is the MAJOR-2 guard
// (review round 4): --page past the end returns 200 with total_rows=96 and
// ZERO day keys, is_has_more:false — a complete-looking EMPTY calendar at
// exit 0. An agent walking pages to recover a truncation reads "complete"
// at every overrun. total_rows is the COLLECTION total (unchanged by
// paging — page=2 and page=99 both return total_rows=96), so it cannot
// detect an overrun by comparison; the ONLY signal is len(PostsByDays)==0
// && TotalRows>0 with Page>0. That MUST warn naming the overrun + total_rows
// and exit 2 (partial), NOT 0 (complete). day_counts MUST marshal as [] not
// null so the output shape does not change between branches.
//
// RED-on-revert: drop the overrun branch in runScheduleQueue and exit is 0
// with no warning — both assertions fail.
func TestRunScheduleQueue_PagePastEnd_WarnsAndExits2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// page=99 past the end: 200, total_rows=96 (collection total), zero
		// day keys, is_has_more:false — the live page=2/page=99 signature.
		w.Write([]byte(`{"posts_by_days":[],"total_rows":96,"rows_limit":200,"is_has_more":false}`))
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runScheduleQueue(context.Background(), c, &out, &errOut, 55576, "", "", 99, false)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 — a page past the end (zero day keys, total_rows>0) is an OVERRUN, not a complete result; a script must not read it as complete; stderr: %s", code, errOut.String())
	}
	stderr := errOut.String()
	if !strings.Contains(stderr, "past the end") {
		t.Errorf("stderr missing \"past the end\" — the warning must name the overrun: %s", stderr)
	}
	if !strings.Contains(stderr, "96") {
		t.Errorf("stderr missing total_rows 96 — the warning must name the collection total: %s", stderr)
	}
	// day_counts MUST marshal as [] (empty slice), NOT null (nil slice) —
	// the output shape must not change between the overrun branch and a
	// normal one. A nil slice with no omitempty marshals as null.
	stdout := out.String()
	if strings.Contains(stdout, `"day_counts": null`) {
		t.Errorf("stdout has day_counts: null — the overrun branch must emit an empty slice [] so the output shape is consistent across branches: %s", stdout)
	}
	if !strings.Contains(stdout, `"day_counts": []`) {
		t.Errorf("stdout missing day_counts: [] — the overrun branch must emit an empty slice, not null: %s", stdout)
	}
}

// TestRunBatchMove_SingleIDBatch_ShapeMatchesMultiID is the MAJOR-3 guard
// (review round 4): --ids 42 prints a PostMoveResult
// ({success,schedule_id,publication_date}) while --ids 42,43 prints a
// BatchMovePostsResult ({success,moved:[…]}). A consumer reading .moved[]
// gets nothing for the single-id case, silently. The single-id routing
// (which closes the when_type asymmetry) MUST be kept, but the OUTPUT SHAPE
// normalised: a single-id batch MUST produce a BatchMovePostsResult with a
// moved array of len 1 — the SAME top-level shape as the multi-id case.
// The `posts move <id>` single positional path keeps PostMoveResult (correct).
//
// RED-on-revert: revert runBatchMove's single-id branch to
// `return runMovePost(...)` and the single-id stdout has no "moved" key —
// the assertion fails.
func TestRunBatchMove_SingleIDBatch_ShapeMatchesMultiID(t *testing.T) {
	// Stub: pre-move GET (when_type guard, schedule-driven=3), POST move,
	// post-move GET with publication_date 15.01.2027.
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && getCalls == 0:
			getCalls++
			w.Write([]byte(`{"id":42,"publication_when_type":3,"schedule_id":55576}`))
		case r.Method == http.MethodGet:
			w.Write([]byte(`{"id":42,"publication_when_type":3,"schedule_id":55576,"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move":
			w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runBatchMove(context.Background(), c, &out, &errOut, []int{42}, 55576)
	if code != 0 {
		t.Fatalf("runBatchMove single-id exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	stdout := out.String()
	// The top-level shape MUST be BatchMovePostsResult — a "moved" array,
	// NOT the flat PostMoveResult. A consumer reading .moved[] must get the
	// entry for the single id.
	if !strings.Contains(stdout, `"moved"`) {
		t.Fatalf("single-id batch stdout missing \"moved\" — the shape MUST be BatchMovePostsResult (same as multi-id), not the flat PostMoveResult: %s", stdout)
	}
	if !strings.Contains(stdout, `"moved": [`) {
		t.Errorf("single-id batch stdout missing \"moved\": [ — moved must be an ARRAY, not a scalar: %s", stdout)
	}
	// moved MUST have exactly one entry carrying the id + the recovered date.
	if !strings.Contains(stdout, `"id": 42`) {
		t.Errorf("single-id batch stdout missing moved[].id 42: %s", stdout)
	}
	if !strings.Contains(stdout, "15.01.2027") {
		t.Errorf("single-id batch stdout missing the recovered publication_date 15.01.2027 in moved[]: %s", stdout)
	}
	// The flat PostMoveResult-only keys MUST NOT appear at the top level
	// (schedule_id at top level is the PostMoveResult shape, not the batch
	// shape — the batch wraps schedule_id inside moved[]).
	if strings.Contains(stdout, `"publication_date": {`) && !strings.Contains(stdout, `"moved"`) {
		t.Errorf("single-id batch has top-level publication_date without moved — wrong shape: %s", stdout)
	}
}

// --- resolveMoveTarget tests (item E: replace the stub) ---

// TestResolveMoveTarget_PositionalOnly is the single-post path: a
// positional arg with no --ids flag produces a non-batch target carrying
// the parsed id.
func TestResolveMoveTarget_PositionalOnly(t *testing.T) {
	target, err := resolveMoveTarget([]string{"42"}, "")
	if err != nil {
		t.Fatalf("resolveMoveTarget: %v", err)
	}
	if target.batch {
		t.Error("batch = true, want false — a positional arg is a single-post move")
	}
	if target.singleID != 42 {
		t.Errorf("singleID = %d, want 42", target.singleID)
	}
}

// TestResolveMoveTarget_IDsFlagOnly is the batch path: --ids with no
// positional produces a batch target carrying the parsed ids.
func TestResolveMoveTarget_IDsFlagOnly(t *testing.T) {
	target, err := resolveMoveTarget(nil, "1,2,3")
	if err != nil {
		t.Fatalf("resolveMoveTarget: %v", err)
	}
	if !target.batch {
		t.Error("batch = false, want true — the --ids flag is a batch move")
	}
	if len(target.ids) != 3 {
		t.Fatalf("len(ids) = %d, want 3", len(target.ids))
	}
	if target.ids[0] != 1 || target.ids[2] != 3 {
		t.Errorf("ids = %v, want [1,2,3]", target.ids)
	}
}

// TestResolveMoveTarget_BothPresent_IsError is the mutual-exclusion guard:
// passing BOTH a positional and --ids is an error.
func TestResolveMoveTarget_BothPresent_IsError(t *testing.T) {
	if _, err := resolveMoveTarget([]string{"42"}, "1,2"); err == nil {
		t.Fatal("resolveMoveTarget with both positional and --ids: expected an error, got nil — they are mutually exclusive")
	}
}

// TestResolveMoveTarget_NeitherPresent_IsError is the presence guard:
// passing NEITHER a positional nor --ids is an error.
func TestResolveMoveTarget_NeitherPresent_IsError(t *testing.T) {
	if _, err := resolveMoveTarget(nil, ""); err == nil {
		t.Fatal("resolveMoveTarget with neither positional nor --ids: expected an error, got nil — one is required")
	}
}

// TestResolveMoveTarget_NonIntegerPositional_IsError verifies a
// non-integer positional is refused with a clear error.
func TestResolveMoveTarget_NonIntegerPositional_IsError(t *testing.T) {
	if _, err := resolveMoveTarget([]string{"abc"}, ""); err == nil {
		t.Fatal("resolveMoveTarget with positional \"abc\": expected an error, got nil — the positional must be an integer")
	}
}

// TestRunBatchMove_SingleIDBatch_RoutesToMovePost is the single-id routing
// guard (item E): a single-id batch (`posts move --ids 42`) MUST route to
// runMovePost (and thus MovePost) so the when_type guard fires — closing the
// asymmetry where `posts move 42` guards when_type but `posts move --ids 42`
// did not, for the same post. The signal that routing happened is the
// when_type guard's GET /posts/{id}/edit: the batch path does NOT issue a
// pre-move GET, the single-post path does.
//
// RED-on-revert: drop the `len(ids) == 1` routing branch in runBatchMove
// and the batch path runs — no pre-move GET is issued (getCalls != 1) and
// the POST runs without the when_type guard.
func TestRunBatchMove_SingleIDBatch_RoutesToMovePost(t *testing.T) {
	var getCalls int
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/42/edit":
			getCalls++
			// when_type=2 (fixed date) — the single-post path refuses this
			// BEFORE the move. The batch path would NOT refuse it.
			w.Write([]byte(`{"id":42,"publication_when_type":2,"schedule_id":55576}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move":
			postCalled = true
			w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newCLITestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runBatchMove(context.Background(), c, &out, &errOut, []int{42}, 55576)
	// The when_type guard refuses a non-schedule post → exit 1.
	if code != 1 {
		t.Fatalf("runBatchMove single-id exit = %d, want 1 — the when_type guard (when_type=2) must refuse the move; stderr: %s", code, errOut.String())
	}
	// The pre-move GET is the signal that routing to MovePost happened.
	if getCalls != 1 {
		t.Errorf("pre-move GET /posts/42/edit issued %d times, want 1 — a single-id batch MUST route to MovePost, which issues a pre-move GET for the when_type guard (item E)", getCalls)
	}
	// The POST must NOT have been issued — the when_type guard refused first.
	if postCalled {
		t.Error("POST /posts/batch/move was issued — the when_type guard must refuse BEFORE the move; a single-id batch routing to MovePost catches this")
	}
}
