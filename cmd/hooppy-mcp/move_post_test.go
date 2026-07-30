package main

import (
	"context"
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
// hooppy_move_post handler end to end and asserts the PUT body carries
// schedule_id = the TARGET (55576), not the original (7) — the whole point
// of a move. This is the regression guard for a handler pointed back at
// UpdatePost (which would echo edit.ScheduleID, not override it).
func TestMovePostTool_WireBodyOverridesScheduleID(t *testing.T) {
	var putBody []byte
	var putCalled bool
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
		case http.MethodPut:
			putCalled = true
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
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
	if !putCalled {
		t.Fatal("PUT /posts/{id} was never issued — MovePost did not run")
	}
	if !strings.Contains(string(putBody), `"schedule_id":55576`) {
		t.Errorf("PUT body does not carry schedule_id 55576 (the target) — a handler pointed at UpdatePost would echo edit.ScheduleID (7); body: %s", putBody)
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

// TestListSchedulePostsTool_WireIssuesOneRequest drives the real
// hooppy_list_schedule_posts handler end to end and asserts exactly ONE
// request is issued (no paged walk) — the issue #106 contract.
func TestListSchedulePostsTool_WireIssuesOneRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"posts_by_days":{"15.01.2027":[{"id":1,"text":"a"}]},"total_rows":1,"rows_limit":1000,"is_has_more":true}`))
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

// Ensure the mcp import is referenced (used via CallToolParams above).
var _ = (*mcp.CallToolParams)(nil)
