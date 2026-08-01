package hooppy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// ListSearchPosts returns posts scraped from external social media pages,
// matching the given filter. Posts must be scraped first via StartParsing.
//
// UNDOCUMENTED: GET /posts-search is not in the public OpenAPI spec (v0.1.0).
// Discovered via API probing — may change without notice.
//
// Filter vocabulary: the API publishes its real filters in every response's
// filters_plug array (slug/type/name/values). The five min_* metric threshold
// fields below (MinLikes, MinViews, MinComments, MinReposts, MinInvolvement)
// are NOT server-side filters — the API silently ignores them and returns an
// unfiltered result set that looks filtered (three different thresholds
// produce byte-identical output). Setting any of them returns an error
// before any request is issued; use SortBy (likes|views|reposts|comments|
// involvement) instead, which DOES work server-side. The fields are kept on
// the struct so callers get a clear error rather than a silent lie — see
// issue #63.
//
// Three more phantom parameters were found in the same sweep (issues #67,
// #73): source_id, source_resource_id, and owner_id are accepted by the
// server and silently dropped — the caller gets an unfiltered result set
// that looks filtered. They are refused here with the same shape as the
// min_* guard. Use source_type (1=social, 2=RSS), content_types,
// photos_amount, video_duration, or text to narrow. Note that source_id
// is phantom on /posts-search but WORKS on /posts (ListPosts) — same name,
// two endpoints, opposite behaviour — so the fix is per-endpoint, never
// per-name.
//
// # Method notes for the next investigator (both cost a wrong answer)
//
//  1. total_rows CAPS AT 10000. A filter over a large collection looks
//     phantom because both the filtered and unfiltered sides read the cap.
//     Judge by RETURNED ROW CONTENT, not total_rows.
//
//  2. An impossible enum value is NOT a probe. source_type=9 returns
//     everything because the server ignores an unrecognised enum rather
//     than matching nothing — indistinguishable from a phantom. Use a
//     different VALID value: source_type=2 returns 0 rows and proves the
//     filter works.
//
// These two notes are why this issue took three rounds to characterise.
func (c *Client) ListSearchPosts(ctx context.Context, f SearchPostsFilter) (*SearchPostsResponse, error) {
	// Refuse the five metric-threshold filters before any request: the API
	// has no such server-side parameters, so emitting them would silently
	// return an unfiltered result set that looks filtered. Sorting by the
	// same metric (SortBy) is the supported path and works server-side.
	// Guard on != 0 (not > 0) so a negative threshold — passed directly or
	// produced by a computed threshold like avg-stddev going negative — is
	// refused too; the old > 0 guard let negatives fall through to an
	// unfiltered result while the help promised the flag errors.
	if f.MinLikes != 0 || f.MinViews != 0 || f.MinComments != 0 || f.MinReposts != 0 || f.MinInvolvement != 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: min_likes/min_views/min_comments/min_reposts/min_involvement are not server-side filters — the API silently ignores them and returns an unfiltered result set; use sort_by (likes|views|reposts|comments|involvement) instead, which does work server-side (issue #63)")
	}
	// Refuse the three phantom ID filters before any request (issues #67,
	// #73): source_id, source_resource_id, and owner_id are accepted by
	// the server and silently dropped — the caller gets an unfiltered
	// result set that looks filtered. Measured by returned row content
	// (not total_rows, which caps at 10000 — see the method notes above):
	//   - source_id=7 (Instagram) returns rows whose own source_id is 1.
	//   - source_resource_id=2228 (Instagram-only) returns rows with
	//     source_id: [1].
	//   - owner_id=<real> returns four different owners.
	// Same defect class as the min_* guard. The fields stay on the struct
	// (source-compatible — existing code still compiles) but any non-zero
	// value now errors. Use source_type, content_types, photos_amount,
	// video_duration, or text to narrow. source_id WORKS on /posts
	// (ListPosts) — phantom only here, so the fix is per-endpoint.
	if f.SourceID != 0 || f.SourceResourceID != 0 || f.OwnerID != 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: source_id/source_resource_id/owner_id are not server-side filters on /posts-search — the API accepts and silently ignores them, returning an unfiltered result set that looks filtered (measured by row content, not total_rows which caps at 10000); use source_type (1=social, 2=RSS), content_types, photos_amount, video_duration, or text to narrow (issues #67, #73)")
	}
	params := url.Values{}
	if f.Text != "" {
		params.Set("text", f.Text)
	}
	if f.DateFrom != "" {
		params.Set("date_from", f.DateFrom)
	}
	if f.DateTo != "" {
		params.Set("date_to", f.DateTo)
	}
	// Reject negatives for the two remaining ID/page filters before any
	// request: the old `> 0` guard let a negative take neither branch —
	// no error, no parameter, an unfiltered result that looks filtered.
	// This is the exact defect class the PhotosAmount/VideoDuration guards
	// below and the min_* guard above close. Reachable from the shipped
	// CLI: cmd/hooppy binds these with IntVar and pflag accepts negatives
	// (--source-type -1 drops the parameter → results from every network
	// while the caller believes they filtered to one; --page -1, or a
	// computed page-1 that underflows, drops the parameter → the server
	// returns page 1, so a paging loop silently re-reads the first page).
	// Zero stays the unset sentinel. SourceID/SourceResourceID/OwnerID are
	// no longer here — they are phantom and refused above on != 0.
	if f.SourceType < 0 || f.Page < 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: source_type/page must be non-negative (got source_type=%d, page=%d); pass 0 to leave any unset", f.SourceType, f.Page)
	}
	if f.SourceType > 0 {
		params.Set("source_type", strconv.Itoa(f.SourceType))
	}
	if f.Page > 0 {
		params.Set("page", strconv.Itoa(f.Page))
	}
	// Sorting — reaches the wire but is NOT differentially measured (see
	// the "assumed" group in TestPhantomFilterSweep). Not in filters_plug,
	// which describes filters only, not sorting or pagination.
	if f.SortBy != "" {
		params.Set("sort_by", f.SortBy)
	}
	if f.SortDirection != "" {
		params.Set("sort_direction", f.SortDirection)
	}
	// Content filters. Each slug below is a real filters_plug entry, but
	// the descriptor is authoritative ONLY for slugs — it is advisory for
	// values. Measured against a live response:
	//   - content_types ships values [text, photos, videos, audios, links]
	//     yet `documents` is a working value the descriptor omits, and
	//     `text` is accepted (returns the unfiltered count).
	//   - photos_amount and video_duration ship values: [] (empty), so the
	//     valid keys are NOT discoverable from the descriptor at all.
	// A value absent from `values` may still work; an empty `values` array
	// does NOT mean the filter takes no argument. We therefore pass caller
	// values through verbatim and never hardcode a value enum — a prior
	// guard hardcoded video_duration to 1..4 from a measurement that only
	// tried 1..4, then a wider measurement found keys 5-8 are real and
	// each returns a distinct result set. Replacing one hardcoded range
	// with another (9 and 10 error today) would repeat the same mistake
	// when the vendor adds them. Reject only negatives, send any
	// non-negative value verbatim, and let the server answer.
	//
	// Measured (NOT guessed) against the live API — recorded here so the
	// pass-through decision is grounded, not assumed:
	//
	//   video_duration (unset = unfiltered):
	//     key  rows
	//     0    4194  (unfiltered)
	//     1    710
	//     2    159
	//     3    3525
	//     4    4036
	//     5    4128
	//     6    4161
	//     7    644
	//     8    677
	//     9,10 server error (non-JSON)
	//   Keys 5-8 are real and each returns a distinct result set; the
	//   prior 1..4 guard would have hard-errored on four working filters.
	//   9 and 10 erroring today does NOT mean the vendor will not add them
	//   — do not re-introduce a hardcoded upper bound.
	//
	//   photos_amount (unset = unfiltered; saturates — "N or more", not
	//   "exactly N"):
	//     key  rows
	//     1    9294
	//     5    566
	//     6    742
	//     10   2172
	//     99   2172  (identical to 10 → saturates, not a phantom class)
	//
	// PhotosAmount and VideoDuration are bucket-key filters. The old
	// `> 0` guard reproduced the exact defect this PR closed for the
	// min_* fields: a negative value took neither branch — no error, no
	// parameter, an unfiltered result that looks filtered. A negative
	// bucket key is never valid, so reject it before any request. Zero
	// stays the unset sentinel; any positive value is passed through
	// verbatim (the valid key space is not enumerable client-side — see
	// the measurement tables above).
	if f.PhotosAmount < 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: photos_amount must be a non-negative bucket key (got %d); pass 0 to leave unset or a positive key from the filters_plug descriptor", f.PhotosAmount)
	}
	if f.PhotosAmount > 0 {
		params.Set("photos_amount", strconv.Itoa(f.PhotosAmount))
	}
	if f.VideoDuration < 0 {
		return nil, fmt.Errorf("hooppy: ListSearchPosts: video_duration must be a non-negative bucket key (got %d); pass 0 to leave unset — keys 1-8 are measured to work (filters_plug values:[] is empty, see issue #63)", f.VideoDuration)
	}
	if f.VideoDuration > 0 {
		params.Set("video_duration", strconv.Itoa(f.VideoDuration))
	}
	if f.ContentTypes != "" {
		params.Set("content_types", f.ContentTypes)
	}
	if f.ContentTypesExclude != "" {
		params.Set("content_types_exclude", f.ContentTypesExclude)
	}
	var resp SearchPostsResponse
	if err := c.doGET(ctx, pathPostsSearchIndex, params, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SearchPostsAllResult is the result of an --all walk over /posts-search
// (ListAllSearchPostsWithFirstAndLastTotal). It carries the accumulated list
// and the first/last total_rows for the high-churn truncation check, plus a
// Capped flag that is true when the walk stopped at the server's reachable
// window (Elasticsearch max_result_window) instead of is_has_more going false.
//
// When Capped is true the list is everything the server could serve via
// offset paging — a bounded, complete-as-possible result, NOT a complete
// list. Older rows exist but are unreachable by page number alone; the caller
// MUST narrow with date_from/date_to to reach them. The truncation check
// (NewAllListEnvelopeHighChurn) MUST be skipped in this case: a capped
// total_rows is a ceiling, not a count (it does not decrease and is_has_more
// never clears), and validating unique-count against it validates against a
// constant. Use NewCappedAllListEnvelope to build the honest envelope
// (is_has_more=true, no count check) and surface a warning to the operator.
type SearchPostsAllResult struct {
	List           []SearchPost
	FirstTotalRows int
	LastTotalRows  int
	Capped         bool
}

// ListAllSearchPosts walks GET /posts-search from page 1 with the given
// filter, accumulating scraped posts until is_has_more is false. The walk
// starts at page 1 so the first page is not fetched twice (the Hooppy API is
// 1-indexed and a request with no page param is byte-identical to ?page=1).
// The filter's non-page fields are preserved across the walk; only Page is
// incremented. See projects.ListAllSchedules for the 1-indexed rationale and
// the sanity cap.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. /posts-search is
// a high-churn collection (scraped posts accumulate), so passing (list,
// totalRows) to NewAllListEnvelope is NOT suitable — its equality check
// (unique == total) false-alarms on every active account, exactly as it does
// for /posts and /notifications (see NewAllListEnvelope for the per call-site
// table). Use ListAllSearchPostsWithTotal and the unique < firstTotal rule
// (see RunDoctor) when a truncated-walk check is needed.
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllSearchPosts returns an error instead
// of looping forever or silently truncating.
//
// If the walk hits the server's reachable window (Elasticsearch
// max_result_window) this entry point returns an ERROR naming the cap — it
// has no way to surface the Capped flag, and returning a silently-capped
// list would repeat the silent-truncation defect. Callers that want the
// bounded partial result (the rows already collected, marked honestly) MUST
// use ListAllSearchPostsWithFirstAndLastTotal and check its Capped field.
func (c *Client) ListAllSearchPosts(ctx context.Context, f SearchPostsFilter) ([]SearchPost, error) {
	res, err := c.ListAllSearchPostsWithFirstAndLastTotal(ctx, f)
	if err != nil {
		return nil, err
	}
	if res.Capped {
		return nil, fmt.Errorf("hooppy: ListAllSearchPosts: walk stopped at the server's reachable window (Elasticsearch max_result_window) after %d rows — older rows exist but are unreachable by offset paging; use ListAllSearchPostsWithFirstAndLastTotal to receive the capped partial result, or narrow with date_from/date_to", len(res.List))
	}
	return res.List, nil
}

// ListAllSearchPostsWithTotal is ListAllSearchPosts but also returns the
// server's last-seen total_rows. It exists for symmetry with the other
// ListAll*WithTotal entry points; for /posts-search specifically, passing
// (list, totalRows) to NewAllListEnvelope is NOT suitable (see
// ListAllSearchPosts). The right shape is the unique < firstTotal rule
// doctor uses for /notifications; use ListAllSearchPostsWithFirstAndLastTotal
// and NewAllListEnvelopeHighChurn. See NewAllListEnvelope for the per
// call-site table.
//
// Like ListAllSearchPosts, this entry point returns an ERROR on a capped walk
// rather than a silently-capped list — use ListAllSearchPostsWithFirstAndLastTotal
// for the bounded partial result.
func (c *Client) ListAllSearchPostsWithTotal(ctx context.Context, f SearchPostsFilter) ([]SearchPost, int, error) {
	res, err := c.ListAllSearchPostsWithFirstAndLastTotal(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	if res.Capped {
		return nil, 0, fmt.Errorf("hooppy: ListAllSearchPostsWithTotal: walk stopped at the server's reachable window (Elasticsearch max_result_window) after %d rows — older rows exist but are unreachable by offset paging; use ListAllSearchPostsWithFirstAndLastTotal to receive the capped partial result, or narrow with date_from/date_to", len(res.List))
	}
	return res.List, res.LastTotalRows, nil
}

// ListAllSearchPostsWithFirstAndLastTotal is ListAllSearchPosts but also
// returns the server's total_rows from the FIRST page and the LAST page, plus
// a Capped flag, in a SearchPostsAllResult. The triple (list, firstTotalRows,
// lastTotalRows) lets a caller distinguish a truncated walk (unique count <
// firstTotalRows) from a benign mid-walk insert (lastTotalRows >
// firstTotalRows) — the distinction NewAllListEnvelope cannot make because it
// receives only one total. High-churn --all call sites (cmd/hooppy/list.go,
// cmd/hooppy-mcp/main.go) use this with NewAllListEnvelopeHighChurn to apply
// the first-total rule instead of the equality check. See
// NewAllListEnvelopeHighChurn and RunDoctor for the rule and the gaps it does
// not close.
//
// When Capped is true the walk stopped at the server's reachable window
// (Elasticsearch max_result_window): the server returned HTTP 500 with
// "Result window is too large" on the next page. The rows in List are
// everything collected so far — a bounded, complete-as-possible result, NOT a
// complete list. The caller MUST skip NewAllListEnvelopeHighChurn (a capped
// total_rows is a ceiling, not a count) and build the envelope with
// NewCappedAllListEnvelope (is_has_more=true) plus a warning naming the
// date_from/date_to remedy. Any OTHER mid-walk error (a generic 500, an
// exhausted 429, a network fault) is NOT the ceiling and still returns an
// error — turning every server error into a partial success would mask real
// failures.
func (c *Client) ListAllSearchPostsWithFirstAndLastTotal(ctx context.Context, f SearchPostsFilter) (*SearchPostsAllResult, error) {
	all := make([]SearchPost, 0)
	var firstTotalRows, lastTotalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, fmt.Errorf("hooppy: ListAllSearchPosts exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		f.Page = page
		resp, err := c.ListSearchPosts(ctx, f)
		if err != nil {
			// The Elasticsearch max_result_window rejection: the server
			// refuses offset paging past its reachable window (from + size >
			// max_result_window). This is a HARD CEILING, not a transient
			// failure and not a truncation the walk can page past. Discarding
			// every row collected so far (the prior behaviour) is the worst
			// available outcome — it is the same shape as the bug this branch
			// set out to fix: a command that cannot give the operator what it
			// already has. Stop at the reachable window and return the rows
			// already collected, marked Capped so the caller labels the result
			// honestly instead of asserting completeness.
			//
			// Only treat a NON-empty walk this way: a result-window error on
			// page 1 (nothing collected) is impossible in practice (the wall
			// is at offset >= max_result_window, well past page 1) but
			// defended against — return the error, there is nothing to return.
			// Any OTHER error (a generic 500, an exhausted 429, a network
			// fault) is NOT the ceiling and MUST fail loud: turning every
			// server error into a partial success would mask real failures.
			if isResultWindowError(err) && len(all) > 0 {
				return &SearchPostsAllResult{
					List:           all,
					FirstTotalRows: firstTotalRows,
					LastTotalRows:  lastTotalRows,
					Capped:         true,
				}, nil
			}
			return nil, err
		}
		if page == 1 {
			firstTotalRows = resp.TotalRows
		}
		all = append(all, resp.List...)
		lastTotalRows = resp.TotalRows
		if !resp.IsHasMore {
			return &SearchPostsAllResult{
				List:           all,
				FirstTotalRows: firstTotalRows,
				LastTotalRows:  lastTotalRows,
			}, nil
		}
	}
}

// ListSourceResources returns the configured source resources (groups of
// external social media pages to scrape from). page is 1-indexed (0 or omit
// = first page); a negative is rejected before any request so a paging loop
// cannot silently re-read page 1. Same defect class as the search/posts/
// accounts/pages page guards (see ListSearchPosts).
//
// UNDOCUMENTED: GET /posts-search/source-resources is not in the public
// OpenAPI spec. The endpoint carries the standard {list, total_rows,
// is_has_more, rows_limit} envelope (see testdata/live/source_resources.json
// and issue #98), so it is paged like its siblings; the page parameter is
// sent verbatim and the server answers.
func (c *Client) ListSourceResources(ctx context.Context, page int) (*SourceResourcesResponse, error) {
	params := url.Values{}
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListSourceResources: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp SourceResourcesResponse
	if err := c.doGET(ctx, pathPostsSearchSources, params, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListAllSourceResources walks GET /posts-search/source-resources from page
// 1, accumulating source resources until is_has_more is false. The walk
// starts at page 1 so the first page is not fetched twice (the Hooppy API is
// 1-indexed and a request with no page param is byte-identical to ?page=1).
// See projects.ListAllSchedules for the 1-indexed rationale and the sanity
// cap.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. Use
// ListAllSourceResourcesWithTotal with NewAllListEnvelope to detect them
// (see NewAllListEnvelope for what it does and does not catch).
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllSourceResources returns an error
// instead of looping forever or silently truncating.
func (c *Client) ListAllSourceResources(ctx context.Context) ([]SourceResource, error) {
	all, _, err := c.ListAllSourceResourcesWithTotal(ctx)
	return all, err
}

// ListAllSourceResourcesWithTotal is ListAllSourceResources but also returns
// the server's last-seen total_rows. The pair (list, totalRows) is meant to
// be passed to NewAllListEnvelope. See projects.ListAllSchedulesWithTotal
// and NewAllListEnvelope for what the envelope catches and what it does not.
func (c *Client) ListAllSourceResourcesWithTotal(ctx context.Context) ([]SourceResource, int, error) {
	all := make([]SourceResource, 0)
	var totalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, fmt.Errorf("hooppy: ListAllSourceResources exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListSourceResources(ctx, page)
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

// GetParsingForm returns the parsing form data (available source resources,
// social accounts that can act as parsers, and whether a parse is in progress).
//
// UNDOCUMENTED: GET /posts-search/parsing/form is not in the public OpenAPI spec.
func (c *Client) GetParsingForm(ctx context.Context) (*ParsingFormResponse, error) {
	var resp ParsingFormResponse
	if err := c.doGET(ctx, pathPostsSearchParseForm, nil, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StartParsing launches a scraping job that pulls posts from the external
// social media pages configured in the given source resource. The job runs
// asynchronously on the server; poll GetParsingForm to check completion.
//
// UNDOCUMENTED: POST /posts-search/parsing/start is not in the public OpenAPI spec.
func (c *Client) StartParsing(ctx context.Context, payload ParsingStartPayload) (*ParsingStartResponse, error) {
	if err := validateDDMMYYYY("date_from", payload.DateFromDay); err != nil {
		return nil, fmt.Errorf("hooppy: StartParsing: %w", err)
	}
	if err := validateDDMMYYYY("date_to", payload.DateToDay); err != nil {
		return nil, fmt.Errorf("hooppy: StartParsing: %w", err)
	}
	var resp ParsingStartResponse
	if err := c.doPOST(ctx, pathPostsSearchParseStart, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StopParsing cancels any in-progress scraping job.
//
// The path is /posts-search/parsing/stop, not /posts-search/parsing. Both
// exist and both answer {"success":true}; only the /stop suffix cancels
// anything. Measured on a live account (issue #94), three arms with the
// in-progress flag asserted true before each stop:
//
//	no stop call, natural duration      idle again at 256.9s
//	DELETE /posts-search/parsing/stop   idle again at  11.2s (stop sent at 6.2s)
//	DELETE /posts-search/parsing        still running past 100s
//
// So a success response is not evidence here, and the suffix-less path was
// what produced the earlier "a parse cannot be cancelled" conclusion.
//
// Poll the result with GetParsingForm, whose is_parsing_in_progress field is
// the working oracle (ParsingFormResponse models it; SearchPostsResponse does
// not). GET /posts-search does NOT carry that key at all, so do not add it to
// SearchPostsResponse expecting the server to fill it — it would decode as
// false on every call and read exactly like "idle".
//
// Retrying this call is safe against a REPEAT cancel: three consecutive
// DELETEs against an idle live account each answered {"success":true} and left
// the flag false. It is not safe against a job started between the first
// attempt and the retry — that attempt will cancel the new job. The window is
// not necessarily short: on a 429 the client honours the server's Retry-After,
// so it is server-controlled and can be seconds rather than the millisecond
// backoff the local options suggest. Whoever starts a parse concurrently with
// a cancel owns that race; the library cannot see it.
//
// UNDOCUMENTED: DELETE /posts-search/parsing/stop is not in the public
// OpenAPI spec.
//
// The response body IS read (decoded into a stopResponse) so the universal
// success gate (checkSuccess, success_gate.go) fires on a 2xx carrying an
// explicit {"success":false} — the same family as PR #134. The prior form
// passed out=nil, so client.do returned after the status check without
// reading the body, and a 2xx success:false was swallowed as a nil error.
// stopResponse is unexported: it exists only to make client.do read the body
// so the gate runs; callers who need the OBSERVED state (did the parse
// actually stop?) use StopParsingAndConfirm, which re-reads the oracle below.
func (c *Client) StopParsing(ctx context.Context) error {
	var resp stopResponse
	return c.doDELETE(ctx, pathPostsSearchParseStop, &resp, true)
}

// stopResponse is the decode target for DELETE /posts-search/parsing/stop.
// It exists so client.do reads the body and the universal success gate
// (checkSuccess) runs — a 2xx {"success":false} becomes a *SuccessFalseError
// instead of a swallowed nil. The decoded Success field itself is NOT the
// evidence the operation happened: the server answers {"success":true} for
// both the working /stop path and the suffix-less sibling that cancels
// nothing (measured, issue #94), and a stop is asynchronous server-side
// (measured ~5s between stop-sent and is_parsing_in_progress going false —
// see StopParsing's doc comment). So Success:true means only "the server
// accepted the request", not "the parse is idle". The OBSERVED state comes
// from GetParsingForm's is_parsing_in_progress via StopParsingAndConfirm.
type stopResponse struct {
	Success bool `json:"success"`
}

// StopParsingResult is the OBSERVED parsing state after a stop request, read
// from the working oracle (GetParsingForm's is_parsing_in_progress), NOT from
// the DELETE's own success body. See StopParsingAndConfirm for why the DELETE
// body is not the evidence.
//
// IsParsingInProgress is the oracle's value after the DELETE. Stopped (the
// convenience the CLI/MCP report) is its negation.
//
// ConfirmErr is non-empty when the DELETE itself succeeded (the stop request
// was accepted) but the oracle re-read failed — the stop MAY have taken
// effect, only the confirmation read failed. A caller MUST NOT report success
// when ConfirmErr is non-empty: claiming the parse stopped without having
// observed it is exactly the defect StopParsingAndConfirm exists to close
// (issue #114 — the command announced success without ever reading what the
// server said). Report "stop accepted, status unconfirmed" instead.
type StopParsingResult struct {
	IsParsingInProgress bool   `json:"is_parsing_in_progress"`
	ConfirmErr          string `json:"confirm_error,omitempty"`
}

// Stopped reports whether the oracle observed the parse as idle after the
// stop request. It is false when the parse is still running OR when the
// confirmation re-read failed (ConfirmErr non-empty) — in both cases the
// command must not claim the parse stopped.
func (r *StopParsingResult) Stopped() bool {
	return !r.IsParsingInProgress && r.ConfirmErr == ""
}

// StopParsingAndConfirm issues the cancel (DELETE /posts-search/parsing/stop)
// and then re-reads the working oracle (GetParsingForm) to report the
// OBSERVED parsing state — not the DELETE's own {"success":true} body.
//
// WHY THE ORACLE, NOT THE DELETE BODY (issue #114). StopParsing's doc comment
// measures that the server answers {"success":true} for a DELETE that cancels
// nothing (the suffix-less sibling) and that a real stop is asynchronous
// (~5s between stop-sent and is_parsing_in_progress going false). So a 2xx
// success:true is "the server accepted the request", not "the parse is idle".
// The prior CLI/MCP code printed {"success":true} after a nil error from
// StopParsing(out=nil) — the body was never read, and even when it is read a
// success:true does not mean the parse stopped. The oracle
// (is_parsing_in_progress) is the thing that was supposed to change; observing
// it is the only report that cannot claim more than it knows.
//
// THE ASYNC GAP. Because the stop is asynchronous, an immediate re-read after
// a stop that WILL succeed can still report is_parsing_in_progress=true. This
// is HONEST, not a false failure: at the moment of the read the parse IS
// still running. The command reports "stop accepted, still in progress" and
// the operator re-runs 'search status' to confirm the transition. This is
// preferred over claiming success on the DELETE body alone, which would
// announce a stop that never happened when the server's success:true was a
// lie (the measured suffix-less-sibling case). A polling loop that waited for
// idle would claim to know the stop will eventually take effect — more than
// the single read knows — so a single immediate re-read is the chosen shape.
//
// ERROR CONTRACT. A non-nil error means the DELETE itself failed (transport
// error, non-2xx, or a 2xx {"success":false} caught by the universal gate);
// the stop request did not reach a decided-success state. A nil error with
// ConfirmErr non-empty means the DELETE succeeded but the oracle re-read
// failed — the stop may have worked, only confirmation failed; report it as
// "unconfirmed", never as success. A nil error with ConfirmErr empty means
// the oracle was read; IsParsingInProgress is the observed state.
func (c *Client) StopParsingAndConfirm(ctx context.Context) (*StopParsingResult, error) {
	if err := c.StopParsing(ctx); err != nil {
		return nil, err
	}
	form, err := c.GetParsingForm(ctx)
	if err != nil {
		// The DELETE succeeded (StopParsing returned nil above); only the
		// confirmation re-read failed. Surface this distinctly so the caller
		// can report "stop accepted, status unconfirmed" instead of either
		// claiming success or reporting a generic error that implies the
		// stop itself failed.
		return &StopParsingResult{ConfirmErr: err.Error()}, nil
	}
	return &StopParsingResult{IsParsingInProgress: form.IsParsingInProgress}, nil
}

// GetSearchPostEdit returns a scraped post's data in a format suitable for
// re-publishing. The response includes texts and attachments (photos with
// their URLs and metadata) that can be passed directly to RewriteSearchPost
// or POST /posts with as_copy=1.
//
// This is the correct way to copy photos from a scraped post: the edit
// endpoint returns photo objects with internal Hooppy IDs and source URLs
// that the server can process. Scraped VK photo IDs (owner_id + photo id)
// do NOT work — only the edit endpoint's attachment data does.
//
// UNDOCUMENTED: GET /posts-search/{id}/edit is not in the public OpenAPI spec.
func (c *Client) GetSearchPostEdit(ctx context.Context, searchPostID int) (*SearchPostEditResponse, error) {
	var resp SearchPostEditResponse
	path := fmt.Sprintf(pathPostsSearchEdit, searchPostID)
	params := url.Values{}
	params.Set("as_copy", "1")
	if err := c.doGET(ctx, path, params, &resp, true); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SourceContent is the resolved content of a scraped post, ready to publish
// via PublishPost. The attachment mapping (read shape → write shape) has
// already been applied: photos and videos share one {type: "photos"} group
// with array data. An unknown attachment kind would have errored at resolve
// time (ResolveSearchPost), so every entry here is a kind the vendor's write
// switch knows.
//
// SearchPostID is carried from the resolve step so PublishPost can send it in
// the ids wire field — the server uses it to link the created post to its
// scraped source (the is_used flag on the scraped post, dedup tracking).
type SourceContent struct {
	SearchPostID int          // the source scraped post id (for the ids wire field + slot recovery)
	Texts        []PostText   // the resolved text (from GET /edit?as_copy=1)
	Attachments  []Attachment // write-shape attachments (plural type, array data for photos)
}

// PublishTarget is where to publish a SourceContent. The targeting and
// scheduling fields follow the measured contract (spec fact 7):
//   - SelectedPagesIDs: []int, flat form — the server resolves page→source
//     itself (a create with [2355344] read back as
//     selected_pages_by_source_ids: {"3":[2355344]}).
//   - SchedulesIDs: []int, array on create (NOT the comma-string convention
//     /posts/batch/move uses). Schedule count is plan-gated: >5 requires VIP
//     or plan_type=2; hard ceiling 50.
//   - PublicationDate: nested {date, hours, minutes} for when_type=2.
//     project_id is required when publication_how_type=2 (not modelled here —
//     the caller's HowType carries it).
type PublishTarget struct {
	PublicationWhenType int
	PublicationHowType  int
	SelectedPagesIDs    []int
	SchedulesIDs        []int
	PublicationDate     *PublicationDate
}

// knownReadAttachmentKinds is the set of attachment kinds the READ side
// (GET /posts-search/{id}/edit?as_copy=1) emits in the attachments array's
// per-item "type" field. The read side uses SINGULAR type names ("photo",
// "video", "audio", "document", "link", "copyright", ...) — NOT the plural
// write-side group names ("photos", "audios", "documents") the vendor's
// getFormData switch keys on. This map gates the READ-side types that reach
// mapEditAttachmentsToWriteShape's default branch (everything except
// "photo"/"video", which are folded into the photos group). A read-side type
// NOT in this set is an error at resolve time — fact 4 (silently dropped
// videos) is the whole reason this mapping exists; a mapping that fails open
// reproduces it.
//
// "copyright" is included — it is the server's name for the VK source link,
// measured on deferred posts. "photo" and "video" are NOT here because they
// are handled by the case branch above the default, not by this allowlist.
var knownReadAttachmentKinds = map[string]bool{
	"audio":            true,
	"document":         true,
	"link":             true,
	"ad":               true,
	"poll":             true,
	"repost":           true,
	"source":           true,
	"comment":          true,
	"title":            true,
	"telegram_buttons": true,
	"location":         true,
	"settings":         true,
	"copyright":        true,
}

// mapEditAttachmentsToWriteShape transforms the read shape (one entry per
// media item, singular type, object data) into the write shape (one entry per
// group, plural type, array data). Photos and videos share the "photos"
// group — the vendor's getFormData switches on "photos" with no "videos"
// case; videos ride inside the photos group and the server sorts them out by
// each item's own type field (spec fact 4, measured both directions).
//
// Other known kinds (link, copyright, poll, etc.) pass through as-is — the
// server accepts the same singular type in the write body for non-media
// attachments (measured: copyright and link pass through and survive the
// round trip).
//
// An attachment kind the mapping does not know is an explicit error, never a
// silent drop — fact 4 is the whole reason this spec exists; a mapping that
// fails open reproduces the silent video-drop.
func mapEditAttachmentsToWriteShape(readAttachments []Attachment) ([]Attachment, error) {
	var photosGroup []interface{}
	var others []Attachment
	for _, att := range readAttachments {
		switch att.Type {
		case "photo", "video":
			photosGroup = append(photosGroup, att.Data)
		default:
			if !knownReadAttachmentKinds[att.Type] {
				return nil, fmt.Errorf("attachment kind %q is not a known READ-side attachment type (photo, video, audio, document, link, ad, poll, repost, source, comment, title, telegram_buttons, location, settings, copyright) — refusing to silently drop it (spec fact 4: a mapping that fails open reproduces the silent video-drop)", att.Type)
			}
			others = append(others, att)
		}
	}
	var result []Attachment
	if len(photosGroup) > 0 {
		result = append(result, Attachment{Type: "photos", Data: photosGroup})
	}
	result = append(result, others...)
	return result, nil
}

// ResolveSearchPost fetches the resolved content of a scraped post via
// GET /posts-search/{id}/edit?as_copy=1 and maps it into the write shape
// PublishPost expects. This is the GET-side resolver the product uses
// product-wide (spec fact 1): as_copy=1 is a GET-side resolver, not a
// server-side copy. The server returns the resolved content; the client is
// responsible for carrying it into the write.
//
// The attachment mapping (read shape → write shape) is applied here:
// photos and videos are grouped into one {type: "photos"} entry with array
// data. An unknown attachment kind returns an error — fact 4 (silently
// dropped videos) is the whole reason this mapping exists.
//
// The as_copy=1 query parameter is load-bearing: omitting it is the
// historical bug that yields empty posts (spec fact 1, F4). GetSearchPostEdit
// sets it; this function delegates to GetSearchPostEdit.
func (c *Client) ResolveSearchPost(ctx context.Context, searchPostID int) (SourceContent, error) {
	edit, err := c.GetSearchPostEdit(ctx, searchPostID)
	if err != nil {
		return SourceContent{}, err
	}
	attachments, err := mapEditAttachmentsToWriteShape(edit.Attachments)
	if err != nil {
		return SourceContent{}, fmt.Errorf("hooppy: ResolveSearchPost(%d): %w", searchPostID, err)
	}
	return SourceContent{
		SearchPostID: searchPostID,
		Texts:        edit.Texts,
		Attachments:  attachments,
	}, nil
}

// PublishPost creates one post via POST /posts from already-resolved content.
// This is the single maintained write path (spec fact 2: only POST /posts is
// maintained; /posts/copy and /posts/import appear zero times in the vendor's
// 9 MB web bundle). The content MUST come from ResolveSearchPost (which
// applies the attachment mapping); passing raw edit attachments would send
// the read shape (singular type, object data) that the write endpoint does
// not accept.
//
// The as_copy=1 field in the POST body is a mode marker (spec fact 1: it
// instructs nothing — the server does not auto-copy from ids). The resolved
// content (texts + attachments) is carried in the body; the ids field links
// the created post to its scraped source.
//
// Fail-closed guards (run before any request):
//   - Empty resolved content (no texts AND no attachments) is refused — the
//     server would accept it and create an empty post (spec F5).
//   - A schedule-driven publish (when_type=3) with an empty schedules list
//     is refused — it would target no schedule.
//
// Slot recovery: for when_type=3, the assigned schedule slot is recovered via
// the same snapshot-diff mechanism the prior Import/Rewrite paths used (see
// fillScheduleSlots). The before-snapshot is taken inside this method.
//
// doPOST never retries (no retryable param) — a create must not be retried
// after a committed write (issue #87). Enforced by TestRetryPolicySweep.
// idsWireField builds the ids wire field for POST /posts with as_copy=1.
// The ids field carries the scraped post ID the new post is copied from.
// When content.SearchPostID is 0 (a hand-built SourceContent not derived from
// a resolve step), the ids field is sent EMPTY — sending "0" would reference
// a non-existent scraped post id 0, which the server may reject or silently
// mis-attribute. A direct PublishPost caller building SourceContent by hand
// (not via ResolveSearchPost) should leave SearchPostID at 0; the post is
// published from the provided texts/attachments alone, with no source link.
func idsWireField(searchPostID int) string {
	if searchPostID == 0 {
		return ""
	}
	return strconv.Itoa(searchPostID)
}

func (c *Client) PublishPost(ctx context.Context, content SourceContent, target PublishTarget) (*PostIDResponse, error) {
	if len(content.Texts) == 0 && len(content.Attachments) == 0 {
		return nil, fmt.Errorf("hooppy: PublishPost: resolved content is empty (no texts, no attachments) — refusing to create an empty post (the server would accept it and return a post with no content); the source post may have no text and no media, or the resolve failed silently")
	}
	if target.PublicationWhenType == 3 && len(target.SchedulesIDs) == 0 {
		return nil, fmt.Errorf("hooppy: PublishPost: publication_when_type=3 (by schedule) requires at least one schedule ID in schedules_ids — got an empty list, which would target no schedule")
	}
	// Normalize nil slices to empty — the server expects arrays, not null.
	texts := content.Texts
	if texts == nil {
		texts = []PostText{}
	}
	attachments := content.Attachments
	if attachments == nil {
		attachments = []Attachment{}
	}
	pages := target.SelectedPagesIDs
	if pages == nil {
		pages = []int{}
	}
	schedules := target.SchedulesIDs
	if schedules == nil {
		schedules = []int{}
	}
	// Before snapshot for slot recovery: when when_type=3, snapshot the
	// schedule's posts BEFORE the create so fillScheduleSlots can diff
	// after. PublishPost creates one post, so idsSentCount=1. Walk ALL
	// pages (default page size is 20); a single-page snapshot would miss
	// pre-existing posts beyond page 1 and mis-attribute them as "created".
	// See fillScheduleSlots for WHY the list surface is used instead of
	// GET /posts/{id}/edit.
	var beforeSnapshot []Post
	var beforeErr error
	if target.PublicationWhenType == 3 && len(schedules) > 0 {
		beforeSnapshot, _, beforeErr = c.ListAllPostsWithTotal(ctx, ListPostsFilter{ScheduleID: schedules[0]})
		// A failed before snapshot is NOT fatal — the create proceeds,
		// and fillScheduleSlots reports the failure in SlotLookupError.
	}
	body := struct {
		AsCopy               int              `json:"as_copy"`
		PublicationWhenType  int              `json:"publication_when_type"`
		PublicationHowType   int              `json:"publication_how_type"`
		PublicationWhereType int              `json:"publication_where_type"`
		SelectedPagesIDs     []int            `json:"selected_pages_ids"`
		SchedulesIDs         []int            `json:"schedules_ids"`
		PublicationDate      *PublicationDate `json:"publication_date,omitempty"`
		Texts                []PostText       `json:"texts"`
		Attachments          []Attachment     `json:"attachments"`
		IDs                  string           `json:"ids"`
	}{
		AsCopy:               1,
		PublicationWhenType:  target.PublicationWhenType,
		PublicationHowType:   target.PublicationHowType,
		PublicationWhereType: 1,
		SelectedPagesIDs:     pages,
		SchedulesIDs:         schedules,
		PublicationDate:      target.PublicationDate,
		Texts:                texts,
		Attachments:          attachments,
		IDs:                  idsWireField(content.SearchPostID),
	}
	var resp PostIDResponse
	if err := c.doPOST(ctx, pathPosts, body, &resp); err != nil {
		return nil, err
	}
	c.fillScheduleSlots(ctx, &resp, target.PublicationWhenType, schedules, beforeSnapshot, beforeErr, 1)
	if err := checkCreateID("POST "+pathPosts, resp.ID, resp.IDs, resp.SlotLookupError); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LinkAttachment builds a link attachment from a URL string.
func LinkAttachment(url string) Attachment {
	return Attachment{Type: "link", Data: url}
}

// SourceAttachment builds a source attachment from a URL string.
// "source" is the UI's name for the original post link.
func SourceAttachment(url string) Attachment {
	return Attachment{Type: "source", Data: url}
}

// CopyrightAttachment builds a copyright attachment from a URL string.
// "copyright" is the server's name for the VK source link.
func CopyrightAttachment(url string) Attachment {
	return Attachment{Type: "copyright", Data: url}
}

// TitleAttachment builds a title attachment from a string.
func TitleAttachment(title string) Attachment {
	return Attachment{Type: "title", Data: title}
}

// PollAttachment builds a poll attachment from a Poll struct.
func PollAttachment(poll Poll) Attachment {
	return Attachment{Type: "poll", Data: poll}
}

// RepostAttachment builds a repost attachment.
func RepostAttachment(link, title string) Attachment {
	return Attachment{Type: "repost", Data: Repost{Link: link, Title: title}}
}

// CommentAttachment builds a comment attachment.
func CommentAttachment(text string, publishByAccount bool) Attachment {
	return Attachment{Type: "comment", Data: Comment{
		Text:             text,
		PublishByAccount: publishByAccount,
	}}
}

// TelegramButtonsAttachment builds a telegram_buttons attachment from a list
// of button {name, link} pairs.
func TelegramButtonsAttachment(buttons []TelegramButton) Attachment {
	return Attachment{Type: "telegram_buttons", Data: TelegramButtons{List: buttons}}
}

// resolveSearchPostIDs resolves the scraped-post id list from a
// CopySearchPostPayload. Precedence (enforced, not just documented — see
// CopySearchPostPayload doc):
//   - SearchPostIDs non-empty AND SearchPostID non-zero -> error (ambiguous).
//   - SearchPostIDs non-empty -> the slice itself (batch, caller order
//     preserved).
//   - SearchPostID non-zero -> []int{SearchPostID} (single, the legacy path).
//   - both empty -> error before any request (nothing to publish).
//
// Validation: every element of SearchPostIDs must be positive (id > 0); a
// zero or negative id is rejected with the offending index. The scalar path
// mirrors this: a negative SearchPostID is rejected; 0 is the unset
// sentinel, so the both-empty guard handles it. Duplicates are KEPT — the
// same source post in two schedule slots may be intentional. The function
// reads the slice only; it does NOT mutate payload.SearchPostIDs.
func resolveSearchPostIDs(payload CopySearchPostPayload) ([]int, error) {
	if len(payload.SearchPostIDs) > 0 && payload.SearchPostID != 0 {
		return nil, fmt.Errorf("hooppy: SearchPostIDs and SearchPostID are mutually exclusive — pass only one (the slice for a batch, the scalar for a single post)")
	}
	if len(payload.SearchPostIDs) > 0 {
		for i, id := range payload.SearchPostIDs {
			if id <= 0 {
				return nil, fmt.Errorf("hooppy: SearchPostIDs[%d] = %d — ids must be positive", i, id)
			}
		}
		return payload.SearchPostIDs, nil
	}
	if payload.SearchPostID < 0 {
		return nil, fmt.Errorf("hooppy: SearchPostID = %d — must be a positive id (0 means unset; pass a positive scraped-post id)", payload.SearchPostID)
	}
	if payload.SearchPostID != 0 {
		return []int{payload.SearchPostID}, nil
	}
	return nil, fmt.Errorf("hooppy: SearchPostIDs/SearchPostID is required — pass the slice for a batch or the scalar for a single post")
}

// RewriteSearchPost rewrites one or more scraped posts and publishes them to
// the user's own pages. Pass custom text in payload.Texts to override the
// original(s); leave Texts empty to keep the original text (resolved per post
// via GET /posts-search/{id}/edit?as_copy=1).
//
// This is a thin composition over ResolveSearchPost + PublishPost: for each
// scraped post ID (scalar SearchPostID or batch SearchPostIDs), it resolves
// the content, overrides the text if payload.Texts is non-empty, and publishes
// via POST /posts with as_copy=1. The batch is N independent resolve+publish
// pairs (client-side), NOT one server-side batch — this avoids the server's
// batch-specific defects (form-dependent text/attachment behaviour) and
// allows per-post text (each resolve carries its own original text).
//
// payload.Attachments is ignored — the attachments come from the resolve
// step (the read shape is mapped to the write shape by ResolveSearchPost).
// Passing attachments manually is no longer needed and no longer honoured;
// the caller who needs control over the write body should use PublishPost
// directly.
//
// resolvePublishBatch is the shared core of RewriteSearchPost and
// ImportSearchPost. It iterates the scraped-post id list, resolving and
// publishing each post independently (client-side loop). overrideTexts, when
// non-nil, replaces each post's resolved text (the rewrite single-post
// override); nil keeps the original text (import). callerName prefixes the
// per-post error wrapping.
//
// PARTIAL-RESULT CONTRACT (the reason this helper exists as one place):
// a per-post failure does NOT discard the posts already created. The helper
// implements the three documented outcomes (see PartialPostError):
//   - every post succeeded → (non-nil resp, nil err)
//   - some succeeded, some failed → (non-nil resp, *PartialPostError) — the
//     result carries every id that landed, the error carries every failure
//   - every post failed (batch) → (nil resp, plain error) — NOT
//     *PartialPostError, so a caller type-asserting *PartialPostError reads
//     "total failure" not "some succeeded"; the CLI paths exit 1 (error), not
//     2 (partial)
//
// A caller that ignores the error and reads only the result still sees what
// landed on the partial path; a caller that type-asserts the error gets the
// failures too. This mirrors runImport's per_post array (CLI) and
// ListAllSearchPostsWithFirstAndLastTotal's Capped partial-result convention
// (library) — a partial result is returned populated alongside a typed error,
// never discarded.
//
// The single-post path (len(ids) == 1) does NOT use PartialPostError — a
// single-post failure returns a plain wrapped error with a nil result,
// matching the pre-batch contract (one post, one error, nothing to accumulate).
func (c *Client) resolvePublishBatch(ctx context.Context, ids []int, target PublishTarget, overrideTexts []PostText, noAttachments bool, callerName string) (*PostIDResponse, error) {
	// Batch+Texts broadcast guard (MAJOR 3): a batch (len(ids) > 1) with a
	// non-empty overrideTexts would broadcast one text across all N posts,
	// silently overwriting each post's resolved text with the same string —
	// the exact thing the MCP error message calls impossible. The guard is a
	// payload invariant (checkable before any request), so it fires BEFORE
	// the resolve loop — no GET /edit, no POST /posts. The single-post path
	// (len(ids) == 1) is the legitimate text-override case and is allowed.
	// This is the shared invariant the reviewer asked for at the choke point;
	// resolvePublishBatch is the single place every Rewrite/Import path
	// passes through, so the guard lives here once, not duplicated per caller.
	if len(ids) > 1 && len(overrideTexts) > 0 {
		return nil, fmt.Errorf("hooppy: %s: SearchPostIDs (batch, %d ids) with Texts (non-empty) is a broadcast — one text array overwrites all N posts' resolved text with the same string; for per-post text, call PublishPost per id with each post's own SourceContent.Texts; for a single-post text override, use SearchPostID (scalar) not SearchPostIDs (batch)", callerName, len(ids))
	}
	var resp PostIDResponse
	var failed []PostFailure
	createdNoID := 0 // batch posts that succeeded but returned no id (CreateNoIDError)
	for _, id := range ids {
		content, err := c.ResolveSearchPost(ctx, id)
		if err != nil {
			failed = append(failed, PostFailure{SearchPostID: id, Err: fmt.Errorf("hooppy: %s: resolve %d: %w", callerName, id, err)})
			continue
		}
		// Apply the text override only when the caller actually provided
		// text. The predicate mirrors the batch+Texts broadcast guard above
		// (len(overrideTexts) > 0): an empty non-nil slice []PostText{} is
		// the batch idiom (buildRewritePayload / buildRewriteSearchPostPayload
		// emit it for a batch on main, and the v1.1.2 godoc describes it as
		// the batch idiom) and MUST NOT wipe the resolved text — testing
		// `overrideTexts != nil` here would replace the resolved text with an
		// empty array, silently publishing a text-less post (MAJOR 2).
		if len(overrideTexts) > 0 {
			content.Texts = overrideTexts
		}
		if noAttachments {
			content.Attachments = nil
		}
		r, err := c.PublishPost(ctx, content, target)
		if err != nil {
			var cnid *CreateNoIDError
			if errors.As(err, &cnid) {
				// CreateNoIDError means the create SUCCEEDED and the
				// server omitted the id (success_gate.go:121: "The post
				// may exist on the server"). For a BATCH, this is a
				// success-with-unknown-id — a third bucket, not a
				// failure. Treating it as a failure files every post
				// under `failed`, trips the all-failed gate, and reports
				// "no posts were published" which may be FALSE — the
				// posts may well exist on the server. That wording
				// invites a duplicate-spawning re-run (the round-1
				// blocker's exact hazard). Match runImport's
				// created_no_id status: count it as succeeded, do NOT
				// add a zero id to resp.IDs (a zero flows into
				// move/update/delete as a real-looking handle, issue
				// #131). The single-post path (len(ids) == 1) keeps the
				// pre-batch contract: return the typed error so the
				// caller can type-assert *CreateNoIDError (F2), and the
				// CLI runner maps it to exit 0 the way runImport does.
				if len(ids) > 1 {
					createdNoID++
					continue
				}
			}
			failed = append(failed, PostFailure{SearchPostID: id, Err: fmt.Errorf("hooppy: %s: publish %d: %w", callerName, id, err)})
			continue
		}
		resp.IDs = append(resp.IDs, r.ID)
		resp.Slots = append(resp.Slots, r.Slots...)
		// PublicationDate/ScheduleID are taken from the FIRST successful post
		// only. For a batch (N posts), these fields are MEANINGLESS — each
		// post has its own publication date (when_type=2) or its own schedule
		// slot (when_type=3, recoverable from resp.Slots by id). The batch
		// caller should read resp.Slots (per-post) or resp.IDs (order), not
		// resp.PublicationDate/ScheduleID (first-post only). The fields are
		// kept populated for the single-post path, where they are meaningful.
		if resp.ID == 0 {
			resp.ID = r.ID
			resp.PublicationDate = r.PublicationDate
			resp.ScheduleID = r.ScheduleID
		}
		if r.SlotLookupError != "" {
			if resp.SlotLookupError != "" {
				resp.SlotLookupError += "; "
			}
			resp.SlotLookupError += r.SlotLookupError
		}
	}
	// Single-post path: a failure is a plain error, not a partial — there is
	// nothing to accumulate. Return the wrapped error with a nil result,
	// matching the pre-batch contract.
	if len(ids) == 1 && len(failed) > 0 {
		return nil, failed[0].Err
	}
	if len(failed) == 0 {
		return &resp, nil
	}
	// All-failed batch (len(ids) > 1, every post failed, no created-no-id
	// successes): return a PLAIN error with a nil result — NOT a
	// *PartialPostError. The documented three-outcome contract (see
	// PartialPostError) is:
	//   - err == nil                  → every post succeeded
	//   - err is *PartialPostError     → SOME succeeded, some failed (resp non-nil)
	//   - err is non-nil, not *PartialPostError → EVERY post failed (resp is nil)
	// Returning *PartialPostError with an empty Result.IDs here would collapse
	// outcomes 2 and 3: a caller type-asserting *PartialPostError would read
	// "some succeeded" when nothing did, and the CLI `search rewrite` path
	// (which maps *PartialPostError to exit 2/partial) would exit 2 on a total
	// failure — diverging from runImport, which exits 1 on all-failed. A plain
	// error makes both CLI paths exit 1 (runImport via its own loop, rewrite
	// via die(err)) and lets a caller distinguish total from partial by type.
	//
	// The createdNoID guard: a batch where every post returned 2xx with no id
	// (CreateNoIDError) is NOT all-failed — those posts succeeded with an
	// unknown id (MAJOR 1). Without this guard, createdNoID > 0 with empty
	// resp.IDs would fall into the all-failed branch and report "no posts were
	// published" which may be false. The wording is softened from "no posts
	// were published" to "no post ids were returned" because the former is
	// asserted as fact and may be false when the server omitted ids.
	if len(resp.IDs) == 0 && createdNoID == 0 {
		return nil, fmt.Errorf("hooppy: %s: every post in the batch failed (%d/%d); no post ids were returned — first failure: %w", callerName, len(failed), len(ids), failed[0].Err)
	}
	// Batch partial: some succeeded, some failed. Return the populated result
	// alongside the typed error so a caller never loses already-published ids.
	return &resp, &PartialPostError{Result: &resp, Failed: failed}
}

// RewriteSearchPost rewrites one or more scraped posts and publishes them to
// the user's own pages. Pass custom text in payload.Texts to override the
// original(s); leave Texts empty to keep the original text (resolved per post
// via GET /posts-search/{id}/edit?as_copy=1).
//
// This is a thin composition over ResolveSearchPost + PublishPost: for each
// scraped post ID (scalar SearchPostID or batch SearchPostIDs), it resolves
// the content, overrides the text if payload.Texts is non-empty, and publishes
// via POST /posts with as_copy=1. The batch is N independent resolve+publish
// pairs (client-side), NOT one server-side batch — this avoids the server's
// batch-specific defects (form-dependent text/attachment behaviour) and
// allows per-post text (each resolve carries its own original text).
//
// payload.Attachments is ignored — the attachments come from the resolve
// step (the read shape is mapped to the write shape by ResolveSearchPost).
// Passing attachments manually is no longer needed and no longer honoured;
// the caller who needs control over the write body should use PublishPost
// directly.
//
// PARTIAL-RESULT CONTRACT: a batch (SearchPostIDs with >1 element) where some
// posts succeed and some fail returns a NON-NIL *PostIDResponse (populated
// with every id that landed) alongside a *PartialPostError. A caller never
// loses already-published posts from the return value. The single-post path
// (SearchPostID) returns a plain wrapped error with a nil result on failure,
// matching the pre-batch contract. See PartialPostError for the three-outcome
// distinction.
//
// UNDOCUMENTED: POST /posts with as_copy=1 + ids is not in the public OpenAPI spec.
func (c *Client) RewriteSearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
	ids, err := resolveSearchPostIDs(payload)
	if err != nil {
		return nil, err
	}
	// Converse guard (issue #111): schedules_ids targets the by-schedule
	// queue; every other when_type ignores it. RewriteSearchPost carries
	// payload.SchedulesIDs into the PublishTarget below and PublishPost
	// marshals it onto POST /posts, so without this guard SchedulesIDs +
	// when_type!=3 reaches the wire under a publish-now/at-time intent. The
	// CLI builder guard does not protect an external consumer of this public
	// module; this is the layer below it.
	if len(payload.SchedulesIDs) > 0 && payload.PublicationWhenType != 3 {
		return nil, fmt.Errorf("hooppy: RewriteSearchPost: schedules_ids is set but publication_when_type=%d (not 3) — schedules target the by-schedule queue and are silently dropped or contradicted under other when-types; pass publication_when_type=3 to queue by schedule, or clear schedules_ids to publish as when-type %d intends", payload.PublicationWhenType, payload.PublicationWhenType)
	}
	target := PublishTarget{
		PublicationWhenType: payload.PublicationWhenType,
		PublicationHowType:  payload.PublicationHowType,
		SelectedPagesIDs:    payload.SelectedPagesIDs,
		SchedulesIDs:        payload.SchedulesIDs,
		PublicationDate:     payload.PublicationDate,
	}
	// Override text if the caller provided one (single-post rewrite with
	// custom text). For a batch, Texts is empty — each post keeps its own
	// resolved text (per-post, the whole point of the client-side loop).
	// The batch+Texts broadcast guard lives in resolvePublishBatch (the
	// single choke point every Rewrite/Import path passes through) — see
	// resolvePublishBatch.
	overrideTexts := payload.Texts
	return c.resolvePublishBatch(ctx, ids, target, overrideTexts, payload.NoAttachments, "RewriteSearchPost")
}

// SearchPostEditAttachments builds the attachments array from a scraped post's
// edit response, matching the Hooppy UI's behavior:
//   - Photo AND video attachments are grouped into a single {type: "photos"}
//     attachment (the UI puts both into v.photos; the server stores VK video
//     references as-is and downloads photos with `url` async).
//   - Other attachment types (link, poll, repost, etc.) are passed through
//     as individual {type: <type>, data: <data>} entries.
//
// This is the correct way to preserve ALL attachments when copying a scraped
// post — the server's async download (is_attachments_in_process) only triggers
// for photos with a `url` or `message_id` field inside a {type: "photos"}
// attachment; videos and other types are stored directly.
//
// NOTE: this helper is also used by UpdatePostText (own-post edit), which
// reads GET /posts/{id}/edit (the SAME singular vocabulary the scraped-post
// edit endpoint uses — measured across 11 real own-posts). Do not remove it
// when the scraped-post copy family is refactored.
func SearchPostEditAttachments(editAttachments []Attachment) []Attachment {
	var photosAndVideos []interface{}
	var others []Attachment
	for _, att := range editAttachments {
		if att.Type == "photo" || att.Type == "video" {
			photosAndVideos = append(photosAndVideos, att.Data)
		} else {
			others = append(others, att)
		}
	}
	var result []Attachment
	if len(photosAndVideos) > 0 {
		result = append(result, Attachment{Type: "photos", Data: photosAndVideos})
	}
	result = append(result, others...)
	return result
}

// ScrapedPhotoAttachment builds a {type: "photos"} attachment from a list of
// scraped VK photo descriptors (SearchPostPhoto: id + owner_id). It is kept as
// a Deprecated shim so existing consumers compile; it delegates to
// SearchPostEditAttachments (the maintained grouping helper) by promoting each
// SearchPostPhoto to a singular {type: "photo"} read-shape attachment and
// letting the grouper fold them into one photos group — the same mapping the
// resolve step applies.
//
// Deprecated: use ResolveSearchPost + PublishPost instead. The resolve step
// (GET /posts-search/{id}/edit?as_copy=1) returns attachments already in the
// read shape, and mapEditAttachmentsToWriteShape maps them to the write shape;
// building a photos group by hand from VK photo ids is no longer needed.
func ScrapedPhotoAttachment(photos []SearchPostPhoto) Attachment {
	atts := make([]Attachment, 0, len(photos))
	for _, ph := range photos {
		atts = append(atts, Attachment{Type: "photo", Data: map[string]interface{}{
			"id":       strconv.Itoa(ph.ID),
			"owner_id": ph.OwnerID,
			"type":     "photo",
		}})
	}
	grouped := SearchPostEditAttachments(atts)
	if len(grouped) == 0 {
		return Attachment{}
	}
	return grouped[0] // the photos group (SearchPostEditAttachments emits it first)
}

// SearchPostPhotos extracts the photo/video group from a scraped post's edit
// response as a single *Attachment (the {type: "photos"} group the write
// endpoint expects), or nil when the post has no photos or videos. It is kept
// as a Deprecated shim so existing consumers compile; it delegates to
// SearchPostEditAttachments (the maintained grouping helper).
//
// Deprecated: use ResolveSearchPost, which returns SourceContent.Attachments
// already mapped to the write shape (the photos group plus any non-photo
// attachments). This helper returns only the photos group and drops the rest;
// ResolveSearchPost preserves every attachment.
func SearchPostPhotos(edit *SearchPostEditResponse) *Attachment {
	if edit == nil {
		return nil
	}
	for _, att := range SearchPostEditAttachments(edit.Attachments) {
		if att.Type == "photos" {
			a := att
			return &a
		}
	}
	return nil
}

// SearchPostNonPhotoAttachments extracts every non-photo/video attachment from
// a scraped post's edit response, passing them through as individual
// {type, data} entries. It is kept as a Deprecated shim so existing consumers
// compile; it delegates to SearchPostEditAttachments (the maintained grouping
// helper) and filters out the photos group.
//
// Deprecated: use ResolveSearchPost, which returns SourceContent.Attachments
// carrying both the photos group and the non-photo attachments in one slice,
// already in the write shape.
func SearchPostNonPhotoAttachments(edit *SearchPostEditResponse) []Attachment {
	if edit == nil {
		return nil
	}
	var others []Attachment
	for _, att := range SearchPostEditAttachments(edit.Attachments) {
		if att.Type != "photos" {
			others = append(others, att)
		}
	}
	return others
}

// ImportSearchPost imports one or more scraped posts. Unlike
// RewriteSearchPost (which overrides text), ImportSearchPost keeps each
// post's original text — the resolve step fetches it via
// GET /posts-search/{id}/edit?as_copy=1, and PublishPost sends it as-is.
//
// This is a thin composition over ResolveSearchPost + PublishPost, identical
// to RewriteSearchPost except it does NOT override the resolved text with
// payload.Texts. The batch is N independent resolve+publish pairs
// (client-side), NOT one server-side batch — this avoids the server's
// batch-specific defects (form-dependent text/attachment behaviour) and
// gives each post its own original text.
//
// payload.Attachments is ignored — the attachments come from the resolve
// step. payload.Texts is ignored — the original text from the resolve step
// is used (import = keep original). The caller who needs text or attachment
// control should use ResolveSearchPost + PublishPost directly.
//
// PARTIAL-RESULT CONTRACT: same as RewriteSearchPost — a batch where some
// posts succeed and some fail returns a NON-NIL *PostIDResponse (populated
// with every id that landed) alongside a *PartialPostError. See
// RewriteSearchPost and PartialPostError.
//
// UNDOCUMENTED: POST /posts with as_copy=1 + ids is not in the public OpenAPI spec.
func (c *Client) ImportSearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
	ids, err := resolveSearchPostIDs(payload)
	if err != nil {
		return nil, err
	}
	// Converse guard (issue #111): schedules_ids targets the by-schedule
	// queue; every other when_type ignores it. Import carries
	// payload.SchedulesIDs into the PublishTarget below unconditionally, so
	// without this guard a library consumer calling ImportSearchPost directly
	// with SchedulesIDs + when_type=1 sends both intents onto the wire and the
	// server picks one. The CLI builder guard does not protect an external
	// consumer of this public module; this is the layer below it.
	if len(payload.SchedulesIDs) > 0 && payload.PublicationWhenType != 3 {
		return nil, fmt.Errorf("hooppy: ImportSearchPost: schedules_ids is set but publication_when_type=%d (not 3) — schedules target the by-schedule queue and are sent alongside a publish-now/at-time intent, which the server resolves on its own; pass publication_when_type=3 to queue by schedule, or clear schedules_ids to publish as when-type %d intends", payload.PublicationWhenType, payload.PublicationWhenType)
	}
	target := PublishTarget{
		PublicationWhenType: payload.PublicationWhenType,
		PublicationHowType:  payload.PublicationHowType,
		SelectedPagesIDs:    payload.SelectedPagesIDs,
		SchedulesIDs:        payload.SchedulesIDs,
		PublicationDate:     payload.PublicationDate,
	}
	// Import keeps the original text — pass nil so resolvePublishBatch does
	// NOT override the resolved text (import = keep original).
	return c.resolvePublishBatch(ctx, ids, target, nil, payload.NoAttachments, "ImportSearchPost")
}

// CopySearchPost copies a scraped post to the user's own pages. It is a
// thin deprecated wrapper that delegates to ResolveSearchPost + PublishPost
// (the same corrected primitives ImportSearchPost uses), preserving the
// source post's text and attachments (photos, videos, links) from the
// resolve step.
//
// Deprecated: use ImportSearchPost instead. The historical CopySearchPost
// (PUT /posts/copy) created empty posts — it used a different wire shape
// (singular search_post_id int) from the maintained path and its endpoint
// silently ignored the search_post_ids slice. This wrapper keeps the
// exported symbol so existing consumers compile, but it now DOES what its
// name always claimed: resolves the scraped post via GET /posts-search/{id}/edit?as_copy=1
// and publishes via POST /posts with as_copy=1, carrying text + attachments.
// Use ImportSearchPost for the same behaviour under a non-deprecated name,
// or RewriteSearchPost to override the text.
//
// CopySearchPost is single-post only: SearchPostIDs (batch) is refused
// before any request — the historical PUT /posts/copy endpoint took a
// singular search_post_id and silently ignored the batch slice. Use
// ImportSearchPost or RewriteSearchPost for a batch.
//
// Argument change vs the historical CopySearchPost: payload.Texts and
// payload.Attachments are IGNORED. The historical PUT /posts/copy marshalled
// both into its request body; this shim delegates to resolvePublishBatch,
// which resolves the source post's text and attachments via
// GET /posts-search/{id}/edit?as_copy=1 and carries THOSE into POST /posts.
// A consumer passing payload.Texts now gets the resolved original text (not
// the override) with no error — to override text, use RewriteSearchPost; to
// control attachments, use ResolveSearchPost + PublishPost directly. This
// matches ImportSearchPost (import = keep original) and is the behaviour the
// historical endpoint's name always claimed but never delivered.
//
// UNDOCUMENTED: POST /posts with as_copy=1 + ids is not in the public OpenAPI spec.
func (c *Client) CopySearchPost(ctx context.Context, payload CopySearchPostPayload) (*PostIDResponse, error) {
	if len(payload.SearchPostIDs) > 0 {
		return nil, fmt.Errorf("hooppy: CopySearchPost: SearchPostIDs (batch) is not supported — the historical PUT /posts/copy endpoint took a singular search_post_id and silently ignored the batch slice; use ImportSearchPost or RewriteSearchPost for a batch (they join SearchPostIDs into the ids wire field on POST /posts with as_copy=1)")
	}
	if payload.SearchPostID == 0 {
		return nil, fmt.Errorf("hooppy: CopySearchPost: SearchPostID is required (the scalar scraped post id to copy); use ImportSearchPost for the same behaviour under a non-deprecated name")
	}
	// Converse guard (issue #111): schedules_ids targets the by-schedule
	// queue; every other when_type ignores it. The shim carries
	// payload.SchedulesIDs into the PublishTarget below, so without this guard
	// SchedulesIDs + when_type!=3 reaches POST /posts under a publish-now/
	// at-time intent — the historical PUT /posts/copy guard, preserved across
	// the collapse onto resolve+publish. The CLI guard does not protect an
	// external consumer of this public module; this is the layer below it.
	if len(payload.SchedulesIDs) > 0 && payload.PublicationWhenType != 3 {
		return nil, fmt.Errorf("hooppy: CopySearchPost: schedules_ids is set but publication_when_type=%d (not 3) — schedules target the by-schedule queue and are silently dropped or contradicted under other when-types; pass publication_when_type=3 to queue by schedule, or clear schedules_ids to publish as when-type %d intends", payload.PublicationWhenType, payload.PublicationWhenType)
	}
	// Delegate to the same resolve+publish path ImportSearchPost uses. This
	// is a single-post path (SearchPostID scalar), so resolvePublishBatch
	// returns a plain wrapped error on failure (not PartialPostError).
	target := PublishTarget{
		PublicationWhenType: payload.PublicationWhenType,
		PublicationHowType:  payload.PublicationHowType,
		SelectedPagesIDs:    payload.SelectedPagesIDs,
		SchedulesIDs:        payload.SchedulesIDs,
		PublicationDate:     payload.PublicationDate,
	}
	return c.resolvePublishBatch(ctx, []int{payload.SearchPostID}, target, nil, payload.NoAttachments, "CopySearchPost")
}
