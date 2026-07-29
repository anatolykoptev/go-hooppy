package hooppy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListNotifications returns the user's notifications via GET /notifications.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
func (c *Client) ListNotifications(ctx context.Context, page int) (*NotificationsResponse, error) {
	params := url.Values{}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp NotificationsResponse
	if err := c.doGET(ctx, pathNotifications, params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListAllNotifications walks GET /notifications from page 1, accumulating
// notifications until is_has_more is false. The walk starts at page 1 so
// the first page is not fetched twice (the Hooppy API is 1-indexed and a
// request with no page param is byte-identical to ?page=1). See
// projects.ListAllSchedules for the 1-indexed rationale and the sanity cap.
//
// Duplicates arising from a mid-walk collection shift are NOT removed: with
// offset pagination, a row inserted or deleted mid-walk shifts the window
// and the server re-serves a row already seen. This entry point drops the
// server's total_rows, so it cannot detect such duplicates. Use
// ListAllNotificationsWithTotal with NewAllListEnvelope to detect them (see
// NewAllListEnvelope for what it does and does not catch).
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllNotifications returns an error
// instead of looping forever or silently truncating.
func (c *Client) ListAllNotifications(ctx context.Context) ([]Notification, error) {
	all, _, err := c.ListAllNotificationsWithTotal(ctx)
	return all, err
}

// ListAllNotificationsWithTotal is ListAllNotifications but also returns the
// server's last-seen total_rows. The pair (list, totalRows) is meant to be
// passed to NewAllListEnvelope. See projects.ListAllSchedulesWithTotal and
// NewAllListEnvelope for what the envelope catches and what it does not.
func (c *Client) ListAllNotificationsWithTotal(ctx context.Context) ([]Notification, int, error) {
	all, _, last, err := c.ListAllNotificationsWithTotals(ctx)
	return all, last, err
}

// ListAllNotificationsWithTotals is ListAllNotifications but also returns the
// server's total_rows from the FIRST page and the LAST page. The triple
// (list, firstTotalRows, lastTotalRows) lets a caller distinguish a
// truncated walk (unique count < firstTotalRows) from a benign mid-walk
// insert (lastTotalRows > firstTotalRows) — the distinction
// NewAllListEnvelope cannot make because it receives only one total.
// doctor uses this to avoid false-alarms on the high-churn /notifications
// log; the static-collection callers (projects, schedules, etc.) do not
// need it and continue to use ListAllNotificationsWithTotal +
// NewAllListEnvelope.
func (c *Client) ListAllNotificationsWithTotals(ctx context.Context) ([]Notification, int, int, error) {
	all := make([]Notification, 0)
	var firstTotalRows, lastTotalRows int
	for page := 1; ; page++ {
		if page > maxListAllPages {
			return nil, 0, 0, fmt.Errorf("hooppy: ListAllNotifications exceeded %d pages without is_has_more going false — aborting to avoid an unbounded walk", maxListAllPages)
		}
		resp, err := c.ListNotifications(ctx, page)
		if err != nil {
			return nil, 0, 0, err
		}
		if page == 1 {
			firstTotalRows = resp.TotalRows
		}
		all = append(all, resp.List...)
		lastTotalRows = resp.TotalRows
		if !resp.IsHasMore {
			return all, firstTotalRows, lastTotalRows, nil
		}
	}
}
