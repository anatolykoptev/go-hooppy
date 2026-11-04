package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The id-list properties of the ops-backed tools (search_post_ids,
// selected_pages_ids, schedules_ids) changed from comma strings to int
// arrays; an agent prompted against the old schema keeps sending strings.
// ops.IDList is the one place that decodes BOTH forms. Two things around it
// cannot live in the type, because the server framework's schema inference
// (jsonschema-go via go-mcpserver's AddTool) takes no per-type override and
// validates arguments against the inferred array-only schema BEFORE the
// handler's JSON decode runs:
//   - tools/call: a string value is rewritten to its array form (through
//     ops.IDList, not a second parser) so it passes validation;
//   - tools/list: the property's schema is widened to oneOf [array of
//     integers, string], so the contract an agent reads says both are fine.
//
// Which tool properties are id lists is derived from ops.Registry's input
// types, never from a hand-kept list: a new ops field of type IDList is
// covered the moment it exists.

var idListType = reflect.TypeFor[ops.IDList]()

// idListProps maps an MCP tool name to the JSON property names of its
// ops.IDList fields.
func idListProps() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, e := range ops.Registry {
		for _, f := range ops.InputFields(e.Input) {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft != idListType || f.JSONName == "" || f.JSONName == "-" {
				continue
			}
			if out[e.MCPTool] == nil {
				out[e.MCPTool] = map[string]bool{}
			}
			out[e.MCPTool][f.JSONName] = true
		}
	}
	return out
}

// idListMiddleware is the receiving middleware registerTools installs.
func idListMiddleware() mcp.Middleware {
	props := idListProps()
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/call":
				if r, ok := req.(*mcp.CallToolRequest); ok && r.Params != nil {
					if err := normalizeIDListArgs(r.Params, props[r.Params.Name]); err != nil {
						return &mcp.CallToolResult{
							IsError: true,
							Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
						}, nil
					}
				}
			case "tools/list":
				res, err := next(ctx, method, req)
				if lr, ok := res.(*mcp.ListToolsResult); ok && err == nil {
					advertiseIDLists(lr, props)
				}
				return res, err
			}
			return next(ctx, method, req)
		}
	}
}

// normalizeIDListArgs rewrites a string-valued id-list argument to the array
// form, decoding it with ops.IDList so there is exactly one parser. Values
// that are already arrays (or absent) are left alone for the schema to judge.
func normalizeIDListArgs(p *mcp.CallToolParamsRaw, fields map[string]bool) error {
	if len(fields) == 0 || len(p.Arguments) == 0 {
		return nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(p.Arguments, &args); err != nil {
		return nil // not an object — the framework reports it
	}
	changed := false
	for name := range fields {
		raw, ok := args[name]
		if !ok || len(raw) == 0 || raw[0] != '"' {
			continue
		}
		var ids ops.IDList
		if err := json.Unmarshal(raw, &ids); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		b, err := json.Marshal(ids)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		args[name] = b
		changed = true
	}
	if !changed {
		return nil
	}
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("re-encode arguments: %w", err)
	}
	p.Arguments = b
	return nil
}

// advertiseIDLists widens the id-list properties of the listed tools to
// oneOf [array of integers, comma-separated string]. The tools are copied:
// the registered *mcp.Tool values are shared server state.
func advertiseIDLists(lr *mcp.ListToolsResult, props map[string]map[string]bool) {
	tools := make([]*mcp.Tool, len(lr.Tools))
	copy(tools, lr.Tools)
	for i, t := range tools {
		fields := props[t.Name]
		if len(fields) == 0 {
			continue
		}
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			continue
		}
		var schema map[string]any
		if json.Unmarshal(raw, &schema) != nil {
			continue
		}
		properties, _ := schema["properties"].(map[string]any)
		for name := range fields {
			p, ok := properties[name].(map[string]any)
			if !ok {
				continue
			}
			widened := map[string]any{
				"oneOf": []any{
					map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
					map[string]any{"type": "string", "description": "comma-separated integers, e.g. \"10,11\" (the same list as the array form)"},
				},
			}
			if d, ok := p["description"]; ok {
				widened["description"] = d
			}
			properties[name] = widened
		}
		cp := *t
		cp.InputSchema = schema
		tools[i] = &cp
	}
	lr.Tools = tools
}
