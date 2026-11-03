package main

import (
	"reflect"
	"testing"

	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/spf13/cobra"
)

// TestOpFlags_PointerSetOnlyWhenChanged is the explicit-presence guard for
// the flag binder: a flag the user did NOT pass must leave the pointer nil,
// and `--delete-after-days 0` must produce a non-nil pointer to 0 —
// explicit zero is the whole reason the fields are pointers (0 disables
// auto-delete; the old int-sentinel input could not express it).
//
// RED-on-revert: filling pointers unconditionally → Name/DeletePostsHour
// come back non-nil → the nil assertions fail.
func TestOpFlags_PointerSetOnlyWhenChanged(t *testing.T) {
	cmd := &cobra.Command{Use: "update"}
	if err := RegisterOpFlags(cmd, reflect.TypeOf(ops.UpdateScheduleInput{})); err != nil {
		t.Fatalf("RegisterOpFlags: %v", err)
	}
	for _, name := range []string{"name", "state", "delete-after-days", "delete-after-hours"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s was not registered", name)
		}
	}
	if err := cmd.Flags().Parse([]string{"--delete-after-days", "0", "--state", "2"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var in ops.UpdateScheduleInput
	if err := FillOpInput(cmd, &in); err != nil {
		t.Fatalf("FillOpInput: %v", err)
	}
	if in.DeletePostsDay == nil || *in.DeletePostsDay != 0 {
		t.Errorf("DeletePostsDay = %v, want pointer to 0 — an explicit --delete-after-days 0 must be preserved", in.DeletePostsDay)
	}
	if in.State == nil || *in.State != 2 {
		t.Errorf("State = %v, want pointer to 2", in.State)
	}
	if in.Name != nil {
		t.Errorf("Name = %q, want nil — an unpassed flag must not produce a value", *in.Name)
	}
	if in.DeletePostsHour != nil {
		t.Errorf("DeletePostsHour = %d, want nil — an unpassed flag must not produce a value", *in.DeletePostsHour)
	}
}

// opflagsFixture exercises every supported field kind, both value and
// pointer forms, plus the opt-out and undeclared cases.
type opflagsFixture struct {
	Count   int     `flag:"count" flagusage:"how many"`
	Label   string  `flag:"label" flagusage:"what to call it"`
	Verbose bool    `flag:"verbose" flagusage:"chatty output"`
	IDs     []int   `flag:"ids" flagusage:"comma-separated ids"`
	Ptr     *int    `flag:"ptr" flagusage:"optional number"`
	Opted   int     `flag:"-"`
	Plain   float64 // no flag tag — skipped by the binder (parity tests police this)
}

func TestOpFlags_ValueKindsAndOptOuts(t *testing.T) {
	cmd := &cobra.Command{Use: "cmd"}
	if err := RegisterOpFlags(cmd, reflect.TypeOf(opflagsFixture{})); err != nil {
		t.Fatalf("RegisterOpFlags: %v", err)
	}
	for _, name := range []string{"count", "label", "verbose", "ids", "ptr"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s was not registered", name)
		}
	}
	if cmd.Flags().Lookup("opted") != nil {
		t.Error("field tagged flag:\"-\" got a flag — the opt-out marker must be honoured")
	}
	if cmd.Flags().Lookup("plain") != nil {
		t.Error("untagged field got a flag — only flag-tagged fields bind")
	}
	if err := cmd.Flags().Parse([]string{"--count", "3", "--ids", "1,2,7", "--verbose"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var in opflagsFixture
	if err := FillOpInput(cmd, &in); err != nil {
		t.Fatalf("FillOpInput: %v", err)
	}
	if in.Count != 3 || !in.Verbose || in.Label != "" {
		t.Errorf("value fields = %+v, want Count=3 Verbose=true Label=unset-default", in)
	}
	if len(in.IDs) != 3 || in.IDs[0] != 1 || in.IDs[1] != 2 || in.IDs[2] != 7 {
		t.Errorf("IDs = %v, want [1 2 7]", in.IDs)
	}
	if in.Ptr != nil {
		t.Errorf("Ptr = %d, want nil — unpassed pointer flag must stay nil", *in.Ptr)
	}
}

// TestOpFlags_UnsupportedTypeRefused: a flag-tagged field of a kind the
// binder does not support must fail registration loudly — a silently
// skipped field would be an ops input that exists on MCP but not on the
// CLI (the drift the ops layer exists to prevent).
func TestOpFlags_UnsupportedTypeRefused(t *testing.T) {
	type bad struct {
		When float64 `flag:"when"`
	}
	cmd := &cobra.Command{Use: "cmd"}
	err := RegisterOpFlags(cmd, reflect.TypeOf(bad{}))
	if err == nil {
		t.Fatal("RegisterOpFlags accepted a float64 flag field — unsupported types must be refused")
	}
}
