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
//
// The delete-after fields carry an explicit *Set bool instead of a sentinel:
// 0 is a VALID value for both (delete_posts_day=0 disables auto-delete,
// delete_posts_hour=0 is midnight), so the cobra closure passes
// Flags().Changed("delete-after-*") and only a key whose flag was given is
// sent — `--delete-after-days 30` alone must not reset delete_posts_hour.
type scheduleUpdateArgs struct {
	id                  int
	name                string
	state               int
	deleteAfterDays     int
	deleteAfterDaysSet  bool
	deleteAfterHours    int
	deleteAfterHoursSet bool
}

// runScheduleUpdate is the testable core of `hooppy schedules update`. It
// routes through UpdateScheduleFromEdit (read-modify-write) — NOT the raw
// UpdateSchedule partial writer, which drops 36 of 72 fields the server
// carries on /edit and would silently destroy the schedule's times, page
// selection, and project binding on a name-only change.
//
// A no-op call (no override flag given) is refused locally with exit 1
// BEFORE any client is constructed or request issued — UpdateScheduleFromEdit
// itself refuses empty overrides, but the CLI should fail before reaching
// the library. The MCP equivalent and the library helper both have tests;
// this core gives the CLI path its own.
//
// --delete-after-days/--delete-after-hours map to the schedule's
// delete_posts_day/delete_posts_hour keys (auto-delete posts N days after
// publication). A key is sent ONLY when its flag was given — see
// scheduleUpdateArgs for why 0 cannot be the unset sentinel.
//
// Returns the process exit code (0 on success, 1 on any refusal/error) and
// writes JSON to out / diagnostics to errOut, never calling os.Exit itself.
func runScheduleUpdate(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, args scheduleUpdateArgs) int {
	if args.id == 0 {
		fmt.Fprintln(errOut, "hooppy: schedules update: id is required (got 0)")
		return 1
	}
	if args.name == "" && args.state == 0 && !args.deleteAfterDaysSet && !args.deleteAfterHoursSet {
		fmt.Fprintln(errOut, "hooppy: schedules update: at least one of --name, --state, --delete-after-days or --delete-after-hours is required")
		return 1
	}
	if args.deleteAfterDaysSet && args.deleteAfterDays < 0 {
		fmt.Fprintf(errOut, "hooppy: schedules update: --delete-after-days must be >= 0 (got %d)\n", args.deleteAfterDays)
		return 1
	}
	if args.deleteAfterHoursSet && (args.deleteAfterHours < 0 || args.deleteAfterHours > 23) {
		fmt.Fprintf(errOut, "hooppy: schedules update: --delete-after-hours must be in 0..23 (got %d)\n", args.deleteAfterHours)
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
	if args.deleteAfterDaysSet {
		b, err := json.Marshal(args.deleteAfterDays)
		if err != nil {
			fmt.Fprintf(errOut, "error: marshal --delete-after-days: %v\n", err)
			return 1
		}
		overrides["delete_posts_day"] = b
	}
	if args.deleteAfterHoursSet {
		b, err := json.Marshal(args.deleteAfterHours)
		if err != nil {
			fmt.Fprintf(errOut, "error: marshal --delete-after-hours: %v\n", err)
			return 1
		}
		overrides["delete_posts_hour"] = b
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
