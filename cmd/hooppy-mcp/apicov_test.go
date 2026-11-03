package main

import (
	"context"
	"testing"

	"github.com/anatolykoptev/go-hooppy/internal/apicov"
)

// TestAPICov_DerivedMCPToolsMatchServer cross-checks the AST derivation
// behind the README coverage table against the tools the server really
// lists: the two sets must be equal, so a tool registered in a shape apicov
// cannot see (or a derived name that is not a real tool) fails here instead
// of silently dropping or inventing a table cell.
func TestAPICov_DerivedMCPToolsMatchServer(t *testing.T) {
	surf, err := apicov.Derive("../..")
	if err != nil {
		t.Fatal(err)
	}
	cs := newMCPClientSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	real := map[string]bool{}
	for _, tool := range res.Tools {
		real[tool.Name] = true
		if _, ok := surf.MCP[tool.Name]; !ok {
			t.Errorf("registered tool %q is not found by the apicov AST derivation — the coverage table would omit it", tool.Name)
		}
	}
	for name := range surf.MCP {
		if !real[name] {
			t.Errorf("apicov derived tool %q but the server does not list it", name)
		}
	}
}
