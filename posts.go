package hooppy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ListPostsFilter narrows the GET /posts query.
type ListPostsFilter struct {
	IsPublished     *bool  // nil = no filter; true = published; false = unpublished
	PublicationDate string // dd.mm.yyyy
	SourceID        int
	AccountID       int
	PageID          int
	ScheduleID      int
	ProjectID       int
	Page            int
}

// ListPosts returns posts matching the given filter.
//
// Two phantom parameters were found in the same sweep as the /posts-search
// phantoms (issues #67, #73): page_id and account_id are accepted by the
// server and silently dropped — an impossible id returns the full collection
// that looks filtered. They are refused before any request with the same
// shape as the min_* guard on ListSearchPosts. Use schedule_id, source_id,
// or project_id to narrow. Note that source_id WORKS on /posts but is
// phantom on /posts-search — same name, two endpoints, opposite behaviour —
// so the fix is per-endpoint, never per-name.
//
// # Method notes for the next investigator (both cost a wrong answer)
//
//  1. total_rows CAPS AT 10000. A filter over a large collection looks
//     phantom because both the filtered and unfiltered sides read the cap.
//     Judge by RETURNED ROW CONTENT, not total_rows.
//
//  2. An impossible enum value is NOT a probe: the server ignores an
//     unrecognised enum rather than matching nothing, so it returns
//     everything — indistinguishable from a phantom. Use a different VALID
//     value to prove a filter works.
//
// These two notes are why this issue took three rounds to characterise.
func (c *Client) ListPosts(ctx context.Context, f ListPostsFilter) (*PostsResponse, error) {
	// Refuse the two phantom ID filters before any request (issues #67,
	// #73): page_id and account_id are accepted by the server and silently
	// dropped — an impossible id returns the full collection that looks
	// filtered. Measured by returned row content (not total_rows, which
	// caps at 10000 — see the method notes above). Same defect class as
	// the min_* and /posts-search phantom guards. The fields stay on the
	// struct (source-compatible) but any non-zero value now errors. Use
	// schedule_id, source_id, or project_id to narrow.
	if f.PageID != 0 || f.AccountID != 0 {
		return nil, fmt.Errorf("hooppy: ListPosts: page_id/account_id are not server-side filters on /posts — the API accepts and silently ignores them, returning the full collection that looks filtered (measured by row content, not total_rows which caps at 10000); use schedule_id, source_id, or project_id to narrow (issues #67, #73)")
	}
	params := url.Values{}
	if f.IsPublished != nil {
		val := 0
		if *f.IsPublished {
			val = 1
		}
		params.Set("is_published", strconv.Itoa(val))
	}
	if f.PublicationDate != "" {
		params.Set("publication_date", f.PublicationDate)
	}
	// Reject negatives before any request: the old `> 0` guard let a
	// negative take neither branch — no error, no parameter, an unfiltered
	// result that looks filtered. Same defect class as the posts-search
	// ID/page guards (see posts_search.go). Reachable from the shipped CLI
	// (cmd/hooppy binds these with IntVar; pflag accepts negatives). Zero
	// stays the unset sentinel. AccountID/PageID are no longer here — they
	// are phantom and refused above on != 0.
	if f.SourceID < 0 || f.ScheduleID < 0 || f.ProjectID < 0 || f.Page < 0 {
		return nil, fmt.Errorf("hooppy: ListPosts: source_id/schedule_id/project_id/page must be non-negative (got source_id=%d, schedule_id=%d, project_id=%d, page=%d); pass 0 to leave any unset", f.SourceID, f.ScheduleID, f.ProjectID, f.Page)
	}
	if f.SourceID > 0 {
		params.Set("source_id", strconv.Itoa(f.SourceID))
	}
	if f.ScheduleID > 0 {
		params.Set("schedule_id", strconv.Itoa(f.ScheduleID))
	}
	if f.ProjectID > 0 {
		params.Set("project_id", strconv.Itoa(f.ProjectID))
	}
	if f.Page > 0 {
		params.Set("page", strconv.Itoa(f.Page))
	}
	var resp PostsResponse
	if err := c.doGET(ctx, pathPosts, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListAllPosts walks GET /posts from page 1 with the given filter,
// accumulating posts until is_has_more is false. The walk starts at page 1
// so the first page is not fetched twice (the Hooppy API is 1-indexed and a
// request with no page param is byte-identical to ?page=1). The filter's
// non-page fields are preserved across the walk; only Page is incremented.
// See projects.ListAllSchedules for the 1-indexed rationale and the sanity
// cap.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. To detect a
// TRUNCATED walk (rows the server initially reported but never served), the
// rule doctor uses for /notifications applies: flag when the unique-id
// count is LESS than the first-page total_rows (see RunDoctor for the rule
// and the gaps it does not close). Do NOT use NewAllListEnvelope here: its
// equality check (unique == total) is right for low-churn collections but
// wrong for /posts, a high-churn collection where a post created or
// published between page fetches makes unique != lastTotal on a healthy
// account — the equality check would false-alarm on healthy accounts,
// exactly as it did for /notifications before PR #64. The first-total rule
// is not yet wired into a /posts entry point; tracked in #70. See
// NewAllListEnvelope for the per call-site table of which collections that
// check suits.
func (c *Client) ListAllPosts(ctx context.Context, f ListPostsFilter) ([]Post, error) {
	all, _, err := c.ListAllPostsWithTotal(ctx, f)
	return all, err
}

// ListAllPostsWithTotal is ListAllPosts but also returns the server's
// last-seen total_rows. It exists for symmetry with the other
// ListAll*WithTotal entry points; for /posts specifically, passing
// (list, totalRows) to NewAllListEnvelope is NOT suitable. Its equality
// check (unique == total) false-alarms on every active account: a post
// created or published mid-walk makes the last-seen total_rows differ from
// the unique-id count. The right shape is the unique < firstTotal rule
// doctor uses for /notifications (see RunDoctor), but no /posts entry point
// exposes the first-page total yet; tracked in #70. See NewAllListEnvelope
// for the per call-site table of which collections the equality check does
// suit.
func (c *Client) ListAllPostsWithTotal(ctx context.Context, f ListPostsFilter) ([]Post, int, error) {
	all := make([]Post, 0)
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllPosts exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		f.Page = page
		resp, err := c.ListPosts(ctx, f)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, resp.List...)
		totalRows = resp.TotalRows
		if !resp.IsHasMore {
			return all, totalRows, nil
		}
	}
}

// CreatePost creates a post with the given payload. The payload must be one
// of PostPublishNowPayload, PostPublishAtPayload, PostPublishBySchedulePayload,
// or PostPublishByProjectPayload.
func (c *Client) CreatePost(ctx context.Context, payload interface{}) (*CreatePostResponse, error) {
	var resp CreatePostResponse
	if err := c.doPOST(ctx, pathPosts, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdatePost updates an existing post via PUT /posts/{id}. The payload must
// be one of the PostPublish*Payload types (same as CreatePost).
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) UpdatePost(ctx context.Context, id int, payload interface{}) (*DeletePostResponse, error) {
	var resp DeletePostResponse
	if err := c.doPUT(ctx, fmt.Sprintf(pathPostUpdate, id), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PostEditResponse is the full editable state of a user's own post, returned
// by GET /posts/{id}/edit. It mirrors SearchPostEditResponse but adds
// ScheduleID (needed for PUT /posts/{id} updates, which use schedule_id
// singular — not schedules_ids plural like the create/import endpoints).
//
// Measured limitation: GET /posts/{id}/edit returns a SINGLE schedule_id
// (an int, not an array). A post that belongs to several schedules cannot
// round-trip all of its schedule associations through this endpoint — only
// one survives the full-state PUT. This is a property of the edit endpoint,
// not a client-side choice.
//
// Page targets are returned as objects keyed by social network source ID,
// NOT as the flat selected_pages_ids array used by the create/publish-now
// endpoints:
//   - SelectedPagesBySourceIDs: the post's currently selected page IDs,
//     grouped as {source_id: [page_id, ...]}. For a schedule-driven post
//     (publication_when_type=3) this is {} — pages come from the schedule.
//   - AllPagesIDsBySourceIDs: the full menu of pages available to select,
//     grouped the same way (used by the Hooppy UI to render the picker).
//
// UNDOCUMENTED: GET /posts/{id}/edit is not in the public OpenAPI spec.
type PostEditResponse struct {
	ID                       int              `json:"id"`
	PublicationWhenType      int              `json:"publication_when_type"`
	PublicationHowType       int              `json:"publication_how_type"`
	PublicationWhereType     int              `json:"publication_where_type"`
	PublicationDate          *PublicationDate `json:"publication_date"`
	CreatedBy                int              `json:"created_by"`
	Texts                    []PostText       `json:"texts"`
	Attachments              []Attachment     `json:"attachments"`
	SelectedPagesBySourceIDs map[int][]int    `json:"selected_pages_by_source_ids"`
	AllPagesIDsBySourceIDs   map[int][]int    `json:"all_pages_ids_by_source_ids"`
	ScheduleID               int              `json:"schedule_id"`
	ProjectID                int              `json:"project_id"`
}

// GetPostEdit returns a user's own post in editable format — the full state
// needed to send back via PUT /posts/{id} (UpdatePost). Unlike
// GetSearchPostEdit (which is for scraped posts), this returns schedule_id
// and project_id for the existing post.
//
// UNDOCUMENTED: GET /posts/{id}/edit is not in the public OpenAPI spec.
func (c *Client) GetPostEdit(ctx context.Context, postID int) (*PostEditResponse, error) {
	var resp PostEditResponse
	if err := c.doGET(ctx, fmt.Sprintf(pathPostEdit, postID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdatePostText is a high-level helper that changes ONLY the text of an
// existing post while preserving its schedule, project, attachments, page
// targets, and publication settings. It fetches the current post state via
// GetPostEdit, swaps the text of each existing per-network text variant
// (keeping every entry's SourceID), and sends the full payload back via
// PUT /posts/{id}.
//
// Page-target guard: a schedule-driven post (publication_when_type=3, by
// schedule) carries an EMPTY selected_pages_by_source_ids — its page targets
// come from the schedule, not from the post's own selection, so sending an
// empty selection back is harmless. A non-schedule-driven post (when_type
// != 3) carries its OWN page targets; if the edit response does not provide
// them (empty selection), the helper refuses rather than send a request
// that would clear the targets (publishing to nothing). The discriminator
// is when_type, NOT where_type: measured on a live account, where_type=1
// appears on both schedule-driven and non-schedule-driven posts alike —
// the field that actually separates them is when_type (3=by schedule). This
// is the same discriminator the sibling guard below uses and the same one
// ImportSearchPost/CopySearchPost/RewriteSearchPost use.
//
// Schedule guard: a schedule-driven post (publication_when_type=3) MUST
// carry a non-zero schedule_id recovered from the edit response. When
// schedule_id is 0 the helper refuses to issue any request — mirroring the
// create-path guard in ImportSearchPost/CopySearchPost/RewriteSearchPost
// that refuses when_type=3 with an empty schedules_ids. The schedule_id
// field is sent WITHOUT omitempty so a zero is transmitted explicitly
// rather than silently dropped (which would leave the server with a
// by-schedule post targeted at no schedule).
//
// Null normalization: the server expects arrays and objects, not null —
// matching the three sibling writers (CopySearchPost, RewriteSearchPost,
// ImportSearchPost) which all normalize nil slices/maps to empty. A
// text-only post (zero attachments) or an edit response omitting the
// selection key yields nil values that encoding/json marshals as null;
// UpdatePostText normalizes both to []Attachment{} / map[int][]int{} before
// building the payload.
//
// Attachments: GET /posts/{id}/edit returns the SAME singular vocabulary
// the scraped-post edit endpoint does — measured across 11 real own-posts
// (20 attachments of type "photo" and 1 of type "video", no pre-grouped
// "photos" type). The PUT /posts/{id} endpoint expects photos and videos
// grouped under a single {type: "photos"} attachment, so the scraped-post
// grouping helper SearchPostEditAttachments is the correct transform here
// (verified by a 12-call live round trip through UpdatePostText with 1, 2,
// 3 and 6 attachments — every post kept its photos and the one video,
// which still read type "video" afterwards). This is measured, not assumed.
//
// project_id is sent back from edit.ProjectID so a project-scoped post
// does not lose its association through the full-state PUT — the same
// class of wipe the schedule_id guard exists to prevent.
//
// This is the correct way to edit a scheduled post's text — the low-level
// UpdatePost requires the full payload (schedule_id singular, attachments
// grouped as {type: "photos"}, selected_pages_by_source_ids as an object,
// etc.) or the server returns 500.
func (c *Client) UpdatePostText(ctx context.Context, postID int, newText string) (*DeletePostResponse, error) {
	edit, err := c.GetPostEdit(ctx, postID)
	if err != nil {
		return nil, err
	}

	// Preserve per-network text variants: replace only .Text, keep each
	// entry's SourceID. Fall back to a single shared entry if the server
	// returned none.
	texts := edit.Texts
	if len(texts) == 0 {
		texts = []PostText{{Text: newText, SourceID: 0}}
	} else {
		for i := range texts {
			texts[i].Text = newText
		}
	}

	// Recover the page selection the server actually returned. The edit
	// response uses selected_pages_by_source_ids (an object keyed by source
	// ID), NOT the flat selected_pages_ids array used by publish-now.
	selection := edit.SelectedPagesBySourceIDs

	// Fail closed: a non-schedule-driven post (when_type != 3) carries its
	// own page targets. If we cannot recover them (empty selection), refuse
	// rather than send a request that would clear the targets (publishing
	// to nothing). The discriminator is when_type (3=by schedule), NOT
	// where_type — where_type=1 appears on both schedule-driven and
	// non-schedule-driven posts (measured on a live account).
	if edit.PublicationWhenType != 3 && len(selection) == 0 {
		return nil, fmt.Errorf("hooppy: UpdatePostText post %d: publication_when_type=%d is not 3 (by schedule) and no page selection could be recovered from the edit response — refusing to send a request that would clear page targets",
			postID, edit.PublicationWhenType)
	}

	// Fail closed: a schedule-driven post (when_type=3, by schedule) with a
	// zero schedule_id would be published to no schedule — the same
	// publish-to-nothing hole the create-path guard prevents. The edit
	// endpoint returns a single schedule_id (not an array); when it is 0
	// the association cannot be recovered, so refuse before issuing any
	// request.
	if edit.PublicationWhenType == 3 && edit.ScheduleID == 0 {
		return nil, fmt.Errorf("hooppy: UpdatePostText post %d: publication_when_type=3 (by schedule) but the edit response carried schedule_id=0 — cannot recover the schedule association, refusing to send a request that would target no schedule",
			postID)
	}

	// Server expects arrays and objects, not null — matching the three
	// sibling writers (CopySearchPost, RewriteSearchPost, ImportSearchPost).
	// A text-only post yields a nil attachments slice; an edit response
	// omitting the selection key yields a nil map. Both marshal as null,
	// which the server may interpret as "clear". Normalize to empty.
	attachments := SearchPostEditAttachments(edit.Attachments)
	if attachments == nil {
		attachments = []Attachment{}
	}
	if selection == nil {
		selection = map[int][]int{}
	}
	payload := struct {
		AsCopy                   int              `json:"as_copy"`
		PublicationWhenType      int              `json:"publication_when_type"`
		PublicationHowType       int              `json:"publication_how_type"`
		PublicationWhereType     int              `json:"publication_where_type"`
		PublicationDate          *PublicationDate `json:"publication_date,omitempty"`
		Texts                    []PostText       `json:"texts"`
		Attachments              []Attachment     `json:"attachments"`
		SelectedPagesBySourceIDs map[int][]int    `json:"selected_pages_by_source_ids"`
		ScheduleID               int              `json:"schedule_id"`
		ProjectID                int              `json:"project_id,omitempty"`
	}{
		AsCopy:                   0,
		PublicationWhenType:      edit.PublicationWhenType,
		PublicationHowType:       edit.PublicationHowType,
		PublicationWhereType:     edit.PublicationWhereType,
		PublicationDate:          edit.PublicationDate,
		Texts:                    texts,
		Attachments:              attachments,
		SelectedPagesBySourceIDs: selection,
		ScheduleID:               edit.ScheduleID,
		ProjectID:                edit.ProjectID,
	}
	return c.UpdatePost(ctx, postID, payload)
}

// DeletePost removes a single post by ID.
func (c *Client) DeletePost(ctx context.Context, id int) (*DeletePostResponse, error) {
	var resp DeletePostResponse
	if err := c.doDELETE(ctx, fmt.Sprintf(pathPostDelete, id), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// MaxBatchDeleteIDs is the maximum number of post IDs allowed in a single
// BatchDeletePosts call. Requests exceeding this limit are rejected to
// prevent unbounded request body size.
const MaxBatchDeleteIDs = 1000

// BatchDeletePosts removes multiple posts by ID. IDs are joined with commas.
// A maximum of MaxBatchDeleteIDs IDs (1000) may be passed in a single call;
// larger batches must be split by the caller.
func (c *Client) BatchDeletePosts(ctx context.Context, ids []int) (*DeletePostResponse, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("hooppy: BatchDeletePosts requires at least one ID")
	}
	if len(ids) > MaxBatchDeleteIDs {
		return nil, fmt.Errorf("hooppy: BatchDeletePosts received %d IDs, max is %d — split into multiple calls", len(ids), MaxBatchDeleteIDs)
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = strconv.Itoa(id)
	}
	body := BatchDeletePostsRequest{IDs: strings.Join(strs, ",")}
	var resp DeletePostResponse
	if err := c.doPOST(ctx, pathPostsBatchDelete, body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
