package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// newStubClient builds a hooppy.Client pointed at an httptest server, so the
// search runners can be exercised end-to-end against a stub without a live
// token or network. Mirrors the library's newTestClient but lives in the
// cmd/hooppy test package (the runners are package main).
func newStubClient(t *testing.T, srv *httptest.Server) *hooppy.Client {
	t.Helper()
	c, err := hooppy.NewClient(hooppy.Config{Token: "x", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// F1 — `search stop` against a stub whose DELETE returns 2xx but where the
// parse is still running: the command must NOT report success. The oracle
// (GET /posts-search/parsing/form) reports is_parsing_in_progress=true after
// the DELETE, so runStopParsing must exit 2 (accepted but not yet idle — a
// partial outcome, NOT an error) and stdout must not contain "success":true.
//
// Exit 2 (not 1) is the fix for finding 3: the prior code returned 1 for all
// three non-idle conditions, so `search stop` on a genuinely running parse
// normally exited 1 and exited 0 mainly when it cancelled nothing — it
// reported failure exactly when it worked. Exit 1 is now reserved for a
// failed DELETE; exit 2 = accepted-but-not-yet-idle / unconfirmed.
//
// RED-on-revert: revert runStopParsing to print {"success":true} after a nil
// StopParsing error (the pre-fix shape — ignore the oracle), and stdout
// contains "success":true → this assertion fails. A command that always
// reports success regardless of the observed state is exactly the #114 defect.
func TestRunStopParsing_StillRunning_NotSuccess_F1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "DELETE /posts-search/parsing/stop":
			w.Write([]byte(`{"success":true}`))
		case "GET /posts-search/parsing/form":
			// The oracle: the parse is STILL running after the stop. A 2xx
			// success:true on the DELETE that did not stop the parse is the
			// exact case #114 closes — the command must not claim success.
			w.Write([]byte(`{"is_parsing_in_progress":true,"source_resources":[],"social_accounts":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runStopParsing(context.Background(), newStubClient(t, srv), &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 — a stop whose oracle still shows in_progress is ACCEPTED-but-not-yet-idle (a partial outcome, not an error); exit 1 is reserved for a failed DELETE (finding 3)", code)
	}
	if strings.Contains(out.String(), `"success":true`) {
		t.Errorf("stdout reported success, but the parse is still running:\n%s\n— the command must not claim a stop it did not observe (issue #114)", out.String())
	}
	if !strings.Contains(out.String(), `"is_parsing_in_progress":true`) {
		t.Errorf("stdout should report the observed state (is_parsing_in_progress=true), got:\n%s", out.String())
	}
}

// F9a — `search stop` against a stub whose DELETE FAILS (5xx): the command
// must exit 1 (the DELETE itself failed — an error, not a partial outcome).
// This is the other direction of finding 3: exit 1 is reserved for a failed
// DELETE, exit 2 for accepted-but-not-yet-idle. Both directions must hold
// independently so a script can tell "the stop request failed" from "the stop
// was accepted but the parse is still winding down".
//
// RED-on-revert: revert runStopParsing to return 1 for the still-running case
// AND this case (the prior shape — all non-idle → 1), and this test still
// passes BUT F1 goes RED (F1 wants 2). The pair (F1=2, F9a=1) is what proves
// the exit-code split is real, not a blanket non-zero.
func TestRunStopParsing_DeleteFails_Exit1_F9a(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "DELETE /posts-search/parsing/stop":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"success":false}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runStopParsing(context.Background(), newStubClient(t, srv), &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 — a FAILED DELETE is an error (exit 1), not a partial outcome (exit 2); finding 3 reserves 1 for the DELETE failure and 2 for accepted-but-not-yet-idle", code)
	}
	if strings.Contains(out.String(), `"success":true`) {
		t.Errorf("stdout reported success after a failed DELETE:\n%s", out.String())
	}
}

// F9b — `search stop` against a stub whose DELETE succeeds and the oracle
// re-read FAILS (ConfirmErr set): the command must exit 2 (accepted but
// unconfirmed — a partial outcome, not a DELETE error). The stop MAY have
// worked; never claim success unconfirmed, but do not report it as a DELETE
// failure either.
func TestRunStopParsing_Unconfirmed_Exit2_F9b(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "DELETE /posts-search/parsing/stop":
			w.Write([]byte(`{"success":true}`))
		case "GET /posts-search/parsing/form":
			// The oracle re-read fails — the stop was accepted but
			// confirmation failed.
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runStopParsing(context.Background(), newStubClient(t, srv), &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 — a stop whose DELETE was accepted but whose confirmation re-read failed is a PARTIAL outcome (exit 2), not a DELETE error (exit 1); finding 3", code)
	}
	if strings.Contains(out.String(), `"success":true`) {
		t.Errorf("stdout reported success for an unconfirmed stop:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `"unconfirmed":true`) {
		t.Errorf("stdout should report unconfirmed=true, got:\n%s", out.String())
	}
}

// F2 — `search stop` against a stub where the stop genuinely took effect:
// reports success. This is the pair that stops F1 from being satisfied by a
// command that always fails. The oracle reports is_parsing_in_progress=false
// after the DELETE, so runStopParsing must exit 0 and stdout must contain
// "success":true.
//
// RED-on-revert: revert runStopParsing to always return 1 (a command that
// never reports success), and code != 0 → this assertion fails.
func TestRunStopParsing_Stopped_ReportsSuccess_F2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "DELETE /posts-search/parsing/stop":
			w.Write([]byte(`{"success":true}`))
		case "GET /posts-search/parsing/form":
			w.Write([]byte(`{"is_parsing_in_progress":false,"source_resources":[],"social_accounts":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runStopParsing(context.Background(), newStubClient(t, srv), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a stop whose oracle shows idle MUST report success (the pair to F1; a command that always fails would pass F1 alone):\nstderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"success":true`) {
		t.Errorf("stdout should report success for an observed-idle parse, got:\n%s", out.String())
	}
}

// F3 — `search rewrite --schedules 10,11` with a when-type that ignores
// schedules (when-type 1, publish now): fails before issuing any request.
// The assertion is NOT merely exit code != 0 — it is that NO request reached
// the server (reqCount == 0). A test that only checked the exit code would
// pass against a command that published to the live queue and then errored.
//
// RED-on-revert: remove the schedules-dropped guard from buildRewritePayload
// and the builder returns a payload with err == nil → runRewriteSearchPost
// proceeds to c.RewriteSearchPost → reqCount becomes 1 → this assertion fails.
func TestRunRewriteSearchPost_SchedulesDropped_NoRequest_F3(t *testing.T) {
	var reqCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	// Batch (--post-ids) so the only request, if the guard is broken, is the
	// single RewriteSearchPost call (no per-post attachment download path).
	code := runRewriteSearchPost(context.Background(), newStubClient(t, srv), &out, &errOut,
		0, "2001", "" /*text*/, 1, 1, "123", "10,11", "", "", "", false)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 — schedules with when-type 1 must be refused before any request (issue #111)", code)
	}
	if got := reqCount.Load(); got != 0 {
		t.Fatalf("reqCount = %d, want 0 — the guard must fail BEFORE any request reaches the server; a request means the post was published with a different meaning than the caller asked (issue #111). stderr: %s", got, errOut.String())
	}
	if !strings.Contains(errOut.String(), "schedules") || !strings.Contains(errOut.String(), "--when-type 3") {
		t.Errorf("stderr should name the schedules/when-type-3 cause, got:\n%s", errOut.String())
	}
}

// TestRunRewriteSearchPost_SchedulesQueued_OK is the pair to F3: when
// --schedules is given with when-type 3, the schedules ARE sent on the POST
// /posts body and the request reaches the server. Without this, F3 is
// satisfied by a guard that refuses every schedule (the exact mutation F7
// proves is green today: mutating buildRewritePayload's guard to refuse ALL
// --schedules left the entire cmd/hooppy package green).
//
// RED-on-revert (F7): mutate buildRewritePayload's guard to
// `if len(schedIDs) > 0 {` (refuse every --schedules regardless of when-type)
// and this test goes RED — the guard fires on when-type 3 too, no POST
// reaches the server, exit code becomes 1.
func TestRunRewriteSearchPost_SchedulesQueued_OK(t *testing.T) {
	var postBody map[string]interface{}
	var sawPost atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts" {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &postBody)
			sawPost.Store(true)
		}
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	// Batch (--post-ids) so the only request is the single RewriteSearchPost
	// call (no per-post attachment download path). when-type 3 + schedules.
	code := runRewriteSearchPost(context.Background(), newStubClient(t, srv), &out, &errOut,
		0, "2001", "" /*text*/, 3, 1, "", "10,11", "", "", "", false)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — schedules with when-type 3 should queue and succeed:\nstderr: %s", code, errOut.String())
	}
	if !sawPost.Load() {
		t.Fatalf("no POST /posts reached the server — the request must be issued when schedules pair with when-type 3")
	}
	sched, _ := postBody["schedules_ids"].([]interface{})
	if len(sched) != 2 {
		t.Errorf("schedules_ids on the POST wire = %v, want [10 11] — the schedules must be sent when when-type is 3", postBody["schedules_ids"])
	}
}

// F4 — the same for `search copy --schedules 10,11` with when-type 1: fails
// before issuing any request (reqCount == 0). copy shares the trap (its
// default was also when-type 1).
//
// RED-on-revert: remove the schedules-dropped guard from buildCopyPayload and
// the builder returns a payload with err == nil → runCopySearchPost proceeds
// to c.CopySearchPost → reqCount becomes 1 → this assertion fails.
func TestRunCopySearchPost_SchedulesDropped_NoRequest_F4(t *testing.T) {
	var reqCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runCopySearchPost(context.Background(), newStubClient(t, srv), &out, &errOut,
		1001, 1, 1, "123", "10,11", "", "", "")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 — schedules with when-type 1 must be refused before any request (issue #111)", code)
	}
	if got := reqCount.Load(); got != 0 {
		t.Fatalf("reqCount = %d, want 0 — the guard must fail BEFORE any request reaches the server; a request means the post was published with a different meaning than the caller asked (issue #111). stderr: %s", got, errOut.String())
	}
	if !strings.Contains(errOut.String(), "schedules") || !strings.Contains(errOut.String(), "--when-type 3") {
		t.Errorf("stderr should name the schedules/when-type-3 cause, got:\n%s", errOut.String())
	}
}

// TestRunCopySearchPost_SchedulesQueued_OK is the pair to F4: when --schedules
// is given with the (new) default when-type 3, the schedules ARE sent on the
// PUT body and the request reaches the server. This stops F4 from being
// satisfied by a guard that refuses all schedules. (A when-type-3 copy also
// issues a before-snapshot and a slot-lookup GET around the PUT, so the
// assertion is on the PUT body's schedules_ids, not a total request count.)
func TestRunCopySearchPost_SchedulesQueued_OK(t *testing.T) {
	var putBody map[string]interface{}
	var sawPut atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/posts/copy" {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &putBody)
			sawPut.Store(true)
		}
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runCopySearchPost(context.Background(), newStubClient(t, srv), &out, &errOut,
		1001, 3, 1, "", "10,11", "", "", "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — schedules with when-type 3 should queue and succeed:\nstderr: %s", code, errOut.String())
	}
	if !sawPut.Load() {
		t.Fatalf("no PUT /posts/copy reached the server — the request must be issued when schedules pair with when-type 3")
	}
	sched, _ := putBody["schedules_ids"].([]interface{})
	if len(sched) != 2 {
		t.Errorf("schedules_ids on the PUT wire = %v, want [10 11] — the schedules must be sent when when-type is 3", putBody["schedules_ids"])
	}
}

// F6 — `search import --schedules 10,11` with when-type 1 fails before issuing
// any request. import completed the guard set: copy and rewrite refuse the
// combination, import accepted it (PR #138 review, F1).
//
// import is a worse case than its two siblings, not an equal one. copy and
// rewrite set SchedulesIDs inside a `switch whenType` so a non-3 when-type
// merely drops the flag; buildImportPayload set it in the payload literal with
// no switch at all, so the schedules reached the wire while when_type said
// publish-now — the server was handed a payload naming both intents.
//
// RED-on-revert: remove the schedules-dropped guard from buildImportPayload
// and the builder returns err == nil → runImport proceeds to the client →
// reqCount becomes non-zero → this assertion fails.
func TestRunImport_SchedulesDropped_NoRequest_F6(t *testing.T) {
	var reqCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runImport(context.Background(), newStubClient(t, srv), &out, &errOut,
		importArgs{postID: 1001, whenType: 1, howType: 1, schedules: "10,11"})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 — schedules with when-type 1 must be refused before any request (issue #111)", code)
	}
	if got := reqCount.Load(); got != 0 {
		t.Fatalf("reqCount = %d, want 0 — the guard must fail BEFORE any request reaches the server; a request means the post was published with a different meaning than the caller asked (issue #111). stderr: %s", got, errOut.String())
	}
	if !strings.Contains(errOut.String(), "schedules") || !strings.Contains(errOut.String(), "--when-type 3") {
		t.Errorf("stderr should name the schedules/when-type-3 cause, got:\n%s", errOut.String())
	}
}

// TestRunImport_SchedulesQueued_OK is the pair to F6: with when-type 3 the
// schedules ARE sent and the request reaches the server. Without this, F6 is
// satisfied by a guard that refuses every schedule.
func TestRunImport_SchedulesQueued_OK(t *testing.T) {
	var putBody map[string]interface{}
	var sawPut atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/posts/import" {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &putBody)
			sawPut.Store(true)
		}
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runImport(context.Background(), newStubClient(t, srv), &out, &errOut,
		importArgs{postIDs: "3001,3002", whenType: 3, howType: 2, schedules: "10,11"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — schedules with when-type 3 should queue and succeed:\nstderr: %s", code, errOut.String())
	}
	if !sawPut.Load() {
		t.Fatalf("no PUT /posts/import reached the server — the request must be issued when schedules pair with when-type 3")
	}
	sched, _ := putBody["schedules_ids"].([]interface{})
	if len(sched) != 2 {
		t.Errorf("schedules_ids on the PUT wire = %v, want [10 11] — the schedules must be sent when when-type is 3", putBody["schedules_ids"])
	}
}
