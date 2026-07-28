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
//
// Filter vocabulary: the API publishes its real filters in every response's
// filters_plug array (slug/type/name/values). The five min_* metric threshold
// fields below (MinLikes, MinViews, MinComments, MinReposts, MinInvolvement)
// are NOT server-side filters — the API silently ignores them and returns an
// unfiltered result set that looks filtered (three different thresholds
// produce byte-identical output). Setting any of them returns an error
// before any request is issued; use SortBy (likes|views|reposts|comments|
// involvement) instead, which DOES work server-side. The fields are kept on
// the struct so callers get a clear error rather than a silent lie — see
// issue #63.
func (c *Client) ListSearchPosts(ctx context.Context, f SearchPostsFilter) (*SearchPostsResponse, error) {
	// Refuse the five metric-threshold filters before any request: the API
	// has no such server-side parameters, so emitting them would silently
	// return an unfiltered result set that looks filtered. Sorting by the
	// same metric (SortBy) is the supported path and works server-side.
	// Guard on != 0 (not > 0) so a negative threshold — passed directly or
	// produced by a computed threshold like avg-stddev going negative — is
	// refused too; the old > 0 guard let negatives fall through to an
	// unfiltered result while the help promised the flag errors.
	if f.MinLikes != 0 || f.MinViews != 0 || f.MinComments != 0 || f.MinReposts != 0 || f.MinInvolvement != 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: min_likes/min_views/min_comments/min_reposts/min_involvement are not server-side filters — the API silently ignores them and returns an unfiltered result set; use sort_by (likes|views|reposts|comments|involvement) instead, which does work server-side (issue #63)")
	}
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
	// Sorting (empirically verified — not in filters_plug, which describes
	// filters only, not sorting or pagination).
	if f.SortBy != "" {
		params.Set("sort_by", f.SortBy)
	}
	if f.SortDirection != "" {
		params.Set("sort_direction", f.SortDirection)
	}
	// Content filters. Each slug below is a real filters_plug entry, but
	// the descriptor is authoritative ONLY for slugs — it is advisory for
	// values. Measured against a live response:
	//   - content_types ships values [text, photos, videos, audios, links]
	//     yet `documents` is a working value the descriptor omits, and
	//     `text` is accepted (returns the unfiltered count).
	//   - photos_amount and video_duration ship values: [] (empty), so the
	//     valid keys are NOT discoverable from the descriptor at all.
	//     Measured on a live account (video content only), video_duration
	//     accepts keys 1-4 and each changes the result set: unset 4194;
	//     =1 → 710; =2 → 159; =3 → 3525; =4 → 4036. The counts overlap
	//     (3 and 4 alone exceed the unfiltered total), so these are
	//     overlapping or cumulative ranges, not disjoint buckets. The
	//     vendor does not document the range semantics, so the meaning of
	//     each key is unknown — no labels (short/medium/long, etc.) are
	//     inferred. photos_amount was measured too and also filters
	//     (unset 10000; =1 → 9297; =5 → 566).
	// A value absent from `values` may still work; an empty `values` array
	// does NOT mean the filter takes no argument. We therefore pass caller
	// strings through verbatim and never hardcode a value enum.
	// PhotosAmount and VideoDuration are bucket-key filters with a finite
	// valid key space. The old `> 0` guard reproduced the exact defect this
	// PR closed for the min_* fields: a negative value took neither branch —
	// no error, no parameter, an unfiltered result that looks filtered. A
	// negative bucket key is never valid, so reject it before any request.
	// VideoDuration's valid keys were measured at 1-4 (each changes the
	// result set; filters_plug values:[] is empty); reject anything outside
	// that range with an error naming it. PhotosAmount's upper bound is not
	// confirmed (5 was measured to filter), so only the negative hole is
	// closed here — zero stays the unset sentinel.
	if f.PhotosAmount < 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: photos_amount must be a non-negative bucket key (got %d); pass 0 to leave unset or a positive key from the filters_plug descriptor", f.PhotosAmount)
	}
	if f.PhotosAmount > 0 {
		params.Set("photos_amount", strconv.Itoa(f.PhotosAmount))
	}
	if f.VideoDuration != 0 {
		if f.VideoDuration < 1 || f.VideoDuration > 4 {
			return nil, fmt.Errorf("hooppy: ListSearchPosts: video_duration must be in 1..4 (got %d); pass 0 to leave unset — keys 1-4 are the measured valid bucket keys (filters_plug values:[] is empty, see issue #63)", f.VideoDuration)
		}
		params.Set("video_duration", strconv.Itoa(f.VideoDuration))
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
	// Fail closed: a schedule-driven copy (when_type=3) targeted at an empty
	// schedules list publishes to nothing. Refuse before issuing any request.
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) == 0 {
		return nil, fmt.Errorf("hooppy: CopySearchPost: publication_when_type=3 (by schedule) requires at least one schedule ID in schedules_ids — got an empty list, which would target no schedule")
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
	// Fail closed: a schedule-driven rewrite (when_type=3) targeted at an
	// empty schedules list publishes to nothing. Refuse before issuing any
	// request.
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) == 0 {
		return nil, fmt.Errorf("hooppy: RewriteSearchPost: publication_when_type=3 (by schedule) requires at least one schedule ID in schedules_ids — got an empty list, which would target no schedule")
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

// SearchPostEditAttachments builds the attachments array from a scraped post's
// edit response, matching the Hooppy UI's behavior:
//   - Photo AND video attachments are grouped into a single {type: "photos"}
//     attachment (the UI puts both into v.photos; the server stores VK video
//     references as-is and downloads photos with `url` async).
//   - Other attachment types (link, poll, repost, etc.) are passed through
//     as individual {type: <type>, data: <data>} entries.
//
// This is the correct way to preserve ALL attachments when copying a scraped
// post — the server's async download (is_attachments_in_process) only triggers
// for photos with a `url` or `message_id` field inside a {type: "photos"}
// attachment; videos and other types are stored directly.
func SearchPostEditAttachments(editAttachments []Attachment) []Attachment {
	var photosAndVideos []interface{}
	var others []Attachment
	for _, att := range editAttachments {
		if att.Type == "photo" || att.Type == "video" {
			photosAndVideos = append(photosAndVideos, att.Data)
		} else {
			others = append(others, att)
		}
	}
	var result []Attachment
	if len(photosAndVideos) > 0 {
		result = append(result, Attachment{Type: "photos", Data: photosAndVideos})
	}
	result = append(result, others...)
	return result
}

// ImportSearchPost copies a scraped post via PUT /posts/import. Unlike
// RewriteSearchPost (POST /posts with as_copy=1), the import endpoint
// accepts comma-separated search post IDs in its ids field and can copy
// multiple posts in one request. This wrapper sends a SINGLE id:
// payload.SearchPostID is serialized (via strconv.Itoa) as the sole entry
// in ids. A batch (multi-id) form is scoped to issue #54 and is not
// implemented here.
//
// The server downloads photos async (is_attachments_in_process=1 → 0) when
// attachments contain photo objects with a `url` field. Videos are stored as
// VK video references (no download needed). Text must be passed explicitly —
// the server does NOT auto-copy text from the original post.
//
// UNDOCUMENTED: PUT /posts/import is not in the public OpenAPI spec.
func (c *Client) ImportSearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
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
	// Fail closed: a schedule-driven import (when_type=3) targeted at an
	// empty schedules list publishes to nothing. Refuse before issuing any
	// request — the CLI `search import` command defaults to when_type=3
	// with an empty --schedules, which is exactly this trap.
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) == 0 {
		return nil, fmt.Errorf("hooppy: ImportSearchPost: publication_when_type=3 (by schedule) requires at least one schedule ID in schedules_ids — got an empty list, which would target no schedule")
	}
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
	if err := c.doPUT(ctx, pathPostsImport, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
