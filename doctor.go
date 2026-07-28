package hooppy

import (
	"context"
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
// UNDOCUMENTED: these strings are not in the public OpenAPI spec. They were
// inferred from the vendor's error messages and may change without notice.
var errorClassTable = []struct {
	class   string
	needles []string
}{
	{
		class: classExpiredCredential,
		needles: []string{
			"необходимо переподключить", // RU: "you need to reconnect"
			"токен истёк",               // RU: "token expired"
			"токен истек",               // RU: "token expired" (ё-free)
			"reconnect the account",
			"access token has expired",
			"token expired",
			"обновите доступ", // RU: "update access"
		},
	},
	{
		class: classMissingMedia,
		needles: []string{
			"требуется изображение", // RU: "image required"
			"необходимо прикрепить", // RU: "need to attach"
			"требуется медиа",       // RU: "media required"
			"image is required",
			"media is required",
			"requires an image",
			"no media attached",
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
type DoctorReport struct {
	SinceDays       int                 `json:"since_days"`
	WindowStart     time.Time           `json:"window_start"`
	Groups          []DoctorGroup       `json:"groups"`
	UnparseableRows []DoctorUnparseable `json:"unparseable_rows,omitempty"`
}

// RunDoctor walks the notification log, filters to error rows whose
// operation_date falls inside the last sinceDays, groups them by
// (page_id, error message), classifies each group, and resolves page names
// via GET /accounts/pages (the narrow Page struct — id/source/social ids/
// name/photo only, no credential fields). It is read-only and mutates
// nothing.
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
// UnparseableRows — never silently dropped.
func (c *Client) RunDoctor(ctx context.Context, sinceDays int) (*DoctorReport, error) {
	if sinceDays < 0 {
		sinceDays = 0
	}
	windowStart := time.Now().AddDate(0, 0, -sinceDays)

	notifications, err := c.ListAllNotifications(ctx)
	if err != nil {
		return nil, fmt.Errorf("hooppy: doctor: walk notifications: %w", err)
	}

	// Resolve page names via the narrow Page struct (no credential fields).
	// The embedded `page` object in each notification is NOT decoded —
	// Notification does not model it, so the vendor's token fields are
	// dropped at the decode boundary.
	pages, err := c.ListAllPages(ctx, ListPagesFilter{})
	if err != nil {
		return nil, fmt.Errorf("hooppy: doctor: walk pages: %w", err)
	}
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
		SinceDays:       sinceDays,
		WindowStart:     windowStart,
		Groups:          make([]DoctorGroup, 0, len(groups)),
		UnparseableRows: unparseable,
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
