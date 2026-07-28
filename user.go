package hooppy

import "context"

// GetUser returns the current authenticated user via GET /users/me.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) GetUser(ctx context.Context) (*UserResponse, error) {
	var resp UserResponse
	if err := c.doGET(ctx, pathUser, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
