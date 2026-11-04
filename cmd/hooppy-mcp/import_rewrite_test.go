package main

import (
	"context"
	"encoding/json"
	"io"
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
// (in-memory MCP transport → handler → ops.ImportSearchPost → resolve+publish →
// httptest stub where every POST /posts returns 500) and asserts IsError=true.
//
// RED-on-revert: revert the op's batch loop to return *PartialPostError on
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
			"search_post_ids":       []int{11, 22, 33},
			"publication_when_type": 1,
			"publication_how_type":  1,
			"selected_pages_ids":    []int{1},
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
			"search_post_ids":       []int{11, 22, 33},
			"publication_when_type": 1,
			"publication_how_type":  1,
			"selected_pages_ids":    []int{1},
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

// F20 — hooppy_import_search_post refuses schedules_ids with
// publication_when_type=1 BEFORE any request reaches the server (assert
// request count zero), and refuses an explicitly-given when-type 0 and 4. The
// round-3 BLOCKER shipped the import handler with neither guard: schedules_ids
// with when-type 1 was silently dropped (the post published to pages NOW), and
// when-type 0 fell through `default:` into the same publish-now branch. The
// ops layer owns both guards now — and an OMITTED when-type defaults to 3 (the
// safe queued path), never 0.
//
// This drives the real handler end to end (in-memory MCP transport → handler
// → ops.ImportSearchPost validation → errResult) with a stub server that counts
// requests. The guard fires inside the builder, BEFORE client() is called, so
// no request reaches the server.
//
// RED-on-revert: delete either guard from ops.resolveSearchTarget → the op
// proceeds to the request → requests reach the server → reqCount > 0 → this
// test fails.
func TestF20_MCP_ImportSearchPost_GuardsBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{
			"schedules_with_when_type_1",
			map[string]any{
				"search_post_id":        2001,
				"publication_when_type": 1,
				"selected_pages_ids":    []int{123},
				"schedules_ids":         []int{10, 11},
			},
		},
		{
			"when_type_0_omitted_field",
			map[string]any{
				"search_post_id":        2001,
				"publication_when_type": 0,
				"selected_pages_ids":    []int{123},
			},
		},
		{
			"when_type_4_out_of_range",
			map[string]any{
				"search_post_id":        2001,
				"publication_when_type": 4,
				"selected_pages_ids":    []int{123},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reqCount int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&reqCount, 1)
				w.Write([]byte(`{"id":5001}`))
			}))
			defer srv.Close()

			t.Setenv("HOOPPY_TOKEN", "test-token")
			t.Setenv("HOOPPY_BASE_URL", srv.URL)

			cs := newMCPClientSession(t)
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "hooppy_import_search_post",
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Fatalf("IsError=false — the guard must fire and return an error result for %s; result=%s", tc.name, toolResultText(res))
			}
			if got := atomic.LoadInt32(&reqCount); got != 0 {
				t.Fatalf("reqCount = %d, want 0 — the guard must fire BEFORE any request reaches the server (an irreversible publish would have happened for %s); stderr/result=%s", got, tc.name, toolResultText(res))
			}
		})
	}
}

// F1 (task spec) — `search import --when-type 3 --to <page>` must refuse
// BEFORE any HTTP call, on both surfaces. On MCP the journey is
// hooppy_import_search_post with publication_when_type:3 +
// selected_pages_ids set: today it silently published to the schedule's
// pages, discarding the caller's target. The op owns the guard; this test
// drives the real tool-call path (in-memory transport → adapter → op) and
// counts requests.
//
// Mutation: delete the when-type-3/pages check in
// ops/publish_search.go (resolveSearchTarget). The op proceeds to the
// request → reqCount > 0 → this test goes RED. Its CLI sibling is
// TestF1_CLI_ImportPagesUnderWhenType3 in cmd/hooppy/import_text_test.go.
func TestF1_MCP_ImportPagesUnderWhenType3(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()

	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_import_search_post",
		Arguments: map[string]any{
			"search_post_id":        2001,
			"publication_when_type": 3,
			"schedules_ids":         []int{10},
			"selected_pages_ids":    []int{99},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError=false — selected_pages_ids under publication_when_type 3 must be refused (issue #139); result=%s", toolResultText(res))
	}
	if !strings.Contains(toolResultText(res), "selected_pages_ids") {
		t.Errorf("error should name selected_pages_ids so an agent can correct it, got: %s", toolResultText(res))
	}
	if got := atomic.LoadInt32(&reqCount); got != 0 {
		t.Fatalf("reqCount = %d, want 0 — the refusal must fire BEFORE any request (a request means the post published to the wrong target, issue #139)", got)
	}
}

// F2 (task spec) — publish_date/publish_hours/publish_minutes are only
// meaningful with publication_when_type 2. Under 1 or 3 they were silently
// dropped (issue #140): the post published NOW or on the schedule's own
// slots — a DIFFERENT time than the caller asked. Both refusal shapes drive
// the real tool path and assert zero requests.
//
// Mutation: delete the when-type!=2 date-flags check in
// ops/publish_search.go (resolveSearchTarget) → the op proceeds →
// reqCount > 0 → both subtests go RED. CLI sibling:
// TestF2_CLI_ImportDateUnderWhenType1And3 in cmd/hooppy/import_text_test.go.
func TestF2_MCP_ImportDateUnderWhenType1And3(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{
			"date_under_when_type_1",
			map[string]any{
				"search_post_id":        2001,
				"publication_when_type": 1,
				"selected_pages_ids":    []int{123},
				"publish_date":          "01.01.2027",
			},
		},
		{
			"date_under_when_type_3",
			map[string]any{
				"search_post_id":        2001,
				"publication_when_type": 3,
				"schedules_ids":         []int{10},
				"publish_date":          "01.01.2027",
				"publish_hours":         "10",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reqCount int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&reqCount, 1)
				w.Write([]byte(`{"id":5001}`))
			}))
			defer srv.Close()

			t.Setenv("HOOPPY_TOKEN", "test-token")
			t.Setenv("HOOPPY_BASE_URL", srv.URL)

			cs := newMCPClientSession(t)
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "hooppy_import_search_post",
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Fatalf("IsError=false — date flags under publication_when_type !=2 must be refused (issue #140); result=%s", toolResultText(res))
			}
			if !strings.Contains(toolResultText(res), "publish_") {
				t.Errorf("error should name the publish_* date fields so an agent can correct it, got: %s", toolResultText(res))
			}
			if got := atomic.LoadInt32(&reqCount); got != 0 {
				t.Fatalf("reqCount = %d, want 0 — the refusal must fire BEFORE any request (issue #140)", got)
			}
		})
	}
}

// F3 (task spec) — a `search import --when-type 2 --date ... --to <page>`
// produces THE SAME request body on both surfaces. The op builds the body,
// so byte-equality is the assertion both sides run against the same literal:
// this test asserts it from the MCP tool's recorded POST /posts request; the
// ops test (TestF3_ImportWhenType2_DatePages_GoldenBody) and the CLI runner
// test (TestF3_CLI_ImportBodyMatchesMCPTool) assert the identical literal.
//
// Mutation: drop publication_date from the op's target/body → all three go
// RED.
func TestF3_MCP_ImportBodyMatchesCLI(t *testing.T) {
	// Author-written expectation, derived from openapi.yaml POST /posts
	// (publication_how_type, line 2020-2023): direct pages (selected_pages_ids)
	// under when-type 2 is how_type 1; 2 would need a project_id. Not a
	// recorded server exchange. Same literal as ops.goldenImportWhenType2Body.
	const golden = `{"as_copy":1,"publication_when_type":2,"publication_how_type":1,"publication_where_type":1,"selected_pages_ids":[99],"schedules_ids":[],"publication_date":{"date":"01.02.2027","hours":"10","minutes":"30"},"texts":[{"text":"x","source_id":0}],"attachments":[],"ids":"1001"}`

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"1001","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			gotBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":7001}`))
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
			"search_post_id":        1001,
			"publication_when_type": 2,
			"selected_pages_ids":    []int{99},
			"publish_date":          "01.02.2027",
			"publish_hours":         "10",
			"publish_minutes":       "30",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError=true for a valid when-type-2 import: %s", toolResultText(res))
	}
	if gotBody == nil {
		t.Fatal("no POST /posts request reached the stub")
	}
	if got := strings.TrimSuffix(string(gotBody), "\n"); got != golden {
		t.Errorf("MCP tool POST /posts body =\n%s\nwant byte-equal to the CLI's body\n%s\n— both surfaces emit the identical body because the op builds it (F3)", got, golden)
	}
}
