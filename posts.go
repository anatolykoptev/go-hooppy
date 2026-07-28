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
func (c *Client) ListPosts(ctx context.Context, f ListPostsFilter) (*PostsResponse, error) {
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
	if f.SourceID > 0 {
		params.Set("source_id", strconv.Itoa(f.SourceID))
	}
	if f.AccountID > 0 {
		params.Set("account_id", strconv.Itoa(f.AccountID))
	}
	if f.PageID > 0 {
		params.Set("page_id", strconv.Itoa(f.PageID))
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
// UNDOCUMENTED: GET /posts/{id}/edit is not in the public OpenAPI spec.
type PostEditResponse struct {
	ID                   int              `json:"id"`
	PublicationWhenType  int              `json:"publication_when_type"`
	PublicationHowType   int              `json:"publication_how_type"`
	PublicationWhereType int              `json:"publication_where_type"`
	PublicationDate      *PublicationDate `json:"publication_date"`
	CreatedBy            int              `json:"created_by"`
	Texts                []PostText       `json:"texts"`
	Attachments          []Attachment     `json:"attachments"`
	ScheduleID           int              `json:"schedule_id"`
	ProjectID            int              `json:"project_id"`
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
// existing post while preserving its schedule, attachments, and publication
// settings. It fetches the current post state via GetPostEdit, swaps the
// text, and sends the full payload back via PUT /posts/{id}.
//
// This is the correct way to edit a scheduled post's text — the low-level
// UpdatePost requires the full payload (schedule_id singular, attachments
// grouped as {type: "photos"}, etc.) or the server returns 500.
func (c *Client) UpdatePostText(ctx context.Context, postID int, newText string) (*DeletePostResponse, error) {
	edit, err := c.GetPostEdit(ctx, postID)
	if err != nil {
		return nil, err
	}
	attachments := SearchPostEditAttachments(edit.Attachments)
	payload := struct {
		AsCopy               int              `json:"as_copy"`
		PublicationWhenType  int              `json:"publication_when_type"`
		PublicationHowType   int              `json:"publication_how_type"`
		PublicationWhereType int              `json:"publication_where_type"`
		PublicationDate      *PublicationDate `json:"publication_date,omitempty"`
		Texts                []PostText       `json:"texts"`
		Attachments          []Attachment     `json:"attachments"`
		SelectedPagesIDs     []int            `json:"selected_pages_ids"`
		ScheduleID           int              `json:"schedule_id,omitempty"`
	}{
		AsCopy:               0,
		PublicationWhenType:  edit.PublicationWhenType,
		PublicationHowType:   edit.PublicationHowType,
		PublicationWhereType: edit.PublicationWhereType,
		PublicationDate:      edit.PublicationDate,
		Texts:                []PostText{{Text: newText, SourceID: 0}},
		Attachments:          attachments,
		SelectedPagesIDs:     []int{},
		ScheduleID:           edit.ScheduleID,
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
