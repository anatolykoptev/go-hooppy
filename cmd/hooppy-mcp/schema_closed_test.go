package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// parameterlessTools is the allowlist of tools whose input struct is
// legitimately fieldless — their schema has an empty (absent) properties map.
// Every entry MUST name the tool and say WHY it takes no parameters; a new
// tool only belongs here when its input type really is `struct{}`. A tool
// that has parameters but whose schema inference failed does NOT belong here:
// it lands an open schema ({type:object}, no properties, no
// additionalProperties:false) and the closed-schema assertion below fails.
var parameterlessTools = map[string]string{
	"hooppy_get_user":       "getUserInput is struct{} — GET /user takes no parameters",
	"hooppy_parsing_status": "input is an anonymous struct{} — GET parsing form/status takes no parameters",
	"hooppy_stop_parsing":   "input is an anonymous struct{} — DELETE parsing takes no parameters",
}

// captureSlog redirects the DEFAULT slog logger (the one mcpserver.AddTool's
// slog.Warn calls) into a buffer for the duration of fn, then restores it.
// registerTools runs inside fn, so any "schema inference failed" warning a
// malformed jsonschema tag produces is captured verbatim — the signal that is
// otherwise a WARN line on startup nobody reads (issues #80, #120, #143).
//
// go-mcpserver v0.21.0 (and v0.22.1) expose no option to fail registration on
// inference failure — AddTool logs and falls back to an open schema — so the
// captured log plus the advertised-schema assertions below are the loud gate.
func captureSlog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// TestMCPTools_EveryInputSchemaIsClosed asserts that EVERY tool registered by
// registerTools — the same registration path the binary serves — advertises a
// closed, non-empty input schema. The tool list comes from a live ListTools
// over an in-memory MCP session (newMCPClientSession), never a hand-written
// table, so a tool added to registerTools is covered automatically.
//
// The failure class (issues #80, #120, #143): a jsonschema struct tag whose
// value begins "WORD=" (e.g. `jsonschema:"1=publish now, 2=…"`) makes
// jsonschema.ForType fail; mcpserver.AddTool logs a WARN and registers the
// tool with an OPEN schema {type:object} — no properties, no required set, no
// additionalProperties:false — so the agent gets no parameter contract and a
// publishing tool's arguments go unvalidated.
//
// Two independent assertions catch the class:
//  1. The slog captured during registration must contain no "schema inference
//     failed" / "schema resolve failed" line — a log-substring check alone is
//     NOT the gate (a reworded message would silence it), which is why
//  2. every advertised schema must be {type:object,
//     additionalProperties:false, properties:<non-empty>} — the properties
//     clause relaxed only for a parameterlessTools entry.
//
// RED-on-revert (F1): restore `jsonschema:"1=publish now, …"` on one
// PublicationWhenType field and the test fails naming that tool.
// RED-on-add (F2): register one more tool whose input struct has a malformed
// tag and it fails naming the NEW tool — the list is derived from the live
// server, not a table.
func TestMCPTools_EveryInputSchemaIsClosed(t *testing.T) {
	var cs *mcp.ClientSession
	regLog := captureSlog(t, func() { cs = newMCPClientSession(t) })
	for _, line := range strings.Split(regLog, "\n") {
		if strings.Contains(line, "schema inference failed") || strings.Contains(line, "schema resolve failed") {
			t.Errorf("tool registration degraded to an open schema: %s", line)
		}
	}

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("ListTools returned no tools — the per-tool assertions below would vacuously pass")
	}

	for _, tool := range res.Tools {
		schema := decodeAdvertisedSchema(t, tool)
		if typ, _ := schema["type"].(string); typ != "object" {
			t.Errorf("%s: input schema type = %v, want \"object\"", tool.Name, schema["type"])
		}
		// The closed-schema marker: jsonschema-go emits
		// additionalProperties:false for every struct, and go-mcpserver's
		// portableSchema keeps false verbatim (the closed-object idiom). The
		// open-schema fallback has no additionalProperties key at all, so a
		// missing key IS an open schema — there is no second way to pass.
		if ap, ok := schema["additionalProperties"]; !ok || ap != false {
			t.Errorf("%s: input schema is not closed — additionalProperties = %v (absent or non-false means unvalidated arguments reach the handler; the #143 class)", tool.Name, schema["additionalProperties"])
		}
		props, _ := schema["properties"].(map[string]any)
		if len(props) == 0 {
			if reason, ok := parameterlessTools[tool.Name]; !ok {
				t.Errorf("%s: input schema has no properties — either the tool is genuinely parameterless (add a commented parameterlessTools entry) or its schema inference failed and an open schema was registered", tool.Name)
			} else {
				t.Logf("%s: parameterless — %s", tool.Name, reason)
			}
		}
	}
}

// TestMCPTools_UnknownArgumentRejected is the F1 journey at the wire: an
// agent calling a publishing tool with a typo'd argument name must be
// REJECTED by the schema — additionalProperties:false makes the key a
// validation error — not silently dropped and routed through the remaining
// fields. With the open schema this is exactly what happened: the typo'd key
// passed through unmarshal and was ignored.
//
// The call never reaches the Hooppy client: validation fails inside the
// mcpserver.AddTool wrapper before the handler runs, so no token or httptest
// stub is needed.
func TestMCPTools_UnknownArgumentRejected(t *testing.T) {
	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "hooppy_import_search_post",
		Arguments: map[string]any{
			"search_post_id":        11,
			"publication_when_type": 1,
			"selected_pages_ids":    "1",
			"selected_pages_id":     "1", // typo'd key — must be refused, not ignored
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("CallTool with an unknown argument succeeded — the open schema silently dropped the typo'd key instead of rejecting the call; result=%s", toolResultText(res))
	}
	if !strings.Contains(toolResultText(res), "selected_pages_id") {
		t.Errorf("rejection does not name the unknown key — got %q; the agent needs the typo'd argument named to fix the call", toolResultText(res))
	}
}

// decodeAdvertisedSchema marshals the tool's advertised InputSchema (type
// `any` after go-mcpserver's portableSchema rewrite) back to a JSON object.
// The same decode trick schemaPropertyNames uses: the concrete Go type is an
// implementation detail, the wire shape is the contract.
func decodeAdvertisedSchema(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("%s: marshal advertised input schema: %v", tool.Name, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("%s: advertised input schema is not a JSON object: %v (raw: %s)", tool.Name, err, raw)
	}
	return schema
}
