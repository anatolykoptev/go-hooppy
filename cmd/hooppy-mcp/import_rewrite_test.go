package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// F19 — the MCP tools report a TOTAL batch failure as an error result
// (IsError=true), NOT as a successful tool call whose result.ids is empty.
// Combined with MAJOR 1 (resolvePublishBatch returns a plain error, not
// *PartialPostError, on all-failed), the handler's `errors.As(&ppe)` does NOT
// match for a total failure, so the plain error falls through to errResult
// (IsError=true). An agent-facing surface that re-runs on ambiguity would
// otherwise treat an empty all-failed batch as success and duplicate every
// post on the next call (MAJOR 5).
//
// This test drives the real hooppy_import_search_post handler end to end
// (in-memory MCP transport → handler → ImportSearchPost → resolve+publish →
// httptest stub where every POST /posts returns 500) and asserts IsError=true.
//
// RED-on-revert: revert resolvePublishBatch to return *PartialPostError on
// all-failed → the handler's errors.As(&ppe) matches → it returns
// jsonResult(...) (IsError=false) → this test fails. (Also RED if the handler
// is changed to return jsonResult for a plain error.)
func TestF19_MCP_TotalBatchFailure_IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"x","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			// Every publish fails — a total batch failure.
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"boom"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_import_search_post",
		Arguments: map[string]any{
			"search_post_ids":       "11,22,33",
			"publication_when_type": 1,
			"publication_how_type":  1,
			"selected_pages_ids":    "1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool hooppy_import_search_post: %v", err)
	}
	if !res.IsError {
		t.Fatalf("MCP result IsError=false for a total batch failure — an all-failed batch MUST be reported as an error (MAJOR 5), not a successful tool call whose result.ids is empty; result=%s", toolResultText(res))
	}
	// The error text must name the total failure (not an empty success body).
	if !strings.Contains(toolResultText(res), "every post") {
		t.Errorf("MCP error text does not name the total failure — got %q; want it to mention \"every post\"", toolResultText(res))
	}
}

// TestMCP_PartialBatch_StatusDiscriminator pins the MAJOR 5 status
// discriminator: a PARTIAL batch (some succeeded, some failed) returns a
// non-error result carrying a `status: "partial"` field, so an agent can tell
// partial from clean success WITHOUT parsing the error string. A total
// failure is IsError=true (F19); clean success is the bare PostIDResponse;
// partial is {status:"partial", result, partial_error}.
func TestMCP_PartialBatch_StatusDiscriminator(t *testing.T) {
	var postCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"x","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			n := atomic.AddInt32(&postCount, 1)
			if n == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message":"boom"}`))
				return
			}
			w.Write([]byte(`{"id":90001}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_import_search_post",
		Arguments: map[string]any{
			"search_post_ids":       "11,22,33",
			"publication_when_type": 1,
			"publication_how_type":  1,
			"selected_pages_ids":    "1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// Partial is NOT an error result — it carries the ids that landed.
	if res.IsError {
		t.Fatalf("MCP result IsError=true for a PARTIAL batch — partial must be a non-error result carrying the successful ids (MAJOR 5); result=%s", toolResultText(res))
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(toolResultText(res)), &body); err != nil {
		t.Fatalf("result not valid JSON: %v\n%s", err, toolResultText(res))
	}
	if got, _ := body["status"].(string); got != "partial" {
		t.Fatalf("result.status = %q, want \"partial\" — a partial batch MUST carry a status discriminator so an agent can tell partial from clean success (MAJOR 5); result=%s", got, toolResultText(res))
	}
	if _, ok := body["partial_error"]; !ok {
		t.Errorf("result has no partial_error field — the partial failure detail must be carried alongside the status discriminator")
	}
}
