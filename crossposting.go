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
	if id <= 0 {
		return nil, fmt.Errorf("hooppy: GetCrossPostingEdit: id must be a positive integer (got %d); a zero or negative id builds an invalid path (/cross-posting/%d/edit) and the server cannot resolve it", id, id)
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
	if id <= 0 {
		return nil, fmt.Errorf("hooppy: GetCrossPostingStatistics: id must be a positive integer (got %d); a zero or negative id builds an invalid path (/cross-posting/%d/statistics) and the server cannot resolve it", id, id)
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
	// the original key; the *_name / *_unknown keys are additive. A server
	// key already occupying an injected alias is refused, not silently
	// overwritten (see injectEnum).
	injections := []struct {
		key string
		v   EnumValue
	}{
		{"search_mode", EnumValue(resp.SearchMode)},
		{"search_mode_direction", EnumValue(resp.SearchModeDirection)},
		{"determine_best_by", EnumValue(resp.DetermineBestBy)},
		{"check_when_type", EnumValue(resp.CheckWhenType)},
		{"check_interval", EnumValue(resp.CheckInterval)},
	}
	for _, inj := range injections {
		if err := injectEnum(m, inj.key, inj.v); err != nil {
			return nil, fmt.Errorf("hooppy: EnrichedCrossPostingEditMap: %w", err)
		}
	}
	return m, nil
}

// injectEnum injects the decoded enum name (and the unknown flag when the
// bundle does not define the value) into a row map alongside the raw integer.
// The raw integer stays in the original key; the *_name / *_unknown keys are
// additive presentation aliases.
//
// A server key already occupying an injected alias is REFUSED, not silently
// overwritten: the API owns its key namespace, and clobbering a server field
// to inject a derived label would hide real data behind a presentation alias.
// Marshal errors are propagated rather than swallowed — marshalling a string
// or bool does not fail in practice, but a silent drop on the only error path
// is the pattern that hides a real failure behind a green-looking result.
func injectEnum(m map[string]json.RawMessage, key string, v EnumValue) error {
	nameKey := key + "_name"
	if _, ok := m[nameKey]; ok {
		return fmt.Errorf("injectEnum: refusing to overwrite server key %q with the injected presentation alias", nameKey)
	}
	nameBytes, err := json.Marshal(v.Name)
	if err != nil {
		return fmt.Errorf("injectEnum: marshal name for %q: %w", key, err)
	}
	m[nameKey] = nameBytes
	if v.Unknown {
		unkKey := key + "_unknown"
		if _, ok := m[unkKey]; ok {
			return fmt.Errorf("injectEnum: refusing to overwrite server key %q with the injected presentation alias", unkKey)
		}
		unkBytes, err := json.Marshal(true)
		if err != nil {
			return fmt.Errorf("injectEnum: marshal unknown for %q: %w", key, err)
		}
		m[unkKey] = unkBytes
	}
	return nil
}

// enrichCrossPostingRows returns the list rows as per-row maps with a decoded
// *_name (and *_unknown when the bundle does not define the value) injected
// for each of the five enums, alongside the raw integer. Mirrors
// EnrichedCrossPostingEditMap's injection applied to every list row. The list
// row has no Raw (the list surface decodes only the modelled fields), so the
// row map carries the modelled fields; the injected keys are additive.
func enrichCrossPostingRows(list []CrossPosting) ([]map[string]json.RawMessage, error) {
	rows := make([]map[string]json.RawMessage, 0, len(list))
	for i := range list {
		b, err := json.Marshal(list[i])
		if err != nil {
			return nil, fmt.Errorf("hooppy: enrichCrossPostingRows: marshal row %d: %w", i, err)
		}
		var row map[string]json.RawMessage
		if err := json.Unmarshal(b, &row); err != nil {
			return nil, fmt.Errorf("hooppy: enrichCrossPostingRows: decode row %d: %w", i, err)
		}
		injections := []struct {
			key string
			v   EnumValue
		}{
			{"search_mode", EnumValue(list[i].SearchMode)},
			{"search_mode_direction", EnumValue(list[i].SearchModeDirection)},
			{"determine_best_by", EnumValue(list[i].DetermineBestBy)},
			{"check_when_type", EnumValue(list[i].CheckWhenType)},
			{"check_interval", EnumValue(list[i].CheckInterval)},
		}
		for _, inj := range injections {
			if err := injectEnum(row, inj.key, inj.v); err != nil {
				return nil, fmt.Errorf("hooppy: enrichCrossPostingRows: %w", err)
			}
		}
		// Normalise the FlexInt timestamps. FlexInt passes the wire form
		// through on marshal, so without this the field is a number or a
		// string depending on what the server happened to send — and this map
		// is a presentation surface read by an agent, which is precisely the
		// consumer least able to absorb a field that changes type between
		// calls. The typed accessor already exists; forwarding the vendor's
		// polymorphism to the caller is a choice, not a necessity.
		for _, ts := range []struct {
			key string
			v   FlexInt
		}{
			{"last_check_date", list[i].LastCheckDate},
			{"instagram_last_check_date", list[i].InstagramLastCheckDate},
		} {
			if _, present := row[ts.key]; !present {
				continue
			}
			if !ts.v.IsSet() {
				row[ts.key] = json.RawMessage("null")
				continue
			}
			row[ts.key] = json.RawMessage(strconv.FormatInt(ts.v.Int64(), 10))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// EnrichedCrossPostingsMap builds the agent-facing presentation of a
// /cross-posting list page: the typed response with a decoded *_name (and
// *_unknown when the bundle does not define the value) injected for each of
// the five enums on every row. Mirrors EnrichedCrossPostingEditMap for the
// list surface — the MCP tool description promises "the enum integers are
// decoded to names in the response", and this is what makes that true on the
// list path (the bare MarshalJSON emits only the raw integer). The raw
// integers stay in the original keys; the injected keys are additive.
func EnrichedCrossPostingsMap(resp *CrossPostingsResponse) (map[string]json.RawMessage, error) {
	if resp == nil {
		return nil, fmt.Errorf("hooppy: EnrichedCrossPostingsMap: nil response")
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("hooppy: EnrichedCrossPostingsMap: marshal: %w", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("hooppy: EnrichedCrossPostingsMap: decode: %w", err)
	}
	rows, err := enrichCrossPostingRows(resp.List)
	if err != nil {
		return nil, err
	}
	listBytes, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("hooppy: EnrichedCrossPostingsMap: marshal enriched list: %w", err)
	}
	out["list"] = listBytes
	return out, nil
}

// EnrichedAllCrossPostingsMap builds the agent-facing presentation of an --all
// walk: the AllListEnvelope shape {list, total_rows, is_has_more} with enum
// names injected on every row. is_has_more is pinned false (the envelope
// convention for a complete walk). total is the server's last-seen total_rows
// (NOT len(list)) — pass the value NewAllListEnvelope validated against.
func EnrichedAllCrossPostingsMap(list []CrossPosting, total int) (map[string]json.RawMessage, error) {
	rows, err := enrichCrossPostingRows(list)
	if err != nil {
		return nil, err
	}
	listBytes, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("hooppy: EnrichedAllCrossPostingsMap: marshal list: %w", err)
	}
	totalBytes, err := json.Marshal(total)
	if err != nil {
		return nil, fmt.Errorf("hooppy: EnrichedAllCrossPostingsMap: marshal total_rows: %w", err)
	}
	hasMoreBytes, err := json.Marshal(false)
	if err != nil {
		return nil, fmt.Errorf("hooppy: EnrichedAllCrossPostingsMap: marshal is_has_more: %w", err)
	}
	return map[string]json.RawMessage{
		"list":        listBytes,
		"total_rows":  totalBytes,
		"is_has_more": hasMoreBytes,
	}, nil
}
