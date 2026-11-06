package ops

import (
	"context"
	"strconv"

	"github.com/anatolykoptev/go-hooppy"
)

// GetPostEditInput is the parameter set of `posts edit` /
// hooppy_get_post_edit: none besides the post id, which is the positional
// <post-id> on the CLI and the explicit `id` field on MCP.
type GetPostEditInput struct{}

// GetPostEdit is `posts edit` / hooppy_get_post_edit: a post's full editable
// state — texts, attachments, schedule_id, project_id, publication type and
// date, and the page selection. It refuses a post id <= 0 before any request
// (*ValidationError).
func GetPostEdit(ctx context.Context, c *hooppy.Client, id int, _ GetPostEditInput) (*hooppy.PostEditResponse, error) {
	if id <= 0 {
		return nil, &ValidationError{msg: "id is required and must be a positive post id (got " + strconv.Itoa(id) + ")"}
	}
	return c.GetPostEdit(ctx, id)
}
