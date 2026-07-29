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
// schedule_id. One call.
//
// Batch path: calls ListPosts filtered by schedule_id — ONE call — which
// returns every queued post with its slot (Post.PublicationDate, the
// {date, time, timestamp, source_timestamp} shape). The created ids are
// matched against the list rows; each match's PostPublicationDate is
// converted to the {date, hours, minutes} PublicationDate shape (hours/
// minutes from the time field, date from the timestamp formatted as
// dd.mm.yyyy in UTC — see the caveat below). Per-id GetPostEdit fallback
// only for ids the list did not return; N+1 calls against a rate-limited
// vendor is what the list path avoids.
//
// Date format caveat (batch path only): the list surface's
// PostPublicationDate.Date is a display string ("29 Июля") in the account's
// timezone, not dd.mm.yyyy. The conversion formats the timestamp as
// dd.mm.yyyy in UTC. For accounts in a positive-offset timezone this can be
// off by one day for posts near midnight local time; the single path
// (GetPostEdit) returns the correct dd.mm.yyyy date directly and is not
// affected. The hours/minutes are always correct (parsed from the time
// field, which is account-local and matches the slot).
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
		// prescribes for missing ids.
		c.fillSlotsPerID(ctx, resp, createdIDs)
		if resp.SlotLookupError != "" {
			resp.SlotLookupError = fmt.Sprintf("slot lookup: ListPosts(schedule_id=%d) failed (%v); per-id fallback attempted — %s", scheduleIDs[0], err, resp.SlotLookupError)
		} else {
			resp.SlotLookupError = fmt.Sprintf("slot lookup: ListPosts(schedule_id=%d) failed (%v); per-id fallback used", scheduleIDs[0], err)
		}
		return
	}

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
		resp.Slots = append(resp.Slots, ScheduleSlot{
			ID:              id,
			PublicationDate: postPubDateToPublicationDate(ppd),
		})
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
// dd.mm.yyyy in UTC (see the caveat in fillScheduleSlots).
func postPubDateToPublicationDate(ppd *PostPublicationDate) *PublicationDate {
	if ppd == nil {
		return nil
	}
	pd := &PublicationDate{}
	// Hours/minutes from the time field ("HH:MM").
	if parts := strings.Split(ppd.Time, ":"); len(parts) == 2 {
		pd.Hours = strings.TrimSpace(parts[0])
		pd.Minutes = strings.TrimSpace(parts[1])
	}
	// Date from the timestamp, formatted as dd.mm.yyyy in UTC.
	if ppd.Timestamp.IsSet() {
		pd.Date = time.Unix(ppd.Timestamp.Int64(), 0).UTC().Format("02.01.2006")
	} else if ppd.Date != "" {
		// No timestamp — pass the display date through as-is (format may
		// differ from dd.mm.yyyy; documented in fillScheduleSlots).
		pd.Date = ppd.Date
	}
	return pd
}
