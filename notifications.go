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
	// Reject negatives before any request: the old `> 0` guard let a
	// negative take neither branch — no error, no page parameter, the
	// server returns page 1, and a caller's paging loop silently re-reads
	// the first page. Same defect class the sweep closed across the
	// search/posts/accounts/pages filters (see posts_search.go). Zero
	// stays the unset sentinel.
	if page < 0 {
		return nil, fmt.Errorf("hooppy: ListNotifications: page must be non-negative (got %d); pass 0 to leave unset", page)
	}
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
// server's total_rows, so it cannot detect such duplicates. To detect a
// TRUNCATED walk (rows the server initially reported but never served), use
// ListAllNotificationsWithFirstAndLastTotal and flag when the unique-id
// count is LESS than the first-page total_rows (see RunDoctor for the rule
// and the gaps it does not close). Do NOT use NewAllListEnvelope here: its
// equality check (unique == total) is right for low-churn collections but
// wrong for /notifications, a high-churn log where a mid-walk insert makes
// unique != lastTotal on every active account — the equality check would
// false-alarm on healthy accounts. See NewAllListEnvelope for the per
// call-site table of which collections that check suits.
//
// The walk is bounded by maxListAllPages; if the server never clears
// is_has_more within that bound, ListAllNotifications returns an error
// instead of looping forever or silently truncating.
func (c *Client) ListAllNotifications(ctx context.Context) ([]Notification, error) {
	all, _, err := c.ListAllNotificationsWithTotal(ctx)
	return all, err
}

// ListAllNotificationsWithTotal is ListAllNotifications but also returns the
// server's last-seen total_rows. It exists for symmetry with the other
// ListAll*WithTotal entry points; for /notifications specifically, passing
// (list, totalRows) to NewAllListEnvelope is NOT suitable. Its equality
// check (unique == total) false-alarms on every active account: a mid-walk
// insert makes the last-seen total_rows differ from the unique-id count.
// For /notifications use ListAllNotificationsWithFirstAndLastTotal and the
// unique < firstTotal rule instead (see RunDoctor). See NewAllListEnvelope
// for the per call-site table of which collections the equality check does
// suit.
func (c *Client) ListAllNotificationsWithTotal(ctx context.Context) ([]Notification, int, error) {
	all, _, last, err := c.ListAllNotificationsWithFirstAndLastTotal(ctx)
	return all, last, err
}

// ListAllNotificationsWithFirstAndLastTotal is ListAllNotifications but also
// returns the server's total_rows from the FIRST page and the LAST page.
// The triple (list, firstTotalRows, lastTotalRows) lets a caller
// distinguish a truncated walk (unique count < firstTotalRows) from a
// benign mid-walk insert (lastTotalRows > firstTotalRows) — the distinction
// NewAllListEnvelope cannot make because it receives only one total.
// doctor uses this to avoid false-alarms on the high-churn /notifications
// log. The low-churn NewAllListEnvelope callers walk projects and schedules
// only (low-churn collections where a mid-walk shift is rare); the posts
// caller is high-churn and is NOT covered by the first-total rule — see
// NewAllListEnvelope for the per call-site table and the open follow-up
// (#70).
func (c *Client) ListAllNotificationsWithFirstAndLastTotal(ctx context.Context) ([]Notification, int, int, error) {
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
