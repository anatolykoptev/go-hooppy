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

// publishStub serves the resolve GET and the publish POST for one scraped
// post and records every POST /posts body plus the total request count.
func publishStub(t *testing.T) (posts *[][]byte, reqs *int32) {
	t.Helper()
	var bodies [][]byte
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Write([]byte(`{"id":"1001","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			b, _ := io.ReadAll(r.Body)
			bodies = append(bodies, b)
			w.Write([]byte(`{"id":7001}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			w.Write([]byte(`{"data":[],"meta":{"current_page":1,"last_page":1,"per_page":20,"total":0}}`))
		default:
			w.Write([]byte(`{"id":7001}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)
	return &bodies, &n
}

// F1 (MCP) — an OMITTED publication_when_type defaults to 3 (by schedule),
// the safe default: a published post cannot be un-published.
//
// Mutation: ops/publish_search.go:168 `derefInt(t.PublicationWhenType, 3)`
// 3 → 1. Then schedules_ids alone becomes when-type 1 + schedules (refused,
// no POST → IsError, this test fails) and selected_pages_ids alone becomes a
// publish-NOW (a request reaches the stub → the second test fails).
func TestF1_MCP_OmittedWhenTypeDefaultsToSchedule(t *testing.T) {
	posts, _ := publishStub(t)
	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hooppy_import_search_post",
		Arguments: map[string]any{"search_post_id": 1001, "schedules_ids": []int{10, 11}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError=true — an omitted publication_when_type with schedules_ids must queue by schedule: %s", toolResultText(res))
	}
	if len(*posts) != 1 {
		t.Fatalf("POST count = %d, want 1", len(*posts))
	}
	var body map[string]any
	if err := json.Unmarshal((*posts)[0], &body); err != nil {
		t.Fatalf("POST body is not JSON: %v", err)
	}
	if got := body["publication_when_type"]; got != float64(3) {
		t.Errorf("publication_when_type = %v, want 3 — an omitted when-type must default to by-schedule, never publish-now (1)", got)
	}
}

func TestF1_MCP_OmittedWhenTypeWithPagesIsRefused(t *testing.T) {
	_, reqs := publishStub(t)
	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hooppy_import_search_post",
		Arguments: map[string]any{"search_post_id": 1001, "selected_pages_ids": []int{99}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError=false — selected_pages_ids with an omitted when-type (=3) must be refused (issue #139); result=%s", toolResultText(res))
	}
	if got := atomic.LoadInt32(reqs); got != 0 {
		t.Fatalf("reqCount = %d, want 0 — the refusal must fire BEFORE any request; a request here is a publish-NOW the caller never asked for", got)
	}
}

// F3 (finding 3) — the MCP wire break. These fields were comma strings
// ("10,11") and became int arrays; an agent still sending the string form must
// reach the op with the SAME list, so the two forms produce byte-equal POST
// bodies. Covers all three id-list fields (one call per form).
//
// Mutation: drop the string branch in ops.IDList.UnmarshalJSON (or the
// string → array pre-pass in normalizeIDListArgs) → the string call is
// refused by the schema → IsError → this test goes RED.
func TestF3_MCP_IDListStringAndArrayAreByteEqual(t *testing.T) {
	call := func(args map[string]any) []byte {
		t.Helper()
		posts, _ := publishStub(t)
		cs := newMCPClientSession(t)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "hooppy_import_search_post", Arguments: args})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("IsError=true for %v: %s", args, toolResultText(res))
		}
		if len(*posts) == 0 {
			t.Fatalf("no POST /posts reached the stub for %v", args)
		}
		return (*posts)[0]
	}

	bySchedule := func(v any) []byte {
		return call(map[string]any{"search_post_id": 1001, "schedules_ids": v})
	}
	arr, str := bySchedule([]int{10, 11}), bySchedule("10,11")
	if string(arr) != string(str) {
		t.Errorf("schedules_ids \"10,11\" body =\n%s\nwant byte-equal to [10,11] body\n%s", str, arr)
	}
	if !strings.Contains(string(str), `"schedules_ids":[10,11]`) {
		t.Errorf("schedules_ids \"10,11\" did not reach the wire as [10,11]: %s", str)
	}

	byPages := func(v any) []byte {
		return call(map[string]any{"search_post_id": 1001, "publication_when_type": 1, "selected_pages_ids": v})
	}
	if a, s := byPages([]int{99, 100}), byPages("99,100"); string(a) != string(s) {
		t.Errorf("selected_pages_ids \"99,100\" body =\n%s\nwant byte-equal to [99,100] body\n%s", s, a)
	}

	// search_post_ids: the batch form, caller order preserved. Two POSTs each;
	// compare them pairwise.
	batch := func(v any) [][]byte {
		posts, _ := publishStub(t)
		cs := newMCPClientSession(t)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "hooppy_import_search_post",
			Arguments: map[string]any{"search_post_ids": v, "schedules_ids": []int{10}},
		})
		if err != nil || res.IsError {
			t.Fatalf("batch call(%v): err=%v result=%s", v, err, toolResultText(res))
		}
		return *posts
	}
	ba, bs := batch([]int{2002, 2001}), batch("2002,2001")
	if len(ba) != 2 || len(bs) != 2 {
		t.Fatalf("batch POST counts = %d / %d, want 2 / 2", len(ba), len(bs))
	}
	for i := range ba {
		if string(ba[i]) != string(bs[i]) {
			t.Errorf("search_post_ids \"2002,2001\" POST %d =\n%s\nwant byte-equal to [2002,2001] body\n%s", i, bs[i], ba[i])
		}
	}
}

// The advertised schema must say BOTH forms are accepted — an agent reads it,
// and a schema that only advertised the array would send it back to the new
// form while the old prompts keep working by accident.
func TestMCP_IDListFieldsAdvertiseArrayAndString(t *testing.T) {
	cs := newMCPClientSession(t)
	for _, tool := range []string{"hooppy_import_search_post", "hooppy_rewrite_search_post", "hooppy_copy_search_post"} {
		schema := decodeAdvertisedSchema(t, findTool(t, cs, tool))
		props, _ := schema["properties"].(map[string]any)
		for _, field := range []string{"selected_pages_ids", "schedules_ids", "search_post_ids"} {
			p, ok := props[field].(map[string]any)
			if !ok {
				if field == "search_post_ids" && tool == "hooppy_copy_search_post" {
					continue // copy is single-post: no batch field
				}
				t.Errorf("%s: property %s missing", tool, field)
				continue
			}
			raw, _ := json.Marshal(p)
			var kinds []string
			alts, _ := p["oneOf"].([]any)
			for _, a := range alts {
				if m, ok := a.(map[string]any); ok {
					if ty, _ := m["type"].(string); ty != "" {
						kinds = append(kinds, ty)
					}
				}
			}
			if strings.Join(kinds, ",") != "array,string" {
				t.Errorf("%s.%s schema = %s, want oneOf [array, string] (an int array OR a comma-separated string)", tool, field, raw)
			}
			if d, _ := p["description"].(string); d == "" {
				t.Errorf("%s.%s lost its description", tool, field)
			}
		}
		if ap, ok := schema["additionalProperties"]; !ok || ap != false {
			t.Errorf("%s: schema no longer closed (additionalProperties = %v)", tool, schema["additionalProperties"])
		}
	}
}

// Finding 6 — an all-failed batch is an error result, and it must still carry
// the per-post record: which posts were attempted and why each failed. The
// op builds that record on EVERY path; the handler used to drop it with
// errResult(err.Error()), so an agent could not tell which of N posts to
// retry.
//
// Mutation: cmd/hooppy-mcp/main.go registerImportSearchPost — replace the
// per_post-carrying error result with errResult(err.Error()) → RED.
func TestMCP_ImportAllFailed_ErrorCarriesPerPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"x","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"boom"}`))
		default:
			w.Write([]byte(`{"data":[],"meta":{"current_page":1,"last_page":1,"per_page":20,"total":0}}`))
		}
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hooppy_import_search_post",
		Arguments: map[string]any{"search_post_ids": []int{11, 22}, "schedules_ids": []int{10}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError=false for a total batch failure; result=%s", toolResultText(res))
	}
	txt := toolResultText(res)
	if !strings.Contains(txt, "every post") {
		t.Errorf("error text lost the failure summary: %s", txt)
	}
	for _, want := range []string{"per_post", `"search_post_id": 11`, `"search_post_id": 22`, `"status": "failed"`} {
		if !strings.Contains(txt, want) {
			t.Errorf("error result does not carry %q — the per-post record of what was attempted must survive the all-failed path; got:\n%s", want, txt)
		}
	}
}
