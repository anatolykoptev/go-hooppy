package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// crossPostingEditFixture loads the 95-key scrubbed /edit fixture from the
// repo's testdata/live (the MCP tests run in cmd/hooppy-mcp).
func crossPostingEditFixture(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		filepath.Join("..", "..", "testdata", "live", "cross_posting_edit.json"),
		filepath.Join("testdata", "live", "cross_posting_edit.json"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	t.Fatal("could not load cross_posting_edit.json fixture")
	return ""
}

// TestListCrossPostingsTool_RegisteredAndReachable is the reachability guard:
// a tool defined but never registered is the failure class this repo has
// shipped. The tool MUST appear in ListTools and be callable.
func TestListCrossPostingsTool_RegisteredAndReachable(t *testing.T) {
	cs := newMCPClientSession(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tc := range []string{
		"hooppy_list_cross_postings",
		"hooppy_get_cross_posting_edit",
		"hooppy_get_cross_posting_statistics",
	} {
		found := false
		for _, tool := range tools.Tools {
			if tool.Name == tc {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool %q is NOT registered — a tool defined but never registered is the failure class this repo has shipped", tc)
		}
	}
}

// TestGetCrossPostingEditTool_EmitsDecodedEnumNames is the MCP half of the
// "decode, do not translate away" surface: the tool emits the full raw body
// with decoded enum names injected alongside the raw integers. An agent is
// the primary consumer here — the names are the value.
func TestGetCrossPostingEditTool_EmitsDecodedEnumNames(t *testing.T) {
	// Build a 95-key /edit body with real enum values.
	var base map[string]json.RawMessage
	if err := json.Unmarshal([]byte(crossPostingEditFixture(t)), &base); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]int{
		"search_mode": 3, "search_mode_direction": 2,
		"determine_best_by": 4, "check_when_type": 2, "check_interval": 6,
	} {
		b, _ := json.Marshal(v)
		base[k] = b
	}
	body, _ := json.Marshal(base)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/cross-posting/2899/edit" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hooppy_get_cross_posting_edit",
		Arguments: map[string]any{"id": 2899},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(toolResultText(res)), &m); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if string(m["search_mode"]) != "3" {
		t.Errorf("search_mode raw = %s, want 3 (raw integer must survive alongside the name)", m["search_mode"])
	}
	want := map[string]string{
		"search_mode_name":           "best",
		"search_mode_direction_name": "newest_first",
		"determine_best_by_name":     "views",
		"check_when_type_name":       "at_fixed_times",
		"check_interval_name":        "daily",
	}
	for k, wantName := range want {
		got, ok := m[k]
		if !ok {
			t.Errorf("missing injected enum-name key %q — an agent needs the decoded name alongside the raw integer", k)
			continue
		}
		var gotStr string
		if err := json.Unmarshal(got, &gotStr); err != nil {
			t.Errorf("decode %q: %v", k, err)
			continue
		}
		if gotStr != wantName {
			t.Errorf("%q = %q, want %q", k, gotStr, wantName)
		}
	}
	// All 95 original keys preserved (plus injected *_name keys).
	if len(m) < 95 {
		t.Errorf("result has %d keys, want >= 95 (the full raw body must be preserved — no field dropped)", len(m))
	}
}

// TestGetCrossPostingEditTool_ZeroIDRefused is the fail-closed guard: id=0
// returns IsError without a request.
func TestGetCrossPostingEditTool_ZeroIDRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("id=0 must be refused before any request")
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hooppy_get_cross_posting_edit",
		Arguments: map[string]any{"id": 0},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("id=0 must return IsError=true (fail-closed), got false")
	}
}

// TestGetCrossPostingStatisticsTool_AllZeroIsRealData is the MCP half of F4:
// an all-zero statistics array is emitted as data, not an error.
func TestGetCrossPostingStatisticsTool_AllZeroIsRealData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cross-posting/2899/statistics" {
			w.Write([]byte(`{"statistics":[{"date":"25.07.2026","posts_found_amount":0,"posts_filtered_amount":0,"posts_duplicates_amount":0,"posts_taken_amount":0,"errors":0}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hooppy_get_cross_posting_statistics",
		Arguments: map[string]any{"id": 2899},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("all-zero counters must NOT be an error — they are a real measurement: %s", toolResultText(res))
	}
	txt := toolResultText(res)
	if !strings.Contains(txt, "posts_found_amount") {
		t.Errorf("result missing posts_found_amount — the counters must be present: %s", txt)
	}
}

// TestListCrossPostingsTool_AllWalksEveryPage is the MCP --all guard.
func TestListCrossPostingsTool_AllWalksEveryPage(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`{"list":[{"id":3,"name":"c"}],"total_rows":3,"is_has_more":false,"rows_limit":20}`))
			return
		}
		w.Write([]byte(`{"list":[{"id":1,"name":"a"},{"id":2,"name":"b"}],"total_rows":3,"is_has_more":true,"rows_limit":20}`))
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hooppy_list_cross_postings",
		Arguments: map[string]any{"all": true},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	if calls != 2 {
		t.Errorf("walk issued %d requests, want 2 (two pages)", calls)
	}
	var env struct {
		List []struct {
			ID int `json:"id"`
		} `json:"list"`
		IsHasMore bool `json:"is_has_more"`
	}
	if err := json.Unmarshal([]byte(toolResultText(res)), &env); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if len(env.List) != 3 {
		t.Errorf("--all walked %d connections, want 3", len(env.List))
	}
	if env.IsHasMore {
		t.Error("AllListEnvelope must pin is_has_more=false")
	}
}
