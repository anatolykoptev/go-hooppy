package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/spf13/cobra"
)

// postIDsFromArgs resolves the post ids of `posts delete` / `posts move`: the
// positional <post-id> or the --ids flag (already filled into flagIDs),
// mutually exclusive, exactly one required. Whether the ids are valid is the
// op's business; this only decides which source the command line used and
// reports whether it was the positional (single) form.
func postIDsFromArgs(cmd *cobra.Command, args []string, flagIDs ops.IDList) (ids ops.IDList, positional bool, err error) {
	hasPositional := len(args) > 0
	hasIDs := cmd.Flags().Changed("ids")
	switch {
	case hasPositional && hasIDs:
		return nil, false, fmt.Errorf("positional post-id and --ids are mutually exclusive — pass only one (the scalar for a single post, the comma-separated list for a batch)")
	case !hasPositional && !hasIDs:
		return nil, false, fmt.Errorf("%s requires a positional post-id or --ids", cmd.CommandPath())
	case hasIDs:
		return flagIDs, false, nil
	}
	id, perr := strconv.Atoi(args[0])
	if perr != nil {
		return nil, true, fmt.Errorf("positional post-id %q is not a valid integer: %w", args[0], perr)
	}
	return ops.IDList{id}, true, nil
}

// moveInputFromCmd reads a parsed `posts move` command into the op input.
func moveInputFromCmd(cmd *cobra.Command, args []string) (in ops.MovePostsInput, positional bool, err error) {
	if err = FillOpInput(cmd, &in); err != nil {
		return in, false, err
	}
	in.IDs, positional, err = postIDsFromArgs(cmd, args, in.IDs)
	return in, positional, err
}

// runMovePosts is the thin CLI adapter for `hooppy posts move`. All validation,
// the when_type guard and the date recovery live in ops.MovePosts (shared with
// the MCP tools); this only shapes I/O and returns the process exit code
// (0 on success, 1 on error) without calling os.Exit.
//
// The result carries the server-assigned publication_date — a move re-slots
// the post to the TAIL of the target queue, and moving into a booked schedule
// is a silent months-long delay otherwise. The date is the load-bearing
// output; the caller MUST see it. The positional <post-id> form prints the
// flat PostMoveResult ({success, schedule_id, publication_date}); --ids
// prints the batch shape ({success, moved:[…]}) even for one id, so a script
// reading .moved[] gets the same shape for one id as for many. Warnings go to
// stderr after the JSON, keeping stdout a single document.
func runMovePosts(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, in ops.MovePostsInput, positional bool) int {
	res, err := ops.MovePosts(ctx, c, in)
	if err != nil {
		var ve *ops.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintf(errOut, "hooppy: posts move: %s\n", err)
		} else {
			fmt.Fprintf(errOut, "error: %v\n", err)
		}
		return 1
	}
	var v any = res
	if positional {
		v = res.SingleResult()
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	emitBatchMoveWarnings(errOut, res)
	return 0
}

// emitBatchMoveWarnings writes the batch-path warnings to errOut: one line
// naming any post whose date was not recovered, and one line per post whose
// recovered date is the epoch or a past date (the stopped-schedule
// signature). Printing AFTER the JSON encode keeps stdout a single JSON
// document the caller can parse.
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
