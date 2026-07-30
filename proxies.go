package hooppy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListProxies returns the user's proxies via GET /proxies. page is 1-indexed
// (0 or omit = first page); a negative is rejected before any request so a
// paging loop cannot silently re-read page 1. Same defect class as the
// search/posts/accounts/pages page guards (see posts_search.go).
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0). The endpoint carries
// the standard {list, total_rows, is_has_more, rows_limit} envelope (see
// testdata/live/proxies.json), so it is paged like its siblings; the page
// parameter is sent verbatim and the server answers.
func (c *Client) ListProxies(ctx context.Context, page int) (*ProxiesResponse, error) {
	params := url.Values{}
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListProxies: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp ProxiesResponse
	if err := c.doGET(ctx, pathProxies, params, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListAllProxies walks GET /proxies from page 1, accumulating proxies until
// is_has_more is false. The walk starts at page 1 so the first page is not
// fetched twice (the Hooppy API is 1-indexed and a request with no page param
// is byte-identical to ?page=1). See projects.ListAllSchedules for the
// 1-indexed rationale and the sanity cap.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. Use
// ListAllProxiesWithTotal with NewAllListEnvelope to detect them (see
// NewAllListEnvelope for what it does and does not catch).
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllProxies returns an error instead of
// looping forever or silently truncating.
func (c *Client) ListAllProxies(ctx context.Context) ([]Proxy, error) {
	all, _, err := c.ListAllProxiesWithTotal(ctx)
	return all, err
}

// ListAllProxiesWithTotal is ListAllProxies but also returns the server's
// last-seen total_rows. The pair (list, totalRows) is meant to be passed to
// NewAllListEnvelope. See projects.ListAllSchedulesWithTotal and
// NewAllListEnvelope for what the envelope catches and what it does not.
func (c *Client) ListAllProxiesWithTotal(ctx context.Context) ([]Proxy, int, error) {
	all := make([]Proxy, 0)
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllProxies exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListProxies(ctx, page)
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

// CreateProxy creates a new proxy via POST /proxies.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) CreateProxy(ctx context.Context, payload ProxyPayload) (*ProxyResponse, error) {
	var resp ProxyResponse
	if err := c.doPOST(ctx, pathProxies, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateProxy updates an existing proxy via PUT /proxies/{id}.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) UpdateProxy(ctx context.Context, id int, payload ProxyPayload) (*ProxyResponse, error) {
	var resp ProxyResponse
	if err := c.doPUT(ctx, fmt.Sprintf(pathProxyByID, id), payload, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteProxy deletes a proxy via DELETE /proxies/{id}.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) DeleteProxy(ctx context.Context, id int) (*ProxyResponse, error) {
	var resp ProxyResponse
	if err := c.doDELETE(ctx, fmt.Sprintf(pathProxyByID, id), &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}
