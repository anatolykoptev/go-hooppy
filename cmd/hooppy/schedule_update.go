package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/anatolykoptev/go-hooppy"
)

// scheduleUpdateArgs carries the `schedules update` subcommand flags into
// the testable runScheduleUpdate core, decoupled from cobra flag binding
// and os.Exit. Mirrors the importArgs/runImport split in import_text.go.
type scheduleUpdateArgs struct {
	id    int
	name  string
	state int
}

// runScheduleUpdate is the testable core of `hooppy schedules update`. It
// routes through UpdateScheduleFromEdit (read-modify-write) — NOT the raw
// UpdateSchedule partial writer, which drops 36 of 72 fields the server
// carries on /edit and would silently destroy the schedule's times, page
// selection, and project binding on a name-only change.
//
// A no-op call (neither name nor state set) is refused locally with exit 1
// BEFORE any client is constructed or request issued — UpdateScheduleFromEdit
// itself refuses empty overrides, but the CLI should fail before reaching
// the library. The MCP equivalent and the library helper both have tests;
// this core gives the CLI path its own.
//
// Returns the process exit code (0 on success, 1 on any refusal/error) and
// writes JSON to out / diagnostics to errOut, never calling os.Exit itself.
func runScheduleUpdate(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, args scheduleUpdateArgs) int {
	if args.id == 0 {
		fmt.Fprintln(errOut, "hooppy: schedules update: id is required (got 0)")
		return 1
	}
	if args.name == "" && args.state == 0 {
		fmt.Fprintln(errOut, "hooppy: schedules update: at least one of --name or --state is required")
		return 1
	}
	overrides := map[string]json.RawMessage{}
	if args.name != "" {
		b, err := json.Marshal(args.name)
		if err != nil {
			fmt.Fprintf(errOut, "error: marshal --name: %v\n", err)
			return 1
		}
		overrides["name"] = b
	}
	if args.state != 0 {
		b, err := json.Marshal(args.state)
		if err != nil {
			fmt.Fprintf(errOut, "error: marshal --state: %v\n", err)
			return 1
		}
		overrides["state"] = b
	}
	resp, err := c.UpdateScheduleFromEdit(ctx, args.id, overrides)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}
