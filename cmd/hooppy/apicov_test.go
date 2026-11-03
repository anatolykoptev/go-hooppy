package main

import (
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy/internal/apicov"
	"github.com/spf13/cobra"
)

// TestAPICov_DerivedCLIPathsMatchRealTree cross-checks the AST derivation
// behind the README coverage table against the real cobra tree: every leaf
// command the binary registers must appear in the derived CLI map (else the
// table would silently omit it), and every derived command that reaches a
// client method must resolve in the tree. A registration pattern apicov
// cannot see turns this RED instead of dropping a table row.
func TestAPICov_DerivedCLIPathsMatchRealTree(t *testing.T) {
	surf, err := apicov.Derive("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := buildRootCommand()
	leaves := map[string]bool{}
	var walk func(cmd *cobra.Command, prefix string)
	walk = func(cmd *cobra.Command, prefix string) {
		path := strings.TrimSpace(prefix + " " + cmd.Name())
		if subs := cmd.Commands(); len(subs) > 0 {
			for _, s := range subs {
				walk(s, path)
			}
			return
		}
		leaves[path] = true
	}
	for _, sub := range root.Commands() {
		walk(sub, "")
	}
	for path := range leaves {
		if _, ok := cliSurfaceOnly[path]; ok {
			continue
		}
		if _, ok := surf.CLI[path]; !ok {
			t.Errorf("real command %q is not found by the apicov AST derivation — the coverage table would omit it", path)
		}
	}
	for path, refs := range surf.CLI {
		if len(refs) > 0 && resolveCLIPath(root, path) == nil {
			t.Errorf("apicov derived command %q (reaches %v) but it does not resolve in the real tree", path, refs)
		}
	}
}
