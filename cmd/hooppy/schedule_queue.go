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
// (TotalRows), the next slot (the FIRST day with posts), the booked-until
// date (the LAST day with posts), and the per-day counts so a glance shows
// how the queue is distributed. The raw envelope is available via --json.
type scheduleQueueSummary struct {
	ScheduleID  int                `json:"schedule_id"`
	TotalRows   int                `json:"total_rows"`
	RowsLimit   int                `json:"rows_limit"`
	IsHasMore   bool               `json:"is_has_more"`
	NextSlot    string             `json:"next_slot,omitempty"`    // first day with posts (dd.mm.yyyy)
	BookedUntil string             `json:"booked_until,omitempty"` // last day with posts (dd.mm.yyyy)
	DayCounts   []scheduleDayCount `json:"day_counts"`
}

// scheduleDayCount is one day's entry in the per-day counts.
type scheduleDayCount struct {
	Day   string `json:"day"`   // dd.mm.yyyy
	Count int    `json:"count"` // posts scheduled for that day
}

// runScheduleQueue is the testable core of `hooppy schedules queue <id>`.
// It issues exactly ONE request (ListSchedulePosts — no paged walk) and
// prints either the raw envelope (--json) or a summary (depth, next slot,
// booked-until, per-day counts) to out, diagnostics to errOut. Returns the
// process exit code (0 on success, 1 on error). Never calls os.Exit itself.
//
// The one-request contract is load-bearing: issue #106 explicitly forbids
// a paged walk. The endpoint returns the whole calendar in one envelope;
// paging would issue multiple requests against a one-request contract and
// would not change the result.
func runScheduleQueue(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, scheduleID int, asJSON bool) int {
	if scheduleID == 0 {
		fmt.Fprintln(errOut, "error: schedules queue requires a schedule ID (got 0)")
		return 1
	}
	resp, err := c.ListSchedulePosts(ctx, scheduleID)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(errOut, "error encoding output: %v\n", err)
			return 1
		}
		return 0
	}
	summary := buildScheduleQueueSummary(resp, scheduleID)
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summary); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}

// buildScheduleQueueSummary transforms the raw SchedulePostsResponse into
// the default summary. Days are sorted chronologically by their dd.mm.yyyy
// key (parsed as a date, NOT lexicographically — "01.02.2027" must sort
// after "31.01.2027", and a string sort would put it before). NextSlot is
// the first day with posts; BookedUntil is the last. Both are omitted
// (omitempty) when the queue is empty so an empty schedule reads as
// {"total_rows":0,...} not {"next_slot":"","booked_until":""}.
func buildScheduleQueueSummary(resp *hooppy.SchedulePostsResponse, scheduleID int) scheduleQueueSummary {
	s := scheduleQueueSummary{
		ScheduleID: scheduleID,
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
	if len(s.DayCounts) > 0 {
		s.NextSlot = s.DayCounts[0].Day
		s.BookedUntil = s.DayCounts[len(s.DayCounts)-1].Day
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
