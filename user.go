package hooppy

import "context"

// GetUser returns the current authenticated user via GET /users/me.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) GetUser(ctx context.Context) (*UserResponse, error) {
	var resp UserResponse
	if err := c.doGET(ctx, pathUser, nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetSettings returns the account settings via GET /users/settings.
// Narrowly modelled: only timezone_id, timezone_offset, and the timezones
// array are decoded. The response carries api_token, gpt_key, and
// ru_captcha_key — none of them are modelled, so they are dropped at decode
// and cannot reach any re-marshal. See TestSettings_DecodeCredentialHygiene.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) GetSettings(ctx context.Context) (*SettingsResponse, error) {
	var resp SettingsResponse
	if err := c.doGET(ctx, pathUserSettings, nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}
