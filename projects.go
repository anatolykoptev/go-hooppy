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
	// Reject negatives before any request: the old `> 0` guard let a
	// negative take neither branch — no error, no page parameter, the
	// server returns page 1, and a caller's paging loop silently re-reads
	// the first page. Same defect class the sweep closed across the
	// search/posts/accounts/pages filters (see posts_search.go). Reachable
	// from the shipped CLI (cmd/hooppy binds --page with IntVar; pflag
	// accepts negatives) and the MCP tool (in.Page, no schema minimum).
	// Zero stays the unset sentinel.
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListProjects: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
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
	// Reject negatives before any request: the old `> 0` guard let a
	// negative take neither branch — no error, no page parameter, the
	// server returns page 1, and a caller's paging loop silently re-reads
	// the first page. Same defect class the sweep closed across the
	// search/posts/accounts/pages filters (see posts_search.go). Reachable
	// from the shipped CLI (cmd/hooppy binds --page with IntVar; pflag
	// accepts negatives) and the MCP tool (in.Page, no schema minimum).
	// Zero stays the unset sentinel.
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListSchedules: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
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
// schedules until is_has_more is false. The walk MUST start at page 1: the
// Hooppy API is 1-indexed and a request with no page param is
// byte-identical to ?page=1, so a walk starting at page 0 fetches the first
// page twice. Starting at page 1 removes that one duplicate source only.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. Use
// ListAllSchedulesWithTotal with NewAllListEnvelope to detect them (see
// NewAllListEnvelope for what it does and does not catch).
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllSchedules returns an error
// instead of looping forever or silently truncating.
func (c *Client) ListAllSchedules(ctx context.Context) ([]Schedule, error) {
	all, _, err := c.ListAllSchedulesWithTotal(ctx)
	return all, err
}

// ListAllSchedulesWithTotal is ListAllSchedules but also returns the
// server's last-seen total_rows. The pair (list, totalRows) is meant to be
// passed to NewAllListEnvelope, which fails loud when the count of unique
// ids does not match total_rows — a server that cleared is_has_more early
// served a truncated list. See NewAllListEnvelope for the specific failure
// this catches and the ones it does not.
func (c *Client) ListAllSchedulesWithTotal(ctx context.Context) ([]Schedule, int, error) {
	all := make([]Schedule, 0)
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
// projects until is_has_more is false. The walk starts at page 1 so the
// first page is not fetched twice (see ListAllSchedules for the 1-indexed
// rationale and the sanity cap). Starting at page 1 removes that one
// duplicate source only.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. Use
// ListAllProjectsWithTotal with NewAllListEnvelope to detect them (see
// NewAllListEnvelope for what the envelope catches and what it does not).
func (c *Client) ListAllProjects(ctx context.Context) ([]Project, error) {
	all, _, err := c.ListAllProjectsWithTotal(ctx)
	return all, err
}

// ListAllProjectsWithTotal is ListAllProjects but also returns the server's
// last-seen total_rows. See ListAllSchedulesWithTotal.
func (c *Client) ListAllProjectsWithTotal(ctx context.Context) ([]Project, int, error) {
	all := make([]Project, 0)
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

// NewAllListEnvelope builds the AllListEnvelope for an --all walk and is a
// fail-loud gate for ONE specific truncation failure: the server cleared
// is_has_more early while its total_rows still exceeded the rows served.
// It passes the server's last-seen totalRows through (NOT len(list)) and
// errors when the count of UNIQUE ids in list does not equal totalRows.
//
// What this DOES catch:
//   - is_has_more cleared early with a stale total_rows that does not match
//     the unique rows served.
//   - A duplicate row served across two pages that masks a missing row when
//     total_rows was adjusted down to match the raw length: e.g. page 1
//     serves [1,2], page 2 serves [2,3] (row 2 duplicated, row 4 missing),
//     total_rows=4, raw len=4 — a raw-length check passes, but unique ids
//     {1,2,3}=3 != 4, so the envelope errors.
//
// What this DOES NOT catch:
//   - A walk that is missing rows but whose total_rows was also adjusted
//     down to match the (short) unique set it served. With offset
//     pagination, a row deleted mid-walk shifts everything after it down by
//     one; the missing row is never served, total_rows drops by one to
//     match, and unique_count == total_rows passes while the list is short
//     by one item. This check is NOT a proof that the walk was complete.
//
// Measured premise (read-only, GET /posts): the server's total_rows
// honours the query filter, not the unfiltered collection total. An
// unfiltered request, a request filtered by social network, and a request
// filtered by schedule each return a progressively smaller total_rows
// consistent with the rows actually served, and a filtered walk that fits
// in a single page reports is_has_more=false with total_rows equal to the
// served count. So the unique-count check above is safe under filters — a
// filtered --all walk does not error unconditionally against an unfiltered
// total.
//
// idFunc extracts the unique identity of each element. It MUST be non-nil;
// the unique-count is meaningless without it. This is consistent with the
// fail-loud choice already made for maxListAllPages.
func NewAllListEnvelope[T any](list []T, totalRows int, idFunc func(T) int) (AllListEnvelope, error) {
	if idFunc == nil {
		return AllListEnvelope{}, fmt.Errorf("hooppy: NewAllListEnvelope requires a non-nil idFunc — the unique-count check is meaningless without it")
	}
	unique := make(map[int]struct{}, len(list))
	for _, item := range list {
		unique[idFunc(item)] = struct{}{}
	}
	if len(unique) != totalRows {
		return AllListEnvelope{}, fmt.Errorf("hooppy: --all walk returned %d unique rows but the server's total_rows=%d — the walk was truncated (is_has_more cleared early with a stale total_rows, or a duplicate row masked a missing one); refusing to report a short list as complete", len(unique), totalRows)
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
