package main

import (
	"encoding/json"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// schedulePostsBody is a GET /posts/schedules/{id}/posts fixture: a
// schedule with 4 posts across 3 days. The keys are dd.mm.yyyy; the
// fixture is deliberately ordered so a raw-string sort would mis-order
// "01.02.2027" before "31.01.2027" — the chronological-sort guard below
// catches that bug.
const schedulePostsBody = `{
	"posts_by_days": {
		"15.01.2027": [{"id":101,"text":"a"},{"id":102,"text":"b"}],
		"31.01.2027": [{"id":103,"text":"c"}],
		"01.02.2027": [{"id":104,"text":"d"}]
	},
	"total_rows": 4,
	"rows_limit": 1000,
	"is_has_more": false
}`

// TestBuildScheduleQueueSummary_TotalRowsAndBookedUntil is the
// output-shape guard for issue #106: the default summary MUST surface
// TotalRows (queue depth) and BookedUntil (the LAST day with posts). These
// are the two values an operator needs to decide whether to move posts
// INTO this schedule; omitting either is the issue #106 regression.
//
// RED-on-revert: drop BookedUntil from the summary and the assertion fails.
func TestBuildScheduleQueueSummary_TotalRowsAndBookedUntil(t *testing.T) {
	resp := &hooppy.SchedulePostsResponse{}
	if err := json.Unmarshal([]byte(schedulePostsBody), resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s := buildScheduleQueueSummary(resp, 55576)
	if s.TotalRows != 4 {
		t.Errorf("TotalRows = %d, want 4 (queue depth MUST appear in the summary)", s.TotalRows)
	}
	if s.BookedUntil != "01.02.2027" {
		t.Errorf("BookedUntil = %q, want \"01.02.2027\" (the LAST day with posts MUST appear in the summary — this is the booked-until date an operator needs before moving posts in)", s.BookedUntil)
	}
	if s.FirstBookedDay != "15.01.2027" {
		t.Errorf("FirstBookedDay = %q, want \"15.01.2027\" (the FIRST day with posts)", s.FirstBookedDay)
	}
}

// TestBuildScheduleQueueSummary_ChronologicalDayOrder verifies the per-day
// counts are sorted CHRONOLOGICALLY, not lexicographically. "01.02.2027"
// must sort AFTER "31.01.2027" (a raw-string sort would put it before).
// This is the off-by-one-month bug a string sort introduces.
func TestBuildScheduleQueueSummary_ChronologicalDayOrder(t *testing.T) {
	resp := &hooppy.SchedulePostsResponse{}
	if err := json.Unmarshal([]byte(schedulePostsBody), resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s := buildScheduleQueueSummary(resp, 55576)
	if len(s.DayCounts) != 3 {
		t.Fatalf("len(DayCounts) = %d, want 3", len(s.DayCounts))
	}
	want := []string{"15.01.2027", "31.01.2027", "01.02.2027"}
	for i, w := range want {
		if s.DayCounts[i].Day != w {
			t.Errorf("DayCounts[%d].Day = %q, want %q (chronological order — a raw-string sort would put 01.02.2027 before 31.01.2027)", i, s.DayCounts[i].Day, w)
		}
	}
	if s.DayCounts[0].Count != 2 {
		t.Errorf("DayCounts[0].Count = %d, want 2 (two posts on 15.01.2027)", s.DayCounts[0].Count)
	}
	if s.DayCounts[1].Count != 1 {
		t.Errorf("DayCounts[1].Count = %d, want 1", s.DayCounts[1].Count)
	}
	if s.DayCounts[2].Count != 1 {
		t.Errorf("DayCounts[2].Count = %d, want 1", s.DayCounts[2].Count)
	}
}

// TestBuildScheduleQueueSummary_EmptyQueue verifies an empty schedule
// produces a summary with TotalRows=0, no DayCounts, and omitted
// FirstBookedDay/BookedUntil (omitempty — an empty schedule reads as
// {"total_rows":0,...} not {"first_booked_day":"","booked_until":""}).
func TestBuildScheduleQueueSummary_EmptyQueue(t *testing.T) {
	resp := &hooppy.SchedulePostsResponse{
		PostsByDays: map[string][]hooppy.Post{},
		TotalRows:   0,
	}
	s := buildScheduleQueueSummary(resp, 55576)
	if s.TotalRows != 0 {
		t.Errorf("TotalRows = %d, want 0", s.TotalRows)
	}
	if len(s.DayCounts) != 0 {
		t.Errorf("len(DayCounts) = %d, want 0", len(s.DayCounts))
	}
	if s.FirstBookedDay != "" {
		t.Errorf("FirstBookedDay = %q, want \"\" (omitempty on empty queue)", s.FirstBookedDay)
	}
	if s.BookedUntil != "" {
		t.Errorf("BookedUntil = %q, want \"\" (omitempty on empty queue)", s.BookedUntil)
	}
}

// TestBuildScheduleQueueSummary_NilResponse verifies a nil response does
// not panic — the summary reads as an empty queue.
func TestBuildScheduleQueueSummary_NilResponse(t *testing.T) {
	s := buildScheduleQueueSummary(nil, 55576)
	if s.TotalRows != 0 {
		t.Errorf("TotalRows = %d, want 0 (nil response)", s.TotalRows)
	}
	if s.ScheduleID != 55576 {
		t.Errorf("ScheduleID = %d, want 55576 (carried even on nil response)", s.ScheduleID)
	}
}

// TestBuildScheduleQueueSummary_MalformedKeyDoesNotAbort verifies a
// malformed day key (not dd.mm.yyyy) does not abort the whole summary —
// it falls back to a raw-string sort for that key, so one bad key from the
// server never hides the rest of the calendar.
func TestBuildScheduleQueueSummary_MalformedKeyDoesNotAbort(t *testing.T) {
	resp := &hooppy.SchedulePostsResponse{
		PostsByDays: map[string][]hooppy.Post{
			"15.01.2027": {{ID: 1}},
			"not-a-date": {{ID: 2}},
			"31.01.2027": {{ID: 3}},
		},
		TotalRows: 3,
	}
	s := buildScheduleQueueSummary(resp, 55576)
	if len(s.DayCounts) != 3 {
		t.Fatalf("len(DayCounts) = %d, want 3 (a malformed key must not drop entries)", len(s.DayCounts))
	}
	// The two parseable keys sort chronologically; the malformed key
	// falls back to raw-string sort, which places "not-a-date" before
	// the numeric keys. The exact position of the malformed key is not
	// asserted (it is a fallback), only that all three survive.
	days := make(map[string]bool, 3)
	for _, dc := range s.DayCounts {
		days[dc.Day] = true
	}
	for _, want := range []string{"15.01.2027", "31.01.2027", "not-a-date"} {
		if !days[want] {
			t.Errorf("Day %q missing from DayCounts — a malformed key must not drop the other entries", want)
		}
	}
}
