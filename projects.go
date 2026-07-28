package hooppy

import (
	"context"
	"fmt"
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

// UpdateProject updates a project's name via PUT /posts/projects/{id}.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) UpdateProject(ctx context.Context, id int, name string) (*DeleteResponse, error) {
	var resp DeleteResponse
	if err := c.doPUT(ctx, fmt.Sprintf(pathProjectDelete, id), map[string]string{"name": name}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteProject deletes a project via DELETE /posts/projects/{id}.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) DeleteProject(ctx context.Context, id int) (*DeleteResponse, error) {
	var resp DeleteResponse
	if err := c.doDELETE(ctx, fmt.Sprintf(pathProjectDelete, id), &resp); err != nil {
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

// CreateSchedule creates a new publication schedule via POST /posts/schedules.
// Use NewSchedulePayload(name) to get a payload with sensible defaults,
// then override fields as needed.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) CreateSchedule(ctx context.Context, payload SchedulePayload) (*ScheduleResponse, error) {
	var resp ScheduleResponse
	if err := c.doPOST(ctx, pathSchedules, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateSchedule updates an existing schedule via PUT /posts/schedules/{id}.
// The payload must include ALL required fields (use NewSchedulePayload then
// override, or reuse the fields from an existing Schedule).
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) UpdateSchedule(ctx context.Context, id int, payload SchedulePayload) (*ScheduleResponse, error) {
	var resp ScheduleResponse
	if err := c.doPUT(ctx, fmt.Sprintf(pathScheduleDelete, id), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteSchedule deletes a schedule via DELETE /posts/schedules/{id}.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) DeleteSchedule(ctx context.Context, id int) (*ScheduleResponse, error) {
	var resp ScheduleResponse
	if err := c.doDELETE(ctx, fmt.Sprintf(pathScheduleDelete, id), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
