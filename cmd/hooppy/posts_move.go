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

// movePostCore is the shared core of the single-post move: it validates
// the target, calls MovePost (POST /posts/batch/move + pre-move when_type
// guard + post-move date recovery), and returns the result + exit code
// (0 on success, 1 on error, with the error already printed to errOut). It
// does NOT print the result or the per-field warnings — the caller decides
// how to surface those (the single positional path prints a PostMoveResult
// and inline warnings; the single-id batch path wraps into a
// BatchMovePostsResult and runs the batch warning aggregation). Extracted so
// the single-id batch can route through MovePost (for the when_type guard)
// WITHOUT inheriting the single-post OUTPUT SHAPE.
func movePostCore(ctx context.Context, c *hooppy.Client, errOut io.Writer, postID, toScheduleID int) (*hooppy.PostMoveResult, int) {
	if toScheduleID <= 0 {
		fmt.Fprintln(errOut, "error: --to-schedule is required (a positive schedule id) — a move targeted at no schedule or a negative id would publish to nothing")
		return nil, 1
	}
	res, err := c.MovePost(ctx, postID, toScheduleID)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return nil, 1
	}
	return res, 0
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
//
// The output shape is the flat PostMoveResult ({success, schedule_id,
// publication_date}). This is correct for the single positional path. The
// single-id BATCH path (`--ids 42`) normalises to BatchMovePostsResult
// instead (see runBatchMove) so a consumer reading .moved[] gets the same
// shape for one id as for many.
func runMovePost(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, postID, toScheduleID int) int {
	res, code := movePostCore(ctx, c, errOut, postID, toScheduleID)
	if res == nil {
		return code
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

// emitBatchMoveWarnings writes the batch-path warnings to errOut: one line
// naming any post whose date was not recovered, and one line per post whose
// recovered date is the epoch or a past date (the stopped-schedule
// signature). Printing AFTER the JSON encode keeps stdout a single JSON
// document the caller can parse. Shared by the N>1 batch path and the
// single-id batch path so the warning surface is identical.
func emitBatchMoveWarnings(errOut io.Writer, res *hooppy.BatchMovePostsResult) {
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
	for _, m := range res.Moved {
		if m.Warning != "" {
			fmt.Fprintf(errOut, "warn: post %d: %s\n", m.ID, m.Warning)
		}
	}
}

// printBatchMoveResult encodes a BatchMovePostsResult to out and emits the
// batch warnings to errOut. Returns 0 on success, 1 on encode error.
func printBatchMoveResult(out, errOut io.Writer, res *hooppy.BatchMovePostsResult) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	emitBatchMoveWarnings(errOut, res)
	return 0
}

// runBatchMove is the testable core of `hooppy posts move --ids 1,2,3
// --to-schedule N`. It calls BatchMovePosts (POST /posts/batch/move with
// posts_ids as a comma-joined STRING + per-post date recovery) and prints
// the result as JSON to out, diagnostics to errOut. Returns the process
// exit code (0 on success, 1 on error). Never calls os.Exit itself.
//
// A single-id batch is routed to MovePost (via movePostCore) so the
// when_type guard fires — closing the asymmetry where `posts move 42` guards
// when_type but `posts move --ids 42` did not, for the same post. The
// result is then NORMALISED to a BatchMovePostsResult (Moved: [{id, …}]) so
// the OUTPUT SHAPE matches the multi-id case — a consumer reading .moved[]
// gets the entry for the single id instead of nothing. The `posts move <id>`
// single positional path keeps the flat PostMoveResult (correct). For N>1
// the batch path runs and when_type is unchecked (see BatchMovePosts doc).
func runBatchMove(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, ids []int, toScheduleID int) int {
	if len(ids) == 0 {
		fmt.Fprintln(errOut, "error: --ids requires at least one post ID")
		return 1
	}
	if toScheduleID <= 0 {
		fmt.Fprintln(errOut, "error: --to-schedule is required (a positive schedule id) — a move targeted at no schedule or a negative id would publish to nothing")
		return 1
	}
	// Route a single-id batch through MovePost so the when_type guard fires
	// (item E): `posts move --ids 42` and `posts move 42` now behave
	// identically. Then WRAP the result into a BatchMovePostsResult so the
	// output shape matches the multi-id case — a consumer reading .moved[]
	// gets the single entry instead of nothing. Zero extra requests.
	if len(ids) == 1 {
		r, code := movePostCore(ctx, c, errOut, ids[0], toScheduleID)
		if r == nil {
			return code
		}
		batch := &hooppy.BatchMovePostsResult{
			Success: r.Success,
			Moved: []hooppy.MovedPost{{
				ID:              ids[0],
				ScheduleID:      r.ScheduleID,
				PublicationDate: r.PublicationDate,
				SlotLookupError: r.SlotLookupError,
				Warning:         r.Warning,
			}},
		}
		return printBatchMoveResult(out, errOut, batch)
	}
	res, err := c.BatchMovePosts(ctx, ids, toScheduleID)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return printBatchMoveResult(out, errOut, res)
}
