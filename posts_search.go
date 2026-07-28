package hooppy

import (
	"context"
	"net/url"
	"strconv"
)

// ListSearchPosts returns posts scraped from external social media pages,
// matching the given filter. Posts must be scraped first via StartParsing.
//
// UNDOCUMENTED: GET /posts-search is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) ListSearchPosts(ctx context.Context, f SearchPostsFilter) (*SearchPostsResponse, error) {
	params := url.Values{}
	if f.Text != "" {
		params.Set("text", f.Text)
	}
	if f.DateFrom != "" {
		params.Set("date_from", f.DateFrom)
	}
	if f.DateTo != "" {
		params.Set("date_to", f.DateTo)
	}
	if f.SourceType > 0 {
		params.Set("source_type", strconv.Itoa(f.SourceType))
	}
	if f.SourceID > 0 {
		params.Set("source_id", strconv.Itoa(f.SourceID))
	}
	if f.SourceResourceID > 0 {
		params.Set("source_resource_id", strconv.Itoa(f.SourceResourceID))
	}
	if f.OwnerID > 0 {
		params.Set("owner_id", strconv.Itoa(f.OwnerID))
	}
	if f.Page > 0 {
		params.Set("page", strconv.Itoa(f.Page))
	}
	var resp SearchPostsResponse
	if err := c.doGET(ctx, pathPostsSearchIndex, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListSourceResources returns the configured source resources (groups of
// external social media pages to scrape from).
//
// UNDOCUMENTED: GET /posts-search/source-resources is not in the public OpenAPI spec.
func (c *Client) ListSourceResources(ctx context.Context) (*SourceResourcesResponse, error) {
	var resp SourceResourcesResponse
	if err := c.doGET(ctx, pathPostsSearchSources, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetParsingForm returns the parsing form data (available source resources,
// social accounts that can act as parsers, and whether a parse is in progress).
//
// UNDOCUMENTED: GET /posts-search/parsing/form is not in the public OpenAPI spec.
func (c *Client) GetParsingForm(ctx context.Context) (*ParsingFormResponse, error) {
	var resp ParsingFormResponse
	if err := c.doGET(ctx, pathPostsSearchParseForm, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StartParsing launches a scraping job that pulls posts from the external
// social media pages configured in the given source resource. The job runs
// asynchronously on the server; poll GetParsingForm to check completion.
//
// UNDOCUMENTED: POST /posts-search/parsing/start is not in the public OpenAPI spec.
func (c *Client) StartParsing(ctx context.Context, payload ParsingStartPayload) (*ParsingStartResponse, error) {
	var resp ParsingStartResponse
	if err := c.doPOST(ctx, pathPostsSearchParseStart, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StopParsing cancels any in-progress scraping job.
//
// UNDOCUMENTED: DELETE /posts-search/parsing is not in the public OpenAPI spec.
func (c *Client) StopParsing(ctx context.Context) error {
	return c.doDELETE(ctx, pathPostsSearchParseStop, nil)
}

// CopySearchPost copies a scraped post (from GET /posts-search) to the user's
// own pages. The server auto-fills text and attachments from the scraped post
// identified by payload.SearchPostID — no need to pass texts/attachments.
//
// This is the simplest way to re-publish a scraped post: just provide the
// scraped post ID and where to publish it (page IDs for immediate/scheduled,
// or schedule IDs for by-schedule mode).
//
// UNDOCUMENTED: PUT /posts/copy with search_post_id is not in the public OpenAPI spec.
func (c *Client) CopySearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
	// Server expects arrays, not null — initialize nil slices to empty.
	if payload.Texts == nil {
		payload.Texts = []PostText{}
	}
	if payload.Attachments == nil {
		payload.Attachments = []Attachment{}
	}
	if payload.SelectedPagesIDs == nil {
		payload.SelectedPagesIDs = []int{}
	}
	if payload.SchedulesIDs == nil {
		payload.SchedulesIDs = []int{}
	}
	var resp PostIDResponse
	if err := c.doPUT(ctx, pathPostsCopy, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RewriteSearchPost rewrites a scraped post (from GET /posts-search) and
// publishes it to the user's own pages. Pass custom text in payload.Texts to
// override the original. To keep the original photos, download them from the
// scraped post's photos[].url, upload via UploadMedia, and pass the resulting
// media IDs in payload.Attachments.
//
// UNDOCUMENTED: PUT /posts/rewrite with search_post_id is not in the public OpenAPI spec.
func (c *Client) RewriteSearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
	if payload.Texts == nil {
		payload.Texts = []PostText{}
	}
	if payload.Attachments == nil {
		payload.Attachments = []Attachment{}
	}
	if payload.SelectedPagesIDs == nil {
		payload.SelectedPagesIDs = []int{}
	}
	if payload.SchedulesIDs == nil {
		payload.SchedulesIDs = []int{}
	}
	var resp PostIDResponse
	if err := c.doPUT(ctx, pathPostsRewrite, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ScrapedPhotoAttachment builds an Attachment from a scraped post's photos.
// The server copies the photos asynchronously from the source social network.
// Works for immediate publish (when_type=1); for scheduled publish (when_type=2),
// upload photos via UploadMedia first and use those IDs instead.
func ScrapedPhotoAttachment(photos []SearchPostPhoto) Attachment {
	items := make([]map[string]interface{}, 0, len(photos))
	for _, ph := range photos {
		items = append(items, map[string]interface{}{
			"id":       strconv.Itoa(ph.ID),
			"owner_id": ph.OwnerID,
			"type":     "photo",
		})
	}
	return Attachment{
		Type: "photos",
		Data: items,
	}
}
