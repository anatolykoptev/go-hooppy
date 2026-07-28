package hooppy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListAccountsFilter narrows the GET /accounts query.
type ListAccountsFilter struct {
	SourceID int // 0 = no filter
	Page     int // 0 = no pagination
}

// ListAccounts returns the connected social network accounts.
func (c *Client) ListAccounts(ctx context.Context, f ListAccountsFilter) (*AccountsResponse, error) {
	params := url.Values{}
	if f.SourceID > 0 {
		params.Set("source_id", strconv.Itoa(f.SourceID))
	}
	if f.Page > 0 {
		params.Set("page", strconv.Itoa(f.Page))
	}
	var resp AccountsResponse
	if err := c.doGET(ctx, pathAccounts, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListPagesFilter narrows the GET /accounts/pages query.
type ListPagesFilter struct {
	SourceID  int
	AccountID int
	Page      int
}

// ListPages returns the groups/pages connected to the user's accounts.
func (c *Client) ListPages(ctx context.Context, f ListPagesFilter) (*PagesResponse, error) {
	params := url.Values{}
	if f.SourceID > 0 {
		params.Set("source_id", strconv.Itoa(f.SourceID))
	}
	if f.AccountID > 0 {
		params.Set("account_id", strconv.Itoa(f.AccountID))
	}
	if f.Page > 0 {
		params.Set("page", strconv.Itoa(f.Page))
	}
	var resp PagesResponse
	if err := c.doGET(ctx, pathPages, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListAllPages walks GET /accounts/pages from page 1, accumulating pages
// until is_has_more is false. The walk starts at page 1 so the first page
// is not fetched twice (the Hooppy API is 1-indexed and a request with no
// page param is byte-identical to ?page=1). See projects.ListAllSchedules
// for the 1-indexed rationale and the sanity cap.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. Use
// ListAllPagesWithTotal with NewAllListEnvelope to detect them (see
// NewAllListEnvelope for what it does and does not catch).
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllPages returns an error instead of
// looping forever or silently truncating.
func (c *Client) ListAllPages(ctx context.Context, f ListPagesFilter) ([]Page, error) {
	all, _, err := c.ListAllPagesWithTotal(ctx, f)
	return all, err
}

// ListAllPagesWithTotal is ListAllPages but also returns the server's
// last-seen total_rows. The pair (list, totalRows) is meant to be passed
// to NewAllListEnvelope. See projects.ListAllSchedulesWithTotal and
// NewAllListEnvelope for what the envelope catches and what it does not.
func (c *Client) ListAllPagesWithTotal(ctx context.Context, f ListPagesFilter) ([]Page, int, error) {
	all := make([]Page, 0)
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllPages exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		f.Page = page
		resp, err := c.ListPages(ctx, f)
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

// DisconnectPage disconnects a page (social media group) by ID via
// DELETE /accounts/pages/{id}. This is idempotent — deleting a
// non-existent page returns success.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) DisconnectPage(ctx context.Context, id int) (*DeleteResponse, error) {
	var resp DeleteResponse
	if err := c.doDELETE(ctx, fmt.Sprintf(pathPageDisconnect, id), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
