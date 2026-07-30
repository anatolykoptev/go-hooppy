package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-kit/cli"
	"github.com/spf13/cobra"
)

// This file is the mechanical guard for issue #77: no cobra command in the
// tree set Args, so every one inherited ArbitraryArgs — a junk positional
// was silently dropped and the command body ran, returning a right-looking
// answer to a question nobody asked (`hooppy accounts wibble` printed the
// accounts list; `hooppy accounts pages` looked like a subcommand and returned
// the ACCOUNTS list while the real command is `hooppy pages list`).
//
// Two layers, both required:
//
//  1. TestCommandTreeExplicitArgs walks the ENTIRE command tree from the root
//     and fails naming any command whose Args is nil. A command added later
//     that forgets Args cannot inherit ArbitraryArgs by omission — this test
//     goes RED naming it. This is the part that matters most: a test that
//     merely asserts today's commands reject junk would not catch a NEW
//     command added without Args.
//
//  2. TestArgs_RejectJunkPositional / TestArgs_PositionalArity /
//     TestArgs_PositionalRightCountRuns behaviourally pin the invariant: a
//     junk or wrong-count positional yields a cobra arity error and the
//     command body never runs (no API call attempted), while a correct-count
//     positional still reaches the body (guard against over-tightening).

// newFullRoot builds the complete hooppy command tree exactly as main() does,
// minus Execute(). The register* functions only attach commands and flags;
// the Run closures (which call os.Exit via mustClient/die) are never invoked
// by inspection-only tests. Used by TestCommandTreeExplicitArgs and the
// behavioural tests (which drive Execute with Args validation firing before
// any Run closure runs).
func newFullRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := cli.NewRoot(cli.RootConfig{Use: "hooppy", Short: "test"})
	registerAccounts(root)
	registerPages(root)
	registerPosts(root)
	registerProjects(root)
	registerSchedules(root)
	registerFiles(root)
	registerUser(root)
	registerWatermarks(root)
	registerProxies(root)
	registerNotifications(root)
	registerSearch(root)
	registerMCPConfig(root)
	registerDoctor(root)
	return root
}

// TestCommandTreeExplicitArgs walks root.Commands() recursively and fails
// naming any command whose Args is nil. Every application command MUST set an
// explicit arity (cobra.NoArgs / cobra.ExactArgs(n) / cobra.MaximumNArgs(n))
// so it cannot inherit ArbitraryArgs — the defect in issue #77.
//
// RED-on-revert: remove Args from any command and this test fails naming that
// command's CommandPath.
//
// The root itself is intentionally NOT visited: it is the fixed entry point,
// not a "command added later", and cobra's default legacyArgs already errors
// "unknown command" (with did-you-mean suggestions) on an unmatched first
// positional — the root does not have the silent-drop defect. Walking
// root.Commands() (the children) is the surface that grows and that this
// gate protects.
//
// cobra's auto-generated `help` and `completion` subcommands are skipped:
// they are framework-owned (not application commands), cobra configures
// their own Args, and they are only attached during Execute() — not present
// in this freshly built tree. Skipping them is safe because the gate is
// about application commands inheriting ArbitraryArgs by omission, and these
// two are neither application-owned nor omission-prone.
func TestCommandTreeExplicitArgs(t *testing.T) {
	root := newFullRoot(t)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" {
				// Framework-generated; cobra owns their Args. See the
				// comment above for why skipping each is safe.
				continue
			}
			if sub.Args == nil {
				t.Errorf("command %q has nil Args — every command must set an explicit arity (cobra.NoArgs / cobra.ExactArgs(n) / cobra.MaximumNArgs(n)) so it cannot inherit ArbitraryArgs (issue #77); a junk positional would be silently dropped and the command body would run, returning a right-looking answer to a question nobody asked", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(root)
}

// apiStubServer returns an httptest server that records every hit and returns
// a generic 200 JSON. The behavioural tests point the CLI at it via
// HOOPPY_TOKEN/HOOPPY_BASE_URL so that a regression (Args not firing → Run
// body running) is observable as an HTTP hit, not just a missing error.
func apiStubServer(t *testing.T, hits *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.Header().Set("Content-Type", "application/json")
		// DisconnectPage decodes DeleteResponse; ListAccounts decodes
		// AccountsResponse. A success envelope satisfies both.
		w.Write([]byte(`{"success":true,"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
	}))
}

// runRoot executes the root command with the given args, capturing stdout/stderr.
// Returns the error from Execute (cobra returns arity errors; main() is what
// would os.Exit(1) — the test asserts on the returned error instead).
func runRoot(t *testing.T, root *cobra.Command, args ...string) (err error, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return err, out.String(), errBuf.String()
}

// TestArgs_RejectJunkPositional verifies that a junk positional on a NoArgs
// leaf (accounts), a NoArgs group (pages), and an ExactArgs(1) leaf given an
// extra arg (pages disconnect 1 2) all yield an error AND do NOT execute the
// command body — no API call is attempted. This is the user-observable fix
// for issue #77: the damaging case was not a bad error message but a
// right-looking answer to a question nobody asked.
//
// RED-on-revert: drop NoArgs from accounts (or pages) and `accounts wibble`
// runs the body → the stub is hit → hits != 0 fails the test.
func TestArgs_RejectJunkPositional(t *testing.T) {
	cases := []struct {
		name string
		args []string // args after "hooppy"
	}{
		{"leaf NoArgs: accounts wibble", []string{"accounts", "wibble"}},
		{"group NoArgs: pages wibble", []string{"pages", "wibble"}},
		{"leaf ExactArgs(1) over-count: pages disconnect 1 2", []string{"pages", "disconnect", "1", "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int
			srv := apiStubServer(t, &hits)
			defer srv.Close()
			t.Setenv("HOOPPY_TOKEN", "test-token")
			t.Setenv("HOOPPY_BASE_URL", srv.URL)

			root := newFullRoot(t)
			err, _, _ := runRoot(t, root, tc.args...)
			if err == nil {
				t.Fatalf("expected an arity error for %v, got nil — a junk positional must be rejected, not silently dropped (issue #77)", tc.args)
			}
			if hits != 0 {
				t.Fatalf("command body ran for %v: %d API call(s) attempted — arity validation MUST fire before the Run body so no request is issued for a rejected positional (issue #77)", tc.args, hits)
			}
		})
	}
}

// TestArgs_PositionalArity verifies that a positional-taking command rejects
// the wrong count with a cobra arity error (under-count AND over-count), and
// that no API call is attempted. pages disconnect takes exactly one int id.
//
// RED-on-revert: change pages disconnect from ExactArgs(1) to NoArgs (or
// remove Args) and the under-count case runs the body → hits != 0 fails.
func TestArgs_PositionalArity(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"under-count: pages disconnect (0 args)", []string{"pages", "disconnect"}},
		{"over-count: pages disconnect 1 2 3", []string{"pages", "disconnect", "1", "2", "3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int
			srv := apiStubServer(t, &hits)
			defer srv.Close()
			t.Setenv("HOOPPY_TOKEN", "test-token")
			t.Setenv("HOOPPY_BASE_URL", srv.URL)

			root := newFullRoot(t)
			err, _, _ := runRoot(t, root, tc.args...)
			if err == nil {
				t.Fatalf("expected an arity error for %v, got nil — a wrong-count positional must be rejected before the body runs", tc.args)
			}
			if hits != 0 {
				t.Fatalf("command body ran for %v: %d API call(s) attempted — a wrong-count positional must be rejected before any request", tc.args, hits)
			}
		})
	}
}

// TestArgs_PositionalRightCountRuns verifies that a positional-taking command
// with the CORRECT count still reaches the body (the stub is hit). This
// guards against over-tightening: setting NoArgs on a command that should
// take one positional would silently break a working command.
//
// RED-on-revert: change pages disconnect from ExactArgs(1) to NoArgs and the
// body never runs → hits == 0 fails.
func TestArgs_PositionalRightCountRuns(t *testing.T) {
	var hits int
	srv := apiStubServer(t, &hits)
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	root := newFullRoot(t)
	err, _, _ := runRoot(t, root, "pages", "disconnect", "123")
	if err != nil {
		t.Fatalf("expected the body to run for `pages disconnect 123` (one positional is valid), got error: %v — ExactArgs(1) must accept exactly one arg (guard against over-tightening)", err)
	}
	if hits != 1 {
		t.Fatalf("expected exactly 1 API call for `pages disconnect 123`, got %d — the body must run when the correct number of positionals is supplied", hits)
	}
}

// TestCommandWiring_MoveAndQueueSubcommands is the cobra wiring guard
// (item O): resolveMoveTarget is well covered but could be correct and
// unwired. This test builds the root command and asserts the `move` and
// `queue` subcommands exist, their Use strings name the positionals,
// --to-schedule is marked required on `move`, --page is wired on `queue`,
// and the Run closures are non-nil (the actual execution path — Run calls
// resolveMoveTarget → runMovePost/runBatchMove — is covered by the direct
// tests of those functions; the move Run calls os.Exit so it cannot be
// driven via Execute in-process).
//
// RED-on-revert: remove the moveCmd registration, drop MarkFlagRequired, or
// set Run to nil and the corresponding assertion fails.
func TestCommandWiring_MoveAndQueueSubcommands(t *testing.T) {
	root := newFullRoot(t)

	// Find the `posts` parent, then `move` under it.
	var postsCmd, moveCmd, queueCmd *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Name() == "posts" {
			postsCmd = sub
		}
		if sub.Name() == "schedules" {
			for _, ssub := range sub.Commands() {
				if ssub.Name() == "queue" {
					queueCmd = ssub
				}
			}
		}
	}
	if postsCmd == nil {
		t.Fatal("posts subcommand not registered under root")
	}
	for _, sub := range postsCmd.Commands() {
		if sub.Name() == "move" {
			moveCmd = sub
		}
	}
	if moveCmd == nil {
		t.Fatal("move subcommand not registered under posts")
	}
	if queueCmd == nil {
		t.Fatal("queue subcommand not registered under schedules")
	}

	// Use strings MUST name the positionals so --help shows what to pass.
	if !strings.Contains(moveCmd.Use, "move") {
		t.Errorf("move Use = %q, must contain \"move\"", moveCmd.Use)
	}
	if !strings.Contains(queueCmd.Use, "queue") {
		t.Errorf("queue Use = %q, must contain \"queue\"", queueCmd.Use)
	}
	if !strings.Contains(queueCmd.Use, "schedule-id") {
		t.Errorf("queue Use = %q, must name the <schedule-id> positional", queueCmd.Use)
	}

	// --to-schedule MUST be marked required on move. cobra stores the
	// required annotation as BashCompOneRequiredFlag on the flag.
	toSched := moveCmd.Flags().Lookup("to-schedule")
	if toSched == nil {
		t.Fatal("move command has no --to-schedule flag")
	}
	if _, ok := toSched.Annotations["cobra_annotation_bash_completion_one_required_flag"]; !ok {
		t.Error("--to-schedule is NOT marked required on the move command — a move without a target is meaningless")
	}

	// --page MUST be wired on queue (item B).
	pageFlag := queueCmd.Flags().Lookup("page")
	if pageFlag == nil {
		t.Error("queue command has no --page flag — item B requires --page to be wired")
	}

	// Run closures MUST be non-nil — a command with a nil Run is a group,
	// not a leaf, and would silently do nothing when invoked. The move Run
	// calls resolveMoveTarget → runMovePost/runBatchMove (both directly
	// tested); the queue Run calls runScheduleQueue (directly tested).
	if moveCmd.Run == nil {
		t.Error("move command has a nil Run — it would do nothing when invoked; Run must call resolveMoveTarget and the move path")
	}
	if queueCmd.Run == nil {
		t.Error("queue command has a nil Run — it would do nothing when invoked; Run must call runScheduleQueue")
	}
}
