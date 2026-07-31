package hooppy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ListCrossPostings returns the operator's cross-posting connections via
// GET /cross-posting. A connection is a server-side rule engine entry: it
// collects from source publics on a timer, ranks by engagement, filters by
// thresholds, deduplicates, and publishes into a project/schedule. The list
// row carries 89 keys; CrossPosting models the ones an operator/agent reads
// (identity, state, the five enums, the four thresholds, take_amount, the
// check schedule). The /edit response is the lossless full-state surface.
//
// 20 rows per page; pass page (1-indexed, 0 = first page) to paginate, or
// ListAllCrossPostings to walk every page.
//
// UNDOCUMENTED: GET /cross-posting is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
func (c *Client) ListCrossPostings(ctx context.Context, page int) (*CrossPostingsResponse, error) {
	params := url.Values{}
	// Reject negatives before any request: the old `> 0` guard let a
	// negative take neither branch — no error, no page parameter, the
	// server returns page 1, and a caller's paging loop silently re-reads
	// the first page. Same defect class the sweep closed across the other
	// list filters. Reachable from the shipped CLI (--page IntVar; pflag
	// accepts negatives) and the MCP tool. Zero stays the unset sentinel.
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListCrossPostings: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp CrossPostingsResponse
	if err := c.doGET(ctx, pathCrossPostings, params, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListAllCrossPostings walks /cross-posting from page 1, accumulating
// connections until is_has_more is false. The walk starts at page 1 (the
// Hooppy API is 1-indexed; see ListAllSchedules for the rationale) and is
// bounded by maxListAllPages.
//
// Cross-posting connections are LOW-CHURN (configured connections, rarely
// created/deleted), so the NewAllListEnvelope equality check (unique ==
// lastTotal) is the correct truncation gate — use ListAllCrossPostingsWithTotal
// + NewAllListEnvelope rather than the high-churn first-total rule.
//
// Duplicates arising from a mid-walk collection shift are NOT removed; see
// ListAllSchedules for the offset-pagination caveat.
//
// UNDOCUMENTED: GET /cross-posting is not in the public OpenAPI spec (v0.1.0).
func (c *Client) ListAllCrossPostings(ctx context.Context) ([]CrossPosting, error) {
	all, _, err := c.ListAllCrossPostingsWithTotal(ctx)
	return all, err
}

// ListAllCrossPostingsWithTotal is ListAllCrossPostings but also returns the
// server's last-seen total_rows. The pair (list, totalRows) is meant for
// NewAllListEnvelope, which fails loud when the count of unique ids does not
// match total_rows. Cross-posting is low-churn → equality check, not the
// high-churn first-total rule.
func (c *Client) ListAllCrossPostingsWithTotal(ctx context.Context) ([]CrossPosting, int, error) {
	all := make([]CrossPosting, 0)
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllCrossPostings exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListCrossPostings(ctx, page)
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

// GetCrossPostingEdit returns a connection's full editable state via
// GET /cross-posting/{id}/edit. The response carries 95 keys — six more than
// the list row — and embeds source_resources, accounts_for_parsing, projects,
// schedules, watermarks, social_pages_by_accounts,
// selected_pages_by_source_ids, plus the three collection-filter blobs
// posts_filter, posts_text, posts_upgrade.
//
// The response round-trips LOSSLESSLY: CrossPostingEditResponse keeps the raw
// /edit body alongside the typed view (Raw), the way UpdateScheduleFromEdit
// keeps map[string]json.RawMessage — so the future write path (read-modify-
// write, the schedules precedent) cannot lose fields. See
// CrossPostingEditResponse for the round-trip guarantee and the typed fields.
//
// There is no direct GET /cross-posting/{id} (405, same shape as
// /posts-search/{id} and /posts/schedules/{id}); /edit is the full-state read.
//
// UNDOCUMENTED: GET /cross-posting/{id}/edit is not in the public OpenAPI
// spec (v0.1.0). Discovered via API probing — may change without notice.
func (c *Client) GetCrossPostingEdit(ctx context.Context, id int) (*CrossPostingEditResponse, error) {
	if id == 0 {
		return nil, fmt.Errorf("hooppy: GetCrossPostingEdit: id is required (got 0)")
	}
	var resp CrossPostingEditResponse
	if err := c.doGET(ctx, fmt.Sprintf(pathCrossPostingEdit, id), nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetCrossPostingStatistics returns a connection's per-day statistics via
// GET /cross-posting/{id}/statistics: found, filtered, duplicates, taken, and
// errors, one row per day.
//
// ABSENT vs ZERO: a non-empty Statistics array with all-zero counters is a
// REAL measurement (the engine ran and found/filtered/took nothing); an EMPTY
// array is absent data (the engine has not run). Use HasData to distinguish —
// a connection that is configured but not producing (state=0, all-zero
// counters across days) is the live state on this account today and is real
// data, not absent.
//
// UNDOCUMENTED: GET /cross-posting/{id}/statistics is not in the public
// OpenAPI spec (v0.1.0). Discovered via API probing — may change without notice.
func (c *Client) GetCrossPostingStatistics(ctx context.Context, id int) (*CrossPostingStatisticsResponse, error) {
	if id == 0 {
		return nil, fmt.Errorf("hooppy: GetCrossPostingStatistics: id is required (got 0)")
	}
	var resp CrossPostingStatisticsResponse
	if err := c.doGET(ctx, fmt.Sprintf(pathCrossPostingStatistics, id), nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// EnrichedCrossPostingEditMap builds the agent-facing presentation of a
// connection's /edit state: the full raw body decoded into
// map[string]json.RawMessage (all 95 keys preserved) with a decoded
// *_name and *_unknown key injected for each of the five enums. An agent
// reads the name; the raw integer stays in the original key for round-tripping.
// This is the "decode, do not translate away" surface — emit both.
//
// The enriched map is a PRESENTATION, not the round-trip source: the
// round-trip is CrossPostingEditResponse.Raw (byte-identical via MarshalJSON).
// Re-marshalling the enriched map reorders keys and adds the injected ones, so
// it is NOT byte-identical to the fixture — use it for display, not for the
// write path.
func EnrichedCrossPostingEditMap(resp *CrossPostingEditResponse) (map[string]json.RawMessage, error) {
	if resp == nil || len(resp.Raw) == 0 {
		return nil, fmt.Errorf("hooppy: EnrichedCrossPostingEditMap: nil or empty response")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(resp.Raw, &m); err != nil {
		return nil, fmt.Errorf("hooppy: EnrichedCrossPostingEditMap: decode raw body: %w", err)
	}
	// Inject decoded enum names + unknown flags. The raw integer stays in
	// the original key; the *_name / *_unknown keys are additive.
	injectEnum := func(key string, v EnumValue) {
		if b, err := json.Marshal(v.Name); err == nil {
			m[key+"_name"] = b
		}
		if v.Unknown {
			if b, err := json.Marshal(true); err == nil {
				m[key+"_unknown"] = b
			}
		}
	}
	injectEnum("search_mode", EnumValue(resp.SearchMode))
	injectEnum("search_mode_direction", EnumValue(resp.SearchModeDirection))
	injectEnum("determine_best_by", EnumValue(resp.DetermineBestBy))
	injectEnum("check_when_type", EnumValue(resp.CheckWhenType))
	injectEnum("check_interval", EnumValue(resp.CheckInterval))
	return m, nil
}
