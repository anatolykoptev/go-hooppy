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
