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

// ListAllWatermarks walks GET /watermarks from page 1, accumulating
// watermarks until is_has_more is false. The walk starts at page 1 so the
// first page is not fetched twice (the Hooppy API is 1-indexed and a request
// with no page param is byte-identical to ?page=1). See
// projects.ListAllSchedules for the 1-indexed rationale and the sanity cap.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. Use
// ListAllWatermarksWithTotal with NewAllListEnvelope to detect them (see
// NewAllListEnvelope for what it does and does not catch).
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllWatermarks returns an error instead
// of looping forever or silently truncating.
func (c *Client) ListAllWatermarks(ctx context.Context) ([]Watermark, error) {
	all, _, err := c.ListAllWatermarksWithTotal(ctx)
	return all, err
}

// ListAllWatermarksWithTotal is ListAllWatermarks but also returns the
// server's last-seen total_rows. The pair (list, totalRows) is meant to be
// passed to NewAllListEnvelope. See projects.ListAllSchedulesWithTotal and
// NewAllListEnvelope for what the envelope catches and what it does not.
func (c *Client) ListAllWatermarksWithTotal(ctx context.Context) ([]Watermark, int, error) {
	all := make([]Watermark, 0)
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllWatermarks exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListWatermarks(ctx, page)
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
