package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// schedulePostsBody is a GET /posts/schedules/{id}/posts fixture: a
// schedule with 4 posts across 3 days. The keys are dd.mm.yyyy; the
// fixture is deliberately ordered so a raw-string sort would mis-order
// "01.02.2027" before "31.01.2027" — the chronological-sort guard below
// catches that bug.
const schedulePostsBody = `{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":101,"text":"a"},{"id":102,"text":"b"}]},"31.01.2027":{"day_name":"Вс","day_date":"31 Января","posts":[{"id":103,"text":"c"}]},"01.02.2027":{"day_name":"Пн","day_date":"1 Февраля","posts":[{"id":104,"text":"d"}]}},"total_rows":4,"rows_limit":1000,"is_has_more":false}`

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
	s := buildScheduleQueueSummary(resp, 55576, "", "", 0)
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
	s := buildScheduleQueueSummary(resp, 55576, "", "", 0)
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
		PostsByDays: map[string]hooppy.ScheduleDay{},
		TotalRows:   0,
	}
	s := buildScheduleQueueSummary(resp, 55576, "", "", 0)
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
	s := buildScheduleQueueSummary(nil, 55576, "", "", 0)
	if s.TotalRows != 0 {
		t.Errorf("TotalRows = %d, want 0 (nil response)", s.TotalRows)
	}
	if s.ScheduleID != 55576 {
		t.Errorf("ScheduleID = %d, want 55576 (carried even on nil response)", s.ScheduleID)
	}
}

// TestBuildScheduleQueueSummary_NarrowedQueryOmitsScheduleWideFields is the
// MAJOR-1 guard (review round 4): a --from/--to (or --page) query is a
// SUBSET by construction, so the server correctly answers is_has_more:false
// and the truncation guard (keyed on IsHasMore alone) never fires. The
// window's first/last day keys are the WINDOW's bounds, NOT the schedule's
// first_booked_day/booked_until. Emitting them under the schedule-wide
// field names prints a schedule-wide fact that is silently wrong —
// reachable with the very --from/--to flags the truncation warning points
// an operator to. A narrowed query MUST NOT emit first_booked_day or
// booked_until; day_counts (accurate for the window) carries the detail.
//
// RED-on-revert: drop the narrowed guard in buildScheduleQueueSummary and
// BookedUntil/FirstBookedDay are populated from the window's day keys —
// the assertions fail.
// TestBuildScheduleQueueSummary_PageOneIsNotNarrowed pins the fix for a guard
// keyed on "was a flag set" instead of "is the response a subset". `--page 1` is
// byte-identical to no page at all — the server's default IS page one — so the
// response is the same complete calendar and the schedule-wide fields are
// legitimate. Suppressing them for `page=1` starved the natural loop bound: a
// script walking pages from 1 would never see first_booked_day/booked_until even
// on the complete first page.
//
// `page > 1` is the correct condition. Truncation is still handled: page=1 with
// is_has_more:true hits the separate IsHasMore branch, covered below.
func TestBuildScheduleQueueSummary_PageOneIsNotNarrowed(t *testing.T) {
	resp := &hooppy.SchedulePostsResponse{
		PostsByDays: map[string]hooppy.ScheduleDay{
			"31.07.2026": {Posts: []hooppy.Post{{ID: 1}}},
			"12.01.2027": {Posts: []hooppy.Post{{ID: 2}}},
		},
		TotalRows: 96,
		IsHasMore: false,
	}
	noPage := buildScheduleQueueSummary(resp, 55576, "", "", 0)
	pageOne := buildScheduleQueueSummary(resp, 55576, "", "", 1)

	if pageOne.FirstBookedDay != noPage.FirstBookedDay || pageOne.BookedUntil != noPage.BookedUntil {
		t.Errorf("page=1 summary (%q…%q) differs from no-page (%q…%q) — page 1 IS the server default, so the response is the same complete calendar and both fields are legitimate",
			pageOne.FirstBookedDay, pageOne.BookedUntil, noPage.FirstBookedDay, noPage.BookedUntil)
	}
	if pageOne.BookedUntil != "12.01.2027" {
		t.Errorf("page=1 BookedUntil = %q, want \"12.01.2027\" — suppressing it treats a complete first page as a subset", pageOne.BookedUntil)
	}
	// page=2 remains a subset and must still suppress.
	pageTwo := buildScheduleQueueSummary(resp, 55576, "", "", 2)
	if pageTwo.BookedUntil != "" {
		t.Errorf("page=2 BookedUntil = %q, want \"\" — page 2 IS a subset; relaxing the guard to page>1 must not relax it past 1", pageTwo.BookedUntil)
	}
	// A truncated page one still exits through the IsHasMore path: booked_until
	// suppressed, first_booked_day kept (page one starts at the calendar's
	// beginning, so its FIRST key is genuinely the schedule's first booked day).
	trunc := &hooppy.SchedulePostsResponse{
		PostsByDays: map[string]hooppy.ScheduleDay{"31.07.2026": {Posts: []hooppy.Post{{ID: 1}}}},
		TotalRows:   500, RowsLimit: 200, IsHasMore: true,
	}
	tp := buildScheduleQueueSummary(trunc, 55576, "", "", 1)
	if tp.BookedUntil != "" {
		t.Errorf("truncated page=1 BookedUntil = %q, want \"\" — the last key of a truncated read is the last day of page one, not the schedule's end", tp.BookedUntil)
	}
	if tp.FirstBookedDay != "31.07.2026" {
		t.Errorf("truncated page=1 FirstBookedDay = %q, want \"31.07.2026\" — page one begins at the calendar's start, so its first key IS the schedule's first booked day; suppressing it loses a correct answer", tp.FirstBookedDay)
	}
}

// TestScheduleQueueSummary_DayCountsMarshalsAsEmptyArray asserts on the
// MARSHALLED BYTES, not the struct. A nil DayCounts serialises as `null` while a
// populated one serialises as `[]…`, so the output shape changed between the
// empty and non-empty branches — and a struct-level assertion cannot see that,
// which is why the inconsistency survived a round that set out to fix it.
func TestScheduleQueueSummary_DayCountsMarshalsAsEmptyArray(t *testing.T) {
	cases := []struct {
		name string
		resp *hooppy.SchedulePostsResponse
	}{
		{"nil response", nil},
		{"empty calendar", &hooppy.SchedulePostsResponse{PostsByDays: map[string]hooppy.ScheduleDay{}, TotalRows: 0}},
		{"page past the end", &hooppy.SchedulePostsResponse{PostsByDays: map[string]hooppy.ScheduleDay{}, TotalRows: 96}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(buildScheduleQueueSummary(tc.resp, 55576, "", "", 0))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got := string(b)
			if !strings.Contains(got, `"day_counts":[]`) {
				t.Errorf("marshalled summary lacks `\"day_counts\":[]` — a nil slice ships as `null` and the output shape then differs between the empty and non-empty branches: %s", got)
			}
			if strings.Contains(got, `"day_counts":null`) {
				t.Errorf("marshalled summary carries `\"day_counts\":null`: %s", got)
			}
		})
	}
}

func TestBuildScheduleQueueSummary_NarrowedQueryOmitsScheduleWideFields(t *testing.T) {
	// A windowed response: only September shown, is_has_more:false (the
	// server answers complete for the window). The real schedule is booked
	// to 12.01.2027 — but the window's last key is 29.09.2026, which MUST
	// NOT be emitted as booked_until.
	resp := &hooppy.SchedulePostsResponse{
		PostsByDays: map[string]hooppy.ScheduleDay{
			"01.09.2026": {Posts: []hooppy.Post{{ID: 1}}},
			"15.09.2026": {Posts: []hooppy.Post{{ID: 2}}},
			"29.09.2026": {Posts: []hooppy.Post{{ID: 3}}},
		},
		TotalRows: 96, // the COLLECTION total — unchanged by narrowing
		IsHasMore: false,
	}
	// Narrowed by date_from/date_to.
	s := buildScheduleQueueSummary(resp, 55576, "01.09.2026", "30.09.2026", 0)
	if s.BookedUntil != "" {
		t.Errorf("BookedUntil = %q, want \"\" — a narrowed query's last day key (29.09.2026) is the WINDOW's last day, not the schedule's booked-until (12.01.2027); emitting it as booked_until is the silent-wrong-answer defect", s.BookedUntil)
	}
	if s.FirstBookedDay != "" {
		t.Errorf("FirstBookedDay = %q, want \"\" — a narrowed query's first day key is the WINDOW's start, not the schedule's first booked day", s.FirstBookedDay)
	}
	// day_counts MUST still carry the window's days (the accurate detail).
	if len(s.DayCounts) != 3 {
		t.Errorf("len(DayCounts) = %d, want 3 — the window's per-day counts must still appear", len(s.DayCounts))
	}
	// Narrowed by page alone (page=2 past page one) — same invariant.
	s2 := buildScheduleQueueSummary(resp, 55576, "", "", 2)
	if s2.BookedUntil != "" {
		t.Errorf("page-narrowed BookedUntil = %q, want \"\" — a --page query is also a subset; its last day key is not the schedule's booked-until", s2.BookedUntil)
	}
	if s2.FirstBookedDay != "" {
		t.Errorf("page-narrowed FirstBookedDay = %q, want \"\" — a --page query's first day key is not the schedule's first booked day", s2.FirstBookedDay)
	}
}

// TestBuildScheduleQueueSummary_MalformedKeyDoesNotAbort verifies a
// malformed day key (not dd.mm.yyyy) does not abort the whole summary —
// it falls back to a raw-string sort for that key, so one bad key from the
// server never hides the rest of the calendar.
func TestBuildScheduleQueueSummary_MalformedKeyDoesNotAbort(t *testing.T) {
	resp := &hooppy.SchedulePostsResponse{
		PostsByDays: map[string]hooppy.ScheduleDay{
			"15.01.2027": {Posts: []hooppy.Post{{ID: 1}}},
			"not-a-date": {Posts: []hooppy.Post{{ID: 2}}},
			"31.01.2027": {Posts: []hooppy.Post{{ID: 3}}},
		},
		TotalRows: 3,
	}
	s := buildScheduleQueueSummary(resp, 55576, "", "", 0)
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
