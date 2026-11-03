package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/anatolykoptev/go-hooppy"
)

// scheduleSummary is the read shape emitted by `schedules show` and per row
// of `schedules list --repeated`: the identity fields plus the auto-delete
// flags the schedule carries on its /edit state.
type scheduleSummary struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	State           int    `json:"state"`
	IsPostsRepeated int    `json:"is_posts_repeated"`
	DeletePostsDay  int    `json:"delete_posts_day"`
	DeletePostsHour int    `json:"delete_posts_hour"`
}

// scheduleSummaryFromEdit projects a schedule's /edit state onto the
// summary shape. One projection shared by show and list --repeated keeps
// the two outputs field-identical.
func scheduleSummaryFromEdit(edit *hooppy.ScheduleEditResponse) scheduleSummary {
	return scheduleSummary{
		ID:              edit.ID,
		Name:            edit.Name,
		State:           edit.State,
		IsPostsRepeated: edit.IsPostsRepeated,
		DeletePostsDay:  edit.DeletePostsDay,
		DeletePostsHour: edit.DeletePostsHour,
	}
}

// scheduleSummaryFromList projects a schedules-LIST row onto the summary
// shape. The list endpoint carries the same is_posts_repeated and
// delete_posts_* fields as /edit (issue #158) — used by `schedules list
// --repeated`, which must not issue an /edit read per row.
func scheduleSummaryFromList(s hooppy.Schedule) scheduleSummary {
	return scheduleSummary{
		ID:              s.ID,
		Name:            s.Name,
		State:           s.State,
		IsPostsRepeated: s.IsPostsRepeated,
		DeletePostsDay:  s.DeletePostsDay,
		DeletePostsHour: s.DeletePostsHour,
	}
}

// runScheduleShow is the testable core of `hooppy schedules show <id>`. It
// fetches the schedule's full /edit state and prints the summary as JSON.
// Returns the process exit code (0 on success, 1 on any refusal/error) and
// writes JSON to out / diagnostics to errOut, never calling os.Exit itself.
func runScheduleShow(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, id int) int {
	if id == 0 {
		fmt.Fprintln(errOut, "hooppy: schedules show: id is required (got 0)")
		return 1
	}
	edit, err := c.GetScheduleEdit(ctx, id)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(scheduleSummaryFromEdit(edit)); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}
