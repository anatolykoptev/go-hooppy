package hooppy

import (
	"context"
	"fmt"
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
	// Sorting
	if f.SortBy != "" {
		params.Set("sort_by", f.SortBy)
	}
	if f.SortDirection != "" {
		params.Set("sort_direction", f.SortDirection)
	}
	// Metric filters
	if f.MinLikes > 0 {
		params.Set("min_likes", strconv.Itoa(f.MinLikes))
	}
	if f.MinViews > 0 {
		params.Set("min_views", strconv.Itoa(f.MinViews))
	}
	if f.MinComments > 0 {
		params.Set("min_comments", strconv.Itoa(f.MinComments))
	}
	if f.MinReposts > 0 {
		params.Set("min_reposts", strconv.Itoa(f.MinReposts))
	}
	if f.MinInvolvement > 0 {
		params.Set("min_involvement", strconv.FormatFloat(f.MinInvolvement, 'f', -1, 64))
	}
	// Content filters
	if f.PhotosAmount > 0 {
		params.Set("photos_amount", strconv.Itoa(f.PhotosAmount))
	}
	if f.ContentTypes != "" {
		params.Set("content_types", f.ContentTypes)
	}
	if f.ContentTypesExclude != "" {
		params.Set("content_types_exclude", f.ContentTypesExclude)
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

// GetSearchPostEdit returns a scraped post's data in a format suitable for
// re-publishing. The response includes texts and attachments (photos with
// their URLs and metadata) that can be passed directly to RewriteSearchPost
// or POST /posts with as_copy=1.
//
// This is the correct way to copy photos from a scraped post: the edit
// endpoint returns photo objects with internal Hooppy IDs and source URLs
// that the server can process. Scraped VK photo IDs (owner_id + photo id)
// do NOT work — only the edit endpoint's attachment data does.
//
// UNDOCUMENTED: GET /posts-search/{id}/edit is not in the public OpenAPI spec.
func (c *Client) GetSearchPostEdit(ctx context.Context, searchPostID int) (*SearchPostEditResponse, error) {
	var resp SearchPostEditResponse
	path := fmt.Sprintf(pathPostsSearchEdit, searchPostID)
	params := url.Values{}
	params.Set("as_copy", "1")
	if err := c.doGET(ctx, path, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SearchPostPhotos extracts photo data from a SearchPostEditResponse's
// attachments and returns them as a single Attachment of type "photos"
// suitable for passing in CopySearchPostPayload.Attachments.
//
// The edit endpoint returns attachments as [{type: "photo", data: {...}}, ...].
// The POST /posts endpoint expects [{type: "photos", data: [{...}, ...]}].
// This helper does the transformation.
func SearchPostPhotos(edit *SearchPostEditResponse) *Attachment {
	var photos []interface{}
	for _, att := range edit.Attachments {
		if att.Type == "photo" || att.Type == "video" {
			photos = append(photos, att.Data)
		}
	}
	if len(photos) == 0 {
		return nil
	}
	return &Attachment{
		Type: "photos",
		Data: photos,
	}
}

// SearchPostNonPhotoAttachments extracts all non-photo/video attachments from
// a SearchPostEditResponse and returns them as Attachment objects suitable for
// passing in CopySearchPostPayload.Attachments.
//
// The edit endpoint returns attachments as [{type: "copyright", data: "url"}, ...].
// These are passed through as-is — the server accepts the same types in POST /posts.
// Supported types seen in deferred posts: copyright (VK source link), link
// (external URL). The UI also supports: poll, repost, source, comment, title,
// telegram_buttons, location, ad, audios, documents, settings.
func SearchPostNonPhotoAttachments(edit *SearchPostEditResponse) []Attachment {
	var result []Attachment
	for _, att := range edit.Attachments {
		if att.Type == "photo" || att.Type == "video" {
			continue
		}
		result = append(result, att)
	}
	return result
}

// LinkAttachment builds a link attachment from a URL string.
func LinkAttachment(url string) Attachment {
	return Attachment{Type: "link", Data: url}
}

// SourceAttachment builds a source attachment from a URL string.
// "source" is the UI's name for the original post link.
func SourceAttachment(url string) Attachment {
	return Attachment{Type: "source", Data: url}
}

// CopyrightAttachment builds a copyright attachment from a URL string.
// "copyright" is the server's name for the VK source link.
func CopyrightAttachment(url string) Attachment {
	return Attachment{Type: "copyright", Data: url}
}

// TitleAttachment builds a title attachment from a string.
func TitleAttachment(title string) Attachment {
	return Attachment{Type: "title", Data: title}
}

// PollAttachment builds a poll attachment from a Poll struct.
func PollAttachment(poll Poll) Attachment {
	return Attachment{Type: "poll", Data: poll}
}

// RepostAttachment builds a repost attachment.
func RepostAttachment(link, title string) Attachment {
	return Attachment{Type: "repost", Data: Repost{Link: link, Title: title}}
}

// CommentAttachment builds a comment attachment.
func CommentAttachment(text string, publishByAccount bool) Attachment {
	return Attachment{Type: "comment", Data: Comment{
		Text:             text,
		PublishByAccount: publishByAccount,
	}}
}

// TelegramButtonsAttachment builds a telegram_buttons attachment from a list
// of button {name, link} pairs.
func TelegramButtonsAttachment(buttons []TelegramButton) Attachment {
	return Attachment{Type: "telegram_buttons", Data: TelegramButtons{List: buttons}}
}

// RewriteSearchPost rewrites a scraped post (from GET /posts-search) and
// publishes it to the user's own pages. Pass custom text in payload.Texts to
// override the original. To keep the original photos, call GetSearchPostEdit
// first, use SearchPostPhotos to extract them, and pass the result in
// payload.Attachments.
//
// Uses POST /posts with as_copy=1 (same as the Hooppy UI). The search_post_id
// is passed in the request so the server knows the source.
//
// UNDOCUMENTED: POST /posts with as_copy=1 + search_post_id is not in the public OpenAPI spec.
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
	// POST /posts with as_copy=1 — same format the UI uses.
	body := struct {
		AsCopy               int              `json:"as_copy"`
		PublicationWhenType  int              `json:"publication_when_type"`
		PublicationHowType   int              `json:"publication_how_type"`
		PublicationWhereType int              `json:"publication_where_type"`
		SelectedPagesIDs     []int            `json:"selected_pages_ids"`
		SchedulesIDs         []int            `json:"schedules_ids"`
		PublicationDate      *PublicationDate `json:"publication_date,omitempty"`
		Texts                []PostText       `json:"texts"`
		Attachments          []Attachment     `json:"attachments"`
		IDs                  string           `json:"ids"`
	}{
		AsCopy:               1,
		PublicationWhenType:  payload.PublicationWhenType,
		PublicationHowType:   payload.PublicationHowType,
		PublicationWhereType: 1,
		SelectedPagesIDs:     payload.SelectedPagesIDs,
		SchedulesIDs:         payload.SchedulesIDs,
		PublicationDate:      payload.PublicationDate,
		Texts:                payload.Texts,
		Attachments:          payload.Attachments,
		IDs:                  strconv.Itoa(payload.SearchPostID),
	}
	var resp PostIDResponse
	if err := c.doPOST(ctx, pathPosts, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ScrapedPhotoAttachment builds an Attachment from scraped post photos.
//
// DEPRECATED: scraped photo IDs (VK owner_id + photo id) cannot be attached
// to your own post — VK doesn't allow cross-group photo references. Use
// GetSearchPostEdit + SearchPostPhotos instead to get working attachment data.
// This helper is kept for reference only.
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
