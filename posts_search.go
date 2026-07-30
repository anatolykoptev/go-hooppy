package hooppy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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
//
// Three more phantom parameters were found in the same sweep (issues #67,
// #73): source_id, source_resource_id, and owner_id are accepted by the
// server and silently dropped — the caller gets an unfiltered result set
// that looks filtered. They are refused here with the same shape as the
// min_* guard. Use source_type (1=social, 2=RSS), content_types,
// photos_amount, video_duration, or text to narrow. Note that source_id
// is phantom on /posts-search but WORKS on /posts (ListPosts) — same name,
// two endpoints, opposite behaviour — so the fix is per-endpoint, never
// per-name.
//
// # Method notes for the next investigator (both cost a wrong answer)
//
//  1. total_rows CAPS AT 10000. A filter over a large collection looks
//     phantom because both the filtered and unfiltered sides read the cap.
//     Judge by RETURNED ROW CONTENT, not total_rows.
//
//  2. An impossible enum value is NOT a probe. source_type=9 returns
//     everything because the server ignores an unrecognised enum rather
//     than matching nothing — indistinguishable from a phantom. Use a
//     different VALID value: source_type=2 returns 0 rows and proves the
//     filter works.
//
// These two notes are why this issue took three rounds to characterise.
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
	// Refuse the three phantom ID filters before any request (issues #67,
	// #73): source_id, source_resource_id, and owner_id are accepted by
	// the server and silently dropped — the caller gets an unfiltered
	// result set that looks filtered. Measured by returned row content
	// (not total_rows, which caps at 10000 — see the method notes above):
	//   - source_id=7 (Instagram) returns rows whose own source_id is 1.
	//   - source_resource_id=2228 (Instagram-only) returns rows with
	//     source_id: [1].
	//   - owner_id=<real> returns four different owners.
	// Same defect class as the min_* guard. The fields stay on the struct
	// (source-compatible — existing code still compiles) but any non-zero
	// value now errors. Use source_type, content_types, photos_amount,
	// video_duration, or text to narrow. source_id WORKS on /posts
	// (ListPosts) — phantom only here, so the fix is per-endpoint.
	if f.SourceID != 0 || f.SourceResourceID != 0 || f.OwnerID != 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: source_id/source_resource_id/owner_id are not server-side filters on /posts-search — the API accepts and silently ignores them, returning an unfiltered result set that looks filtered (measured by row content, not total_rows which caps at 10000); use source_type (1=social, 2=RSS), content_types, photos_amount, video_duration, or text to narrow (issues #67, #73)")
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
	// Reject negatives for the two remaining ID/page filters before any
	// request: the old `> 0` guard let a negative take neither branch —
	// no error, no parameter, an unfiltered result that looks filtered.
	// This is the exact defect class the PhotosAmount/VideoDuration guards
	// below and the min_* guard above close. Reachable from the shipped
	// CLI: cmd/hooppy binds these with IntVar and pflag accepts negatives
	// (--source-type -1 drops the parameter → results from every network
	// while the caller believes they filtered to one; --page -1, or a
	// computed page-1 that underflows, drops the parameter → the server
	// returns page 1, so a paging loop silently re-reads the first page).
	// Zero stays the unset sentinel. SourceID/SourceResourceID/OwnerID are
	// no longer here — they are phantom and refused above on != 0.
	if f.SourceType < 0 || f.Page < 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: source_type/page must be non-negative (got source_type=%d, page=%d); pass 0 to leave any unset", f.SourceType, f.Page)
	}
	if f.SourceType > 0 {
		params.Set("source_type", strconv.Itoa(f.SourceType))
	}
	if f.Page > 0 {
		params.Set("page", strconv.Itoa(f.Page))
	}
	// Sorting — reaches the wire but is NOT differentially measured (see
	// the "assumed" group in TestPhantomFilterSweep). Not in filters_plug,
	// which describes filters only, not sorting or pagination.
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
	// A value absent from `values` may still work; an empty `values` array
	// does NOT mean the filter takes no argument. We therefore pass caller
	// values through verbatim and never hardcode a value enum — a prior
	// guard hardcoded video_duration to 1..4 from a measurement that only
	// tried 1..4, then a wider measurement found keys 5-8 are real and
	// each returns a distinct result set. Replacing one hardcoded range
	// with another (9 and 10 error today) would repeat the same mistake
	// when the vendor adds them. Reject only negatives, send any
	// non-negative value verbatim, and let the server answer.
	//
	// Measured (NOT guessed) against the live API — recorded here so the
	// pass-through decision is grounded, not assumed:
	//
	//   video_duration (unset = unfiltered):
	//     key  rows
	//     0    4194  (unfiltered)
	//     1    710
	//     2    159
	//     3    3525
	//     4    4036
	//     5    4128
	//     6    4161
	//     7    644
	//     8    677
	//     9,10 server error (non-JSON)
	//   Keys 5-8 are real and each returns a distinct result set; the
	//   prior 1..4 guard would have hard-errored on four working filters.
	//   9 and 10 erroring today does NOT mean the vendor will not add them
	//   — do not re-introduce a hardcoded upper bound.
	//
	//   photos_amount (unset = unfiltered; saturates — "N or more", not
	//   "exactly N"):
	//     key  rows
	//     1    9294
	//     5    566
	//     6    742
	//     10   2172
	//     99   2172  (identical to 10 → saturates, not a phantom class)
	//
	// PhotosAmount and VideoDuration are bucket-key filters. The old
	// `> 0` guard reproduced the exact defect this PR closed for the
	// min_* fields: a negative value took neither branch — no error, no
	// parameter, an unfiltered result that looks filtered. A negative
	// bucket key is never valid, so reject it before any request. Zero
	// stays the unset sentinel; any positive value is passed through
	// verbatim (the valid key space is not enumerable client-side — see
	// the measurement tables above).
	if f.PhotosAmount < 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: photos_amount must be a non-negative bucket key (got %d); pass 0 to leave unset or a positive key from the filters_plug descriptor", f.PhotosAmount)
	}
	if f.PhotosAmount > 0 {
		params.Set("photos_amount", strconv.Itoa(f.PhotosAmount))
	}
	if f.VideoDuration < 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: video_duration must be a non-negative bucket key (got %d); pass 0 to leave unset — keys 1-8 are measured to work (filters_plug values:[] is empty, see issue #63)", f.VideoDuration)
	}
	if f.VideoDuration > 0 {
		params.Set("video_duration", strconv.Itoa(f.VideoDuration))
	}
	if f.ContentTypes != "" {
		params.Set("content_types", f.ContentTypes)
	}
	if f.ContentTypesExclude != "" {
		params.Set("content_types_exclude", f.ContentTypesExclude)
	}
	var resp SearchPostsResponse
	if err := c.doGET(ctx, pathPostsSearchIndex, params, &resp, true); err != nil {
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
	if err := c.doGET(ctx, pathPostsSearchSources, nil, &resp, true); err != nil {
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
	if err := c.doGET(ctx, pathPostsSearchParseForm, nil, &resp, true); err != nil {
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
	if err := validateDDMMYYYY("date_from", payload.DateFromDay); err != nil {
		return nil, fmt.Errorf("hooppy: StartParsing: %w", err)
	}
	if err := validateDDMMYYYY("date_to", payload.DateToDay); err != nil {
		return nil, fmt.Errorf("hooppy: StartParsing: %w", err)
	}
	var resp ParsingStartResponse
	if err := c.doPOST(ctx, pathPostsSearchParseStart, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StopParsing cancels any in-progress scraping job.
//
// The path is /posts-search/parsing/stop, not /posts-search/parsing. Both
// exist and both answer {"success":true}; only the /stop suffix cancels
// anything. Measured on a live account (issue #94), three arms with the
// in-progress flag asserted true before each stop:
//
//	no stop call, natural duration      idle again at 256.9s
//	DELETE /posts-search/parsing/stop   idle again at  11.2s (stop sent at 6.2s)
//	DELETE /posts-search/parsing        still running past 100s
//
// So a success response is not evidence here, and the suffix-less path was
// what produced the earlier "a parse cannot be cancelled" conclusion.
//
// Poll the result with GetParsingForm, whose is_parsing_in_progress field is
// the working oracle (ParsingFormResponse models it; SearchPostsResponse does
// not). GET /posts-search does NOT carry that key at all, so do not add it to
// SearchPostsResponse expecting the server to fill it — it would decode as
// false on every call and read exactly like "idle".
//
// Retrying this call is safe against a REPEAT cancel: three consecutive
// DELETEs against an idle live account each answered {"success":true} and left
// the flag false. It is not safe against a job started between the first
// attempt and the retry — that attempt will cancel the new job. The window is
// not necessarily short: on a 429 the client honours the server's Retry-After,
// so it is server-controlled and can be seconds rather than the millisecond
// backoff the local options suggest. Whoever starts a parse concurrently with
// a cancel owns that race; the library cannot see it.
//
// UNDOCUMENTED: DELETE /posts-search/parsing/stop is not in the public
// OpenAPI spec.
func (c *Client) StopParsing(ctx context.Context) error {
	return c.doDELETE(ctx, pathPostsSearchParseStop, nil, true)
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
	// Fail closed: PUT /posts/copy takes a singular search_post_id int and does
	// NOT read search_post_ids. This method marshals the payload wholesale, so a
	// library consumer that sets SearchPostIDs would otherwise see the slice on
	// the wire (json tag "search_post_ids,omitempty") with err == nil — a
	// phantom batch the server silently ignores. Removing --post-ids from the
	// CLI closed one caller; this guard closes the published library surface
	// (the CLI is one of several). The batch-capable endpoints are
	// RewriteSearchPost (POST /posts with as_copy=1) and ImportSearchPost
	// (PUT /posts/import), which join SearchPostIDs into the ids wire field.
	// Refuse here before any request.
	if len(payload.SearchPostIDs) > 0 {
		return nil, fmt.Errorf("hooppy: CopySearchPost: SearchPostIDs is not supported on PUT /posts/copy — this endpoint takes a singular search_post_id int and silently ignores search_post_ids (a non-empty slice would marshal onto the wire with err == nil); for a batch use RewriteSearchPost (POST /posts with as_copy=1) or ImportSearchPost (PUT /posts/import), which join SearchPostIDs into the ids wire field")
	}
	// Fail closed: a schedule-driven copy (when_type=3) targeted at an empty
	// schedules list publishes to nothing. Refuse before issuing any request.
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) == 0 {
		return nil, fmt.Errorf("hooppy: CopySearchPost: publication_when_type=3 (by schedule) requires at least one schedule ID in schedules_ids — got an empty list, which would target no schedule")
	}
	// Before snapshot for slot recovery: when when_type=3, snapshot the
	// schedule's posts BEFORE the create so fillScheduleSlots can diff
	// after. CopySearchPost is always single (SearchPostIDs is refused
	// above), so idsSentCount=1 — a single create is a batch of one and
	// uses the same snapshot-diff path. Walk ALL pages (default page size
	// is 20); a single-page snapshot would miss pre-existing posts beyond
	// page 1 and mis-attribute them as "created". See fillScheduleSlots
	// for WHY the list surface is used instead of GET /posts/{id}/edit.
	var beforeSnapshot []Post
	var beforeErr error
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) > 0 {
		beforeSnapshot, _, beforeErr = c.ListAllPostsWithTotal(ctx, ListPostsFilter{ScheduleID: payload.SchedulesIDs[0]})
		// A failed before snapshot is NOT fatal — the create proceeds,
		// and fillScheduleSlots reports the failure in SlotLookupError.
	}
	var resp PostIDResponse
	// doPUT retryable=false: PUT /posts/copy CREATES a post (a copy of the
	// scraped source). Non-idempotent — a 5xx/timeout after the write
	// committed, retried, would publish a second copy. Same hazard class as
	// ImportSearchPost (PUT /posts/import) and createPostWithMode (PUT
	// /posts/{mode}); all create-shaped PUTs pass false. Enforced by
	// TestRetryPolicySweep and pinned by TestRetryPolicy_CreateNotRetried.
	if err := c.doPUT(ctx, pathPostsCopy, payload, &resp, false); err != nil {
		return nil, err
	}
	// Report the assigned slot when the post was created into a schedule
	// (when_type=3). Best-effort: a lookup failure populates
	// SlotLookupError, not an error return — the post exists. CopySearchPost
	// is always single, so idsSentCount=1.
	c.fillScheduleSlots(ctx, &resp, payload.PublicationWhenType, payload.SchedulesIDs, beforeSnapshot, beforeErr, 1)
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
	if err := c.doGET(ctx, path, params, &resp, true); err != nil {
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

// copySearchPostIDs resolves the ids wire field shared by RewriteSearchPost
// and ImportSearchPost. The server's ids field is a comma-separated string of
// scraped post IDs; the server assigns schedule slots in the order it
// receives them, so the caller's slice order is preserved on the wire.
//
// Precedence (enforced, not just documented — see CopySearchPostPayload doc):
//   - SearchPostIDs non-empty AND SearchPostID non-zero → error (ambiguous).
//   - SearchPostIDs non-empty → joined with ',' in caller order (batch).
//   - SearchPostID non-zero → strconv.Itoa (single, the legacy path).
//   - both empty → error before any request (nothing to copy).
//
// Validation: every element of SearchPostIDs must be positive (id > 0); a
// zero or negative id is rejected with the offending index
// (SearchPostIDs[i] = v — ids must be positive). The scalar path mirrors
// this: a negative SearchPostID is rejected (a negative scraped-post id is
// never real); 0 is the unset sentinel, so the both-empty guard handles it.
// Duplicates are KEPT — the same source post in two schedule slots may be
// intentional, and the order contract means the caller is authoritative over
// the ids list. The function reads the slice only; it does NOT mutate
// payload.SearchPostIDs (no sort, no dedupe, no reorder) — the slice header
// shares backing storage with the caller's array.
//
// CopySearchPost does NOT use this helper — it refuses SearchPostIDs before
// any request and serializes SearchPostID as the singular search_post_id int
// (different wire shape, different endpoint).
func copySearchPostIDs(payload CopySearchPostPayload) (string, error) {
	if len(payload.SearchPostIDs) > 0 && payload.SearchPostID != 0 {
		return "", fmt.Errorf("hooppy: SearchPostIDs and SearchPostID are mutually exclusive — pass only one (the slice for a batch, the scalar for a single post)")
	}
	if len(payload.SearchPostIDs) > 0 {
		parts := make([]string, len(payload.SearchPostIDs))
		for i, id := range payload.SearchPostIDs {
			if id <= 0 {
				return "", fmt.Errorf("hooppy: SearchPostIDs[%d] = %d — ids must be positive", i, id)
			}
			parts[i] = strconv.Itoa(id)
		}
		return strings.Join(parts, ","), nil
	}
	// Scalar path: 0 is the unset sentinel (the both-empty guard below fires
	// when both fields are 0/empty). A negative is never a real scraped-post id
	// — reject it before any request, matching the batch arm's positivity
	// discipline. The old code sent a negative straight onto the wire.
	if payload.SearchPostID < 0 {
		return "", fmt.Errorf("hooppy: SearchPostID = %d — must be a positive id (0 means unset; pass a positive scraped-post id)", payload.SearchPostID)
	}
	if payload.SearchPostID != 0 {
		return strconv.Itoa(payload.SearchPostID), nil
	}
	return "", fmt.Errorf("hooppy: SearchPostIDs/SearchPostID is required — pass the slice for a batch copy or the scalar for a single post")
}

// RewriteSearchPost rewrites one or more scraped posts (from GET /posts-search)
// and publishes them to the user's own pages. Pass custom text in
// payload.Texts to override the original(s). To keep the original photos for
// a single-post rewrite, call GetSearchPostEdit first, use SearchPostPhotos to
// extract them, and pass the result in payload.Attachments.
//
// Uses POST /posts with as_copy=1 (same as the Hooppy UI). The scraped post
// ID(s) are passed in the ids field: a single id via payload.SearchPostID, or
// a batch via payload.SearchPostIDs (comma-joined in caller order — the server
// assigns schedule slots in that order). See CopySearchPostPayload for the
// precedence and mutual-exclusion rules.
//
// UNDOCUMENTED: POST /posts with as_copy=1 + ids is not in the public OpenAPI spec.
func (c *Client) RewriteSearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
	ids, err := copySearchPostIDs(payload)
	if err != nil {
		return nil, err
	}
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
	// request. Fires for the batch form too — a batch of N posts targeted
	// at no schedule is N times the damage of one.
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) == 0 {
		return nil, fmt.Errorf("hooppy: RewriteSearchPost: publication_when_type=3 (by schedule) requires at least one schedule ID in schedules_ids — got an empty list, which would target no schedule")
	}
	// Before snapshot for slot recovery: when when_type=3, snapshot the
	// schedule's posts BEFORE the create so fillScheduleSlots can diff
	// after. This fires for BOTH single and batch — a single create is a
	// batch of one and uses the same snapshot-diff path (the server returns
	// {"id": ...} for a single, {"success": true} for a batch, but the diff
	// recovers the created ids from the list either way). Walk ALL pages
	// (default page size is 20); a single-page snapshot would miss
	// pre-existing posts beyond page 1, causing them to be mis-attributed
	// as "created" by the diff. See fillScheduleSlots for WHY the list
	// surface is used instead of GET /posts/{id}/edit.
	idsSentCount := len(payload.SearchPostIDs)
	if idsSentCount == 0 {
		// Scalar single-post form (SearchPostID).
		idsSentCount = 1
	}
	var beforeSnapshot []Post
	var beforeErr error
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) > 0 {
		beforeSnapshot, _, beforeErr = c.ListAllPostsWithTotal(ctx, ListPostsFilter{ScheduleID: payload.SchedulesIDs[0]})
		// A failed before snapshot is NOT fatal — the create proceeds,
		// and fillScheduleSlots reports the failure in SlotLookupError.
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
		IDs:                  ids,
	}
	var resp PostIDResponse
	if err := c.doPOST(ctx, pathPosts, body, &resp); err != nil {
		return nil, err
	}
	// Report the assigned slot when the post was created into a schedule
	// (when_type=3). Best-effort: a lookup failure populates
	// SlotLookupError, not an error return — the post exists.
	c.fillScheduleSlots(ctx, &resp, payload.PublicationWhenType, payload.SchedulesIDs, beforeSnapshot, beforeErr, idsSentCount)
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

// ImportSearchPost copies one or more scraped posts via PUT /posts/import.
// Unlike RewriteSearchPost (POST /posts with as_copy=1), the import endpoint
// accepts comma-separated search post IDs in its ids field and can copy
// multiple posts in one request. Pass a single id via payload.SearchPostID or
// a batch via payload.SearchPostIDs (comma-joined in caller order — the server
// assigns schedule slots in that order). See CopySearchPostPayload for the
// precedence and mutual-exclusion rules.
//
// The server downloads photos async (is_attachments_in_process=1 → 0) when
// attachments contain photo objects with a `url` field. Videos are stored as
// VK video references (no download needed).
//
// Text handling is FORM-DEPENDENT (measured against the live endpoint, not
// assumed — see the batch-import text note in CHANGELOG):
//   - SINGLE-id import (SearchPostID): text must be passed explicitly — the
//     server does NOT auto-copy text from the original post for the single
//     form. The CLI `search import --post-id` fills Texts from
//     GetSearchPostEdit; a library caller that sends an empty/nil Texts gets a
//     post with no text.
//   - BATCH import (SearchPostIDs): the server DOES auto-copy each post's
//     original text. A batch import of two scraped posts with an empty texts
//     slice was measured to create two posts, each carrying its own source
//     text. The CLI `search import --post-ids` therefore sends an empty
//     (non-nil) Texts slice and relies on this auto-copy; do NOT send an
//     explicit empty-text entry ([]PostText{{Text: ""}}) for a batch — that
//     risks publishing blank across the whole batch.
//
// Attachments follow the SAME form-dependent rule (measured, not inferred
// from the text parallel): the BATCH form auto-fetches attachments
// server-side from the source ids (send attachments:[] → post gets its
// photos); the SINGLE form does NOT auto-fetch — send attachments:[] on a
// single import and the created post has ZERO attachments. So the explicit
// attachment send on the CLI single-post and strip-batch paths is
// load-bearing, not redundant. See the runImport doc comment in
// cmd/hooppy/import_text.go for the three-row probe table.
//
// UNDOCUMENTED: PUT /posts/import is not in the public OpenAPI spec.
func (c *Client) ImportSearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
	ids, err := copySearchPostIDs(payload)
	if err != nil {
		return nil, err
	}
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
	// with an empty --schedules, which is exactly this trap. Fires for the
	// batch form too — a batch of N posts targeted at no schedule is N times
	// the damage of one.
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) == 0 {
		return nil, fmt.Errorf("hooppy: ImportSearchPost: publication_when_type=3 (by schedule) requires at least one schedule ID in schedules_ids — got an empty list, which would target no schedule")
	}
	// Before snapshot for slot recovery: when when_type=3, snapshot the
	// schedule's posts BEFORE the create so fillScheduleSlots can diff
	// after. This fires for BOTH single and batch — a single create is a
	// batch of one and uses the same snapshot-diff path (the server returns
	// {"id": ...} for a single, {"success": true} for a batch, but the diff
	// recovers the created ids from the list either way). Walk ALL pages
	// (default page size is 20); a single-page snapshot would miss
	// pre-existing posts beyond page 1, causing them to be mis-attributed
	// as "created" by the diff. See fillScheduleSlots for WHY the list
	// surface is used instead of GET /posts/{id}/edit.
	idsSentCount := len(payload.SearchPostIDs)
	if idsSentCount == 0 {
		// Scalar single-post form (SearchPostID).
		idsSentCount = 1
	}
	var beforeSnapshot []Post
	var beforeErr error
	if payload.PublicationWhenType == 3 && len(payload.SchedulesIDs) > 0 {
		beforeSnapshot, _, beforeErr = c.ListAllPostsWithTotal(ctx, ListPostsFilter{ScheduleID: payload.SchedulesIDs[0]})
		// A failed before snapshot is NOT fatal — the create proceeds,
		// and fillScheduleSlots reports the failure in SlotLookupError.
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
		IDs:                  ids,
	}
	var resp PostIDResponse
	// doPUT retryable=false: PUT /posts/import CREATES posts, so it is
	// non-idempotent — a 5xx/timeout after the write committed, retried,
	// would duplicate the created posts in a live publishing queue (issue
	// #87). The full-state Update* PUTs target a known id and converge on
	// re-send, so they pass true. Enforced by TestRetryPolicySweep and
	// pinned by TestRetryPolicy_CreateNotRetried.
	if err := c.doPUT(ctx, pathPostsImport, body, &resp, false); err != nil {
		return nil, err
	}
	// Report the assigned slot when the post was created into a schedule
	// (when_type=3). Best-effort: a lookup failure populates
	// SlotLookupError, not an error return — the post exists.
	c.fillScheduleSlots(ctx, &resp, payload.PublicationWhenType, payload.SchedulesIDs, beforeSnapshot, beforeErr, idsSentCount)
	return &resp, nil
}
