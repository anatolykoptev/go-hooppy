package hooppy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListWatermarks returns the user's watermarks via GET /watermarks.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) ListWatermarks(ctx context.Context, page int) (*WatermarksResponse, error) {
	params := url.Values{}
	// Reject negatives before any request: the old `> 0` guard let a
	// negative take neither branch — no error, no page parameter, the
	// server returns page 1, and a caller's paging loop silently re-reads
	// the first page. Same defect class the sweep closed across the
	// search/posts/accounts/pages filters (see posts_search.go). Zero
	// stays the unset sentinel.
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListWatermarks: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp WatermarksResponse
	if err := c.doGET(ctx, pathWatermarks, params, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateWatermark creates a new watermark via POST /watermarks.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) CreateWatermark(ctx context.Context, payload WatermarkPayload) (*WatermarkResponse, error) {
	var resp WatermarkResponse
	if err := c.doPOST(ctx, pathWatermarks, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateWatermark updates an existing watermark via PUT /watermarks/{id}.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) UpdateWatermark(ctx context.Context, id int, payload WatermarkPayload) (*WatermarkResponse, error) {
	var resp WatermarkResponse
	if err := c.doPUT(ctx, fmt.Sprintf(pathWatermarkByID, id), payload, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteWatermark deletes a watermark via DELETE /watermarks/{id}.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) DeleteWatermark(ctx context.Context, id int) (*WatermarkResponse, error) {
	var resp WatermarkResponse
	if err := c.doDELETE(ctx, fmt.Sprintf(pathWatermarkByID, id), &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}
