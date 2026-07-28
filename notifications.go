package hooppy

import (
	"context"
	"net/url"
	"strconv"
)

// ListNotifications returns the user's notifications via GET /notifications.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) ListNotifications(ctx context.Context, page int) (*NotificationsResponse, error) {
	params := url.Values{}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp NotificationsResponse
	if err := c.doGET(ctx, pathNotifications, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
