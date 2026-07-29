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
// Single-id path: calls GetPostEdit(id), which returns the slot
// structurally as *PublicationDate ({date, hours, minutes}) plus
// schedule_id. One call. The date is returned directly by the server in
// dd.mm.yyyy format — no timezone conversion is needed.
//
// Batch path: calls ListPosts filtered by schedule_id — ONE call — which
// returns every queued post with its slot (Post.PublicationDate, the
// {date, time, timestamp, source_timestamp} shape). The created ids are
// matched against the list rows; each match's PostPublicationDate is
// converted to the {date, hours, minutes} PublicationDate shape (hours/
// minutes from the time field, date from the timestamp formatted as
// dd.mm.yyyy at the account's timezone offset — see below). Per-id
// GetPostEdit fallback only for ids the list did not return; N+1 calls
// against a rate-limited vendor is what the list path avoids.
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
// date for list-matched ids is OMITTED (Date left empty) and
// SlotLookupError records that the offset was unavailable — a
// stated-unknown date is better than a silently-wrong one. Per-id fallback
// slots (from GetPostEdit) are unaffected: they carry the server-provided
// date directly. The settings endpoint is called at most once per
// fillScheduleSlots call (never per matched id).
//
// scheduleIDs is the payload's SchedulesIDs — the schedule the post was
// created into. The first element is used as the list filter (a batch
// targets one schedule; the server assigns slots from that schedule's
// times).
func (c *Client) fillScheduleSlots(ctx context.Context, resp *PostIDResponse, whenType int, scheduleIDs []int) {
	if whenType != 3 || resp == nil || resp.ID == 0 {
		return
	}

	// Resolve the created ids: the server returns "ids" alongside "id" for
	// a batch; fall back to [ID] for a single-post create or when the server
	// did not return ids.
	createdIDs := resp.IDs
	if len(createdIDs) == 0 {
		createdIDs = []int{resp.ID}
	}

	// ScheduleID for the response: the first schedule from the payload (a
	// batch targets one schedule).
	if len(scheduleIDs) > 0 {
		resp.ScheduleID = scheduleIDs[0]
	}

	// Single-id path: one GetPostEdit call.
	if len(createdIDs) == 1 {
		edit, err := c.GetPostEdit(ctx, createdIDs[0])
		if err != nil {
			resp.SlotLookupError = fmt.Sprintf("slot lookup: GetPostEdit(%d): %v", createdIDs[0], err)
			return
		}
		resp.PublicationDate = edit.PublicationDate
		if edit.ScheduleID != 0 {
			resp.ScheduleID = edit.ScheduleID
		}
		return
	}

	// Batch path: ONE ListPosts call filtered by schedule_id, then match.
	// Per-id GetPostEdit fallback only for ids the list did not return.
	if len(scheduleIDs) == 0 {
		resp.SlotLookupError = "slot lookup: batch path requires at least one schedule ID to filter the list"
		return
	}

	listResp, err := c.ListPosts(ctx, ListPostsFilter{ScheduleID: scheduleIDs[0]})
	if err != nil {
		// List failed — fall back to per-id GetPostEdit for all ids rather
		// than giving up entirely. A total list failure does not mean the
		// posts do not exist; the per-id path is the fallback the task
		// prescribes for missing ids. The per-id path returns the date
		// directly from the server, so no timezone offset is needed here.
		c.fillSlotsPerID(ctx, resp, createdIDs)
		if resp.SlotLookupError != "" {
			resp.SlotLookupError = fmt.Sprintf("slot lookup: ListPosts(schedule_id=%d) failed (%v); per-id fallback attempted — %s", scheduleIDs[0], err, resp.SlotLookupError)
		} else {
			resp.SlotLookupError = fmt.Sprintf("slot lookup: ListPosts(schedule_id=%d) failed (%v); per-id fallback used", scheduleIDs[0], err)
		}
		return
	}

	// Fetch the account timezone offset ONCE for this batch — the
	// list-surface date is a display string, so the batch path formats the
	// timestamp as dd.mm.yyyy at the account's offset (not UTC). A failed
	// lookup does not fail the import; see the offset-unavailable behaviour
	// in the doc comment.
	offset, offsetErr := c.fetchTimezoneOffset(ctx)

	// Build id → PostPublicationDate map from the list.
	slotsByPostID := make(map[int]*PostPublicationDate, len(listResp.List))
	for i := range listResp.List {
		p := &listResp.List[i]
		if p.PublicationDate != nil {
			slotsByPostID[p.ID] = p.PublicationDate
		}
	}

	// Match created ids against the list.
	var missing []int
	for _, id := range createdIDs {
		ppd, ok := slotsByPostID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		pd := postPubDateToPublicationDate(ppd, offset)
		if offsetErr != nil {
			// Offset unavailable — omit the date rather than guessing UTC
			// (a silently-wrong date is worse than a stated-unknown one).
			// Hours/minutes are still correct (from the time field).
			pd.Date = ""
		}
		resp.Slots = append(resp.Slots, ScheduleSlot{
			ID:              id,
			PublicationDate: pd,
		})
	}

	if offsetErr != nil {
		if resp.SlotLookupError != "" {
			resp.SlotLookupError += "; "
		}
		resp.SlotLookupError += fmt.Sprintf("slot lookup: account timezone offset unavailable (%v) — publication dates for list-matched ids omitted, hours/minutes still correct", offsetErr)
	}

	// Per-id fallback for unmatched ids.
	if len(missing) > 0 {
		before := len(resp.Slots)
		c.fillSlotsPerID(ctx, resp, missing)
		filled := len(resp.Slots) - before
		if filled < len(missing) {
			if resp.SlotLookupError == "" {
				resp.SlotLookupError = fmt.Sprintf("slot lookup: %d of %d batch ids not found in list or per-id fallback (schedule_id=%d)", len(missing)-filled, len(createdIDs), scheduleIDs[0])
			}
		}
	}

	// Populate the primary PublicationDate from the first slot (the flat
	// field mirrors the single-path shape for callers who only read ID).
	if len(resp.Slots) > 0 {
		resp.PublicationDate = resp.Slots[0].PublicationDate
	}
}

// fillSlotsPerID calls GetPostEdit for each id and appends a ScheduleSlot
// for each that succeeds. Failures are accumulated into SlotLookupError.
func (c *Client) fillSlotsPerID(ctx context.Context, resp *PostIDResponse, ids []int) {
	var errs []string
	for _, id := range ids {
		edit, err := c.GetPostEdit(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Sprintf("GetPostEdit(%d): %v", id, err))
			continue
		}
		resp.Slots = append(resp.Slots, ScheduleSlot{
			ID:              id,
			PublicationDate: edit.PublicationDate,
		})
	}
	if len(errs) > 0 {
		if resp.SlotLookupError != "" {
			resp.SlotLookupError += "; "
		}
		resp.SlotLookupError += "per-id fallback errors: " + strings.Join(errs, "; ")
	}
}

// postPubDateToPublicationDate converts the list-surface PostPublicationDate
// ({date, time, timestamp, source_timestamp}) to the {date, hours, minutes}
// PublicationDate shape. Hours/minutes are parsed from the time field
// ("14:25" → "14"/"25"); the date is formatted from the timestamp as
// dd.mm.yyyy at the account's timezone offset (not UTC — see
// fillScheduleSlots). A zero offset formats in UTC.
func postPubDateToPublicationDate(ppd *PostPublicationDate, offset int) *PublicationDate {
	if ppd == nil {
		return nil
	}
	pd := &PublicationDate{}
	// Hours/minutes from the time field ("HH:MM").
	if parts := strings.Split(ppd.Time, ":"); len(parts) == 2 {
		pd.Hours = strings.TrimSpace(parts[0])
		pd.Minutes = strings.TrimSpace(parts[1])
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
	return pd
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
