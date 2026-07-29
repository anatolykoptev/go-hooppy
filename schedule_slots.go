package hooppy

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// fillScheduleSlots reads back the publication slot(s) the server assigned
// for a schedule-driven create (publication_when_type=3) and populates the
// slot fields on resp. It is best-effort: a lookup failure populates
// resp.SlotLookupError and returns resp unchanged (no error) — losing the
// id because a follow-up read failed is strictly worse than today's
// behaviour (the post exists; a lookup failure is a reporting problem).
//
// BOTH the single-post and the batch case use ONE mechanism: the schedule
// snapshot-diff over ListAllPostsWithTotal. A single create is a batch of
// one — there is no reason for two mechanisms, and the second one does not
// work. The caller snapshots the schedule's post ids BEFORE the create
// (beforeSnapshot, via ListAllPostsWithTotal filtered by schedule_id), and
// this method snapshots AFTER the create, then computes created = after -
// before. This is correct regardless of the list's sort order, page size,
// or how many posts the schedule already holds, and regardless of whether
// the server returned {"id": ...} (single) or {"success": true} (batch).
//
// WHY the list surface and not GET /posts/{id}/edit: immediately after a
// create, GET /posts/{id}/edit returns HTTP 403 "The post is processing and
// cannot be edited" for roughly a minute (measured ~52s on a real create)
// while the server processes attachments. The list surface returns the
// slot immediately, with no processing window — proven by the batch path
// in the same run, seconds after the same kind of create. A bounded retry
// on /edit was an order of magnitude short of the real window (6s budget
// vs ~52s) and blocking a CLI import for a minute to report a field is a
// worse product than not reporting it; the retry machinery was deleted
// rather than tuned, because the correct source does not have the problem.
//
// Count guard: if len(created) != idsSentCount, do NOT guess — a concurrent
// create by another client is the obvious way this diverges, and quietly
// attributing someone else's post to this caller's batch is a
// data-integrity bug. Populate SlotLookupError naming both counts and
// return what we have.
//
// Failure contract: any failure in this recovery leaves the create
// successful — no error returned, SlotLookupError populated, exit zero.
// The posts exist; this is reporting.
//
// Response shape: for a batch, the recovered ids are emitted in IDs
// (ordered by the queue's own publication timestamp), and ID is set to the
// first recovered id so callers reading only ID get a valid id instead of
// 0. The server does NOT send "ids" in the response — PostIDResponse.IDs
// is populated by this client from the snapshot diff, not decoded from the
// wire.
//
// Date format (batch path): the list surface's PostPublicationDate.Date is
// a display string ("29 Июля") in the account's timezone, not dd.mm.yyyy.
// The conversion formats the unix timestamp as dd.mm.yyyy at the account's
// timezone offset, fetched once per call via GET /users/settings
// (timezone_offset). This makes the batch path agree with the single path
// (GetPostEdit) instead of diverging by a day for posts near midnight
// local time. The hours/minutes are always correct (parsed from the time
// field, which is account-local and matches the slot).
//
// Offset-unavailable behaviour: if the settings lookup fails, the import
// is NOT failed (the id is still returned, exit zero). The publication
// date for recovered ids is OMITTED (Date left empty) and SlotLookupError
// records that the offset was unavailable — a stated-unknown date is
// better than a silently-wrong one. Hours/minutes are still correct (from
// the time field). The settings endpoint is called at most once per
// fillScheduleSlots call (never per recovered id).
//
// beforeSnapshot is the schedule's posts before the create (nil for the
// single-post path). beforeErr is non-nil if the before snapshot failed
// (the batch path cannot diff → SlotLookupError, no ids recovered).
// idsSentCount is the number of ids sent in the batch (0 for single).
// scheduleIDs is the payload's SchedulesIDs — the first element is used as
// the list filter (a batch targets one schedule).
func (c *Client) fillScheduleSlots(ctx context.Context, resp *PostIDResponse, whenType int, scheduleIDs []int, beforeSnapshot []Post, beforeErr error, idsSentCount int) {
	if whenType != 3 || resp == nil {
		return
	}

	// ScheduleID for the response: the first schedule from the payload (a
	// batch targets one schedule).
	if len(scheduleIDs) > 0 {
		resp.ScheduleID = scheduleIDs[0]
	}

	// One path for single and batch: recover the created ids via
	// snapshot-diff. See the fillScheduleSlots doc comment for WHY the
	// list surface is used instead of GET /posts/{id}/edit (403 processing
	// window ~1 min on /edit; list returns the slot immediately).
	c.fillBatchSlotsBySnapshotDiff(ctx, resp, scheduleIDs, beforeSnapshot, beforeErr, idsSentCount)
}

// fillBatchSlotsBySnapshotDiff recovers the created post ids by diffing
// the schedule's post list after the create against the before snapshot
// taken by the caller. Used for BOTH single and batch creates (a single is
// a batch of one). See fillScheduleSlots for the full design rationale.
func (c *Client) fillBatchSlotsBySnapshotDiff(ctx context.Context, resp *PostIDResponse, scheduleIDs []int, before []Post, beforeErr error, idsSentCount int) {
	if len(scheduleIDs) == 0 {
		resp.SlotLookupError = "slot lookup: batch path requires at least one schedule ID to filter the list"
		return
	}
	if beforeErr != nil {
		resp.SlotLookupError = fmt.Sprintf("slot lookup: before-snapshot ListPosts(schedule_id=%d) failed (%v) — cannot diff to recover created ids", scheduleIDs[0], beforeErr)
		return
	}

	// After snapshot: the schedule's posts after the create. Walk ALL
	// pages — a schedule can hold more than one page of posts (the
	// default page size is 20), and a single-page ListPosts would miss
	// created posts beyond the first page, breaking the diff. The before
	// snapshot (taken by the caller) must also walk all pages for the
	// same reason — see the caller in posts_search.go.
	afterList, _, err := c.ListAllPostsWithTotal(ctx, ListPostsFilter{ScheduleID: scheduleIDs[0]})
	if err != nil {
		resp.SlotLookupError = fmt.Sprintf("slot lookup: after-snapshot ListAllPostsWithTotal(schedule_id=%d) failed (%v) — created ids not recovered (posts exist)", scheduleIDs[0], err)
		return
	}

	// Diff: created = after - before.
	beforeSet := make(map[int]struct{}, len(before))
	for i := range before {
		beforeSet[before[i].ID] = struct{}{}
	}
	var created []Post
	for i := range afterList {
		if _, ok := beforeSet[afterList[i].ID]; !ok {
			created = append(created, afterList[i])
		}
	}

	// Count guard: if the diff recovered a different number of ids than
	// were sent, do NOT guess. A concurrent create by another client is
	// the obvious way this diverges; quietly attributing someone else's
	// post to this caller's batch is a data-integrity bug.
	if len(created) != idsSentCount {
		for i := range created {
			resp.IDs = append(resp.IDs, created[i].ID)
		}
		if len(resp.IDs) > 0 {
			resp.ID = resp.IDs[0]
		}
		resp.SlotLookupError = fmt.Sprintf("slot lookup: snapshot-diff recovered %d created ids, but %d were sent — counts differ (concurrent create suspected); returning recovered ids without slot attribution", len(created), idsSentCount)
		return
	}

	// Order created by publication timestamp (ascending — the queue's own
	// publication order).
	sortPostsByTimestamp(created)

	// Fetch the account timezone offset ONCE for this batch — the
	// list-surface date is a display string, so the batch path formats the
	// timestamp as dd.mm.yyyy at the account's offset (not UTC). A failed
	// lookup does not fail the import; see the offset-unavailable behaviour
	// in the fillScheduleSlots doc comment.
	offset, offsetErr := c.fetchTimezoneOffset(ctx)

	for i := range created {
		ppd := created[i].PublicationDate
		if ppd == nil {
			resp.IDs = append(resp.IDs, created[i].ID)
			resp.Slots = append(resp.Slots, ScheduleSlot{ID: created[i].ID})
			continue
		}
		pd, malformedTime := postPubDateToPublicationDate(ppd, offset)
		if offsetErr != nil {
			// Offset unavailable — omit the date rather than guessing UTC
			// (a silently-wrong date is worse than a stated-unknown one).
			// Hours/minutes are still correct (from the time field).
			pd.Date = ""
		}
		if malformedTime != "" {
			// Malformed time field — record it in SlotLookupError so the
			// caller knows the slot's hours/minutes are missing (a slot
			// with no time is a silent failure).
			if resp.SlotLookupError != "" {
				resp.SlotLookupError += "; "
			}
			resp.SlotLookupError += fmt.Sprintf("slot lookup: post %d has malformed time field %q (expected HH:MM) — hours/minutes omitted", created[i].ID, malformedTime)
		}
		resp.IDs = append(resp.IDs, created[i].ID)
		resp.Slots = append(resp.Slots, ScheduleSlot{
			ID:              created[i].ID,
			PublicationDate: pd,
		})
	}

	// ID = first recovered id (ordered by timestamp) so callers reading
	// only ID get a valid id instead of 0.
	if len(resp.IDs) > 0 {
		resp.ID = resp.IDs[0]
	}

	// Populate the primary PublicationDate from the first slot (the flat
	// field mirrors the single-path shape for callers who only read ID).
	if len(resp.Slots) > 0 {
		resp.PublicationDate = resp.Slots[0].PublicationDate
	}

	if offsetErr != nil {
		resp.SlotLookupError = fmt.Sprintf("slot lookup: account timezone offset unavailable (%v) — publication dates for recovered ids omitted, hours/minutes still correct", offsetErr)
	}
}

// sortPostsByTimestamp sorts posts by their PublicationDate.Timestamp
// ascending (the queue's own publication order). Posts without a timestamp
// sort after those with one.
func sortPostsByTimestamp(posts []Post) {
	for i := 1; i < len(posts); i++ {
		for j := i; j > 0; j-- {
			if postTimestamp(&posts[j]) < postTimestamp(&posts[j-1]) {
				posts[j], posts[j-1] = posts[j-1], posts[j]
			} else {
				break
			}
		}
	}
}

// postTimestamp returns the publication timestamp of a post, or
// math.MaxInt64 if absent (sorts after all real timestamps).
func postTimestamp(p *Post) int64 {
	if p.PublicationDate == nil || !p.PublicationDate.Timestamp.IsSet() {
		return 1 << 62
	}
	return p.PublicationDate.Timestamp.Int64()
}

// postPubDateToPublicationDate converts the list-surface PostPublicationDate
// ({date, time, timestamp, source_timestamp}) to the {date, hours, minutes}
// PublicationDate shape. Hours/minutes are parsed from the time field
// ("14:25" → "14"/"25"); the date is formatted from the timestamp as
// dd.mm.yyyy at the account's timezone offset (not UTC — see
// fillScheduleSlots). A zero offset formats in UTC.
//
// Returns (pd, malformedTime) where malformedTime is the original time
// string if it could not be parsed (missing colon, wrong number of parts,
// or empty when a time was expected). The caller populates slot_lookup_error
// with the malformed value — a slot with no time is a silent failure.
func postPubDateToPublicationDate(ppd *PostPublicationDate, offset int) (*PublicationDate, string) {
	if ppd == nil {
		return nil, ""
	}
	pd := &PublicationDate{}
	// Hours/minutes from the time field ("HH:MM"). The list surface
	// zero-pads (e.g. "09:20"); a malformed time (missing colon, wrong
	// number of parts) leaves Hours/Minutes empty and returns the
	// malformed value so the caller can report it.
	var malformed string
	if ppd.Time != "" {
		parts := strings.Split(ppd.Time, ":")
		if len(parts) == 2 {
			pd.Hours = strings.TrimSpace(parts[0])
			pd.Minutes = strings.TrimSpace(parts[1])
		} else {
			malformed = ppd.Time
		}
	}
	// Date from the timestamp, formatted as dd.mm.yyyy at the account's
	// timezone offset. The offset is in hours (GET /users/settings returns
	// timezone_offset as an integer hour value, e.g. 3 for UTC+3).
	if ppd.Timestamp.IsSet() {
		t := time.Unix(ppd.Timestamp.Int64(), 0).UTC()
		if offset != 0 {
			t = t.Add(time.Duration(offset) * time.Hour)
		}
		pd.Date = t.Format("02.01.2006")
	} else if ppd.Date != "" {
		// No timestamp — pass the display date through as-is (format may
		// differ from dd.mm.yyyy; documented in fillScheduleSlots).
		pd.Date = ppd.Date
	}
	return pd, malformed
}

// fetchTimezoneOffset returns the account's timezone offset (in hours) via
// GET /users/settings. Called at most once per fillScheduleSlots call on the
// batch path. A failure returns (0, err) — the caller omits dates rather
// than guessing UTC.
func (c *Client) fetchTimezoneOffset(ctx context.Context) (int, error) {
	settings, err := c.GetSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("GetSettings: %w", err)
	}
	return settings.TimezoneOffset, nil
}
