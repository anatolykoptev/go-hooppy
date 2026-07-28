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
