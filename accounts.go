package hooppy

import (
	"context"
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
