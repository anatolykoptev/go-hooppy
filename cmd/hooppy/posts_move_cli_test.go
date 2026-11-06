package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/spf13/cobra"
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

// editFixtureCLI is a GET /posts/{id}/edit body for post id with the given
// publication_when_type; date, when non-empty, is the server-assigned
// publication_date (the post-move read).
func editFixtureCLI(id, whenType int, date string) string {
	pd := ""
	if date != "" {
		pd = `,"publication_date":{"date":"` + date + `","hours":"12","minutes":"25"}`
	}
	return fmt.Sprintf(`{"id":%d,"publication_when_type":%d,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[{"text":"old","source_id":0}],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":55576,"project_id":0%s}`, id, whenType, pd)
}

// moveCLIStub serves the move flow for any ids: before POST /posts/batch/move
// each post's edit reads with its configured when_type (default 3, by
// schedule), after it the moved state with date. It records every request.
type moveCLIStub struct {
	*httptest.Server
	whenTypes map[int]int
	date      string
	posted    int
	gets      int
	postBody  string
}

func newMoveCLIStub(t *testing.T, whenTypes map[int]int, date string) *moveCLIStub {
	t.Helper()
	st := &moveCLIStub{whenTypes: whenTypes, date: date}
	st.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move":
			st.posted++
			b, _ := io.ReadAll(r.Body)
			st.postBody = string(b)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			st.gets++
			id, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts/"), "/edit"))
			if st.posted == 0 {
				wt, ok := st.whenTypes[id]
				if !ok {
					wt = 3
				}
				w.Write([]byte(editFixtureCLI(id, wt, "")))
				return
			}
			w.Write([]byte(editFixtureCLI(id, 3, st.date)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return st
}

// TestRunMovePosts_Positional_ReportsPublicationDate: the single positional
// form prints the flat PostMoveResult and surfaces the server-assigned
// publication_date — the load-bearing output of a move (a months-long delay
// is silent without it) — and moves via the batch endpoint, never a PUT.
func TestRunMovePosts_Positional_ReportsPublicationDate(t *testing.T) {
	st := newMoveCLIStub(t, nil, "15.01.2027")
	defer st.Close()
	c := newCLITestClient(t, st.Server)

	var out, errOut bytes.Buffer
	code := runMovePosts(context.Background(), c, &out, &errOut, ops.MovePostsInput{IDs: ops.IDList{42}, ToScheduleID: 55576}, true)
	if code != 0 {
		t.Fatalf("runMovePosts exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	if st.posted != 1 || !strings.Contains(st.postBody, `"posts_ids":"42"`) {
		t.Errorf("POST /posts/batch/move issued %d times with body %q, want once with posts_ids \"42\"", st.posted, st.postBody)
	}
	stdout := out.String()
	if strings.Contains(stdout, `"moved"`) {
		t.Errorf("positional form must print the flat PostMoveResult, got the batch shape: %s", stdout)
	}
	if !strings.Contains(stdout, `"schedule_id": 55576`) || !strings.Contains(stdout, "15.01.2027") {
		t.Errorf("stdout missing schedule_id 55576 or the recovered publication_date 15.01.2027: %s", stdout)
	}
}

// TestRunMovePosts_InvalidInput_RefusesBeforeAnyRequest: an empty list, an id
// <= 0 and a target schedule <= 0 are refused with exit 1 before any request.
func TestRunMovePosts_InvalidInput_RefusesBeforeAnyRequest(t *testing.T) {
	cases := map[string]ops.MovePostsInput{
		"target 0":        {IDs: ops.IDList{42}, ToScheduleID: 0},
		"target negative": {IDs: ops.IDList{42}, ToScheduleID: -5},
		"empty ids":       {IDs: nil, ToScheduleID: 55576},
		"id 0":            {IDs: ops.IDList{5, 0}, ToScheduleID: 55576},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			reached := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			var out, errOut bytes.Buffer
			if code := runMovePosts(context.Background(), newCLITestClient(t, srv), &out, &errOut, in, false); code != 1 {
				t.Fatalf("exit = %d, want 1; stderr: %s", code, errOut.String())
			}
			if reached {
				t.Fatal("a request was issued before the refusal")
			}
		})
	}
}

// TestRunMovePosts_EpochDate_WarnsOnStderr: the stopped-schedule trap (a
// recovered 01.01.1970) is a successful move (exit 0) with a loud stderr
// warning.
func TestRunMovePosts_EpochDate_WarnsOnStderr(t *testing.T) {
	st := newMoveCLIStub(t, nil, "01.01.1970")
	defer st.Close()
	var out, errOut bytes.Buffer
	code := runMovePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, ops.MovePostsInput{IDs: ops.IDList{42}, ToScheduleID: 55576}, true)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — an epoch date is a successful move into a stopped schedule; stderr: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "01.01.1970") || !strings.Contains(errOut.String(), "STOPPED") {
		t.Errorf("stderr must carry the stopped-schedule warning naming 01.01.1970 and STOPPED: %s", errOut.String())
	}
}

// TestRunMovePosts_Batch_ReportsPerPostDates: --ids prints {success, moved:[…]}
// with every post's date, for one id and for many alike (a script reading
// .moved[] must not branch on the count).
func TestRunMovePosts_Batch_ReportsPerPostDates(t *testing.T) {
	for _, ids := range []ops.IDList{{10, 20}, {42}} {
		st := newMoveCLIStub(t, nil, "15.01.2027")
		var out, errOut bytes.Buffer
		code := runMovePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, ops.MovePostsInput{IDs: ids, ToScheduleID: 55576}, false)
		st.Close()
		if code != 0 {
			t.Fatalf("ids %v: exit = %d, want 0; stderr: %s", ids, code, errOut.String())
		}
		var got hooppy.BatchMovePostsResult
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("ids %v: stdout is not a BatchMovePostsResult: %v: %s", ids, err, out.String())
		}
		if len(got.Moved) != len(ids) {
			t.Fatalf("ids %v: moved has %d entries, want %d: %s", ids, len(got.Moved), len(ids), out.String())
		}
		for i, m := range got.Moved {
			if m.ID != ids[i] || m.PublicationDate == nil || m.PublicationDate.Date != "15.01.2027" {
				t.Errorf("ids %v: moved[%d] = %+v, want id %d with publication_date 15.01.2027", ids, i, m, ids[i])
			}
		}
	}
}

// TestRunMovePosts_F3_BatchHitsTheWhenTypeGuard is F3 on the CLI: a batch
// containing a post that is not schedule-driven (publication_when_type=2) is
// refused WHOLE — exit 1, the offender named, and no POST /posts/batch/move
// issued, so the schedule-driven posts beside it are not moved either. The
// single positional form is refused the same way.
//
// Mutation: remove the `c.requireScheduleDriven(ctx, ids)` call in
// BatchMovePosts (posts.go) and the batch POSTs → posted == 1.
func TestRunMovePosts_F3_BatchHitsTheWhenTypeGuard(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ids        ops.IDList
		positional bool
	}{
		{"batch of three, one fixed-date", ops.IDList{41, 42, 43}, false},
		{"single positional", ops.IDList{42}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newMoveCLIStub(t, map[int]int{42: 2}, "15.01.2027")
			defer st.Close()
			var out, errOut bytes.Buffer
			code := runMovePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, ops.MovePostsInput{IDs: tc.ids, ToScheduleID: 55576}, tc.positional)
			if code != 1 {
				t.Fatalf("exit = %d, want 1 — the when_type guard must refuse; stdout: %s stderr: %s", code, out.String(), errOut.String())
			}
			if st.posted != 0 {
				t.Fatalf("POST /posts/batch/move issued %d time(s) — the guard must refuse the WHOLE batch before the move", st.posted)
			}
			if !strings.Contains(errOut.String(), "post 42: publication_when_type=2") {
				t.Errorf("stderr must name post 42 and its when_type: %s", errOut.String())
			}
		})
	}
}

// --- postIDsFromArgs: positional <post-id> vs --ids ---

func idsTestCmd(t *testing.T, flags ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "delete"}
	if err := RegisterOpFlags(cmd, reflect.TypeOf(ops.DeletePostsInput{})); err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags(flags); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestPostIDsFromArgs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		flags      []string
		args       []string
		want       ops.IDList
		positional bool
		wantErr    string
	}{
		{name: "positional only", args: []string{"42"}, want: ops.IDList{42}, positional: true},
		{name: "ids only", flags: []string{"--ids", "1,2,3"}, want: ops.IDList{1, 2, 3}},
		{name: "both", flags: []string{"--ids", "1,2"}, args: []string{"42"}, wantErr: "mutually exclusive"},
		{name: "neither", wantErr: "requires a positional post-id or --ids"},
		{name: "non-integer positional", args: []string{"abc"}, wantErr: "not a valid integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := idsTestCmd(t, tc.flags...)
			var in ops.DeletePostsInput
			if err := FillOpInput(cmd, &in); err != nil {
				t.Fatal(err)
			}
			got, positional, err := postIDsFromArgs(cmd, tc.args, in.IDs)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) || positional != tc.positional {
				t.Errorf("ids = %v positional = %v, want %v %v", got, positional, tc.want, tc.positional)
			}
		})
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
