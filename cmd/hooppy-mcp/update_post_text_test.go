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

// newMCPClientSession builds an in-memory MCP server with every tool
// registered via registerTools (the real registration path the binary uses)
// and connects a client session over an in-memory transport. Returning the
// session lets a test call ListTools (to assert a tool is REACHABLE — a tool
// defined but never registered is the exact failure class this repo has
// shipped) and CallTool (to drive the real handler end to end). This is the
// same in-memory transport pattern the go-sdk itself uses in
// client_list_test.go; it exercises the real mcpserver.AddTool wiring, not a
// parallel re-registration.
func newMCPClientSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "hooppy-mcp-test", Version: "test"}, nil)
	registerTools(server)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "hooppy-mcp-test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })
	return clientSession
}

// findTool returns the registered tool with the given name, failing the test
// if it is absent. This is the reachability guard: a tool whose register
// function was never called inside registerTools does not appear in
// ListTools, so this fails rather than silently skipping.
func findTool(t *testing.T, cs *mcp.ClientSession, name string) *mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not registered — registerTools never called its register function (unreachable tool)", name)
	return nil
}

// TestUpdatePostTextTool_RegisteredAndReachable verifies the safe text-only
// tool is wired into registerTools (issue #49: a tool defined but never
// registered is the failure class this repo has shipped) and that
// hooppy_update_post's advertised description now warns the LLM that it
// republishes immediately and drops the schedule, naming the text-only tool
// as the right choice for editing a scheduled post. The warning must live in
// the description the LLM reads when choosing, not in a doc comment.
func TestUpdatePostTextTool_RegisteredAndReachable(t *testing.T) {
	cs := newMCPClientSession(t)

	textTool := findTool(t, cs, "hooppy_update_post_text")
	if textTool.Description == "" {
		t.Fatal("hooppy_update_post_text has an empty description — the LLM has nothing to choose it by")
	}

	updateTool := findTool(t, cs, "hooppy_update_post")
	desc := updateTool.Description
	if !strings.Contains(desc, "schedule") {
		t.Errorf("hooppy_update_post description does not warn about the schedule: %q", desc)
	}
	if !strings.Contains(desc, "hooppy_update_post_text") {
		t.Errorf("hooppy_update_post description does not name hooppy_update_post_text as the safe path: %q", desc)
	}
}

// TestUpdatePostTextTool_WireBodyPreservesSchedule drives the real
// hooppy_update_post_text handler end to end (in-memory MCP transport →
// handler → UpdatePostText → GetPostEdit + UpdatePost → httptest stub) and
// asserts on the decoded PUT body the stub receives — not on the Go struct.
// This is the regression guard for issue #49: the safe path MUST carry
// schedule_id and preserve the fields UpdatePostText exists to preserve
// (attachments, page selection, per-source texts). Asserting on the wire body
// catches a regression where the tool is pointed back at the publish-now
// UpdatePost payload (which carries no schedule_id) even if the Go struct
// happens to keep the field.
//
// RED-on-revert: point the handler at UpdatePost instead of UpdatePostText
// (the publish-now payload has no schedule_id field) and the schedule_id
// assertion fails — the body lacks the key entirely.
func TestUpdatePostTextTool_WireBodyPreservesSchedule(t *testing.T) {
	var putBody []byte
	var putCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// GET /posts/{id}/edit — a schedule-driven post (when_type=3)
			// with a non-zero schedule_id, two per-network text variants,
			// a link attachment, and a recovered page selection.
			w.Write([]byte(`{
				"id":42,
				"publication_when_type":3,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old-vk","source_id":1},{"text":"old-tg","source_id":9}],
				"attachments":[{"type":"link","data":{"url":"https://example.com/a","title":"A"}}],
				"selected_pages_by_source_ids":{"1":[10,11]},
				"all_pages_ids_by_source_ids":{"1":[10,11],"9":[20]},
				"schedule_id":7,
				"project_id":0
			}`))
		case http.MethodPut:
			putCalled = true
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	// Point the handler's NewClientFromEnv at the stub. The handler calls
	// client() → hooppy.NewClientFromEnv(), which reads HOOPPY_TOKEN and
	// HOOPPY_BASE_URL, so setting these drives the real handler path at the
	// stub without changing production code.
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_update_post_text",
		Arguments: map[string]any{
			"id":   42,
			"text": "new text",
		},
	})
	if err != nil {
		t.Fatalf("CallTool hooppy_update_post_text: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	if !putCalled {
		t.Fatal("PUT /posts/{id} was never issued — UpdatePostText did not run")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v (body=%s)", err, putBody)
	}

	// schedule_id MUST be carried (issue #49 regression guard).
	if got, ok := body["schedule_id"]; !ok {
		t.Fatal("PUT body missing \"schedule_id\" — the safe path dropped the schedule (issue #49 regression)")
	} else if got != float64(7) {
		t.Errorf("schedule_id = %v, want 7", got)
	}

	// Per-source texts preserved: both variants, only .Text swapped, each
	// SourceID kept.
	texts, ok := body["texts"].([]interface{})
	if !ok {
		t.Fatalf("texts = %v, want array", body["texts"])
	}
	if len(texts) != 2 {
		t.Fatalf("len(texts) = %d, want 2 (per-network variants preserved)", len(texts))
	}
	srcs := map[interface{}]bool{}
	for _, tx := range texts {
		entry, ok := tx.(map[string]interface{})
		if !ok {
			t.Fatalf("text entry = %v, want object", tx)
		}
		if entry["text"] != "new text" {
			t.Errorf("text = %v, want \"new text\"", entry["text"])
		}
		if entry["source_id"] == nil {
			t.Error("source_id is nil — must be preserved per variant")
		}
		srcs[entry["source_id"]] = true
	}
	if !srcs[float64(1)] || !srcs[float64(9)] {
		t.Errorf("expected source_ids {1,9} preserved, got %v", srcs)
	}

	// Page selection preserved (sent back as selected_pages_by_source_ids,
	// NOT the publish-now selected_pages_ids array).
	if _, ok := body["selected_pages_ids"]; ok {
		t.Error("PUT body contains \"selected_pages_ids\" — publish-now field must not be sent by the text-only path")
	}
	sel, ok := body["selected_pages_by_source_ids"]
	if !ok {
		t.Fatal("PUT body missing \"selected_pages_by_source_ids\"")
	}
	selMap, ok := sel.(map[string]interface{})
	if !ok {
		t.Fatalf("selected_pages_by_source_ids = %v, want object", sel)
	}
	if _, ok := selMap["1"]; !ok {
		t.Errorf("selected_pages_by_source_ids = %v, want key \"1\" with [10,11] preserved", sel)
	}

	// Attachments preserved (the link attachment passes through
	// SearchPostEditAttachments unchanged).
	atts, ok := body["attachments"].([]interface{})
	if !ok {
		t.Fatalf("attachments = %v, want array (not null)", body["attachments"])
	}
	if len(atts) != 1 {
		t.Fatalf("len(attachments) = %d, want 1 (link attachment preserved)", len(atts))
	}
	att, ok := atts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("attachment[0] = %v, want object", atts[0])
	}
	if att["type"] != "link" {
		t.Errorf("attachment type = %v, want \"link\"", att["type"])
	}
}

// TestUpdatePost_WireBodyDropsSchedule is the falsification counterpart: it
// drives the real hooppy_update_post handler and asserts its PUT body carries
// NO schedule_id — proving the premise of issue #49 (the publish-now payload
// the handler sends has no schedule_id field, so editing a scheduled post
// through it drops the schedule). This pins the hazard the new text-only tool
// exists to avoid and documents why hooppy_update_post's description now
// warns.
func TestUpdatePost_WireBodyDropsSchedule(t *testing.T) {
	var putBody []byte
	var putCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			putCalled = true
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_update_post",
		Arguments: map[string]any{
			"id":       42,
			"text":     "new text",
			"page_ids": []int{10},
		},
	})
	if err != nil {
		t.Fatalf("CallTool hooppy_update_post: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	if !putCalled {
		t.Fatal("PUT /posts/{id} was never issued")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	if _, ok := body["schedule_id"]; ok {
		t.Errorf("PUT body contains \"schedule_id\" — hooppy_update_post sends a publish-now payload that should carry no schedule_id (premise of #49); got %v", body["schedule_id"])
	}
	if body["publication_when_type"] != float64(1) {
		t.Errorf("publication_when_type = %v, want 1 (publish now — drops the schedule)", body["publication_when_type"])
	}
}

// toolResultText extracts the text content of a CallToolResult for error
// messages.
func toolResultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
