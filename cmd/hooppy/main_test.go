package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anatolykoptev/go-hooppy"
)

// vendorDate formats a time.Time as the vendor's operation_date string
// (дд.мм.гггг, чч:мм = 02.01.2006, 15:04).
func vendorDate(t time.Time) string {
	return t.Format("02.01.2006, 15:04")
}

// newDoctorTestClient creates a hooppy.Client pointing at an httptest.Server.
func newDoctorTestClient(t *testing.T, srv *httptest.Server) *hooppy.Client {
	t.Helper()
	c, err := hooppy.NewClient(hooppy.Config{
		Token:   "test-token",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// stubDoctorAPIServer serves /notifications and /accounts/pages from the
// given bodies. It mirrors the library-level stubDoctorServer but lives in
// the CLI package so the exit-code test can drive runDoctor end-to-end.
func stubDoctorAPIServer(t *testing.T, notificationsBody, pagesBody string) *httptest.Server {
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

// TestRunDoctor_ExitCode_UnparseableDate verifies that --exit-code gates on
// UnparseableRows, not just Groups. The only error row has an unparseable
// operation_date — it lands in UnparseableRows and Groups is empty. Without
// the fix, the CLI read exit 0 over an undiagnosed publication failure: a
// vendor date-format drift puts every error row in UnparseableRows, and the
// old gate (len(Groups) > 0) would silently clear cron. The fix gates on
// len(Groups) > 0 || len(UnparseableRows) > 0 || WalkIncomplete.
func TestRunDoctor_ExitCode_UnparseableDate(t *testing.T) {
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"not-a-date","data":"Необходимо переподключить аккаунт"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorAPIServer(t, notifications, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 7, true)
	if code == 0 {
		t.Fatalf("runDoctor exit code = 0, want non-zero (unparseable-date row must trigger exit 1); stdout=%s stderr=%s", out.String(), errOut.String())
	}
}

// TestRunDoctor_ExitCode_NoErrors verifies that a clean notification log
// (no error rows) produces exit 0 — the gate does not fire on empty
// UnparseableRows and empty Groups.
func TestRunDoctor_ExitCode_NoErrors(t *testing.T) {
	recent := vendorDate(time.Now().Add(-1 * 24 * time.Hour))
	notifications := `{"list":[
		{"id":1,"is_error":0,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Успешно опубликовано"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorAPIServer(t, notifications, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 7, true)
	if code != 0 {
		t.Fatalf("runDoctor exit code = %d, want 0 (no errors in log); stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

// TestRunDoctor_ExitCode_GroupedErrors verifies that a grouped error inside
// the window still triggers exit 1 — the fix did not regress the original
// gate.
func TestRunDoctor_ExitCode_GroupedErrors(t *testing.T) {
	recent := vendorDate(time.Now().Add(-1 * 24 * time.Hour))
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + recent + `","data":"Устарел ключ доступа"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorAPIServer(t, notifications, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 7, true)
	if code == 0 {
		t.Fatalf("runDoctor exit code = 0, want non-zero (grouped error inside window); stdout=%s stderr=%s", out.String(), errOut.String())
	}
}

// TestRunDoctor_ExitCode_Disabled verifies that --exit-code=false produces
// exit 0 even when errors are present.
func TestRunDoctor_ExitCode_Disabled(t *testing.T) {
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"not-a-date","data":"error"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorAPIServer(t, notifications, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 7, false)
	if code != 0 {
		t.Fatalf("runDoctor exit code = %d, want 0 (--exit-code=false); stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

// stubDoctorPaginatedAPIServer is the CLI-package analogue of the library
// stubDoctorPaginatedServer: serves /notifications and /accounts/pages
// from per-page response maps keyed by the page query parameter.
func stubDoctorPaginatedAPIServer(t *testing.T, notifPages map[string]string, notifDefault string, pagePages map[string]string, pageDefault string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		pg := r.URL.Query().Get("page")
		if pg == "" {
			pg = "1"
		}
		switch r.URL.Path {
		case "/notifications":
			if body, ok := notifPages[pg]; ok {
				w.Write([]byte(body))
				return
			}
			if notifDefault != "" {
				w.Write([]byte(notifDefault))
				return
			}
			t.Errorf("unexpected /notifications page=%s", pg)
			http.NotFound(w, r)
		case "/accounts/pages":
			if body, ok := pagePages[pg]; ok {
				w.Write([]byte(body))
				return
			}
			if pageDefault != "" {
				w.Write([]byte(pageDefault))
				return
			}
			t.Errorf("unexpected /accounts/pages page=%s", pg)
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// TestRunDoctor_ExitCode_WalkIncomplete_Notifications verifies that the
// third disjunct of the exit-code gate (WalkIncomplete) fires when the
// notifications walk is truncated, even with empty Groups and
// UnparseableRows. Without this test the WalkIncomplete disjunct can be
// deleted with the suite still green — the whole mechanism was
// unfalsifiable.
//
// RED-on-revert: if the WalkIncomplete disjunct is dropped from the
// exit-code gate, runDoctor returns 0 and this test fails.
func TestRunDoctor_ExitCode_WalkIncomplete_Notifications(t *testing.T) {
	// Page 1: 2 rows, total_rows=5, has_more=true.
	// Page 2: 2 rows (ids 3,4), total_rows=5, has_more=false.
	// unique = 4 < firstTotal 5 → WalkIncomplete=true.
	// All rows are is_error=0 → Groups empty, UnparseableRows empty.
	notifPages := map[string]string{
		"1": `{"list":[
			{"id":1,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"},
			{"id":2,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"}
		],"total_rows":5,"is_has_more":true,"rows_limit":12}`,
		"2": `{"list":[
			{"id":3,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"},
			{"id":4,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"}
		],"total_rows":5,"is_has_more":false,"rows_limit":12}`,
	}
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorPaginatedAPIServer(t, notifPages, "", nil, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 7, true)
	if code == 0 {
		t.Fatalf("runDoctor exit code = 0, want 1 (WalkIncomplete with empty Groups and UnparseableRows must trigger exit 1); stdout=%s stderr=%s", out.String(), errOut.String())
	}
}

// TestRunDoctor_ExitCode_WalkIncomplete_Pages verifies the same gate
// fires when the PAGES walk is truncated (notifications walk is complete).
func TestRunDoctor_ExitCode_WalkIncomplete_Pages(t *testing.T) {
	notifications := `{"list":[
		{"id":1,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pagePages := map[string]string{
		"1": `{"list":[{"id":100,"source_id":1,"social_page_name":"A"}],"total_rows":3,"is_has_more":true,"rows_limit":20}`,
		"2": `{"list":[{"id":200,"source_id":1,"social_page_name":"B"}],"total_rows":3,"is_has_more":false,"rows_limit":20}`,
	}

	srv := stubDoctorPaginatedAPIServer(t, nil, notifications, pagePages, "")
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 7, true)
	if code == 0 {
		t.Fatalf("runDoctor exit code = 0, want 1 (pages walk truncated must trigger exit 1); stdout=%s stderr=%s", out.String(), errOut.String())
	}
}

// TestRunDoctor_ExitCode_BenignInsertNotFlagged verifies that a mid-walk
// insert (lastTotal > firstTotal) does NOT trigger exit 1 — the corrected
// semantics from finding 2. A healthy active account takes many sequential
// /notifications requests and an insert mid-walk is ordinary, not exotic.
// The old NewAllListEnvelope equality check would false-alarm here.
func TestRunDoctor_ExitCode_BenignInsertNotFlagged(t *testing.T) {
	// Page 1: 2 rows, total_rows=2, has_more=true → firstTotal=2.
	// Page 2: 2 rows (ids 3,4), total_rows=3, has_more=false → lastTotal=3.
	// unique = 4 >= firstTotal 2 → not truncated.
	// lastTotal 3 > firstTotal 2 → benign insert, do NOT flag.
	// All rows is_error=0 → Groups empty, UnparseableRows empty.
	notifPages := map[string]string{
		"1": `{"list":[
			{"id":1,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"},
			{"id":2,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"}
		],"total_rows":2,"is_has_more":true,"rows_limit":12}`,
		"2": `{"list":[
			{"id":3,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"},
			{"id":4,"is_error":0,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"ok"}
		],"total_rows":3,"is_has_more":false,"rows_limit":12}`,
	}
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorPaginatedAPIServer(t, notifPages, "", nil, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 7, true)
	if code != 0 {
		t.Fatalf("runDoctor exit code = %d, want 0 (benign mid-walk insert must NOT trigger exit 1); stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
}

// TestRunDoctor_ExitCode_SinceZero_NoWindow verifies that --since 0
// includes all dated rows (no window), so a 30-day-old error still
// triggers exit 1. The old code clamped sinceDays=0 to windowStart=now,
// dropping every dated row and reading "all clear" on a broken account.
func TestRunDoctor_ExitCode_SinceZero_NoWindow(t *testing.T) {
	old := vendorDate(time.Now().Add(-30 * 24 * time.Hour))
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"` + old + `","data":"Устарел ключ доступа"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorAPIServer(t, notifications, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, 0, true)
	if code == 0 {
		t.Fatalf("runDoctor exit code = 0, want 1 (--since 0 = no window, 30-day-old error must trigger exit 1); stdout=%s stderr=%s", out.String(), errOut.String())
	}
}

// TestRunDoctor_ExitCode_SinceNegative_Rejected verifies that a negative
// --since is rejected with a non-zero exit (RunDoctor returns an error,
// which runDoctor prints and exits 1). The old code silently clamped it
// to 0, the quietest configuration.
func TestRunDoctor_ExitCode_SinceNegative_Rejected(t *testing.T) {
	notifications := `{"list":[
		{"id":1,"is_error":1,"page_id":100,"source_id":1,"operation_date":"01.01.2026, 00:00","data":"error"}
	],"total_rows":1,"is_has_more":false,"rows_limit":12}`
	pages := `{"list":[{"id":100,"source_id":1,"social_page_name":"P"}],"total_rows":1,"is_has_more":false,"rows_limit":20}`

	srv := stubDoctorAPIServer(t, notifications, pages)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runDoctor(context.Background(), c, &out, &errOut, -1, true)
	if code == 0 {
		t.Fatalf("runDoctor exit code = 0, want 1 (negative --since must be rejected); stdout=%s stderr=%s", out.String(), errOut.String())
	}
	if errOut.Len() == 0 {
		t.Errorf("stderr empty, want an error message about --since")
	}
}
