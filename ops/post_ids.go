package ops

import (
	"fmt"
	"strconv"
)

// validatePostIDs is the one local refusal of a post-id list shared by the
// posts write ops, applied BEFORE any request: the list must be non-empty, at
// most max long, and every id a positive integer. An impossible id (0 or
// negative) is accepted by the server, which fabricates a success entry for
// it, so it must never reach the wire. noDup additionally refuses a repeated
// id. The messages name the problem, not a flag, because the id arrives as a
// positional argument or as --ids on the CLI and as `ids` on MCP.
func validatePostIDs(ids []int, max int, noDup bool) error {
	if len(ids) == 0 {
		return &ValidationError{msg: "at least one post id is required"}
	}
	if len(ids) > max {
		return &ValidationError{msg: fmt.Sprintf("%d post ids given, the maximum is %d — split them into several calls", len(ids), max)}
	}
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return &ValidationError{msg: "post id must be a positive integer (got " + strconv.Itoa(id) + ") — the server accepts an impossible id and fabricates a success entry for it"}
		}
		if noDup && seen[id] {
			return &ValidationError{msg: "post id " + strconv.Itoa(id) + " is listed more than once — pass each post once"}
		}
		seen[id] = true
	}
	return nil
}
