package hooppy

import (
	"context"
	"encoding/json"
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
	if err := c.doGET(ctx, pathProjects, params, &resp, true); err != nil {
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
	if err := c.doPUT(ctx, fmt.Sprintf(pathProjectDelete, id), map[string]string{"name": name}, &resp, true); err != nil {
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
	if err := c.doDELETE(ctx, fmt.Sprintf(pathProjectDelete, id), &resp, true); err != nil {
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
	if err := c.doGET(ctx, pathSchedules, params, &resp, true); err != nil {
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
//
// Call sites and whether the collection can change mid-walk (the equality
// check above is only safe for low-churn collections):
//   - cmd/hooppy-mcp/main.go:212 — posts (ListAllPostsWithTotal). HIGH-CHURN:
//     posts are created and published continuously; a post created or
//     published between page fetches shifts the offset window and makes
//     unique != total on a healthy account — the equality check
//     false-alarms here exactly as it did for /notifications before PR #64.
//     NOT covered by the first-total rule; tracked in #70.
//   - cmd/hooppy-mcp/main.go:445 — projects. Low-churn; a project created
//     mid-walk is rare. Equality check is acceptable.
//   - cmd/hooppy-mcp/main.go:483 — schedules. Low-churn; same reasoning.
//   - cmd/hooppy/main.go:350 — projects (CLI). Same as the MCP projects site.
//   - cmd/hooppy/main.go:440 — schedules (CLI). Same as the MCP schedules site.
//
// The posts site is the known gap: it walks the highest-churn collection in
// the API with a check designed for low-churn ones. The fix is to apply the
// first-total rule (unique < firstTotal) there too, as doctor does for
// /notifications; see #70.
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
// CreateSchedule calls payload.Validate() before any request and refuses
// locally if the mode invariant is unmet (publication_how_type=1 with no
// selected_pages_by_source_ids, or how_type=2 with project_id=0). The error
// names the invariant and what would satisfy it — a server 500 that says only
// "Undefined index: <key>" teaches the caller nothing.
//
// NOTE: SchedulePayload models 36 of the 72 keys the server requires on this
// strictly full-state endpoint. Even with the mode invariant satisfied, the
// server may 500 on a missing key that SchedulePayload does not model. The
// proven create path is read-modify-write from an existing schedule's /edit
// response (see UpdateScheduleFromEdit); SchedulePayload-based create is
// kept for callers who can supply the full field set, but the honest default
// (NewSchedulePayload) fails Validate() until the caller completes it.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) CreateSchedule(ctx context.Context, payload SchedulePayload) (*ScheduleResponse, error) {
	if err := payload.Validate(); err != nil {
		return nil, err
	}
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
// WARNING — PARTIAL WRITER, DROPS UNMODELLED FIELDS (issue #66):
// SchedulePayload models 36 of the 72 keys the server carries on
// GET /posts/schedules/{id}/edit. PUTting a SchedulePayload sends only those
// 36 keys; the other 36 (including times, posts_hashtags, posts_links,
// projects, selected_albums_by_source_ids, social_pages_by_accounts,
// social_albums_by_pages, watermarks, and ~24 more the struct does not name)
// are ABSENT from the request body. The server treats PUT as full-state, so
// those fields are reset to their zero/empty values — a schedule's posting
// times, page selection, and project binding can be silently destroyed by a
// call that intended to change only the name.
//
// For any update that must preserve the schedule's existing state, use
// UpdateScheduleFromEdit instead — it fetches the full /edit response, applies
// the caller's change, and sends the complete object back so no unmodelled
// field is dropped. The CLI `schedules update` command routes through
// UpdateScheduleFromEdit for this reason.
//
// UpdateSchedule does NOT call Validate() — it is kept as a low-level seam
// for callers who construct a full payload themselves and do not want a guard
// they have already satisfied. If you are not certain you have a full payload,
// use UpdateScheduleFromEdit.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) UpdateSchedule(ctx context.Context, id int, payload SchedulePayload) (*ScheduleResponse, error) {
	var resp ScheduleResponse
	if err := c.doPUT(ctx, fmt.Sprintf(pathScheduleDelete, id), payload, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateScheduleFromEdit is a read-modify-write helper for updating a single
// schedule without dropping unmodelled fields. It:
//  1. Fetches the full schedule state via GetScheduleEdit (72 keys on the wire).
//  2. Unmarshals the response into map[string]json.RawMessage — every key's
//     raw bytes are preserved, including the 36 keys SchedulePayload does not
//     model.
//  3. Applies the caller's overrides (each value is JSON-marshalled and
//     replaces the corresponding key's RawMessage).
//  4. PUTs the complete object back.
//
// This is the mechanism the controller verified live: a round trip through
// this helper with a change to one field leaves every other field byte-identical
// on the wire. The test (TestUpdateScheduleFromEdit_ByteIdentity) asserts on
// the decoded request body, key by key — not on the Go struct, which by
// definition cannot show the fields it does not model.
//
// overrides is a map of JSON field name → raw JSON value. Use ScheduleOverride
// to build it from typed values:
//
//	overrides, err := hooppy.ScheduleOverride("name", "New Name")
//
// To compose multiple overrides, build the map directly (each value must be
// JSON-marshalled bytes):
//
//	overrides := map[string]json.RawMessage{}
//	nameBytes, _ := json.Marshal("New Name")
//	overrides["name"] = nameBytes
//	stateBytes, _ := json.Marshal(1)
//	overrides["state"] = stateBytes
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) UpdateScheduleFromEdit(ctx context.Context, id int, overrides map[string]json.RawMessage) (*ScheduleResponse, error) {
	if id == 0 {
		return nil, fmt.Errorf("hooppy: UpdateScheduleFromEdit: id is required (got 0)")
	}
	if len(overrides) == 0 {
		return nil, fmt.Errorf("hooppy: UpdateScheduleFromEdit: at least one override is required (got empty map)")
	}
	// 1. Fetch the raw /edit response body — NOT decoded into ScheduleEditResponse,
	//    which would drop the 36 unmodelled keys. We need the raw bytes.
	rawBody, err := c.doGETRaw(ctx, fmt.Sprintf(pathScheduleEdit, id))
	if err != nil {
		return nil, fmt.Errorf("hooppy: UpdateScheduleFromEdit: fetch /edit: %w", err)
	}
	// 2. Unmarshal into map[string]json.RawMessage — preserves every key's
	//    raw value bytes. json.RawMessage is []byte, so re-marshalling carries
	//    the exact wire form through (numbers stay numbers, strings stay
	//    strings, the hostile "1,2,7,9" stays "1,2,7,9").
	var fullState map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &fullState); err != nil {
		return nil, fmt.Errorf("hooppy: UpdateScheduleFromEdit: decode /edit response: %w", err)
	}
	// 3. Refuse a near-empty /edit response before it can become the base of
	//    a full-state write. A 200 with `{}` or a truncated object unmarshals
	//    successfully (json.Unmarshal of `{}` into a map is a no-op success),
	//    so the existing malformed-response guards (non-2xx, empty body, HTML
	//    — each fails to unmarshal) all pass through `{}`. Applying overrides
	//    to an empty map and PUTting the result would write a near-empty
	//    object over a live schedule and silently destroy every field the
	//    overrides do not touch — page targets, times, captions, buttons,
	//    start/stop dates. Irreversible, and reported as success.
	//
	//    A zero-key or near-empty 200 is not exotic: a server-side bug, a
	//    transient 200 with an empty object, a proxy that strips the body, an
	//    auth edge that returns `{}` instead of 401. A `len == 0` test just
	//    moves the cliff — a one- or two-key response is nearly as
	//    destructive. Require the state to be RECOGNISABLY a schedule before
	//    it can be used as the base of a full-state write: the structural keys
	//    a /edit response always carries — id (identity), name (the human
	//    handle, a required field), and publication_how_type (the mode
	//    invariant: 1=manual, 2=by-project — the structural choice that
	//    defines what the schedule IS and which other fields are required).
	//    A response missing any of these is not a schedule's editable state,
	//    whatever else it carries — refuse, name what was missing and how
	//    many keys did arrive, never issue the PUT. The failure is an error
	//    return, not a partial write.
	requiredStructuralKeys := []string{"id", "name", "publication_how_type"}
	var missing []string
	for _, k := range requiredStructuralKeys {
		if _, ok := fullState[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("hooppy: UpdateScheduleFromEdit: /edit response is not a recognisable schedule (got %d key(s), missing structural key(s) %v) — refusing to use a truncated/empty state as the base of a full-state write that would destroy every field not in the overrides", len(fullState), missing)
	}
	// 4. Apply overrides — replace the RawMessage for each overridden key.
	for key, val := range overrides {
		if len(val) == 0 {
			return nil, fmt.Errorf("hooppy: UpdateScheduleFromEdit: override for %q is empty (nil json.RawMessage)", key)
		}
		fullState[key] = val
	}
	// 5. Marshal the complete map and PUT it.
	body, err := json.Marshal(fullState)
	if err != nil {
		return nil, fmt.Errorf("hooppy: UpdateScheduleFromEdit: encode body: %w", err)
	}
	var resp ScheduleResponse
	if err := c.doPUTRaw(ctx, fmt.Sprintf(pathScheduleDelete, id), body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ScheduleOverride builds a single-field override map for UpdateScheduleFromEdit.
// The value is JSON-marshalled. Returns an error if marshalling fails.
func ScheduleOverride(key string, value interface{}) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("hooppy: ScheduleOverride: marshal %q: %w", key, err)
	}
	return map[string]json.RawMessage{key: b}, nil
}

// DeleteSchedule deletes a schedule via DELETE /posts/schedules/{id}.
//
// UNDOCUMENTED: this endpoint is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) DeleteSchedule(ctx context.Context, id int) (*ScheduleResponse, error) {
	var resp ScheduleResponse
	if err := c.doDELETE(ctx, fmt.Sprintf(pathScheduleDelete, id), &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetScheduleEdit returns a schedule's full editable state via
// GET /posts/schedules/{id}/edit. The response carries 72 keys, including
// times — the posting schedule itself (an array of 7 arrays, one per
// weekday, each holding that day's slots) — and nine other fields the list
// response never returns. See ScheduleEditResponse for the modelled fields
// and the evidence behind each type choice.
//
// UNDOCUMENTED: GET /posts/schedules/{id}/edit is not in the public OpenAPI
// spec (v0.1.0). Discovered via API probing — may change without notice.
func (c *Client) GetScheduleEdit(ctx context.Context, id int) (*ScheduleEditResponse, error) {
	var resp ScheduleEditResponse
	if err := c.doGET(ctx, fmt.Sprintf(pathScheduleEdit, id), nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListSchedulePosts returns a schedule's queue — its depth (TotalRows) and
// per-day calendar (PostsByDays, keyed dd.mm.yyyy) — in ONE request via
// GET /posts/schedules/{id}/posts. The LAST key in PostsByDays is the
// booked-until date. One call returns the whole calendar; this method does
// NOT page (issue #106 explicitly forbids a paged walk — the endpoint
// returns the full calendar in one envelope, and paging would issue
// multiple requests against a one-request contract).
//
// UNDOCUMENTED: GET /posts/schedules/{id}/posts is not in the public OpenAPI
// spec. Discovered via API probing — may change without notice.
func (c *Client) ListSchedulePosts(ctx context.Context, scheduleID int) (*SchedulePostsResponse, error) {
	if scheduleID == 0 {
		return nil, fmt.Errorf("hooppy: ListSchedulePosts: scheduleID is required (got 0)")
	}
	var resp SchedulePostsResponse
	if err := c.doGET(ctx, fmt.Sprintf(pathSchedulePosts, scheduleID), nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}
