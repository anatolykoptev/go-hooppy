package hooppy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Doctor classification buckets. The three known classes need different
// responses: expired_credential requires a human to reconnect the account
// (the tool cannot fix it), missing_media is a targeting mistake in a
// schedule (user-fixable), and upstream_error is a transient 5xx from the
// social network (must not carry the weight of the first two). Anything
// unmatched falls into unknown with the raw vendor string preserved.
const (
	classExpiredCredential = "expired_credential"
	classMissingMedia      = "missing_media"
	classUpstreamError     = "upstream_error"
	classUnknown           = "unknown"
)

// errorClassTable maps stable substrings of the vendor's (Hooppy.ru) error
// messages to classification buckets. The strings below are the vendor's
// own Russian and English messages, matched on stable substrings — they may
// change without notice. Adding a new vendor message is a one-line addition
// here. Match order matters: the first matching class wins, so more
// specific classes must precede less specific ones.
//
// MEASURED from the live notification log (not the OpenAPI spec, which does
// not document these strings). The Russian credential message varies in the
// middle by network name ("аккаунта Одноклассники", "аккаунта Twitter",
// "канала Дзен") while "Устарел ключ доступа" and "Обновите подключение"
// are invariant — the needles key on those invariant fragments, never on
// the network name, so a newly-connected network cannot reopen this bug.
// The two English credential messages are structurally unrelated to each
// other and get their own needles. The vendor appends a call-site in
// parentheses ("(getAccessToken)", "(uploadPhoto)", "(storeAlbum)") — the
// needles deliberately exclude it, so it never participates in matching.
var errorClassTable = []struct {
	class   string
	needles []string
}{
	{
		class: classExpiredCredential,
		needles: []string{
			"устарел ключ доступа",               // RU invariant: "access key is outdated"
			"обновите подключение",               // RU invariant: "update the connection"
			"missing valid authorization header", // EN: Facebook/Instagram auth header
			"error validating access token",      // EN: Facebook session invalidated
		},
	},
	{
		class: classMissingMedia,
		needles: []string{
			"нет контента для публикации", // RU: "no content to publish"
		},
	},
	{
		class: classUpstreamError,
		needles: []string{
			"internal server error",
			"bad gateway",
			"service unavailable",
			"gateway timeout",
			"server error",
			"внутренняя ошибка", // RU: "internal error"
			"ошибка 500",        // RU: "error 500"
			"ошибка 502",        // RU: "error 502"
			"ошибка 503",        // RU: "error 503"
			"ошибка 504",        // RU: "error 504"
			"503 service unavailable",
		},
	},
}

// classifyError maps a vendor error string to a classification bucket.
// It performs case-insensitive substring matching against errorClassTable.
// An unmatched string returns classUnknown — it is never forced into a
// known bucket; the raw string is preserved verbatim in the report group.
func classifyError(data string) string {
	lower := strings.ToLower(data)
	for _, entry := range errorClassTable {
		for _, needle := range entry.needles {
			if strings.Contains(lower, strings.ToLower(needle)) {
				return entry.class
			}
		}
	}
	return classUnknown
}

// operationDateLayout is the vendor's operation_date format:
// дд.мм.гггг, чч:мм (e.g. "15.06.2026, 09:00").
const operationDateLayout = "02.01.2006, 15:04"

// parseOperationDate parses the vendor's operation_date string. Returns the
// parsed time and a nil error on success, or a zero time and an error when
// the string does not match the expected format. Callers MUST report
// unparseable rows rather than silently dropping them — dropping a row
// hides exactly the failure doctor exists to surface.
//
// TIMEZONE ASSUMPTION: the vendor renders operation_date in the ACCOUNT's
// timezone (a user setting on hooppy.ru), but this function parses it in
// time.Local — the timezone of the host running `doctor`. The --since
// window comparison in RunDoctor also uses a local time.Now(). If the
// account's timezone differs from the host's, the window boundary can be
// off by the offset between them: a row the account considers "inside the
// last 7 days" may be excluded (or included) by up to that offset. There is
// no conversion here because the account timezone is not exposed by the
// API; the host timezone is the only one available. See the --since flag
// help text for the user-facing statement of this assumption.
func parseOperationDate(s string) (time.Time, error) {
	return time.ParseInLocation(operationDateLayout, s, time.Local)
}

// DoctorGroup is one (page, error message) cluster in the doctor report.
type DoctorGroup struct {
	PageID         int       `json:"page_id"`
	PageName       string    `json:"page_name"`
	SourceID       int       `json:"source_id"`
	Network        string    `json:"network"`
	ErrorText      string    `json:"error_text"`
	Classification string    `json:"classification"`
	Count          int       `json:"count"`
	FirstDate      time.Time `json:"first_date"`
	LastDate       time.Time `json:"last_date"`
}

// DoctorUnparseable is a notification row whose operation_date could not be
// parsed. It is reported (never silently dropped) so the operator can see
// that a failure exists even when the vendor's date format changes.
type DoctorUnparseable struct {
	NotificationID int    `json:"notification_id"`
	PageID         int    `json:"page_id"`
	SourceID       int    `json:"source_id"`
	OperationDate  string `json:"operation_date"`
	ErrorText      string `json:"error_text"`
}

// DoctorReport is the output of RunDoctor: the grouped publication failures
// inside the --since window, plus any rows whose operation_date failed to
// parse. It is the JSON the `hooppy doctor` command prints on stdout.
//
// WalkIncomplete is true when a walk was truncated: the count of unique ids
// is LESS than the server's first-page total_rows. A last-page total greater
// than the first-page total is a benign mid-walk insert (the collection grew
// while doctor was reading it) and does NOT set WalkIncomplete — /notifications
// is a high-churn log where an insert between page fetches is ordinary, not a
// sign of data loss (whether the vendor prunes old rows is unestablished; see
// RunDoctor for the gaps this rule does not close). Doctor does NOT abort on a truncated
// walk — its purpose is to surface failures, not hide them behind a hard
// error — but it flags the walk as incomplete so the operator knows the
// report may be missing rows, and the CLI exit code reflects it.
// WalkIncompleteReason carries a human-readable explanation of which walk
// was truncated and the counts involved, so the operator can decide what to
// do — a bare boolean tells them nothing.
type DoctorReport struct {
	SinceDays int `json:"since_days"`
	// WindowStart is the --since window lower bound. It is the zero time
	// when --since 0 ("no window"); in that case it is OMITTED from the JSON
	// output (see MarshalJSON) rather than serialised as the sentinel
	// "0001-01-01T00:00:00Z".
	WindowStart          time.Time           `json:"window_start"`
	WalkIncomplete       bool                `json:"walk_incomplete,omitempty"`
	WalkIncompleteReason string              `json:"walk_incomplete_reason,omitempty"`
	Groups               []DoctorGroup       `json:"groups"`
	UnparseableRows      []DoctorUnparseable `json:"unparseable_rows,omitempty"`
}

// MarshalJSON omits window_start when it is the zero time (--since 0,
// "no window"). A zero time.Time would otherwise serialise as the sentinel
// "0001-01-01T00:00:00Z", indistinguishable from a real boundary to a
// consumer; omitting the field makes "no window" explicit. When a window
// IS set the struct's normal tags apply via the type alias. This is the
// only custom marshalling on DoctorReport: the zero-window branch shadows
// WindowStart with a nil *time.Time (omitempty) so the rest of the struct
// is marshalled from the embedded alias and stays in sync automatically.
func (r DoctorReport) MarshalJSON() ([]byte, error) {
	type alias DoctorReport
	if r.WindowStart.IsZero() {
		return json.Marshal(struct {
			alias
			WindowStart *time.Time `json:"window_start,omitempty"`
		}{
			alias: alias(r),
		})
	}
	return json.Marshal(alias(r))
}

// RunDoctor walks the notification log, filters to error rows whose
// operation_date falls inside the last sinceDays, groups them by
// (page_id, error message), classifies each group, and resolves page names
// via GET /accounts/pages (the narrow Page struct — id/source/social ids/
// name/photo only, no credential fields). It is read-only and mutates
// nothing.
//
// sinceDays semantics: 0 means "no window" (window start = zero time, so
// every dated row is included); a negative value is rejected with an error
// (never silently clamped — clamping a bad value into the quietest
// configuration hides exactly the failures doctor exists to surface).
//
// Both walks (notifications and pages) use the WithFirstAndLastTotal
// variants to capture the server's first-page and last-page total_rows.
// A walk is flagged incomplete (WalkIncomplete=true) only when the unique
// id count is LESS than the first-page total — that is the truncation
// signal. A last-page total greater than the first-page total is a benign
// mid-walk insert (the collection grew while doctor was reading it) and
// does NOT flag — /notifications is a high-churn log where an insert
// between page fetches is ordinary. Doctor does NOT use NewAllListEnvelope
// here: its equality check (unique == total) is right for the low-churn
// collections its other callers walk (projects, schedules), but wrong for
// this high-churn log where it would false-alarm on every active account.
// See NewAllListEnvelope for the per call-site table. The reason is
// captured in WalkIncompleteReason, not discarded — a bare boolean tells
// the operator nothing about what to do.
//
// What the unique < firstTotal rule does NOT catch (it is a directional
// check for net loss, not a proof of completeness):
//   - Concurrent growth MASKS truncation. Two rows inserted mid-walk plus
//     one row skipped by the offset shift gives unique == firstTotal (the
//     two new ids replace the one missing in the count) and no flag, even
//     though a row was lost.
//   - A SHRINKING collection false-positives. A row that ages out or is
//     pruned mid-walk drops the server's total_rows below the first-page
//     value, so unique < firstTotal on a walk that missed nothing. Whether
//     the vendor prunes /notifications rows is NOT documented — the public
//     OpenAPI spec (v0.1.0) does not cover /notifications at all — and is
//     otherwise unestablished; if the log is strictly append-only this gap
//     closes, but that has not been established and is not asserted here.
//
// Page-name resolution: the notification row embeds a full `page` object
// that carries live OAuth credentials (access_token, bot_token,
// refresh_token, password, wp_app_password, access_token_secret). The
// Notification struct does NOT model `page`, so those fields are dropped at
// the decode boundary — they never enter Go memory. Page names are resolved
// separately via ListAllPages, which decodes into the narrow Page struct
// (no credential fields). Neither path can carry a token into the report.
// See TestRunDoctor_TokensNeverReachOutput for the regression guard.
//
// A row whose operation_date fails to parse is reported in
// UnparseableRows — never silently dropped, and regardless of --since
// (the date could not be parsed, so the window check cannot be applied).
//
// TIMEZONE ASSUMPTION: the --since window is computed against time.Now()
// in the host's local timezone, while the vendor renders operation_date in
// the account's timezone (a user setting on hooppy.ru, not exposed by the
// API). If the two timezones differ, the window boundary can be off by the
// offset between them. See parseOperationDate and the --since flag help.
func (c *Client) RunDoctor(ctx context.Context, sinceDays int) (*DoctorReport, error) {
	if sinceDays < 0 {
		return nil, fmt.Errorf("hooppy: doctor: --since must be >= 0 (got %d); use 0 for no window (all dated rows included)", sinceDays)
	}
	var windowStart time.Time
	if sinceDays > 0 {
		windowStart = time.Now().AddDate(0, 0, -sinceDays)
	}
	// sinceDays == 0 → windowStart stays zero time → opDate.Before(zero) is
	// always false → every dated row is included ("no window").

	notifications, notifFirstTotal, notifLastTotal, err := c.ListAllNotificationsWithFirstAndLastTotal(ctx)
	if err != nil {
		return nil, fmt.Errorf("hooppy: doctor: walk notifications: %w", err)
	}
	walkIncomplete := false
	walkIncompleteReason := ""
	notifUnique := uniqueCount(notifications, func(n Notification) int { return n.ID })
	if notifUnique < notifFirstTotal {
		walkIncomplete = true
		walkIncompleteReason = fmt.Sprintf("notifications walk truncated: %d unique ids < first-page total_rows %d (last-page total_rows %d) — the report may be missing rows", notifUnique, notifFirstTotal, notifLastTotal)
	}

	// Resolve page names via the narrow Page struct (no credential fields).
	// The embedded `page` object in each notification is NOT decoded —
	// Notification does not model it, so the vendor's token fields are
	// dropped at the decode boundary.
	pages, pagesFirstTotal, pagesLastTotal, err := c.ListAllPagesWithFirstAndLastTotal(ctx, ListPagesFilter{})
	if err != nil {
		return nil, fmt.Errorf("hooppy: doctor: walk pages: %w", err)
	}
	pagesUnique := uniqueCount(pages, func(p Page) int { return p.ID })
	if pagesUnique < pagesFirstTotal {
		walkIncomplete = true
		reason := fmt.Sprintf("pages walk truncated: %d unique ids < first-page total_rows %d (last-page total_rows %d) — page-name resolution may be incomplete", pagesUnique, pagesFirstTotal, pagesLastTotal)
		if walkIncompleteReason != "" {
			walkIncompleteReason = walkIncompleteReason + "; " + reason
		} else {
			walkIncompleteReason = reason
		}
	}
	// A last-total > first-total is a benign mid-walk insert (the collection
	// grew while doctor was reading it). /notifications is a high-churn
	// log where this is ordinary; we do NOT flag it. Only
	// unique < firstTotal (rows missing that the server initially said
	// existed) is a truncation signal.
	pageByName := make(map[int]string, len(pages))
	for _, p := range pages {
		pageByName[p.ID] = p.SocialPageName
	}

	type groupKey struct {
		pageID    int
		errorText string
	}
	type groupAcc struct {
		count    int
		first    time.Time
		last     time.Time
		sourceID int
		hasFirst bool
	}

	groups := map[groupKey]*groupAcc{}
	var unparseable []DoctorUnparseable

	for _, n := range notifications {
		// Doctor reports failures only.
		if n.IsError != 1 {
			continue
		}

		opDate, err := parseOperationDate(n.OperationDate)
		if err != nil {
			// REPORT, never drop — a row with an unparseable date may be
			// exactly the failure the operator needs to see.
			unparseable = append(unparseable, DoctorUnparseable{
				NotificationID: n.ID,
				PageID:         n.PageID,
				SourceID:       n.SourceID,
				OperationDate:  n.OperationDate,
				ErrorText:      n.Data,
			})
			continue
		}

		// Filter to the --since window.
		if opDate.Before(windowStart) {
			continue
		}

		key := groupKey{pageID: n.PageID, errorText: n.Data}
		acc, ok := groups[key]
		if !ok {
			acc = &groupAcc{sourceID: n.SourceID, first: opDate, last: opDate, hasFirst: true}
			groups[key] = acc
		}
		acc.count++
		if opDate.Before(acc.first) {
			acc.first = opDate
		}
		if opDate.After(acc.last) {
			acc.last = opDate
		}
	}

	report := &DoctorReport{
		SinceDays:            sinceDays,
		WindowStart:          windowStart,
		WalkIncomplete:       walkIncomplete,
		WalkIncompleteReason: walkIncompleteReason,
		Groups:               make([]DoctorGroup, 0, len(groups)),
		UnparseableRows:      unparseable,
	}
	for key, acc := range groups {
		report.Groups = append(report.Groups, DoctorGroup{
			PageID:         key.pageID,
			PageName:       pageByName[key.pageID],
			SourceID:       acc.sourceID,
			Network:        SourceID(acc.sourceID).String(),
			ErrorText:      key.errorText,
			Classification: classifyError(key.errorText),
			Count:          acc.count,
			FirstDate:      acc.first,
			LastDate:       acc.last,
		})
	}

	return report, nil
}

// uniqueCount returns the count of unique ids extracted from list by idFunc.
// It is the truncation signal doctor uses: unique < first-page total_rows
// means the walk is missing rows the server initially said existed.
func uniqueCount[T any](list []T, idFunc func(T) int) int {
	unique := make(map[int]struct{}, len(list))
	for _, item := range list {
		unique[idFunc(item)] = struct{}{}
	}
	return len(unique)
}
