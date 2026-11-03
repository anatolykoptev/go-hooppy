package main

import (
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/spf13/cobra"
)

// resolveCLIPath walks the command tree along a space-separated path like
// "schedules update". Returns nil when any segment is missing — a registry
// entry whose path does not resolve is exactly the wiring drift the parity
// test exists to catch.
func resolveCLIPath(root *cobra.Command, path string) *cobra.Command {
	cmd := root
	for _, name := range strings.Fields(path) {
		var next *cobra.Command
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			return nil
		}
		cmd = next
	}
	return cmd
}

// TestOpsRegistry_CLIEntriesResolve is parity test (a)+(b) on the CLI side:
// every ops.Registry entry must resolve to a real command on the tree
// buildRootCommand produces (the same tree the binary executes), and every
// field of the entry's Input type must appear as a flag on that command.
// The field set is derived from the Input type's own tags — never from a
// hand-written list — so a field added to the input joins the check
// automatically.
//
// Mutations that go RED here: removing the `flag` tag from an input field
// (the field declares nothing → "no flag tag" error), wiring the op's
// command without RegisterOpFlags (the flags are missing), or registering
// the command under a different name (the path does not resolve).
func TestOpsRegistry_CLIEntriesResolve(t *testing.T) {
	root := buildRootCommand()
	for _, e := range ops.Registry {
		cmd := resolveCLIPath(root, e.CLIPath)
		if cmd == nil {
			t.Errorf("registry entry %q: CLI path %q does not resolve to a registered command", e.Name, e.CLIPath)
			continue
		}
		for _, f := range ops.InputFields(e.Input) {
			if f.FlagName == "" {
				t.Errorf("%s (%s): input field %s has no `flag` tag — every ops input field must declare a CLI flag or be explicitly opted out with flag:\"-\"", e.CLIPath, e.Name, f.GoName)
				continue
			}
			if f.FlagName == "-" {
				continue
			}
			if cmd.Flags().Lookup(f.FlagName) == nil {
				t.Errorf("%s (%s): flag --%s for field %s is not registered on the command", e.CLIPath, e.Name, f.FlagName, f.GoName)
			}
		}
	}
}

// cliSurfaceOnly lists leaf commands that live on the CLI surface only and
// will never become ops.Registry entries — each with the reason it stays
// off. They are excluded from the migration-backlog count.
var cliSurfaceOnly = map[string]string{
	"doctor":     "multi-endpoint diagnostic walk — not a single API op",
	"mcp-config": "prints client setup text — makes no API call",
	"completion": "cobra-generated shell completion — not an operation",
	"help":       "cobra-generated help — not an operation",
}

// TestOpsRegistry_BacklogReport is parity test (c): it prints how many CLI
// commands are NOT yet in the registry — the migration backlog. It does not
// fail on them; the count is the migration's progress metric (goal: zero).
// Derived by walking the real command tree and subtracting registry +
// allowlist entries, never from a hand-enumerated command list.
func TestOpsRegistry_BacklogReport(t *testing.T) {
	root := buildRootCommand()
	migrated := map[string]bool{}
	for _, e := range ops.Registry {
		migrated[e.CLIPath] = true
	}
	var backlog []string
	var walk func(cmd *cobra.Command, prefix string)
	walk = func(cmd *cobra.Command, prefix string) {
		path := strings.TrimSpace(prefix + " " + cmd.Name())
		subs := cmd.Commands()
		if len(subs) == 0 {
			if !migrated[path] {
				if _, ok := cliSurfaceOnly[path]; !ok {
					backlog = append(backlog, path)
				}
			}
			return
		}
		for _, s := range subs {
			walk(s, path)
		}
	}
	for _, sub := range root.Commands() {
		walk(sub, "")
	}
	t.Logf("ops migration backlog: %d CLI commands not yet in ops.Registry: %s", len(backlog), strings.Join(backlog, ", "))
	var stale []string
	for path, reason := range cliSurfaceOnly {
		if resolveCLIPath(root, path) == nil {
			stale = append(stale, path+" (absent: "+reason+")")
		}
	}
	if len(stale) > 0 {
		t.Logf("surface-only allowlist entries not present in the tree: %s", strings.Join(stale, ", "))
	}
}
