package hooppy

import (
	"context"
	"fmt"
)

// CrossPostWithMode creates a post via the specified cross-posting mode
// (PUT /posts/{mode}). This is the generic dispatcher; the mode-specific
// methods (SearchPosts, CopyPost, etc.) are thin wrappers around it.
//
// UNDOCUMENTED: these endpoints are not in the public OpenAPI spec (v0.1.0).
func (c *Client) CrossPostWithMode(ctx context.Context, mode CrossPostMode, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, mode, payload)
}

// createPostWithMode sends a PUT request to /posts/{mode} with the given
// payload. All cross-posting endpoints accept the same payload as POST /posts
// and return {"id":...}.
//
// doPUT retryable=false: every /posts/{mode} endpoint CREATES a post (the
// doc comments on each wrapper below say "creates a post via the X mode").
// Non-idempotent — a 5xx/timeout after the write committed, retried, would
// publish a second post. Same hazard class as ImportSearchPost (PUT
// /posts/import) and CopySearchPost (PUT /posts/copy); all create-shaped PUTs
// pass false. Enforced by TestRetryPolicySweep.
//
// UNDOCUMENTED: these endpoints are not in the public OpenAPI spec (v0.1.0).
func (c *Client) createPostWithMode(ctx context.Context, mode CrossPostMode, payload interface{}) (*PostIDResponse, error) {
	var resp PostIDResponse
	if err := c.doPUT(ctx, fmt.Sprintf("/posts/%s", mode), payload, &resp, false); err != nil {
		return nil, err
	}
	// A 2xx with no id (id:0 / absent) is a create that produced no handle —
	// surface it instead of returning a zero that flows into posts
	// move/update/delete as a real-looking handle (issue #131).
	if err := checkCreateID(fmt.Sprintf("PUT /posts/%s", mode), resp.ID, resp.IDs, resp.SlotLookupError); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SearchPosts creates a post via the "search" mode (PUT /posts/search).
// The payload is the same as CreatePost (PostPublishNowPayload, etc.).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) SearchPosts(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeSearch, payload)
}

// CopyPost creates a post via the "copy" mode (PUT /posts/copy).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) CopyPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeCopy, payload)
}

// SourcesPost creates a post via the "sources" mode (PUT /posts/sources).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) SourcesPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeSources, payload)
}

// ImportPost creates a post via the "import" mode (PUT /posts/import).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) ImportPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeImport, payload)
}

// CrossPost creates a post via the "crosspost" mode (PUT /posts/crosspost).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) CrossPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeCrossPost, payload)
}

// RewritePost creates a post via the "rewrite" mode (PUT /posts/rewrite).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) RewritePost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeRewrite, payload)
}

// TranslatePost creates a post via the "translate" mode (PUT /posts/translate).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) TranslatePost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeTranslate, payload)
}

// QueuePost creates a post via the "queue" mode (PUT /posts/queue).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) QueuePost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeQueue, payload)
}

// DraftPost creates a post via the "drafts" mode (PUT /posts/drafts).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) DraftPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeDrafts, payload)
}

// TemplatePost creates a post via the "templates" mode (PUT /posts/templates).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) TemplatePost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeTemplates, payload)
}

// RSSPost creates a post via the "rss" mode (PUT /posts/rss).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) RSSPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeRSS, payload)
}

// FeedPost creates a post via the "feeds" mode (PUT /posts/feeds).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) FeedPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeFeeds, payload)
}

// TagPost creates a post via the "tags" mode (PUT /posts/tags).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) TagPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeTags, payload)
}

// WatermarkPost creates a post via the "watermarks" mode (PUT /posts/watermarks).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) WatermarkPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeWatermarks, payload)
}

// BatchPost creates a post via the "batch" mode (PUT /posts/batch).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) BatchPost(ctx context.Context, payload interface{}) (*PostIDResponse, error) {
	return c.createPostWithMode(ctx, CrossPostModeBatch, payload)
}
