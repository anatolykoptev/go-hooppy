package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-kit/cli"
	"github.com/spf13/cobra"
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

// --- search subcommand tests (findings 1, 5, 6) ---

// newSearchRoot builds a command tree with the search subcommands registered,
// for flag-registration inspection. The Run closures call os.Exit via
// mustClient/die, so this helper is ONLY used to inspect registered flags —
// never to Execute (which would exit the test process on a valid flag set).
func newSearchRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := cli.NewRoot(cli.RootConfig{Use: "hooppy", Short: "test"})
	registerSearch(root)
	return root
}

// findSub walks a command tree by a sequence of names and returns the leaf,
// or fails the test if any name is missing.
func findSub(t *testing.T, root *cobra.Command, names ...string) *cobra.Command {
	t.Helper()
	cur := root
	for _, n := range names {
		cmd, _, err := cur.Find([]string{n})
		if err != nil || cmd == nil || cmd.Name() != n {
			t.Fatalf("subcommand %q not found under %q: %v", n, cur.Name(), err)
		}
		cur = cmd
	}
	return cur
}

// TestSearchCopy_NoPostIDsFlag verifies the BLOCKER fix: `search copy` does
// NOT register --post-ids. PUT /posts/copy takes a singular search_post_id
// int; the batch slice is silently dropped on that endpoint (the server does
// not read search_post_ids), so --post-ids was a phantom affordance that
// posted search_post_id:0 with an unread array and no error. Removing the
// flag makes cobra reject `--post-ids` with a non-zero exit (unknown flag)
// before any request — the user-observable consequence this test guards.
//
// RED-on-revert: reintroduce the --post-ids flag on copyCmd and Lookup
// returns non-nil → this test fails.
func TestSearchCopy_NoPostIDsFlag(t *testing.T) {
	root := newSearchRoot(t)
	copyCmd := findSub(t, root, "search", "copy")
	if f := copyCmd.Flags().Lookup("post-ids"); f != nil {
		t.Fatal("search copy registers --post-ids — PUT /posts/copy takes a single search_post_id; the batch slice is silently dropped (phantom affordance). Use 'search rewrite' or 'search import' for a batch.")
	}
	// Sanity: --post-id is still registered (the single-post path is valid).
	if f := copyCmd.Flags().Lookup("post-id"); f == nil {
		t.Fatal("search copy is missing --post-id (the single-post flag must remain)")
	}
}

// TestSearchRewriteImport_PostIDsFlagRegistered verifies the two endpoints
// that DO support batch (rewrite POST /posts with as_copy=1, import
// PUT /posts/import) still expose --post-ids — the fix removed it from copy
// only, not from the endpoints whose ids wire field accepts a batch.
func TestSearchRewriteImport_PostIDsFlagRegistered(t *testing.T) {
	root := newSearchRoot(t)
	for _, sub := range []string{"rewrite", "import"} {
		cmd := findSub(t, root, "search", sub)
		if f := cmd.Flags().Lookup("post-ids"); f == nil {
			t.Errorf("search %s is missing --post-ids (batch is supported on this endpoint)", sub)
		}
	}
}

// TestSearchBuilders_FlagValidation is a table test over the three search
// subcommand builders covering flag validation. Each row asserts the builder
// returns an error for an invalid flag combination AND that the error names
// the intended cause (errSub) — a row that only asserted err == nil could
// pass on a DIFFERENT error than the one it targets (finding 5d). The
// validation previously lived inline in the Run closures (untestable because
// they os.Exit). Findings 1, 4, 5 all live in this previously-untested half.
func TestSearchBuilders_FlagValidation(t *testing.T) {
	cases := []struct {
		name   string
		errSub string
		fn     func() error
	}{
		{"copy: --post-id required", "--post-id is required", func() error {
			_, err := buildCopyPayload(0, 1, 1, "123", "", "", "", "")
			return err
		}},
		{"copy: when-type 2 needs date", "--date, --hours, --minutes are required", func() error {
			_, err := buildCopyPayload(1001, 2, 1, "123", "", "", "", "")
			return err
		}},
		{"copy: when-type 3 needs schedules", "--schedules is required", func() error {
			_, err := buildCopyPayload(1001, 3, 1, "", "", "", "", "")
			return err
		}},
		{"rewrite: --post-id and --post-ids mutually exclusive", "mutually exclusive", func() error {
			_, err := buildRewritePayload(1001, "2001,2002", "x", 1, 1, "123", "", "", "", "")
			return err
		}},
		{"rewrite: one id required", "--post-id or --post-ids is required", func() error {
			_, err := buildRewritePayload(0, "", "x", 1, 1, "123", "", "", "", "")
			return err
		}},
		{"rewrite: single-post --text required", "--text is required for --post-id", func() error {
			_, err := buildRewritePayload(1001, "", "", 1, 1, "123", "", "", "", "")
			return err
		}},
		{"rewrite: when-type 3 needs schedules", "--schedules is required", func() error {
			_, err := buildRewritePayload(1001, "", "x", 3, 1, "", "", "", "", "")
			return err
		}},
		// Finding 4: batch rewrite cannot express per-post text.
		{"rewrite: batch + --text refused", "not allowed with --post-ids", func() error {
			_, err := buildRewritePayload(0, "2001,2002", "x", 1, 1, "123", "", "", "", "")
			return err
		}},
		{"import: --post-id and --post-ids mutually exclusive", "mutually exclusive", func() error {
			_, err := buildImportPayload(1001, "2001,2002", 3, 2, "999")
			return err
		}},
		{"import: one id required", "--post-id or --post-ids is required", func() error {
			_, err := buildImportPayload(0, "", 3, 2, "999")
			return err
		}},
		{"import: when-type 3 needs schedules", "--schedules is required", func() error {
			_, err := buildImportPayload(1001, "", 3, 2, "")
			return err
		}},
		{"import: invalid id token", "invalid ID", func() error {
			_, err := buildImportPayload(0, "2001,abc", 3, 2, "999")
			return err
		}},
		// Finding 5b: empty element is a typo, not a silent drop (unified with
		// the MCP strict parser).
		{"import: empty element in id list", "empty element", func() error {
			_, err := buildImportPayload(0, "2001,,2003", 3, 2, "999")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error %q does not name the intended cause %q — a different error passed for this row (finding 5d)", err.Error(), tc.errSub)
			}
		})
	}
}

// TestBuildScheduleTimesOutput_OrderedArray verifies that `schedules times`
// emits the week as an ORDERED array (Mon..Sun), not a map. A map would be
// re-sorted alphabetically by encoding/json (Fri,Mon,Sat,Sun,Thu,Tue,Wed) —
// a structure whose entire meaning is its order, emitted in an order nobody
// reads a week in. The fix is structural: each element carries its day name,
// so the ordering cannot be re-sorted by a marshaller.
//
// The fixture has slots on days 0 (Mon), 2 (Wed), 3 (Thu) only — days 1,
// 4, 5, 6 are empty. The test asserts:
//   - the marshalled output is a 7-element JSON array;
//   - empty days carry "slots": [] (non-nil), NOT null;
//   - byte-order: Mon appears before Tue before Wed in the marshalled
//     string — a byte-order assertion, NOT a decode-into-map comparison
//     (decoding into a map is exactly what would let this regress unnoticed).
//
// RED-on-revert: revert buildScheduleTimesOutput to return
// map[string][]map[string]int64 and the byte-order assertion fails
// (encoding/json emits Fri before Mon before Sat…).
func TestBuildScheduleTimesOutput_OrderedArray(t *testing.T) {
	edit := &hooppy.ScheduleEditResponse{
		ID:   42,
		Name: "S",
		Times: [][]hooppy.ScheduleTimeSlot{
			{{Hours: flexInt(12), Minutes: flexInt(25)}, {Hours: flexInt(14), Minutes: flexInt(25)}}, // Mon
			{}, // Tue (empty)
			{{Hours: flexInt(9), Minutes: flexInt(0)}},   // Wed
			{{Hours: flexInt(18), Minutes: flexInt(30)}}, // Thu
			{}, // Fri (empty)
			{}, // Sat (empty)
			{}, // Sun (empty)
		},
	}
	out := buildScheduleTimesOutput(edit)
	// Marshal via the result directly — Go infers the return type, so a
	// revert to map[string][]... still compiles, marshals, and fails the
	// byte-order assertion below (encoding/json sorts map keys). This is
	// intentional: the test must FAIL on the regression, not fail to compile.
	marshalled, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(marshalled)

	// The output must be a JSON array (starts with '['), not an object.
	if len(got) == 0 || got[0] != '[' {
		t.Fatalf("output is not a JSON array, got:\n%s", got)
	}

	// 7 day markers must be present.
	for _, day := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		if !strings.Contains(got, fmt.Sprintf(`"day":"%s"`, day)) {
			t.Errorf("day %q missing in output:\n%s", day, got)
		}
	}

	// Empty days (Tue, Fri, Sat, Sun) must carry "slots":[] not null.
	for _, day := range []string{"Tue", "Fri", "Sat", "Sun"} {
		wantSub := fmt.Sprintf(`"day":"%s","slots":[]`, day)
		if !strings.Contains(got, wantSub) {
			t.Errorf("empty day %s: expected %q, got:\n%s", day, wantSub, got)
		}
		nullSub := fmt.Sprintf(`"day":"%s","slots":null`, day)
		if strings.Contains(got, nullSub) {
			t.Errorf("empty day %s: slots is null, want [] — got:\n%s", day, got)
		}
	}

	// Byte-order assertion: Mon must appear before Tue before Wed in the
	// raw marshalled bytes. A decode-into-map comparison would let a map
	// regression pass unnoticed (maps have no order); this does not.
	monPos := strings.Index(got, `"day":"Mon"`)
	tuePos := strings.Index(got, `"day":"Tue"`)
	wedPos := strings.Index(got, `"day":"Wed"`)
	if monPos < 0 || tuePos < 0 || wedPos < 0 {
		t.Fatalf("missing day markers in output:\n%s", got)
	}
	if !(monPos < tuePos && tuePos < wedPos) {
		t.Errorf("byte-order violation: Mon(%d) < Tue(%d) < Wed(%d) does not hold in:\n%s", monPos, tuePos, wedPos, got)
	}

	// Sanity: the slots on Mon/Wed/Thu survived.
	if !strings.Contains(got, `"hours":12`) {
		t.Errorf("Mon slot hours=12 missing in:\n%s", got)
	}
}

// flexInt builds a FlexInt from an int64 for test fixtures.
func flexInt(v int64) hooppy.FlexInt {
	var f hooppy.FlexInt
	_ = json.Unmarshal([]byte(fmt.Sprintf("%d", v)), &f)
	return f
}

// TestSearchBuilders_PayloadConstruction is a table test over the three
// search subcommand builders covering payload construction — the previously
// untested half where findings 1 and 5 lived. Asserts the exact payload
// fields that reach the wire, not just err == nil.
func TestSearchBuilders_PayloadConstruction(t *testing.T) {
	t.Run("copy single-post builds scalar payload", func(t *testing.T) {
		p, err := buildCopyPayload(1001, 1, 1, "123,456", "", "", "", "")
		if err != nil {
			t.Fatalf("buildCopyPayload: %v", err)
		}
		if p.SearchPostID != 1001 {
			t.Errorf("SearchPostID = %d, want 1001", p.SearchPostID)
		}
		if len(p.SearchPostIDs) != 0 {
			t.Errorf("SearchPostIDs = %v, want empty (copy is single-post only)", p.SearchPostIDs)
		}
		if got, want := p.SelectedPagesIDs, []int{123, 456}; !reflect.DeepEqual(got, want) {
			t.Errorf("SelectedPagesIDs = %v, want %v", got, want)
		}
	})

	t.Run("rewrite batch builds slice payload in caller order, no text override", func(t *testing.T) {
		// Finding 4: batch rewrite cannot express per-post text, so --text is
		// rejected with --post-ids and --post-ids alone sends an empty Texts
		// slice (the server keeps each post's original text, like import).
		p, err := buildRewritePayload(0, "2003,2001,2002", "", 1, 1, "123", "", "", "", "")
		if err != nil {
			t.Fatalf("buildRewritePayload: %v", err)
		}
		if p.SearchPostID != 0 {
			t.Errorf("SearchPostID = %d, want 0 (batch uses the slice)", p.SearchPostID)
		}
		if got, want := p.SearchPostIDs, []int{2003, 2001, 2002}; !reflect.DeepEqual(got, want) {
			t.Errorf("SearchPostIDs = %v, want %v (caller order preserved)", got, want)
		}
		if p.Texts == nil {
			t.Fatal("Texts = nil, want []PostText{} (empty non-nil) — nil would be normalised by RewriteSearchPost, but the contract is an explicit empty slice so the server keeps original text (finding 4)")
		}
		if len(p.Texts) != 0 {
			t.Errorf("Texts = %v, want [] (batch rewrite sends no text override; NOT [{\"\"}] which risks publishing blank)", p.Texts)
		}
	})

	t.Run("import batch sends EMPTY texts slice (keeps original text)", func(t *testing.T) {
		// Finding 5: batch import must send []PostText{} (empty, non-nil) so
		// ImportSearchPost's nil-normalisation leaves it as-is and the server
		// keeps each post's original text. The old code sent
		// []PostText{{Text: ""}} — an explicit empty-text entry that risks
		// publishing blank across the whole batch.
		p, err := buildImportPayload(0, "3001,3002", 3, 2, "999")
		if err != nil {
			t.Fatalf("buildImportPayload: %v", err)
		}
		if got, want := p.SearchPostIDs, []int{3001, 3002}; !reflect.DeepEqual(got, want) {
			t.Errorf("SearchPostIDs = %v, want %v", got, want)
		}
		if p.Texts == nil {
			t.Fatal("Texts = nil, want []PostText{} (empty non-nil) — nil would be normalised by ImportSearchPost, but the contract is an explicit empty slice so the server keeps original text")
		}
		if len(p.Texts) != 0 {
			t.Errorf("Texts = %v, want [] (empty slice, NOT [{\"\"}] — an explicit empty-text entry risks publishing blank)", p.Texts)
		}
	})

	t.Run("import single-post leaves Texts nil for Run to fill", func(t *testing.T) {
		p, err := buildImportPayload(1001, "", 3, 2, "999")
		if err != nil {
			t.Fatalf("buildImportPayload: %v", err)
		}
		if p.SearchPostID != 1001 {
			t.Errorf("SearchPostID = %d, want 1001", p.SearchPostID)
		}
		if p.Texts != nil {
			t.Errorf("Texts = %v, want nil (Run fills from GetSearchPostEdit in single-post mode)", p.Texts)
		}
	})
}
