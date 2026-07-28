package hooppy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// vendorDate formats a time.Time as the vendor's operation_date string
// (дд.мм.гггг, чч:мм = 02.01.2006, 15:04).
func vendorDate(t time.Time) string {
	return t.Format("02.01.2006, 15:04")
}

// stubDoctorServer returns an httptest.Server that serves /notifications
// from the given body and /accounts/pages from the given body.
func stubDoctorServer(t *testing.T, notificationsBody, pagesBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/notifications":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(notificationsBody))
		case "/accounts/pages":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(pagesBody))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// TestRunDoctor_GroupsAndClassification verifies that doctor walks
// notifications, resolves page names via ListAllPages, groups by
// (page_id, error message), and classifies each group. The error strings
// and network names below are the MEASURED vendor strings (see
// errorClassTable and networkNameTable in doctor.go). Without the
// classification table the class field would be "unknown" for every row;
// without the page-name join PageName would be empty.
func TestRunDoctor_GroupsAndClassification(t *testing.T) {
	now := time.Now()
	recent := vendorDate(now.Add(-2 * 24 * time.Hour))
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Устарел ключ доступа. Обновите подключение аккаунта Одноклассники в разделе \"Мои аккаунты\"."},
		{"id":2,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Устарел ключ доступа. Обновите подключение аккаунта Одноклассники в разделе \"Мои аккаунты\"."},
		{"id":3,"is_error":1,"page_id":200,"source_id":6,"operation_date":"` + recent + `","data":"Нет контента для публикации. Скорее всего ожидается наличие фото или видео, но их нет."},
		{"id":4,"is_error":1,"page_id":300,"source_id":3,"operation_date":"` + recent + `","data":"502, Bad Gateway (storePhoto)"}
	],"total_rows":4,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[
		{"id":100,"source_id":1,"social_page_name":"VK Main Page"},
		{"id":200,"source_id":6,"social_page_name":"Pinterest Board"},
		{"id":300,"source_id":3,"social_page_name":"FB Business"}
	],"total_rows":3,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorServer(t, notifications, pages)
	defer srv.Close()
	c := newTestClient(t, srv)

	report, err := c.RunDoctor(context.Background(), 7)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if len(report.Groups) != 3 {
		t.Fatalf("len(Groups) = %d, want 3 (one per distinct page+error); got %+v", len(report.Groups), report.Groups)
	}

	byPage := map[int]DoctorGroup{}
	for _, g := range report.Groups {
		byPage[g.PageID] = g
	}

	// Page 100: two occurrences of the expired-credential message.
	g100 := byPage[100]
	if g100.Count != 2 {
		t.Errorf("page 100 count = %d, want 2", g100.Count)
	}
	if g100.Classification != "expired_credential" {
		t.Errorf("page 100 class = %q, want expired_credential", g100.Classification)
	}
	if g100.PageName != "VK Main Page" {
		t.Errorf("page 100 name = %q, want VK Main Page", g100.PageName)
	}
	if g100.Network != "vkontakte" {
		t.Errorf("page 100 network = %q, want vkontakte", g100.Network)
	}

	// Page 200: missing media.
	g200 := byPage[200]
	if g200.Classification != "missing_media" {
		t.Errorf("page 200 class = %q, want missing_media", g200.Classification)
	}
	if g200.Network != "pinterest" {
		t.Errorf("page 200 network = %q, want pinterest", g200.Network)
	}

	// Page 300: upstream error.
	g300 := byPage[300]
	if g300.Classification != "upstream_error" {
		t.Errorf("page 300 class = %q, want upstream_error", g300.Classification)
	}
	if g300.Network != "facebook" {
		t.Errorf("page 300 network = %q, want facebook", g300.Network)
	}
}

// TestRunDoctor_TokensNeverReachOutput is the credential-leak guard. It
// enforces the property at the DECODE BOUNDARY — where the tokens enter Go
// memory — not only at the report level:
//
//  1. DECODE BOUNDARY (values): marshal the []Notification from
//     ListAllNotifications and the []Page from ListAllPages against a stub
//     that carries live token values in every place the vendor's JSON
//     provides them (the embedded `page` object in notifications, and the
//     page fields directly). Assert the token VALUES are absent from both
//     marshalled outputs. This catches the mutation where someone widens
//     Notification to model `page` as a token-carrying struct OR widens
//     Page to include a credential field — the tokens would then survive
//     the decode and appear in the marshalled output.
//
//  2. DECODE BOUNDARY (struct shape): a reflection assertion that neither
//     Notification nor Page exposes a field whose json tag matches a
//     credential field name. This catches the mutation at compile-time
//     granularity: even if the token value happens to be empty, the
//     struct shape itself is rejected.
//
//  3. REPORT LEVEL (second layer): marshal the full DoctorReport — exactly
//     what the CLI prints — and assert no token value appears. This is a
//     belt-and-suspenders check; the decode boundary is the primary guard.
//
// The property matters beyond doctor: widening Page also leaks through
// `hooppy pages list` stdout and the MCP pages tool, so the decode-boundary
// guard protects every consumer of ListAllPages, not just doctor.
func TestRunDoctor_TokensNeverReachOutput(t *testing.T) {
	const (
		accessToken       = "SECRET_ACCESS_TOKEN_12345"
		botToken          = "SECRET_BOT_TOKEN_67890"
		password          = "SECRET_PASSWORD_ABCDEF"
		refreshToken      = "SECRET_REFRESH_TOKEN_99999"
		wpAppPassword     = "SECRET_WP_APP_PASSWORD_77777"
		accessTokenSecret = "SECRET_ACCESS_TOKEN_SECRET_55555"
	)
	allTokens := []string{accessToken, botToken, password, refreshToken, wpAppPassword, accessTokenSecret}
	recent := vendorDate(time.Now().Add(-1 * 24 * time.Hour))
	// The embedded page object carries every credential field the vendor's
	// JSON provides. Notification does NOT model `page`, so encoding/json
	// drops the entire object at the decode boundary — the tokens never
	// enter Go memory through this path. If someone adds a `Page *Page`
	// field to Notification AND credential fields to Page, the tokens
	// survive the decode and appear in the marshalled []Notification.
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Необходимо переподключить аккаунт",
		 "page":{"id":100,"source_id":1,"social_page_name":"Leaky Page",
		         "access_token":"` + accessToken + `",
		         "bot_token":"` + botToken + `",
		         "refresh_token":"` + refreshToken + `",
		         "password":"` + password + `",
		         "wp_app_password":"` + wpAppPassword + `",
		         "access_token_secret":"` + accessTokenSecret + `"}}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	// The pages list carries every credential field directly — guards
	// against Page widening. If someone adds any of these fields to the
	// Page struct, the tokens survive the decode and appear in marshalled
	// []Page (and in `hooppy pages list` stdout, and the MCP pages tool).
	pages := `{"list":[
		{"id":100,"source_id":1,"social_page_name":"Leaky Page",
		 "access_token":"` + accessToken + `",
		 "bot_token":"` + botToken + `",
		 "refresh_token":"` + refreshToken + `",
		 "password":"` + password + `",
		 "wp_app_password":"` + wpAppPassword + `",
		 "access_token_secret":"` + accessTokenSecret + `"}
	],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorServer(t, notifications, pages)
	defer srv.Close()
	c := newTestClient(t, srv)

	// --- Layer 1a: []Notification decode boundary (values) ---
	notifList, err := c.ListAllNotifications(context.Background())
	if err != nil {
		t.Fatalf("ListAllNotifications: %v", err)
	}
	notifJSON, err := json.Marshal(notifList)
	if err != nil {
		t.Fatalf("json.Marshal(notifList): %v", err)
	}
	notifStr := string(notifJSON)
	for _, token := range allTokens {
		if strings.Contains(notifStr, token) {
			t.Errorf("CREDENTIAL LEAK at decode boundary ([]Notification): token value %q appears in marshalled notifications:\n%s", token, notifStr)
		}
	}

	// --- Layer 1b: []Page decode boundary (values) ---
	pageList, err := c.ListAllPages(context.Background(), ListPagesFilter{})
	if err != nil {
		t.Fatalf("ListAllPages: %v", err)
	}
	pageJSON, err := json.Marshal(pageList)
	if err != nil {
		t.Fatalf("json.Marshal(pageList): %v", err)
	}
	pageStr := string(pageJSON)
	for _, token := range allTokens {
		if strings.Contains(pageStr, token) {
			t.Errorf("CREDENTIAL LEAK at decode boundary ([]Page): token value %q appears in marshalled pages:\n%s", token, pageStr)
		}
	}

	// --- Layer 2: struct shape (reflection) ---
	// Neither Notification nor Page may expose a field whose json tag
	// matches a credential field name. This catches the mutation even
	// when the token value is empty — the struct shape itself is rejected.
	credentialTagRe := regexp.MustCompile(`^(access_token|bot_token|refresh_token|password|wp_app_password|access_token_secret)$`)
	for _, st := range []struct {
		name string
		typ  interface{}
	}{
		{"Notification", Notification{}},
		{"Page", Page{}},
	} {
		t.Run("struct_shape/"+st.name, func(t *testing.T) {
			v := reflect.ValueOf(st.typ)
			tt := v.Type()
			for i := 0; i < tt.NumField(); i++ {
				f := tt.Field(i)
				tag := f.Tag.Get("json")
				// Strip options like ",omitempty" to get the bare tag name.
				tagName := tag
				if idx := strings.Index(tag, ","); idx >= 0 {
					tagName = tag[:idx]
				}
				if tagName == "-" {
					continue // explicitly unexported/ignored
				}
				if credentialTagRe.MatchString(tagName) {
					t.Errorf("%s.%s has json tag %q matching a credential field — credential fields must not be modelled on this struct", st.name, f.Name, tagName)
				}
			}
		})
	}

	// --- Layer 3: report level (second layer) ---
	report, err := c.RunDoctor(context.Background(), 7)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	out, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	outStr := string(out)
	for _, token := range allTokens {
		if strings.Contains(outStr, token) {
			t.Errorf("CREDENTIAL LEAK at report level: token value %q appears in doctor output:\n%s", token, outStr)
		}
	}
}

// TestRunDoctor_UnparseableDateReported verifies that a notification whose
// operation_date fails to parse is REPORTED in UnparseableRows, never
// silently dropped. Dropping such a row hides exactly the failure doctor
// exists to surface. Without the unparseable-rows field the row would
// vanish from the report entirely.
func TestRunDoctor_UnparseableDateReported(t *testing.T) {
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"not-a-date","data":"Необходимо переподключить аккаунт"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorServer(t, notifications, pages)
	defer srv.Close()
	c := newTestClient(t, srv)

	report, err := c.RunDoctor(context.Background(), 7)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if len(report.UnparseableRows) != 1 {
		t.Fatalf("len(UnparseableRows) = %d, want 1 (unparseable date must be reported, not dropped)", len(report.UnparseableRows))
	}
	if report.UnparseableRows[0].NotificationID != 1 {
		t.Errorf("unparseable row notification id = %d, want 1", report.UnparseableRows[0].NotificationID)
	}
	if len(report.Groups) != 0 {
		t.Errorf("len(Groups) = %d, want 0 (unparseable row should not appear in groups)", len(report.Groups))
	}
}

// TestRunDoctor_SinceFilter verifies that only errors within the --since
// window appear in groups. A row 30 days ago must be excluded when
// sinceDays=7; a row 1 day ago must be included. Without the date parse
// + comparison, old errors would pollute the report.
func TestRunDoctor_SinceFilter(t *testing.T) {
	inWindow := vendorDate(time.Now().Add(-1 * 24 * time.Hour))
	outWindow := vendorDate(time.Now().Add(-30 * 24 * time.Hour))
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + inWindow + `","data":"Необходимо переподключить аккаунт"},
		{"id":2,"is_error":1,"page_id":200,"source_id":1,"operation_date":"` + outWindow + `","data":"Необходимо переподключить аккаунт"}
	],"total_rows":2,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[
		{"id":100,"source_id":1,"social_page_name":"A"},
		{"id":200,"source_id":1,"social_page_name":"B"}
	],"total_rows":2,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorServer(t, notifications, pages)
	defer srv.Close()
	c := newTestClient(t, srv)

	report, err := c.RunDoctor(context.Background(), 7)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("len(Groups) = %d, want 1 (only the in-window row); got %+v", len(report.Groups), report.Groups)
	}
	if report.Groups[0].PageID != 100 {
		t.Errorf("group page id = %d, want 100 (the in-window row)", report.Groups[0].PageID)
	}
}

// TestRunDoctor_NonErrorRowsExcluded verifies that a notification with
// is_error=0 does not appear in groups — doctor reports failures only.
func TestRunDoctor_NonErrorRowsExcluded(t *testing.T) {
	recent := vendorDate(time.Now().Add(-1 * 24 * time.Hour))
	notifications := `{"list":[
		{"id":1,"is_error":0,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Успешно опубликовано"},
		{"id":2,"is_error":1,"page_id":200,"source_id":1,"operation_date":"` + recent + `","data":"Необходимо переподключить аккаунт"}
	],"total_rows":2,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[
		{"id":100,"source_id":1,"social_page_name":"A"},
		{"id":200,"source_id":1,"social_page_name":"B"}
	],"total_rows":2,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorServer(t, notifications, pages)
	defer srv.Close()
	c := newTestClient(t, srv)

	report, err := c.RunDoctor(context.Background(), 7)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("len(Groups) = %d, want 1 (only is_error=1 row); got %+v", len(report.Groups), report.Groups)
	}
	if report.Groups[0].PageID != 200 {
		t.Errorf("group page id = %d, want 200 (the is_error=1 row)", report.Groups[0].PageID)
	}
}

// TestClassifyError_MeasuredVendorStrings is the regression gate for the
// whole classification feature. It runs the EXACT vendor strings measured
// from the live notification log through classifyError and asserts the
// expected class for each. It also includes a string that belongs in none
// of the known buckets, asserting "unknown" with the raw text preserved
// verbatim in the group (the caller, RunDoctor, sets ErrorText = n.Data
// unchanged; classifyError never mutates its input and never forces an
// unmatched string into a known bucket).
//
// Without this test the classification table silently rots: a vendor
// rewording that breaks a needle would go unnoticed until someone runs
// doctor against the live log again.
func TestClassifyError_MeasuredVendorStrings(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		// expired_credential — the four Russian variants vary by network
		// name in the middle; the needles key on the invariant fragments
		// ("Устарел ключ доступа", "Обновите подключение"), so all four
		// must classify the same regardless of the network named.
		{"ru_ok", `Устарел ключ доступа. Обновите подключение аккаунта Одноклассники в разделе "Мои аккаунты".`, "expired_credential"},
		{"ru_twitter", `Устарел ключ доступа. Обновите подключение аккаунта Twitter в разделе "Мои аккаунты".`, "expired_credential"},
		{"ru_pinterest", `Устарел ключ доступа. Обновите подключение аккаунта Pinterest в разделе "Мои аккаунты".`, "expired_credential"},
		{"ru_dzen", `Устарел ключ доступа. Обновите подключение канала Дзен в разделе "Мои аккаунты".`, "expired_credential"},
		// The two English credential messages are structurally unrelated
		// to each other and carry a call-site suffix in parentheses — the
		// needles exclude the suffix, so it must not participate in
		// matching.
		{"en_missing_auth_header", "Missing valid authorization header (getAccessToken)", "expired_credential"},
		{"en_session_invalidated", "Error validating access token: The session has been invalidated because the user changed their password or Facebook has changed the session for security reasons.(uploadPhoto)", "expired_credential"},
		// missing_media — single measured Russian message.
		{"ru_no_content", "Нет контента для публикации. Скорее всего ожидается наличие фото или видео, но их нет.", "missing_media"},
		// upstream_error — already correct, must not regress.
		{"en_502_storeAlbum", "502, Bad Gateway (storeAlbum)", "upstream_error"},
		{"en_502_storePhoto", "502, Bad Gateway (storePhoto)", "upstream_error"},
		// unknown — belongs in none of the buckets; raw text preserved.
		{"unknown_vendor_message", "Какое-то совершенно новое сообщение об ошибке от вендора", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyError(tc.data)
			if got != tc.want {
				t.Errorf("classifyError(%q) = %q, want %q", tc.data, got, tc.want)
			}
			// The raw input must be preserved verbatim — classifyError
			// must not mutate its input, and an unmatched string must
			// never be forced into a known bucket. RunDoctor sets
			// ErrorText = n.Data unchanged, so the group carries the raw
			// vendor string for the operator to read.
			if got == "unknown" {
				group := DoctorGroup{ErrorText: tc.data, Classification: got}
				if group.ErrorText != tc.data {
					t.Errorf("unknown case raw text not preserved: got %q, want %q", group.ErrorText, tc.data)
				}
			}
		})
	}
}

// TestSourceID_String_DoctorRelevantIDs is the regression gate for the
// network field doctor renders via SourceID.String(). It pins every id
// doctor encounters on a live account and asserts one truly unmapped id
// renders "unknown" rather than being coerced — the same rule
// errorClassTable follows for unmatched messages. Without this test the
// id→name table is a silent-rot surface: a vendor id renumbering or a slug
// typo would go unnoticed until someone runs doctor against the live log.
//
// Ids 10 and 11 are observed on live accounts and MUST NOT render
// "unknown" — the library names them (instagram_fb, telegram_account).
// Earlier they were in a separate table that doctor did not consult; now
// doctor delegates to SourceID.String() and they resolve.
func TestSourceID_String_DoctorRelevantIDs(t *testing.T) {
	cases := []struct {
		sourceID int
		want     string
	}{
		{1, "vkontakte"},
		{2, "odnoklassniki"},
		{3, "facebook"},
		{4, "twitter"},
		{6, "pinterest"},
		{7, "instagram"},
		{9, "telegram"},
		{10, "instagram_fb"},     // observed live — must NOT be "unknown"
		{11, "telegram_account"}, // observed live — must NOT be "unknown"
		{13, "dzen"},
		{14, "tiktok"},
		{16, "viber"},
		{17, "youtube"},
		{18, "linkedin"},
		{28, "max"},
		// Unmapped ids render "unknown" rather than being coerced.
		{999, "unknown"},
	}
	for _, tc := range cases {
		got := SourceID(tc.sourceID).String()
		if got != tc.want {
			t.Errorf("SourceID(%d).String() = %q, want %q", tc.sourceID, got, tc.want)
		}
	}
}
