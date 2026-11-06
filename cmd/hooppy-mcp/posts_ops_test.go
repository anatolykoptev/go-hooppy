package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// postsStub is a Hooppy API stub for the posts write/read tools. batchDelete
// is the status and body POST /posts/batch/delete answers; failDelete lists
// post ids whose DELETE /posts/{id} answers 500; whenTypes sets a post's
// publication_when_type before a move (default 3). It records every request.
type postsStub struct {
	*httptest.Server
	mu          sync.Mutex
	requests    []string
	batchBody   string
	moved       bool
	batchDelete struct {
		status int
		body   string
	}
	failDelete  map[string]bool
	whenTypes   map[int]int
	editFixture []byte
}

func newPostsStub(t *testing.T) *postsStub {
	t.Helper()
	st := &postsStub{failDelete: map[string]bool{}, whenTypes: map[int]int{}}
	st.batchDelete.status, st.batchDelete.body = 200, `{"success":true}`
	st.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.requests = append(st.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/delete":
			b, _ := io.ReadAll(r.Body)
			st.batchBody = string(b)
			w.WriteHeader(st.batchDelete.status)
			w.Write([]byte(st.batchDelete.body))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/posts/"):
			id := strings.TrimPrefix(r.URL.Path, "/posts/")
			if st.failDelete[id] {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"cannot delete post ` + id + `"}`))
				return
			}
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move":
			st.moved = true
			b, _ := io.ReadAll(r.Body)
			st.batchBody = string(b)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			if st.editFixture != nil {
				w.Write(st.editFixture)
				return
			}
			id, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts/"), "/edit"))
			wt := 3
			if v, ok := st.whenTypes[id]; ok {
				wt = v
			}
			date := ""
			if st.moved {
				date = `,"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}`
			}
			w.Write([]byte(`{"id":` + strconv.Itoa(id) + `,"publication_when_type":` + strconv.Itoa(wt) + `,"schedule_id":55576` + date + `}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", st.URL)
	return st
}

func callTool(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := newMCPClientSession(t).CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// TestBatchDeleteTool_F1_InvalidIDsRefusedBeforeAnyRequest is F1 on MCP:
// {ids:"5,0,7"} (the comma-string form an agent prompted against the old
// schema sends), an array holding 0, a negative id and a repeated id are all
// tool errors with ZERO HTTP requests. The refusal is the op's own wording
// ("positive integer"/"listed more than once"), not the library's.
//
// Mutation: drop the `id <= 0` check in validatePostIDs (ops/post_ids.go) and
// the refusal comes from the library ("id must be a positive post id") — the
// "positive integer (got 0)" assertion goes RED.
func TestBatchDeleteTool_F1_InvalidIDsRefusedBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  any
		want string
	}{
		{"zero in a comma string", "5,0,7", "positive integer (got 0)"},
		{"zero in an array", []int{5, 0, 7}, "positive integer (got 0)"},
		{"negative", []int{5, -3}, "positive integer (got -3)"},
		{"repeated", "5,6,5", "listed more than once"},
		{"empty string", "", "at least one post id"},
		{"empty array", []int{}, "at least one post id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newPostsStub(t)
			defer st.Close()
			res := callTool(t, "hooppy_batch_delete_posts", map[string]any{"ids": tc.ids})
			if !res.IsError {
				t.Fatalf("expected a tool error, got: %s", toolResultText(res))
			}
			if !strings.Contains(toolResultText(res), tc.want) {
				t.Errorf("error = %q, want it to contain %q", toolResultText(res), tc.want)
			}
			if len(st.requests) != 0 {
				t.Fatalf("%d request(s) reached the server %v — the refusal must fire before any request", len(st.requests), st.requests)
			}
		})
	}
	// The single-id tool shares the op: id 0 is refused the same way.
	st := newPostsStub(t)
	defer st.Close()
	if res := callTool(t, "hooppy_delete_post", map[string]any{"id": 0}); !res.IsError || len(st.requests) != 0 {
		t.Errorf("hooppy_delete_post id=0: IsError=%v requests=%v, want a tool error and no request", res.IsError, st.requests)
	}
}

// TestBatchDeleteTool_CompleteBatch: both id forms reach the server as the
// SAME request — ids as a comma-joined STRING — and every id is reported
// deleted.
func TestBatchDeleteTool_CompleteBatch(t *testing.T) {
	for _, ids := range []any{[]int{5, 6, 7}, "5,6,7"} {
		st := newPostsStub(t)
		res := callTool(t, "hooppy_batch_delete_posts", map[string]any{"ids": ids})
		st.Close()
		if res.IsError {
			t.Fatalf("ids %v: tool error: %s", ids, toolResultText(res))
		}
		if !reflect.DeepEqual(st.requests, []string{"POST /posts/batch/delete"}) || strings.TrimSpace(st.batchBody) != `{"ids":"5,6,7"}` {
			t.Errorf("ids %v: requests = %v body = %s, want one POST with the ids as a comma-joined string", ids, st.requests, st.batchBody)
		}
		var out struct {
			Success bool  `json:"success"`
			Deleted []int `json:"deleted"`
		}
		if err := json.Unmarshal([]byte(toolResultText(res)), &out); err != nil || !out.Success || !reflect.DeepEqual(out.Deleted, []int{5, 6, 7}) {
			t.Errorf("ids %v: result = %s (err %v), want success with deleted [5 6 7]", ids, toolResultText(res), err)
		}
	}
}

// TestBatchDeleteTool_F2_PartialIsAStatusDiscriminatedResult is F2 on MCP: the
// server refuses the batch and fails post 6 on its own — the result is NOT a
// tool error and NOT a clean success: {status:"partial", result:{deleted:[5,7],
// failed:[{id:6,…}]}}, so an agent knows which ids to leave alone.
//
// Mutation: in DeletePosts (ops/posts_delete.go) return `res, nil` instead of
// `res, &PartialDeleteError{Result: res}` — the result is a plain success
// record with no "status":"partial".
func TestBatchDeleteTool_F2_PartialIsAStatusDiscriminatedResult(t *testing.T) {
	st := newPostsStub(t)
	defer st.Close()
	st.batchDelete.status, st.batchDelete.body = 500, `{"error":"boom"}`
	st.failDelete["6"] = true

	res := callTool(t, "hooppy_batch_delete_posts", map[string]any{"ids": []int{5, 6, 7}})
	if res.IsError {
		t.Fatalf("a partial outcome must not be a tool error: %s", toolResultText(res))
	}
	var out struct {
		Status string `json:"status"`
		Result struct {
			Success bool  `json:"success"`
			Deleted []int `json:"deleted"`
			Failed  []struct {
				ID    int    `json:"id"`
				Error string `json:"error"`
			} `json:"failed"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(toolResultText(res)), &out); err != nil {
		t.Fatalf("result is not JSON: %v: %s", err, toolResultText(res))
	}
	if out.Status != "partial" {
		t.Errorf("status = %q, want \"partial\": %s", out.Status, toolResultText(res))
	}
	if out.Result.Success || !reflect.DeepEqual(out.Result.Deleted, []int{5, 7}) || len(out.Result.Failed) != 1 || out.Result.Failed[0].ID != 6 || !strings.Contains(out.Result.Failed[0].Error, "cannot delete post 6") {
		t.Errorf("result = %+v, want deleted [5 7] and failed [6: cannot delete post 6]", out.Result)
	}
}

// TestDeleteTool_SingleIDUsesDELETE: hooppy_delete_post deletes through
// DELETE /posts/{id} and keeps the {"success": true} field.
func TestDeleteTool_SingleIDUsesDELETE(t *testing.T) {
	st := newPostsStub(t)
	defer st.Close()
	res := callTool(t, "hooppy_delete_post", map[string]any{"id": 9})
	if res.IsError || !strings.Contains(toolResultText(res), `"success": true`) {
		t.Fatalf("result: IsError=%v %s", res.IsError, toolResultText(res))
	}
	if !reflect.DeepEqual(st.requests, []string{"DELETE /posts/9"}) {
		t.Errorf("requests = %v, want one DELETE /posts/9", st.requests)
	}
}

// TestMoveTools_F3_BatchHitsTheWhenTypeGuard is F3 on MCP: a batch holding a
// post that is not schedule-driven (publication_when_type=2) is refused WHOLE
// — a tool error naming the post, and no POST /posts/batch/move, so the
// schedule-driven posts beside it are not moved either. The single-id tool is
// refused the same way (one op, one guard).
//
// Mutation: remove the `c.requireScheduleDriven(ctx, ids)` call in
// BatchMovePosts (posts.go) — the batch POSTs and `st.moved` turns true.
func TestMoveTools_F3_BatchHitsTheWhenTypeGuard(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"hooppy_batch_move_posts", map[string]any{"ids": []int{41, 42, 43}, "to_schedule_id": 55576}},
		{"hooppy_batch_move_posts", map[string]any{"ids": "41,42,43", "to_schedule_id": 55576}},
		{"hooppy_move_post", map[string]any{"id": 42, "to_schedule_id": 55576}},
	} {
		st := newPostsStub(t)
		st.whenTypes[42] = 2
		res := callTool(t, tc.tool, tc.args)
		st.Close()
		if !res.IsError {
			t.Fatalf("%s %v: expected a tool error — the when_type guard must refuse; result: %s", tc.tool, tc.args, toolResultText(res))
		}
		if st.moved {
			t.Errorf("%s %v: POST /posts/batch/move was issued — the guard must refuse the whole batch before the move", tc.tool, tc.args)
		}
		if !strings.Contains(toolResultText(res), "post 42: publication_when_type=2") {
			t.Errorf("%s: error must name post 42 and its when_type: %s", tc.tool, toolResultText(res))
		}
	}
}

// TestMoveTools_InvalidInputRefusedBeforeAnyRequest: ids <= 0, an empty list
// and a target <= 0 are tool errors with zero requests, on both move tools.
func TestMoveTools_InvalidInputRefusedBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"hooppy_batch_move_posts", map[string]any{"ids": []int{5, 0}, "to_schedule_id": 55576}},
		{"hooppy_batch_move_posts", map[string]any{"ids": []int{}, "to_schedule_id": 55576}},
		{"hooppy_batch_move_posts", map[string]any{"ids": []int{5}, "to_schedule_id": 0}},
		{"hooppy_move_post", map[string]any{"id": 0, "to_schedule_id": 55576}},
		{"hooppy_move_post", map[string]any{"id": 5, "to_schedule_id": -5}},
	} {
		st := newPostsStub(t)
		res := callTool(t, tc.tool, tc.args)
		st.Close()
		if !res.IsError || len(st.requests) != 0 {
			t.Errorf("%s %v: IsError=%v requests=%v, want a tool error and no request", tc.tool, tc.args, res.IsError, st.requests)
		}
	}
}

// TestMoveTool_SingleKeepsFlatShape: hooppy_move_post returns the flat
// PostMoveResult (not the batch shape) with the recovered date.
func TestMoveTool_SingleKeepsFlatShape(t *testing.T) {
	st := newPostsStub(t)
	defer st.Close()
	res := callTool(t, "hooppy_move_post", map[string]any{"id": 42, "to_schedule_id": 55576})
	if res.IsError {
		t.Fatalf("tool error: %s", toolResultText(res))
	}
	text := toolResultText(res)
	if strings.Contains(text, `"moved"`) || !strings.Contains(text, `"schedule_id": 55576`) || !strings.Contains(text, "15.01.2027") {
		t.Errorf("want the flat PostMoveResult with schedule_id 55576 and 15.01.2027, got: %s", text)
	}
}

// TestGetPostEditTool_F4_ReturnsTheRecordedEditState is F4's MCP half:
// hooppy_get_post_edit returns the post's editable state as JSON, and every
// key it returns carries the value of the recorded live
// GET /posts/{id}/edit response (testdata/live/post_edit.json) — the same
// oracle the CLI test (`posts edit`) checks, so the two surfaces cannot
// disagree about the same recorded post. The tool is registered through
// ops.Registry, so unregistering it also turns
// TestOpsRegistry_MCPEntriesResolve RED.
//
// Mutation: drop registerGetPostEdit(server) from registerTools
// (cmd/hooppy-mcp/main.go) — the tool call fails and the registry parity
// test reports the tool as not registered.
func TestGetPostEditTool_F4_ReturnsTheRecordedEditState(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/live/post_edit.json")
	if err != nil {
		t.Fatal(err)
	}
	st := newPostsStub(t)
	defer st.Close()
	st.editFixture = fixture

	res := callTool(t, "hooppy_get_post_edit", map[string]any{"id": 42})
	if res.IsError {
		t.Fatalf("tool error: %s", toolResultText(res))
	}
	if !reflect.DeepEqual(st.requests, []string{"GET /posts/42/edit"}) {
		t.Errorf("requests = %v, want one GET /posts/42/edit", st.requests)
	}
	assertSubsetOfRecordedEdit(t, []byte(toolResultText(res)), fixture)

	st.requests = nil
	if res := callTool(t, "hooppy_get_post_edit", map[string]any{"id": 0}); !res.IsError || len(st.requests) != 0 {
		t.Errorf("id=0: IsError=%v requests=%v, want a tool error and no request", res.IsError, st.requests)
	}
}

// assertSubsetOfRecordedEdit: see the identical helper in cmd/hooppy — got
// must be a value-for-value subset of the recorded response and carry the
// load-bearing keys.
func assertSubsetOfRecordedEdit(t *testing.T, got, recorded []byte) {
	t.Helper()
	// Both surfaces print the decoded PostEditResponse, so the output must also
	// equal the fixture decoded into that type and indented the same way.
	var decoded hooppy.PostEditResponse
	if err := json.Unmarshal(recorded, &decoded); err != nil {
		t.Fatal(err)
	}
	want, _ := json.MarshalIndent(decoded, "", "  ")
	if strings.TrimSpace(string(got)) != string(want) {
		t.Errorf("output differs from the recorded response decoded as PostEditResponse:\n got: %s\nwant: %s", got, want)
	}
	var g, r map[string]any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("output is not a JSON object: %v: %s", err, got)
	}
	if err := json.Unmarshal(recorded, &r); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"publication_when_type", "texts", "attachments", "schedule_id", "project_id", "selected_pages_by_source_ids"} {
		if _, ok := g[key]; !ok {
			t.Errorf("output lacks %q", key)
		}
	}
	if path, ok := jsonSubset(g, r, ""); !ok {
		t.Errorf("output differs from the recorded response at %s", path)
	}
}

func jsonSubset(got, want any, path string) (string, bool) {
	switch g := got.(type) {
	case map[string]any:
		w, ok := want.(map[string]any)
		if !ok {
			return path, false
		}
		for k, gv := range g {
			wv, ok := w[k]
			if !ok {
				return path + "." + k, false
			}
			if p, ok := jsonSubset(gv, wv, path+"."+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		w, ok := want.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range g {
			if p, ok := jsonSubset(g[i], w[i], path+"[]"); !ok {
				return p, false
			}
		}
		return "", true
	}
	return path, reflect.DeepEqual(got, want)
}
