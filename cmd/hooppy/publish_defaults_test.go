package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/spf13/cobra"
)

// importInputFromFlags builds the ops input the way `hooppy search import`
// does: register the op's flags on a command, parse argv, FillOpInput. It is
// the real flag → input path (a pointer field is set only when its flag was
// given), so an OMITTED --when-type reaches the op as nil.
func importInputFromFlags(t *testing.T, args ...string) ops.ImportSearchPostInput {
	t.Helper()
	cmd := &cobra.Command{Use: "import"}
	if err := RegisterOpFlags(cmd, ops.MustEntry("ImportSearchPost").Input); err != nil {
		t.Fatalf("RegisterOpFlags: %v", err)
	}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%v): %v", args, err)
	}
	var in ops.ImportSearchPostInput
	if err := FillOpInput(cmd, &in); err != nil {
		t.Fatalf("FillOpInput: %v", err)
	}
	return in
}

// F1 (CLI) — an OMITTED --when-type defaults to 3 (by schedule): the safe
// default, because a published post cannot be un-published.
//
// Mutation: ops/publish_search.go:168 `derefInt(t.PublicationWhenType, 3)`
// 3 → 1. Then `--schedules 10,11` alone becomes when-type 1 + schedules
// (refused, no POST → the first test fails on exit/POST count) and `--to 99`
// alone becomes a publish-NOW (a POST reaches the stub → the second test
// fails on the request count).
func TestF1_CLI_OmittedWhenTypeDefaultsToSchedule(t *testing.T) {
	var body map[string]interface{}
	var sawPost atomic.Bool
	srv := httptest.NewServer(searchStub(&body, &sawPost))
	defer srv.Close()
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	in := importInputFromFlags(t, "--post-id", "1001", "--schedules", "10,11")
	if code := runImport(context.Background(), c, &out, &errOut, in); code != 0 {
		t.Fatalf("exit = %d, want 0 — an omitted --when-type with --schedules must queue by schedule. stderr: %s", code, errOut.String())
	}
	if !sawPost.Load() {
		t.Fatal("no POST /posts reached the stub")
	}
	if got := body["publication_when_type"]; got != float64(3) {
		t.Errorf("publication_when_type = %v, want 3 — an omitted when-type must default to by-schedule, never publish-now (1)", got)
	}
}

func TestF1_CLI_OmittedWhenTypeWithPagesIsRefused(t *testing.T) {
	srv, count := countingImportStub(t)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	in := importInputFromFlags(t, "--post-id", "1001", "--to", "99")
	code := runImport(context.Background(), c, &out, &errOut, in)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 — an omitted --when-type is 3, under which --to is refused (issue #139). stderr: %s", code, errOut.String())
	}
	if got := atomic.LoadInt32(count); got != 0 {
		t.Fatalf("reqCount = %d, want 0 — pages with an omitted when-type must be refused BEFORE any request; a request here is a publish-NOW the caller never asked for", got)
	}
}

// Finding 5 (CLI) — `--date ""` is an explicit empty string, i.e. no date. It
// must not trip the #140 refusal under the default when-type 3.
func TestCLI_EmptyDateFlagIsAbsent(t *testing.T) {
	var body map[string]interface{}
	var sawPost atomic.Bool
	srv := httptest.NewServer(searchStub(&body, &sawPost))
	defer srv.Close()
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	in := importInputFromFlags(t, "--post-id", "1001", "--schedules", "10", "--date", "")
	if code := runImport(context.Background(), c, &out, &errOut, in); code != 0 {
		t.Fatalf("exit = %d, want 0 — `--date \"\"` carries no date and must not be refused as a dropped date flag (#140). stderr: %s", code, errOut.String())
	}
	if !sawPost.Load() {
		t.Fatal("no POST /posts reached the stub")
	}
}
