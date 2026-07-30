package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/anatolykoptev/go-hooppy"
)

// scheduleQueueSummary is the default (non-JSON) output of `hooppy
// schedules queue <id>`. It surfaces the four values an operator needs to
// decide whether to move posts INTO this schedule: the queue depth
// (TotalRows), the first booked day (the FIRST day with posts), the
// booked-until date (the LAST day with posts), and the per-day counts so a
// glance shows how the queue is distributed. The raw envelope is available
// via --json.
//
// FirstBookedDay is the first day that HAS posts — NOT the next FREE slot
// (a caller deciding where to move something wants the free slot, which this
// field is not). The name says what it is.
//
// BookedUntil is OMITTED when IsHasMore is true: a truncated response's last
// day key is the last day of page ONE, not the real booked-until date, and
// emitting it would repeat the silent-truncation defect #106 exists to
// remove. total_rows stays — it is the real depth regardless of truncation.
type scheduleQueueSummary struct {
	ScheduleID     int                `json:"schedule_id"`
	TotalRows      int                `json:"total_rows"`
	RowsLimit      int                `json:"rows_limit"`
	IsHasMore      bool               `json:"is_has_more"`
	FirstBookedDay string             `json:"first_booked_day,omitempty"` // first day WITH posts (dd.mm.yyyy) — NOT the next free slot
	BookedUntil    string             `json:"booked_until,omitempty"`     // last day with posts (dd.mm.yyyy); OMITTED when IsHasMore (truncated)
	DayCounts      []scheduleDayCount `json:"day_counts"`
}

// scheduleDayCount is one day's entry in the per-day counts.
type scheduleDayCount struct {
	Day   string `json:"day"`   // dd.mm.yyyy
	Count int    `json:"count"` // posts scheduled for that day
}

// runScheduleQueue is the testable core of `hooppy schedules queue <id>`.
// It issues exactly ONE request (ListSchedulePosts — no paged walk) and
// prints either the raw envelope (--json) or a summary (depth, first booked
// day, booked-until, per-day counts) to out, diagnostics to errOut. Returns
// the process exit code: 0 on success, 1 on error, 2 on a PARTIAL/truncated
// result (is_has_more=true) OR a page OVERRUN (page>0 with zero day keys
// and total_rows>0 — a page past the end of the collection). Never calls
// os.Exit itself.
//
// dateFrom/dateTo (dd.mm.yyyy, "" = unset) and page (0 = unset) are passed
// through to the endpoint to narrow a truncated calendar. A narrowed query
// is a subset by construction; the summary then OMITS first_booked_day and
// booked_until (the window's bounds are NOT the schedule's) and a note is
// written to errOut so the omission is visible. day_counts (accurate for
// the window) still carries the per-day detail.
//
// The one-request contract is load-bearing: issue #106 explicitly forbids
// a paged walk. The endpoint returns the whole calendar in one envelope;
// paging would issue multiple requests against a one-request contract and
// would not change the result.
//
// Truncation: when the server returns is_has_more=true the response is
// PARTIAL — only the first page of days. The summary then OMITS booked_until
// (the last day of page one is NOT the real booked-until date) and a loud
// warning is written to errOut naming --from/--to and --page as the recovery
// levers. The exit code is 2 (partial/truncated) so a script can branch:
// 0=complete, 1=error, 2=partial. total_rows (the real depth) is still
// emitted.
//
// Page overrun: total_rows is the COLLECTION total — it does not change with
// paging (page=2 and page=99 both return total_rows=96 with zero day keys),
// so it cannot detect an overrun by comparison. The ONLY signal is
// page>0 && len(PostsByDays)==0 && TotalRows>0: a page past the end. That
// warns naming the overrun + total_rows, emits day_counts as [] (not null,
// so the output shape stays consistent across branches), and exits 2 — an
// agent walking pages to recover a truncation must not read an overrun as
// "complete" (exit 0).
func runScheduleQueue(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, scheduleID int, dateFrom, dateTo string, page int, asJSON bool) int {
	if scheduleID == 0 {
		fmt.Fprintln(errOut, "error: schedules queue requires a schedule ID (got 0)")
		return 1
	}
	resp, err := c.ListSchedulePosts(ctx, hooppy.ListSchedulePostsFilter{
		ScheduleID: scheduleID,
		DateFrom:   dateFrom,
		DateTo:     dateTo,
		Page:       page,
	})
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	narrowed := dateFrom != "" || dateTo != "" || page > 1
	// Page overrun: page>0 with zero day keys and total_rows>0 is a page
	// PAST THE END (total_rows is the collection total, unchanged by paging,
	// so it cannot detect an overrun by comparison — only this signal can).
	// Warn + exit 2 so a script does not read an overrun as "complete".
	if page > 0 && len(resp.PostsByDays) == 0 && resp.TotalRows > 0 {
		fmt.Fprintf(errOut, "warn: page %d is past the end of the calendar (total_rows=%d, zero day keys returned). total_rows is the collection total and does not change with paging; this page has no data. Use a lower --page.\n", page, resp.TotalRows)
		if asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(resp); err != nil {
				fmt.Fprintf(errOut, "error encoding output: %v\n", err)
				return 1
			}
			return 2
		}
		summary := buildScheduleQueueSummary(resp, scheduleID, dateFrom, dateTo, page)
		// Force day_counts to an empty slice (not nil) so it marshals as []
		// not null — the output shape must not change between branches.
		if summary.DayCounts == nil {
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(summary); err != nil {
			fmt.Fprintf(errOut, "error encoding output: %v\n", err)
			return 1
		}
		return 2
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(errOut, "error encoding output: %v\n", err)
			return 1
		}
		if resp.IsHasMore {
			fmt.Fprintf(errOut, "warn: PARTIAL result — is_has_more=true (total_rows=%d, rows_limit=%d); the calendar is truncated to the first page. Narrow with --from/--to (date_from/date_to) or advance --page to recover the rest. One-request contract: no paged walk.\n", resp.TotalRows, resp.RowsLimit)
			return 2
		}
		return 0
	}
	summary := buildScheduleQueueSummary(resp, scheduleID, dateFrom, dateTo, page)
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summary); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	if resp.IsHasMore {
		fmt.Fprintf(errOut, "warn: PARTIAL result — is_has_more=true (total_rows=%d, rows_limit=%d); booked_until is OMITTED because the last day shown is only the last day of page ONE, not the real booked-until date (and also omitted if --from/--to/--page narrowed the query). Narrow with --from/--to (date_from/date_to) or advance --page to recover the rest. One-request contract: no paged walk.\n", resp.TotalRows, resp.RowsLimit)
		return 2
	}
	// A narrowed (but complete, non-overrun) query omits first_booked_day
	// and booked_until because its day keys are the WINDOW's bounds, not the
	// schedule's. Surface the omission on stderr so it is not silent — the
	// operator narrowing to recover a truncation is the exact scenario
	// where a silent wrong answer bit before. Exit 0: the window's answer
	// is complete for the question asked.
	if narrowed {
		fmt.Fprint(errOut, "note: --from/--to/--page set — first_booked_day and booked_until are OMITTED (the day keys are the narrowed window's bounds, not the schedule's); day_counts reflects the window.\n")
	}
	return 0
}

// buildScheduleQueueSummary transforms the raw SchedulePostsResponse into
// the default summary. Days are sorted chronologically by their dd.mm.yyyy
// key (parsed as a date, NOT lexicographically — "01.02.2027" must sort
// after "31.01.2027", and a string sort would put it before).
//
// dateFrom/dateTo (dd.mm.yyyy, "" = unset) and page (0 = unset) describe
// the query that produced resp. A NARROWED query (any of the three set) is
// a subset BY CONSTRUCTION — its first/last day keys are the window's
// bounds, NOT the schedule's first_booked_day/booked_until. Emitting them
// under the schedule-wide field names would repeat the silent-wrong-answer
// defect #106 exists to remove, reachable with the very --from/--to flags
// the truncation warning points an operator to. So when narrowed, both
// schedule-wide fields are OMITTED; day_counts (accurate for the window)
// carries the per-day detail instead.
//
// FirstBookedDay is the first day with posts; BookedUntil is the last.
// Both are omitted (omitempty) when the queue is empty so an empty schedule
// reads as {"total_rows":0,...} not {"first_booked_day":"","booked_until":""}.
// BookedUntil is ALSO omitted when IsHasMore is true — a truncated
// response's last day key is the last day of page one, not the real
// booked-until date (the silent-truncation defect #106 exists to remove).
func buildScheduleQueueSummary(resp *hooppy.SchedulePostsResponse, scheduleID int, dateFrom, dateTo string, page int) scheduleQueueSummary {
	s := scheduleQueueSummary{
		ScheduleID: scheduleID,
		// Always a slice, never nil: a nil DayCounts marshals as `null`,
		// which changes the output shape between the empty and non-empty
		// branches. Set here so every early return below is already stable.
		DayCounts: []scheduleDayCount{},
	}
	if resp == nil {
		return s
	}
	s.TotalRows = resp.TotalRows
	s.RowsLimit = resp.RowsLimit
	s.IsHasMore = resp.IsHasMore
	if len(resp.PostsByDays) == 0 {
		return s
	}
	type dayKey struct {
		raw    string
		parsed string // yyyy-mm-dd for chronological sort
	}
	keys := make([]dayKey, 0, len(resp.PostsByDays))
	for raw := range resp.PostsByDays {
		parsed := normalizeDayKeyForSort(raw)
		keys = append(keys, dayKey{raw: raw, parsed: parsed})
	}
	// Sort by parsed date; fall back to raw string for unparseable keys
	// (stable and deterministic, never aborts the whole summary on one
	// malformed key).
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].parsed != "" && keys[j].parsed != "" {
			return keys[i].parsed < keys[j].parsed
		}
		if keys[i].parsed != "" {
			return true
		}
		if keys[j].parsed != "" {
			return false
		}
		return keys[i].raw < keys[j].raw
	})
	s.DayCounts = make([]scheduleDayCount, 0, len(keys))
	for _, k := range keys {
		s.DayCounts = append(s.DayCounts, scheduleDayCount{
			Day:   k.raw,
			Count: len(resp.PostsByDays[k.raw]),
		})
	}
	// Schedule-wide first_booked_day/booked_until are emitted ONLY for an
	// UNnarrowed query. A narrowed query (date_from/date_to/page set) is a
	// subset by construction: its first/last day keys are the WINDOW's
	// bounds, not the schedule's. is_has_more is false for a complete
	// window, so the IsHasMore guard alone does not catch this — and the
	// --from/--to flags are the very levers the truncation warning points
	// an operator to. Emitting the window's bounds under the schedule-wide
	// field names would print a schedule-wide fact that is silently wrong.
	// day_counts (accurate for the window) carries the per-day detail.
	narrowed := dateFrom != "" || dateTo != "" || page > 1
	if len(s.DayCounts) > 0 && !narrowed {
		s.FirstBookedDay = s.DayCounts[0].Day
		// BookedUntil is the last day with posts — but ONLY when the
		// response is complete. A truncated (is_has_more=true) response's
		// last day key is the last day of page one; emitting it as
		// booked_until would repeat the silent-truncation defect #106
		// exists to remove. Omit it; the CLI warns loudly instead.
		if !resp.IsHasMore {
			s.BookedUntil = s.DayCounts[len(s.DayCounts)-1].Day
		}
	}
	return s
}

// normalizeDayKeyForSort converts a dd.mm.yyyy key to yyyy-mm-dd for
// chronological sorting. Returns "" if the key does not match the expected
// format (the caller falls back to a raw-string sort for that key, never
// aborts the whole summary).
func normalizeDayKeyForSort(ddmmyyyy string) string {
	if len(ddmmyyyy) != 10 || ddmmyyyy[2] != '.' || ddmmyyyy[5] != '.' {
		return ""
	}
	return ddmmyyyy[6:10] + "-" + ddmmyyyy[3:5] + "-" + ddmmyyyy[0:2]
}
