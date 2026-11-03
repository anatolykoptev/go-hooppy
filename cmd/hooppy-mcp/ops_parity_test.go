package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anatolykoptev/go-hooppy/ops"
)

// schemaPropertyNames extracts the property names of an advertised tool
// input schema. The schema arrives as `any` (go-mcpserver's portableSchema
// rewrites it through a JSON round trip for client compatibility), so the
// test marshals and decodes rather than asserting a concrete Go type.
func schemaPropertyNames(t *testing.T, schema any) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal advertised input schema: %v", err)
	}
	var doc struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("advertised input schema does not decode as a JSON object: %v (raw: %s)", err, raw)
	}
	names := map[string]bool{}
	for k := range doc.Properties {
		names[k] = true
	}
	return names
}

// TestOpsRegistry_MCPEntriesResolve is parity test (a)+(b) on the MCP side:
// every ops.Registry entry must resolve to a registered tool (listed via
// the same server registerTools builds for the binary), and every
// flag-tagged field of the entry's Input type must appear as a property in
// that tool's advertised input schema. The field set is derived from the
// Input type's own tags — never from a hand-written list.
//
// Mutations that go RED here: dropping registerUpdateSchedule from
// registerTools (the tool is absent from ListTools), removing the `flag`
// tag from an input field (the field declares nothing → "no flag tag"
// error), or breaking the embedded-field promotion (the property vanishes
// from the schema).
func TestOpsRegistry_MCPEntriesResolve(t *testing.T) {
	cs := newMCPClientSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	toolsByName := map[string]bool{}
	schemas := map[string]any{}
	for _, tool := range res.Tools {
		toolsByName[tool.Name] = true
		schemas[tool.Name] = tool.InputSchema
	}
	for _, e := range ops.Registry {
		if !toolsByName[e.MCPTool] {
			t.Errorf("registry entry %q: MCP tool %q is not registered on the server", e.Name, e.MCPTool)
			continue
		}
		props := schemaPropertyNames(t, schemas[e.MCPTool])
		for _, f := range ops.InputFields(e.Input) {
			if f.FlagName == "" {
				t.Errorf("%s (%s): input field %s has no `flag` tag — every ops input field must declare a CLI flag or be explicitly opted out with flag:\"-\"", e.MCPTool, e.Name, f.GoName)
				continue
			}
			if f.FlagName == "-" {
				continue
			}
			if f.JSONName == "" {
				t.Errorf("%s (%s): input field %s has no `json` name — it cannot appear in the tool's input schema", e.MCPTool, e.Name, f.GoName)
				continue
			}
			if !props[f.JSONName] {
				t.Errorf("%s (%s): input schema is missing property %q (field %s)", e.MCPTool, e.Name, f.JSONName, f.GoName)
			}
		}
	}
}

// TestOpsRegistry_MCPBacklogReport is parity test (c): it prints how many
// MCP tools are NOT yet in the registry — the migration backlog. It does
// not fail on them; the count is the migration's progress metric (goal:
// zero). Derived from the real ListTools result minus registry entries.
// There are no MCP surface-only tools to allowlist: every hooppy_* tool is
// an API operation.
func TestOpsRegistry_MCPBacklogReport(t *testing.T) {
	cs := newMCPClientSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	migrated := map[string]bool{}
	for _, e := range ops.Registry {
		migrated[e.MCPTool] = true
	}
	var backlog []string
	for _, tool := range res.Tools {
		if !migrated[tool.Name] {
			backlog = append(backlog, tool.Name)
		}
	}
	t.Logf("ops migration backlog: %d MCP tools not yet in ops.Registry: %v", len(backlog), backlog)
}
