package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/anatolykoptev/go-hooppy"
)

// moveTarget is the parsed intent of `hooppy posts move`: either a single
// post id (batch=false, singleID set) or a batch (batch=true, ids set). It is
// produced by resolveMoveTarget so the mutual-exclusion and presence rules are
// testable without driving the cobra Run (which calls os.Exit).
type moveTarget struct {
	batch    bool
	singleID int
	ids      []int
}

// resolveMoveTarget parses the positional post-id arg and the --ids flag into
// a moveTarget. Rules:
//   - positional and --ids are MUTUALLY EXCLUSIVE (pass one, not both);
//   - exactly one is required (neither is an error);
//   - the positional must parse as an int.
//
// Extracted from the cobra Run so the rules can be falsified directly — the
// Run calls os.Exit, so the only path into the validation used to be
// untestable.
func resolveMoveTarget(args []string, idsFlag string) (moveTarget, error) {
	hasPositional := len(args) > 0
	hasIDs := idsFlag != ""
	if hasPositional && hasIDs {
		return moveTarget{}, fmt.Errorf("positional post-id and --ids are mutually exclusive — pass only one (the scalar for a single post, the comma-separated list for a batch)")
	}
	if !hasPositional && !hasIDs {
		return moveTarget{}, fmt.Errorf("posts move requires a positional post-id or --ids")
	}
	if hasIDs {
		return moveTarget{batch: true, ids: parseIntList(idsFlag)}, nil
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		return moveTarget{}, fmt.Errorf("positional post-id %q is not a valid integer: %w", args[0], err)
	}
	return moveTarget{batch: false, singleID: id}, nil
}

// runMovePost is the testable core of `hooppy posts move <id> --to-schedule N`.
// It calls MovePost (POST /posts/batch/move + post-move date recovery) and
// prints the result as JSON to out, diagnostics to errOut. Returns the
// process exit code (0 on success, 1 on error). Never calls os.Exit itself.
//
// The result carries the server-assigned publication_date — a move re-slots
// the post to the TAIL of the target queue, and moving into a booked
// schedule is a silent months-long delay otherwise. The date is the
// load-bearing output; the caller MUST see it.
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
	// Warn hard on stderr if the recovered date is the epoch or any past
	// date — the signature of a move into a STOPPED schedule, which parks
	// the post at 01.01.1970 and would otherwise exit silently. The move
	// succeeded (exit 0); the warning is how the operator sees the
	// stopped-schedule trap.
	if res.Warning != "" {
		fmt.Fprintf(errOut, "warn: %s\n", res.Warning)
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
	// Warn hard on stderr for any entry whose recovered date is the epoch
	// or any past date — the signature of a move into a STOPPED schedule,
	// which parks posts at 01.01.1970 and would otherwise exit silently.
	for _, m := range res.Moved {
		if m.Warning != "" {
			fmt.Fprintf(errOut, "warn: post %d: %s\n", m.ID, m.Warning)
		}
	}
	return 0
}
