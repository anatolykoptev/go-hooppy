package ops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anatolykoptev/go-hooppy"
)

// DeletePostsInput is the single source of the `posts delete` /
// hooppy_batch_delete_posts parameter set. One post or many: a single id
// deletes through DELETE /posts/{id}, several through one
// POST /posts/batch/delete. On the CLI the single id may also be given as the
// positional <post-id> (mutually exclusive with --ids).
type DeletePostsInput struct {
	IDs IDList `json:"ids" jsonschema:"Post IDs to delete: an integer array [10,11] or a comma-separated string \"10,11\". 1 to 1000 positive ids, none repeated. Several ids are deleted in one request." flag:"ids" flagusage:"comma-separated post IDs to delete (batch; mutually exclusive with the positional post-id). 1 to 1000 positive ids, none repeated"`
}

// DeleteFailure is one id the server did not delete and why.
type DeleteFailure struct {
	ID    int    `json:"id"`
	Error string `json:"error"`
}

// DeletePostsResult is the per-id outcome of DeletePosts. Success is true
// only when every id was deleted; Deleted and Failed partition the input.
// Note is set when the batch request itself was refused and the ids were
// therefore deleted one by one.
type DeletePostsResult struct {
	Success bool            `json:"success"`
	Deleted []int           `json:"deleted"`
	Failed  []DeleteFailure `json:"failed,omitempty"`
	Note    string          `json:"note,omitempty"`
}

// PartialDeleteError is returned by DeletePosts together with a populated
// result when some ids were deleted and others were not. A caller tells the
// three outcomes apart by the error: nil = every id deleted, *PartialDeleteError
// = some deleted and some failed (exit 2 on the CLI, a "partial" status on
// MCP), any other error = nothing was deleted or the outcome is unknown.
type PartialDeleteError struct{ Result *DeletePostsResult }

func (e *PartialDeleteError) Error() string {
	parts := make([]string, 0, len(e.Result.Failed))
	for _, f := range e.Result.Failed {
		parts = append(parts, fmt.Sprintf("%d: %s", f.ID, f.Error))
	}
	return fmt.Sprintf("hooppy: DeletePosts: partial: %d deleted %v, %d failed (%s)", len(e.Result.Deleted), e.Result.Deleted, len(e.Result.Failed), strings.Join(parts, "; "))
}

// DeletePosts is `posts delete` / hooppy_batch_delete_posts. It refuses an
// empty list, an id <= 0, a repeated id and more than hooppy.MaxBatchDeleteIDs
// ids before any request (*ValidationError) — the server accepts an impossible
// id and fabricates a success entry for it.
//
// The batch endpoint answers {"success":true} for the whole call and carries
// no per-id result (openapi.yaml POST /posts/batch/delete), so per-id
// outcomes come from the call shape: a clean answer means every id was
// deleted. When the server refuses the batch (an HTTP error other than
// 401/403/429, or success:false) the op cannot know which ids, if any, were
// deleted, so it deletes them one by one with DELETE /posts/{id} and reports
// each; an id already gone after a half-applied batch then shows up as failed
// with the server's own answer. A transport error (no server answer) is
// returned as is: the outcome of the batch is unknown and nothing is retried
// per id.
func DeletePosts(ctx context.Context, c *hooppy.Client, in DeletePostsInput) (*DeletePostsResult, error) {
	ids := []int(in.IDs)
	if err := validatePostIDs(ids, hooppy.MaxBatchDeleteIDs, true); err != nil {
		return nil, err
	}
	res := &DeletePostsResult{Deleted: []int{}}
	if len(ids) == 1 {
		if _, err := c.DeletePost(ctx, ids[0]); err != nil {
			res.Failed = []DeleteFailure{{ID: ids[0], Error: err.Error()}}
			return res, err
		}
		res.Success, res.Deleted = true, []int{ids[0]}
		return res, nil
	}
	resp, err := c.BatchDeletePosts(ctx, ids)
	if err == nil && resp.Success {
		res.Success, res.Deleted = true, append(res.Deleted, ids...)
		return res, nil
	}
	if err != nil && !serverRefused(err) {
		return nil, fmt.Errorf("hooppy: DeletePosts: the batch request got no server answer (%w) — which of the %d posts were deleted is unknown; list them before retrying", err, len(ids))
	}
	why := "the server answered without success:true"
	if err != nil {
		why = err.Error()
	}
	res.Note = "the batch request was refused (" + why + "); the ids were deleted one by one"
	var stop string // why the remaining ids are not attempted, "" while going on
	for _, id := range ids {
		if stop == "" && ctx.Err() != nil {
			stop = ctx.Err().Error()
		}
		if stop != "" {
			res.Failed = append(res.Failed, DeleteFailure{ID: id, Error: "not attempted: " + stop})
			continue
		}
		if _, err := c.DeletePost(ctx, id); err != nil {
			res.Failed = append(res.Failed, DeleteFailure{ID: id, Error: err.Error()})
			if !serverRefused(err) { // auth, rate limit or no answer: every further id would repeat it
				stop = "stopped after " + err.Error()
			}
			continue
		}
		res.Deleted = append(res.Deleted, id)
	}
	switch {
	case len(res.Failed) == 0:
		res.Success = true
		return res, nil
	case len(res.Deleted) == 0:
		return res, fmt.Errorf("hooppy: DeletePosts: every post failed (%d/%d); first: post %d: %s", len(res.Failed), len(ids), res.Failed[0].ID, res.Failed[0].Error)
	}
	return res, &PartialDeleteError{Result: res}
}

// serverRefused reports whether err is the server's own answer to the batch
// request (so per-id deletes can tell what happened), not a transport failure
// and not an auth or rate-limit refusal that would repeat on every id.
func serverRefused(err error) bool {
	var sf *hooppy.SuccessFalseError
	if errors.As(err, &sf) {
		return true
	}
	var ae *hooppy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return false
	}
	return true
}
