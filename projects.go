package hooppy

import (
	"context"
	"net/url"
	"strconv"
)

// ListProjects returns the user's post projects.
func (c *Client) ListProjects(ctx context.Context, page int) (*ProjectsResponse, error) {
	params := url.Values{}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp ProjectsResponse
	if err := c.doGET(ctx, pathProjects, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListSchedules returns the user's publication schedules.
func (c *Client) ListSchedules(ctx context.Context, page int) (*SchedulesResponse, error) {
	params := url.Values{}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp SchedulesResponse
	if err := c.doGET(ctx, pathSchedules, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
