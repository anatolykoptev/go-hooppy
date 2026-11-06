package ops

import (
	"context"

	"github.com/anatolykoptev/go-hooppy"
)

// MovePostsInput is the single source of the `posts move` /
// hooppy_batch_move_posts parameter set. One post or many; on the CLI the
// single id may also be given as the positional <post-id> (mutually
// exclusive with --ids).
type MovePostsInput struct {
	IDs          IDList `json:"ids" jsonschema:"Post IDs to move: an integer array [10,11] or a comma-separated string \"10,11\". 1 to 1000 positive ids. Every post must be schedule-driven (publication_when_type=3); one that is not refuses the whole batch before anything moves." flag:"ids" flagusage:"comma-separated post IDs to move (batch; mutually exclusive with the positional post-id)"`
	ToScheduleID int    `json:"to_schedule_id" jsonschema:"Target schedule ID to move the posts to (REQUIRED, positive). Each post is re-slotted to the TAIL of the target queue; the server assigns each post's new publication_date, which is returned per post in the result." flag:"to-schedule" flagusage:"target schedule ID (required)"`
}

// MovePosts is `posts move` / hooppy_batch_move_posts / hooppy_move_post. It
// refuses an empty list, an id <= 0, more than hooppy.MaxBatchMoveIDs ids and
// a target schedule <= 0 before any request (*ValidationError): a move
// targeted at no schedule would publish to nothing, and the server accepts
// an impossible id or a negative target and fabricates a success entry.
//
// Everything past that is hooppy.BatchMovePosts, the one move implementation
// for a single post and for many: the per-post when_type guard (a post that is
// not schedule-driven refuses the whole batch before the move), the
// comma-joined posts_ids STRING, and the per-post publication_date recovery.
// The result is always the batch shape {success, moved:[…]}; the single-post
// surfaces narrow it with (*hooppy.BatchMovePostsResult).SingleResult.
func MovePosts(ctx context.Context, c *hooppy.Client, in MovePostsInput) (*hooppy.BatchMovePostsResult, error) {
	if err := validatePostIDs(in.IDs, hooppy.MaxBatchMoveIDs, false); err != nil {
		return nil, err
	}
	if in.ToScheduleID <= 0 {
		return nil, errFieldf(in, "ToScheduleID", "must be a positive schedule id (got %d) — a move targeted at no schedule (0) or a negative id would publish to nothing", in.ToScheduleID)
	}
	return c.BatchMovePosts(ctx, in.IDs, in.ToScheduleID)
}
