package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMovePostTool_RegisteredAndReachable verifies the three new tools
// (hooppy_move_post, hooppy_batch_move_posts, hooppy_list_schedule_posts)
// are wired into registerTools — a tool defined but never registered is
// the failure class this repo has shipped (issue #49). Each tool's
// description MUST name the publication_date reporting so an LLM caller
// knows the move's tail-of-queue delay is visible in the result.
func TestMovePostTool_RegisteredAndReachable(t *testing.T) {
	cs := newMCPClientSession(t)

	for _, name := range []string{"hooppy_move_post", "hooppy_batch_move_posts", "hooppy_list_schedule_posts"} {
		tool := findTool(t, cs, name)
		if tool.Description == "" {
			t.Errorf("%s has an empty description — the LLM has nothing to choose it by", name)
		}
	}

	// The move tools' descriptions MUST name the publication_date /
	// tail-of-queue delay so an LLM caller knows the delay is visible.
	moveTool := findTool(t, cs, "hooppy_move_post")
	if !strings.Contains(moveTool.Description, "publication_date") {
		t.Errorf("hooppy_move_post description must name publication_date — the LLM caller needs to know the tail-of-queue delay is visible in the result; missing \"publication_date\": %q", moveTool.Description)
	}
	if !strings.Contains(moveTool.Description, "TAIL") {
		t.Errorf("hooppy_move_post description must name the TAIL re-slotting — this is the load-bearing behaviour a move exists to expose; missing \"TAIL\": %q", moveTool.Description)
	}
	batchTool := findTool(t, cs, "hooppy_batch_move_posts")
	if !strings.Contains(batchTool.Description, "publication_date") {
		t.Errorf("hooppy_batch_move_posts description must name publication_date — the per-post delay is the load-bearing output; missing \"publication_date\": %q", batchTool.Description)
	}

	// The queue tool's description MUST name total_rows (queue depth) and
	// booked-until so an LLM caller knows what the read surfaces.
	queueTool := findTool(t, cs, "hooppy_list_schedule_posts")
	if !strings.Contains(queueTool.Description, "total_rows") {
		t.Errorf("hooppy_list_schedule_posts description must name total_rows — the queue depth is the load-bearing output; missing \"total_rows\": %q", queueTool.Description)
	}
	if !strings.Contains(queueTool.Description, "booked-until") {
		t.Errorf("hooppy_list_schedule_posts description must name booked-until — the LAST day with posts is the load-bearing output for deciding whether to move posts in; missing \"booked-until\": %q", queueTool.Description)
	}
}

// TestMovePostTool_WireBodyOverridesScheduleID drives the real
// hooppy_move_post handler end to end and asserts the POST /posts/batch/move
// body carries schedule_id = the TARGET (55576), not the original (7) — the
// whole point of a move. This is the regression guard for a handler pointed
// back at UpdatePost (which would echo edit.ScheduleID, not override it).
// It also asserts NO PUT is issued — the move uses the batch endpoint.
func TestMovePostTool_WireBodyOverridesScheduleID(t *testing.T) {
	var postBody []byte
	var postCalled, putCalled bool
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(`{"id":42,"publication_when_type":3,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[{"text":"old","source_id":0}],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":7,"project_id":0}`))
			} else {
				w.Write([]byte(`{"id":42,"publication_when_type":3,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[{"text":"old","source_id":0}],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":55576,"project_id":0,"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}}`))
			}
		case http.MethodPost:
			if r.URL.Path != "/posts/batch/move" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			postCalled = true
			postBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		case http.MethodPut:
			putCalled = true
		}
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_move_post",
		Arguments: map[string]any{
			"id":             42,
			"to_schedule_id": 55576,
		},
	})
	if err != nil {
		t.Fatalf("CallTool hooppy_move_post: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued — MovePost did not run")
	}
	if putCalled {
		t.Fatal("PUT /posts/{id} was issued — MovePost must NOT use the full-state PUT; it moves via POST /posts/batch/move")
	}
	if !strings.Contains(string(postBody), `"schedule_id":55576`) {
		t.Errorf("POST body does not carry schedule_id 55576 (the target) — a handler pointed at UpdatePost would echo edit.ScheduleID (7); body: %s", postBody)
	}
	// The result text MUST carry the recovered publication_date.
	resultText := toolResultText(res)
	if !strings.Contains(resultText, "15.01.2027") {
		t.Errorf("tool result missing the recovered publication_date 15.01.2027 — the LLM caller needs the tail-of-queue delay: %s", resultText)
	}
}

// TestBatchMovePostsTool_WireBodyPostsIDsIsString drives the real
// hooppy_batch_move_posts handler end to end and asserts the POST body
// carries posts_ids as a comma-joined STRING ("1,2,3"), not a JSON array —
// the live server 500s on an array (measured 2026-07-30, issue #105).
func TestBatchMovePostsTool_WireBodyPostsIDsIsString(t *testing.T) {
	var postBody []byte
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			postCalled = true
			postBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"id":0,"publication_when_type":3,"publication_how_type":1,"publication_where_type":1,"created_by":1,"texts":[],"attachments":[],"selected_pages_by_source_ids":{},"all_pages_ids_by_source_ids":{},"schedule_id":55576,"project_id":0,"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}}`))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_batch_move_posts",
		Arguments: map[string]any{
			"ids":            []int{1, 2, 3},
			"to_schedule_id": 55576,
		},
	})
	if err != nil {
		t.Fatalf("CallTool hooppy_batch_move_posts: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued")
	}
	bodyStr := string(postBody)
	// posts_ids MUST be a JSON string "1,2,3", not a JSON array [1,2,3].
	if !strings.Contains(bodyStr, `"posts_ids":"1,2,3"`) {
		t.Errorf("POST body does not carry posts_ids as a comma-joined STRING — the live server 500s on a JSON array (issue #105); body: %s", postBody)
	}
	if strings.Contains(bodyStr, `"posts_ids":[`) {
		t.Fatalf("POST body carries posts_ids as a JSON ARRAY — the live server throws ErrorException: explode(...) and returns 500 (issue #105); body: %s", postBody)
	}
}

// TestBatchMovePostsTool_SingleIDBatch_ShapeIsMovedArray is the MAJOR-3
// MCP-side guard (review round 4): a single-id batch (`ids=[42]`) MUST
// return a BatchMovePostsResult with a `moved` array of len 1 — the SAME
// top-level shape as the multi-id case. The prior shape returned a flat
// PostMoveResult, so a consumer reading .moved[] got nothing for one id.
// The single-id routing (for the when_type guard) is kept; only the OUTPUT
// SHAPE is normalised.
//
// RED-on-revert: revert the single-id branch to `return jsonResult(resp)`
// (flat PostMoveResult) and the result JSON has no "moved" key — the
// assertion fails.
func TestBatchMovePostsTool_SingleIDBatch_ShapeIsMovedArray(t *testing.T) {
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
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_batch_move_posts",
		Arguments: map[string]any{
			"ids":            []int{42},
			"to_schedule_id": 55576,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	resultText := toolResultText(res)
	var env map[string]any
	if err := json.Unmarshal([]byte(resultText), &env); err != nil {
		t.Fatalf("result is not valid JSON: %v\ntext: %s", err, resultText)
	}
	moved, ok := env["moved"].([]any)
	if !ok {
		t.Fatalf("single-id batch result has no \"moved\" array — the shape MUST be BatchMovePostsResult (same as multi-id), not the flat PostMoveResult: %s", resultText)
	}
	if len(moved) != 1 {
		t.Fatalf("moved array len = %d, want 1 — a single-id batch wraps the one entry into moved[]: %s", len(moved), resultText)
	}
	entry, _ := moved[0].(map[string]any)
	if entry == nil || entry["id"] != float64(42) {
		t.Errorf("moved[0].id = %v, want 42: %s", entry["id"], resultText)
	}
	if !strings.Contains(resultText, "15.01.2027") {
		t.Errorf("result missing the recovered publication_date 15.01.2027 in moved[]: %s", resultText)
	}
}

// TestListSchedulePostsTool_WireIssuesOneRequest drives the real
// hooppy_list_schedule_posts handler end to end and asserts exactly ONE
// request is issued (no paged walk) — the issue #106 contract.
func TestListSchedulePostsTool_WireIssuesOneRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"posts_by_days":{"15.01.2027":[{"id":1,"text":"a"}]},"total_rows":1,"rows_limit":200,"is_has_more":true}`))
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_list_schedule_posts",
		Arguments: map[string]any{
			"schedule_id": 55576,
		},
	})
	if err != nil {
		t.Fatalf("CallTool hooppy_list_schedule_posts: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	if calls != 1 {
		t.Errorf("tool issued %d requests, want exactly 1 — issue #106 forbids a paged walk even when is_has_more is true", calls)
	}
	// The result MUST carry total_rows and the booked-until day key.
	resultText := toolResultText(res)
	if !strings.Contains(resultText, "total_rows") {
		t.Errorf("tool result missing total_rows — the queue depth is the load-bearing output: %s", resultText)
	}
	if !strings.Contains(resultText, "15.01.2027") {
		t.Errorf("tool result missing the day key 15.01.2027 — the booked-until date is the load-bearing output: %s", resultText)
	}
}

// TestListSchedulePostsTool_TruncationWarningIsStructuredData is the MCP
// fail-closed-on-truncation guard (item A, review round 4 MINOR-4): the CLI
// exits non-zero; the MCP tool MUST also signal truncation — an agent reads
// MCP, where there is no exit code. The signal travels as a STRUCTURED
// `warning` field on a VALID-JSON envelope (not a prose prefix that made
// the truncated result unparseable). The data is still present (total_rows
// is the real depth); the warning names the recovery levers
// (date_from/date_to, page).
//
// RED-on-revert: drop the IsHasMore warning branch and the envelope has no
// `warning` field — the assertions fail. Revert to the prose-prefix shape
// and the result is not valid JSON — the json.Unmarshal assertion fails.
func TestListSchedulePostsTool_TruncationWarningIsStructuredData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posts_by_days":{"15.01.2027":[{"id":1}]},"total_rows":500,"rows_limit":200,"is_has_more":true}`))
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_list_schedule_posts",
		Arguments: map[string]any{
			"schedule_id": 55576,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// The result MUST NOT be an error (the data is still useful) — the
	// warning is a structured field, not IsError.
	if res.IsError {
		t.Fatalf("tool returned IsError=true for a truncated result — the data is still useful; the warning should be a structured field, not an error: %s", toolResultText(res))
	}
	resultText := toolResultText(res)
	// The result MUST be valid JSON (the prior prose-prefix shape was NOT —
	// a parsing agent could not read it). Unmarshal into a map so the
	// envelope shape is asserted without depending on field order.
	var env map[string]any
	if err := json.Unmarshal([]byte(resultText), &env); err != nil {
		t.Fatalf("result is not valid JSON — the truncation signal must travel as a structured `warning` field on a JSON envelope, not a prose prefix: %v\ntext: %s", err, resultText)
	}
	warning, ok := env["warning"].(string)
	if !ok || warning == "" {
		t.Fatalf("result JSON has no non-empty `warning` field — the MCP tool MUST signal truncation as structured data: %s", resultText)
	}
	if !strings.Contains(warning, "PARTIAL") {
		t.Errorf("warning field missing \"PARTIAL\" — the warning must name the truncation: %s", warning)
	}
	if !strings.Contains(warning, "is_has_more") {
		t.Errorf("warning field missing \"is_has_more\" — the warning must name the signal: %s", warning)
	}
	// The data MUST still be present — total_rows is the real depth.
	if _, ok := env["total_rows"]; !ok {
		t.Errorf("result JSON missing total_rows — the data must still be present alongside the warning: %s", resultText)
	}
	if _, ok := env["posts_by_days"]; !ok {
		t.Errorf("result JSON missing posts_by_days — the data must still be present alongside the warning: %s", resultText)
	}
}

// TestListSchedulePostsTool_PageOverrunWarningIsStructuredData covers the
// page-overrun branch on the MCP side. The CLI half has
// TestRunScheduleQueue_PagePastEnd_WarnsAndExits2; without this test the MCP
// half was unguarded, and neutralising its `case in.Page > 0 && …` branch left
// the whole cmd/hooppy-mcp package green — the #81 class (one front-end
// guarded, the sibling not) landing on the fix for that very class.
//
// Measured live 2026-07-30: page=2 and page=99 against a 96-row schedule both
// answer 200 with total_rows=96 and ZERO day keys. So total_rows is the
// collection total and does not change with paging — `len(posts_by_days) == 0
// && total_rows > 0` is the only signal that detects an overrun, which is why
// a missing guard here reads as a complete empty calendar.
func TestListSchedulePostsTool_PageOverrunWarningIsStructuredData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The live shape of a page past the end: the collection total is
		// unchanged, the calendar is empty, and is_has_more is FALSE — so the
		// truncation branch cannot catch this one.
		w.Write([]byte(`{"posts_by_days":{},"total_rows":96,"rows_limit":200,"is_has_more":false}`))
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_list_schedule_posts",
		Arguments: map[string]any{
			"schedule_id": 55576,
			"page":        2,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned IsError=true for a page overrun — total_rows is still the real depth, so the data is useful; the signal belongs in a structured field: %s", toolResultText(res))
	}
	resultText := toolResultText(res)
	var env map[string]any
	if err := json.Unmarshal([]byte(resultText), &env); err != nil {
		t.Fatalf("result is not valid JSON — the overrun signal must travel as a structured `warning` field: %v\ntext: %s", err, resultText)
	}
	warning, ok := env["warning"].(string)
	if !ok || warning == "" {
		t.Fatalf("result JSON has no non-empty `warning` field — a page past the end is NOT a complete result, and an agent walking pages to recover a truncation would read this as done: %s", resultText)
	}
	if !strings.Contains(warning, "PARTIAL") {
		t.Errorf("warning missing \"PARTIAL\": %s", warning)
	}
	if !strings.Contains(warning, "past the end") {
		t.Errorf("warning must say the page is past the end, so the reader knows to lower it: %s", warning)
	}
	// The depth must survive: total_rows is the answer the caller still needs.
	if got, ok := env["total_rows"].(float64); !ok || int(got) != 96 {
		t.Errorf("total_rows = %v, want 96 — the collection total must still be reported alongside the warning: %s", env["total_rows"], resultText)
	}
	// is_has_more must be present AND false. This guards FIXTURE DRIFT: if the
	// stub were edited to true, this test would pass through the truncation
	// branch and quietly stop guarding the overrun one. (It does NOT guard
	// against folding the two branches into one condition — that fold would
	// leave the fixture false and the warning non-empty, so this test would
	// still pass. The branch discriminator is the "past the end" assertion
	// above.) `ok && got` would pass if the key vanished from the envelope,
	// so assert presence too.
	if got, ok := env["is_has_more"].(bool); !ok || got {
		t.Errorf("fixture drift: is_has_more must be present and false for the overrun case (got %v, present=%v); otherwise this test passes via the truncation branch", env["is_has_more"], ok)
	}
}

// TestListSchedulePostsTool_PageFieldPassedToEndpoint verifies the page
// field on the MCP input is forwarded to the endpoint (item B).
func TestListSchedulePostsTool_PageFieldPassedToEndpoint(t *testing.T) {
	var gotPage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPage = r.URL.Query().Get("page")
		w.Write([]byte(`{"posts_by_days":{},"total_rows":0,"rows_limit":200,"is_has_more":false}`))
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_list_schedule_posts",
		Arguments: map[string]any{
			"schedule_id": 55576,
			"page":        3,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if gotPage != "3" {
		t.Errorf("page query param = %q, want \"3\" — the page field must be forwarded to the endpoint", gotPage)
	}
}

// Ensure the mcp import is referenced (used via CallToolParams above).
var _ = (*mcp.CallToolParams)(nil)

// TestBatchMovePostsTool_SingleIDBatch_RoutesToMovePost is the single-id
// routing guard for the MCP batch handler (item E): a single-id batch
// (`hooppy_batch_move_posts` with ids=[42]) MUST route to MovePost so the
// when_type guard fires — closing the asymmetry where `hooppy_move_post`
// guards when_type but `hooppy_batch_move_posts` with a single id did not.
// The signal that routing happened is the when_type guard's GET
// /posts/{id}/edit: the batch path does NOT issue a pre-move GET, the
// single-post path does.
//
// RED-on-revert: drop the `len(in.IDs) == 1` routing branch in the batch
// handler and the batch path runs — no pre-move GET is issued and the POST
// runs without the when_type guard.
func TestBatchMovePostsTool_SingleIDBatch_RoutesToMovePost(t *testing.T) {
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
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_batch_move_posts",
		Arguments: map[string]any{
			"ids":            []int{42},
			"to_schedule_id": 55576,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// The when_type guard refuses a non-schedule post → IsError=true.
	if !res.IsError {
		t.Fatalf("expected IsError=true — the when_type guard (when_type=2) must refuse the move; result: %s", toolResultText(res))
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
