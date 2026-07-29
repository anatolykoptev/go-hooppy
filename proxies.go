package hooppy

import (
	"context"
	"fmt"
)

// ListProxies returns the user's proxies via GET /proxies.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) ListProxies(ctx context.Context) (*ProxiesResponse, error) {
	var resp ProxiesResponse
	if err := c.doGET(ctx, pathProxies, nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
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
