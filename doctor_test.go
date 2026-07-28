package hooppy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
// (page_id, error message), and classifies each group. Without the
// classification table the class field would be "unknown" for every row;
// without the page-name join PageName would be empty.
func TestRunDoctor_GroupsAndClassification(t *testing.T) {
	now := time.Now()
	recent := vendorDate(now.Add(-2 * 24 * time.Hour))
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Необходимо переподключить аккаунт"},
		{"id":2,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Необходимо переподключить аккаунт"},
		{"id":3,"is_error":1,"page_id":200,"source_id":6,"operation_date":"` + recent + `","data":"Требуется изображение для публикации"},
		{"id":4,"is_error":1,"page_id":300,"source_id":3,"operation_date":"` + recent + `","data":"Internal server error 500 от социальной сети"}
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

// TestRunDoctor_TokensNeverReachOutput is the credential-leak guard. The
// stub notification's embedded page object carries live OAuth tokens
// (access_token, bot_token, password). The stub pages list ALSO carries
// them (to guard against someone widening the Page struct). The test
// marshals the full DoctorReport to JSON — exactly what the CLI prints —
// and asserts NONE of the token VALUES appear. It checks values, not key
// names, so it catches any future struct widening that flows tokens into
// the report. This test MUST fail if someone widens Notification to model
// `page` as a token-carrying struct or widens Page/DoctorGroup to include
// a credential field that reaches output.
func TestRunDoctor_TokensNeverReachOutput(t *testing.T) {
	const (
		accessToken = "SECRET_ACCESS_TOKEN_12345"
		botToken    = "SECRET_BOT_TOKEN_67890"
		password    = "SECRET_PASSWORD_ABCDEF"
	)
	recent := vendorDate(time.Now().Add(-1 * 24 * time.Hour))
	// The embedded page object carries the tokens. Notification does NOT
	// model `page`, so encoding/json drops the entire object at the decode
	// boundary — the tokens never enter Go memory through this path.
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Необходимо переподключить аккаунт",
		 "page":{"id":100,"source_id":1,"social_page_name":"Leaky Page",
		         "access_token":"` + accessToken + `",
		         "bot_token":"` + botToken + `",
		         "password":"` + password + `"}}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	// The pages list also carries the tokens — guards against Page widening.
	pages := `{"list":[
		{"id":100,"source_id":1,"social_page_name":"Leaky Page",
		 "access_token":"` + accessToken + `",
		 "bot_token":"` + botToken + `",
		 "password":"` + password + `"}
	],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorServer(t, notifications, pages)
	defer srv.Close()
	c := newTestClient(t, srv)

	report, err := c.RunDoctor(context.Background(), 7)
	if err != nil {
		t.Fatalf("RunDoctor: %v", err)
	}
	out, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	outStr := string(out)
	for _, token := range []string{accessToken, botToken, password} {
		if strings.Contains(outStr, token) {
			t.Errorf("CREDENTIAL LEAK: token value %q appears in doctor output:\n%s", token, outStr)
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

// TestClassifyError verifies the classification table maps known vendor
// substrings to the right bucket and falls back to "unknown" for anything
// unmatched, carrying the raw string in the group (not forcing it into a
// known bucket).
func TestClassifyError(t *testing.T) {
	cases := []struct {
		data string
		want string
	}{
		{"Необходимо переподключить аккаунт", "expired_credential"},
		{"Токен истёк, обновите доступ", "expired_credential"},
		{"Please reconnect the account to continue", "expired_credential"},
		{"Требуется изображение для публикации", "missing_media"},
		{"Image is required for this network", "missing_media"},
		{"Internal server error 500 от социальной сети", "upstream_error"},
		{"503 Service Unavailable", "upstream_error"},
		{"Some completely unknown error message", "unknown"},
	}
	for _, tc := range cases {
		got := classifyError(tc.data)
		if got != tc.want {
			t.Errorf("classifyError(%q) = %q, want %q", tc.data, got, tc.want)
		}
	}
}
