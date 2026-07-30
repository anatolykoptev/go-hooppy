package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/anatolykoptev/go-hooppy"
)

// runMovePost is the testable core of `hooppy posts move <id> --to-schedule N`.
// It calls MovePost (read-modify-write PUT + post-move date recovery) and
// prints the result as JSON to out, diagnostics to errOut. Returns the
// process exit code (0 on success, 1 on error). Never calls os.Exit itself.
//
// The result carries the server-assigned publication_date — a move re-slots
// the post to the TAIL of the target queue, and moving into a booked
// schedule is a silent months-long delay otherwise (measured: into
// schedule 55576 → 15.01.2027). The date is the load-bearing output; the
// caller MUST see it.
func runMovePost(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, postID, toScheduleID int) int {
	if toScheduleID == 0 {
		fmt.Fprintln(errOut, "error: --to-schedule is required (got 0) — a move targeted at no schedule would publish to nothing")
		return 1
	}
	res, err := c.MovePost(ctx, postID, toScheduleID)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	// Warn on stderr if the date was not recovered — the move succeeded,
	// but the operator does not know the new slot.
	if res.SlotLookupError != "" {
		fmt.Fprintf(errOut, "warn: move succeeded but the new publication_date was not recovered: %s\n", res.SlotLookupError)
	}
	return 0
}

// runBatchMove is the testable core of `hooppy posts move --ids 1,2,3
// --to-schedule N`. It calls BatchMovePosts (POST /posts/batch/move with
// posts_ids as a comma-joined STRING + per-post date recovery) and prints
// the result as JSON to out, diagnostics to errOut. Returns the process
// exit code (0 on success, 1 on error). Never calls os.Exit itself.
func runBatchMove(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, ids []int, toScheduleID int) int {
	if len(ids) == 0 {
		fmt.Fprintln(errOut, "error: --ids requires at least one post ID")
		return 1
	}
	if toScheduleID == 0 {
		fmt.Fprintln(errOut, "error: --to-schedule is required (got 0) — a move targeted at no schedule would publish to nothing")
		return 1
	}
	res, err := c.BatchMovePosts(ctx, ids, toScheduleID)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	// Warn on stderr for any entry whose date was not recovered. The move
	// succeeded for that post; the date is reporting. Collecting and
	// printing these AFTER the JSON encode keeps stdout a single JSON
	// document the caller can parse.
	var unread []int
	for _, m := range res.Moved {
		if m.SlotLookupError != "" {
			unread = append(unread, m.ID)
		}
	}
	if len(unread) > 0 {
		sort.Ints(unread)
		fmt.Fprintf(errOut, "warn: move succeeded for %d post(s) but the new publication_date was not recovered for %d post(s): %v\n", len(res.Moved), len(unread), unread)
	}
	return 0
}
