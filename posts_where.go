package hooppy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// PageKeyed is a map keyed by Hooppy page id (as a JSON object key, so a
// string). The server is PHP: an EMPTY associative array can be encoded as
// the list `[]` instead of `{}`. That form has been measured on
// posts_by_days (see ScheduleCalendar) but NOT yet on this endpoint — the
// decode accepts it defensively so one empty map cannot abort the whole
// response and lose the pages list next to it. A NON-empty list is still an
// error: that would be a real shape change.
type PageKeyed[T any] map[string]T

// UnmarshalJSON accepts the object form and the empty-list form.
func (m *PageKeyed[T]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var list []json.RawMessage
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return err
		}
		if len(list) > 0 {
			return fmt.Errorf("page-keyed map: got a JSON array of %d elements; only the EMPTY array is accepted", len(list))
		}
		*m = PageKeyed[T]{}
		return nil
	}
	var v map[string]T
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return err
	}
	if v == nil {
		v = map[string]T{}
	}
	*m = v
	return nil
}

// PublishedPagesAccount is the account half of one social_pages_by_accounts
// entry. The reference documents two example shapes (id/source_id/name, and
// additionally social_id/photo/link); every documented key is modelled.
type PublishedPagesAccount struct {
	ID       int             `json:"id"`
	SourceID int             `json:"source_id"`
	Name     string          `json:"name"`
	Photo    string          `json:"photo,omitempty"`
	Link     string          `json:"link,omitempty"`
	SocialID json.RawMessage `json:"social_id,omitempty"` // string on the wire per the docs example; kept raw (live accounts send social ids as strings)
}

// PublishedPage is one destination page of a post.
type PublishedPage struct {
	ID       int             `json:"id"`
	Name     string          `json:"name"`
	Link     string          `json:"link"`
	Photo    string          `json:"photo,omitempty"`
	Alias    string          `json:"alias,omitempty"`
	Type     string          `json:"type,omitempty"`
	SocialID json.RawMessage `json:"social_id,omitempty"`
}

// PublishedPagesByAccount groups destination pages under their account.
type PublishedPagesByAccount struct {
	Account PublishedPagesAccount `json:"account"`
	Pages   []PublishedPage       `json:"pages"`
}

// PostPublishedPagesResponse is the GET /items/social/pages envelope for
// items_type=posts. The three posts_* maps are keyed by PAGE id.
//
//   - PostsPublishedLinks: page id → public URL of the already-published record.
//   - PostsPublishedPlanned: page id → the «отложка» (scheduled-by-network)
//     marker. The reference documents the key but shows only `{}`, so the
//     value shape is unobserved and kept raw.
//   - PostsErrors: page id → Russian error texts from the last publish attempt.
type PostPublishedPagesResponse struct {
	SocialPagesByAccounts []PublishedPagesByAccount  `json:"social_pages_by_accounts"`
	PostsPublishedLinks   PageKeyed[string]          `json:"posts_published_links"`
	PostsPublishedPlanned PageKeyed[json.RawMessage] `json:"posts_published_planned"`
	PostsErrors           PageKeyed[[]string]        `json:"posts_errors"`
}

// GetPostPublishedPages returns the pages a post publishes to in ONE social
// network, the public URLs of the records already published and the
// per-page publish errors, via GET /items/social/pages?items_type=posts.
// The endpoint answers for a single source_id; a post that has no pages in
// that network yields an APIError (HTTP 500 «No pages for this object.»).
//
// Both ids must be positive: an impossible id is refused before the wire.
// The request is not retried — the server's answers for "no pages" and an
// unknown type are definitive 500s that a retry only repeats.
//
// Documented in the official API reference (openapi.yaml): GET
// /items/social/pages («Где опубликован пост»).
func (c *Client) GetPostPublishedPages(ctx context.Context, postID, sourceID int) (*PostPublishedPagesResponse, error) {
	if postID <= 0 {
		return nil, fmt.Errorf("hooppy: GetPostPublishedPages: postID must be a positive id (got %d)", postID)
	}
	if sourceID <= 0 {
		return nil, fmt.Errorf("hooppy: GetPostPublishedPages: sourceID must be a positive social-network id (got %d)", sourceID)
	}
	params := url.Values{}
	params.Set("items_type", "posts")
	params.Set("item_id", strconv.Itoa(postID))
	params.Set("source_id", strconv.Itoa(sourceID))
	var resp PostPublishedPagesResponse
	if err := c.doGET(ctx, pathItemsSocialPages, params, &resp, false); err != nil {
		return nil, err
	}
	return &resp, nil
}
