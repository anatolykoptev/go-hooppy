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

// CreateProject creates a new project via POST /posts/projects.
// Use NewProjectPayload(name, pageID) to get a payload with sensible
// defaults, then override fields as needed.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) CreateProject(ctx context.Context, payload ProjectPayload) (*ProjectResponse, error) {
	var resp ProjectResponse
	if err := c.doPOST(ctx, pathProjects, payload, &resp); err != nil {
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

// maxListAllPages bounds the ListAll* pagination walks so a server that
// never clears is_has_more cannot spin forever. 1000 pages × 20 rows per
// page = 20000 rows, well beyond any realistic account; the cap is a
// safety net, not a real limit. When the cap is hit the walk returns an
// error rather than silently truncating.
const maxListAllPages = 1000

// ListAllSchedules walks /posts/schedules from page 1, accumulating
// schedules until is_has_more is false, and returns the full list with no
// duplicates. The walk MUST start at page 1: the Hooppy API is 1-indexed
// and a request with no page param is byte-identical to ?page=1, so a
// walk starting at page 0 fetches the first page twice.
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllSchedules returns an error
// instead of looping forever or silently truncating.
func (c *Client) ListAllSchedules(ctx context.Context) ([]Schedule, error) {
	var all []Schedule
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, fmt.Errorf("hooppy: ListAllSchedules exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListSchedules(ctx, page)
		if err != nil {
			return nil, err
		}
		all = append(all, resp.List...)
		if !resp.IsHasMore {
			return all, nil
		}
	}
}

// ListAllProjects walks /posts/projects from page 1, accumulating
// projects until is_has_more is false, and returns the full list with no
// duplicates. See ListAllSchedules for the 1-indexed rationale and the
// sanity cap.
func (c *Client) ListAllProjects(ctx context.Context) ([]Project, error) {
	var all []Project
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, fmt.Errorf("hooppy: ListAllProjects exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListProjects(ctx, page)
		if err != nil {
			return nil, err
		}
		all = append(all, resp.List...)
		if !resp.IsHasMore {
			return all, nil
		}
	}
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
