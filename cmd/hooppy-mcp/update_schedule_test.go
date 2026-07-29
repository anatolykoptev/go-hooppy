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

// TestUpdateScheduleTool_DescriptionPreservesFields verifies the
// hooppy_update_schedule description carries the LLM-facing mitigation for
// issue #81 (the third instance of the CLI-safe/MCP-destructive divergence
// class) in load-bearing terms an LLM reads when choosing the tool. The
// description is the ONLY thing steering the LLM away from treating this as a
// partial update — a future reword that softens the preservation claim keeps
// the tool reachable and a generic "schedule" keyword present, but the
// mitigation is gone. These assertions pin the exact phrasing so such a
// softening fails the gate, not silently ships.
//
// RED-on-revert: revert the description to the old "Uses default settings for
// unset fields" wording → every Contains check below fails.
func TestUpdateScheduleTool_DescriptionPreservesFields(t *testing.T) {
	cs := newMCPClientSession(t)
	tool := findTool(t, cs, "hooppy_update_schedule")
	if tool.Description == "" {
		t.Fatal("hooppy_update_schedule has an empty description — the LLM has nothing to choose it by")
	}
	// The mechanism must be named: a read-modify-write is what makes the
	// preservation real. Dropping this term hides why the tool is safe.
	if !strings.Contains(tool.Description, "read-modify-write") {
		t.Errorf("hooppy_update_schedule description must name the read-modify-write mechanism — this is the LLM-facing safety claim for issue #81; missing \"read-modify-write\": %q", tool.Description)
	}
	// The preservation claim must be explicit and broad. A vague "preserves
	// fields" keeps a keyword but removes the list an LLM needs to trust the
	// tool for a name-only change.
	if !strings.Contains(tool.Description, "preserving every other field") {
		t.Errorf("hooppy_update_schedule description must state it preserves every other field — missing \"preserving every other field\": %q", tool.Description)
	}
	// Two concrete unmodelled-field classes must be named so a meaning-softening
	// reword (e.g. "preserves settings") fails: posting times and page targets
	// are the fields a name-only change would destroy through the partial writer.
	if !strings.Contains(tool.Description, "posting times") {
		t.Errorf("hooppy_update_schedule description must name posting times as a preserved field — missing \"posting times\": %q", tool.Description)
	}
	if !strings.Contains(tool.Description, "page targets") {
		t.Errorf("hooppy_update_schedule description must name page targets as a preserved field — missing \"page targets\": %q", tool.Description)
	}
}

// TestUpdateScheduleTool_WireBodyPreservesUnmodelledFields drives the real
// hooppy_update_schedule handler end to end (in-memory MCP transport →
// handler → UpdateScheduleFromEdit → GetScheduleEdit + PUT → httptest stub)
// and asserts on the decoded PUT request body the stub receives — not on the
// Go struct. This is the regression guard for issue #81: the safe path MUST
// carry the unmodelled keys from the /edit response (times, posts_hashtags_obj,
// projects, social_pages_by_accounts, watermarks, user_id, start_date,
// stop_date, posts_caption, publish_as_story_source_ids). Asserting on the
// wire body catches a regression where the handler is pointed back at the raw
// UpdateSchedule partial writer (which sends only the 36 keys SchedulePayload
// models) even if the Go struct happens to keep a field.
//
// RED-on-revert: point the handler back at c.UpdateSchedule (the partial
// writer, which marshals a SchedulePayload — 36 modelled keys only) and the
// unmodelled-key assertions fail, naming each dropped key.
func TestUpdateScheduleTool_WireBodyPreservesUnmodelledFields(t *testing.T) {
	// A realistic /edit response carrying both modelled and unmodelled keys.
	// The unmodelled keys (times, posts_hashtags_obj, projects,
	// social_pages_by_accounts, watermarks, user_id, start_date, stop_date,
	// posts_caption, publish_as_story_source_ids) are the ones the partial
	// writer drops — they MUST survive the round trip.
	const editResponse = `{
		"id": 42,
		"name": "A",
		"user_id": 1,
		"position": 0,
		"state": 1,
		"is_deleted": 0,
		"start_date": 0,
		"stop_date": 0,
		"publication_how_type": 1,
		"publication_where_type": 1,
		"watermark_id": 0,
		"utm_tags": "",
		"posts_caption": 0,
		"posts_comment": 0,
		"posts_location": 0,
		"posts_hashtags": 0,
		"posts_links": 0,
		"publish_as_story_source_ids": "1,2,7,9",
		"share_stories_to_feed_source_ids": "1,2,7,9",
		"times": [
			[{"hours":12,"minutes":25},{"hours":14,"minutes":25}],
			[],
			[],
			[],
			[],
			[],
			[]
		],
		"posts_hashtags_obj": {"tag1": "value1"},
		"posts_links_obj": {"link1": "https://example.invalid/x"},
		"project_id": 7,
		"projects": [
			{"id": 7, "user_id": 1, "position": 0, "name": "A", "is_deleted": 0, "publication_where_type": 1, "posts_count": 3, "watermark_id": 0, "utm_tags": ""}
		],
		"selected_pages_by_source_ids": {"1": [100, 200]},
		"selected_albums_by_source_ids": {"1": [300]},
		"social_pages_by_accounts": [
			{"account": {"id": 123, "social_id": "3251", "source_id": 6, "name": "A", "photo": "https://example.invalid/x", "link": "https://example.invalid/x"}, "pages": []}
		],
		"social_albums_by_pages": [],
		"watermarks": [
			{"id": 1, "user_id": 1, "name": "A", "file": "https://example.invalid/x", "space": 10, "position": 1, "opacity": 80, "size": 50}
		]
	}`

	// Decode the fixture once to get the full set of keys the PUT body must
	// carry back.
	var fixtureMap map[string]interface{}
	if err := json.Unmarshal([]byte(editResponse), &fixtureMap); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	var capturedPUTBody []byte
	var putReceived bool
	var getReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/42/edit":
			getReceived = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(editResponse))
		case r.Method == http.MethodPut && r.URL.Path == "/posts/schedules/42":
			putReceived = true
			capturedPUTBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"schedules":[{"id":42,"name":"New Name"}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_update_schedule",
		Arguments: map[string]any{
			"id":   42,
			"name": "New Name",
		},
	})
	if err != nil {
		t.Fatalf("CallTool hooppy_update_schedule: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", toolResultText(res))
	}
	if !getReceived {
		t.Error("GET /posts/schedules/42/edit was never issued — the handler did not route through UpdateScheduleFromEdit (issue #81 regression: still on the partial writer)")
	}
	if !putReceived {
		t.Fatal("PUT /posts/schedules/42 was never issued")
	}

	// Decode the PUT body as a generic map — NOT as a Go struct, which by
	// definition cannot show the fields it does not model. This is the entire
	// defect: a struct assertion cannot see the 36 keys SchedulePayload drops.
	var putMap map[string]interface{}
	if err := json.Unmarshal(capturedPUTBody, &putMap); err != nil {
		t.Fatalf("unmarshal PUT body: %v (body=%s)", err, capturedPUTBody)
	}

	// Assert EVERY key from the /edit response is present in the PUT body.
	// This is the byte-identity check: no unmodelled field may be dropped.
	var missingKeys []string
	for key := range fixtureMap {
		if _, ok := putMap[key]; !ok {
			missingKeys = append(missingKeys, key)
		}
	}
	if len(missingKeys) > 0 {
		t.Errorf("PUT body is missing %d key(s) from the /edit response — the MCP handler dropped unmodelled fields (issue #81 regression: routed through the partial writer instead of read-modify-write): %v", len(missingKeys), missingKeys)
	}

	// Explicitly assert the unmodelled keys the partial writer is known to
	// drop — so a RED-on-revert failure names them, not just an opaque count.
	// These are the fields an LLM "change the name" call would silently wipe.
	for _, key := range []string{
		"times",
		"posts_hashtags_obj",
		"projects",
		"social_pages_by_accounts",
		"watermarks",
		"user_id",
		"start_date",
		"stop_date",
		"posts_caption",
		"publish_as_story_source_ids",
	} {
		if _, ok := putMap[key]; !ok {
			t.Errorf("PUT body missing unmodelled key %q — the partial writer drops this; the read-modify-write path must carry it back (issue #81)", key)
		}
	}

	// The KNOWN-HOSTILE string field must be preserved byte-identically.
	// SchedulePayload types it int; a Go struct round trip would mangle it.
	if putMap["publish_as_story_source_ids"] != "1,2,7,9" {
		t.Errorf("publish_as_story_source_ids = %v, want \"1,2,7,9\" (KNOWN-HOSTILE string field mangled by round trip)", putMap["publish_as_story_source_ids"])
	}

	// The overridden key must have the new value.
	if putMap["name"] != "New Name" {
		t.Errorf("PUT body name = %v, want \"New Name\" (the override was not applied)", putMap["name"])
	}
}

// TestUpdateScheduleTool_NoOverridesRefused verifies the handler refuses a
// no-op call (neither name nor state set) locally rather than sending a
// request — UpdateScheduleFromEdit itself refuses empty overrides, but the
// handler should fail before constructing a client.
func TestUpdateScheduleTool_NoOverridesRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — a no-op update must be refused locally", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_update_schedule",
		Arguments: map[string]any{
			"id": 42,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for no-op update, got success")
	}
	if !strings.Contains(toolResultText(res), "at least one of name or state") {
		t.Errorf("error must name the requirement, got: %s", toolResultText(res))
	}
}
