package hooppy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListNotifications returns the user's notifications via GET /notifications.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) ListNotifications(ctx context.Context, page int) (*NotificationsResponse, error) {
	params := url.Values{}
	// Reject negatives before any request: the old `> 0` guard let a
	// negative take neither branch — no error, no page parameter, the
	// server returns page 1, and a caller's paging loop silently re-reads
	// the first page. Same defect class the sweep closed across the
	// search/posts/accounts/pages filters (see posts_search.go). Zero
	// stays the unset sentinel.
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListNotifications: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp NotificationsResponse
	if err := c.doGET(ctx, pathNotifications, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
