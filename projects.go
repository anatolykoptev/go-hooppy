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
//
// ListAllSchedules drops the server's last-seen total_rows. Callers that
// need it (to detect a truncated walk) should use ListAllSchedulesWithTotal
// and allListEnvelope.
func (c *Client) ListAllSchedules(ctx context.Context) ([]Schedule, error) {
	all, _, err := c.ListAllSchedulesWithTotal(ctx)
	return all, err
}

// ListAllSchedulesWithTotal is ListAllSchedules but also returns the
// server's last-seen total_rows. The pair (list, totalRows) is meant to be
// passed to allListEnvelope, which fails loud when len(list) != totalRows
// (a server that cleared is_has_more early served a truncated list —
// indistinguishable from a complete one without the comparison).
func (c *Client) ListAllSchedulesWithTotal(ctx context.Context) ([]Schedule, int, error) {
	var all []Schedule
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllSchedules exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListSchedules(ctx, page)
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

// ListAllProjects walks /posts/projects from page 1, accumulating
// projects until is_has_more is false, and returns the full list with no
// duplicates. See ListAllSchedules for the 1-indexed rationale and the
// sanity cap.
//
// ListAllProjects drops the server's last-seen total_rows. Callers that
// need it should use ListAllProjectsWithTotal and allListEnvelope.
func (c *Client) ListAllProjects(ctx context.Context) ([]Project, error) {
	all, _, err := c.ListAllProjectsWithTotal(ctx)
	return all, err
}

// ListAllProjectsWithTotal is ListAllProjects but also returns the server's
// last-seen total_rows. See ListAllSchedulesWithTotal.
func (c *Client) ListAllProjectsWithTotal(ctx context.Context) ([]Project, int, error) {
	var all []Project
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllProjects exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListProjects(ctx, page)
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

// AllListEnvelope is the response shape for an --all walk: the full
// accumulated list with the server's last-seen total_rows (NOT len(list))
// and is_has_more pinned false. It mirrors the {list, total_rows,
// is_has_more} shape the single-page list endpoints return, so callers
// can treat an --all result identically to a one-page result.
type AllListEnvelope struct {
	List      interface{} `json:"list"`
	TotalRows int         `json:"total_rows"`
	IsHasMore bool        `json:"is_has_more"`
}

// NewAllListEnvelope builds the AllListEnvelope for an --all walk and is the
// single fail-loud gate for a truncated walk. It passes the server's
// last-seen totalRows through (NOT listLen) and errors when listLen !=
// totalRows — a server that cleared is_has_more early serves a short list
// while its total_rows still exceeds the rows served, and without this
// check the truncation is indistinguishable from a complete walk. This is
// consistent with the fail-loud choice already made for maxListAllPages.
//
// listLen is taken explicitly (rather than reflected out of list) so the
// caller hands in the same len it accumulated, making the contract obvious
// at the call site.
func NewAllListEnvelope(list interface{}, listLen, totalRows int) (AllListEnvelope, error) {
	if listLen != totalRows {
		return AllListEnvelope{}, fmt.Errorf("hooppy: --all walk returned %d rows but the server's total_rows=%d — the walk was truncated (is_has_more cleared early); refusing to report a short list as complete", listLen, totalRows)
	}
	return AllListEnvelope{List: list, TotalRows: totalRows, IsHasMore: false}, nil
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
