package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// crossPostingsPage builds a JSON page body for /cross-posting with `count`
// rows of sequential ids, the given total_rows and is_has_more.
func crossPostingsPage(start, count, total int, hasMore bool) string {
	type row struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		State int    `json:"state"`
	}
	list := make([]row, 0, count)
	for i := 0; i < count; i++ {
		list = append(list, row{ID: start + i, Name: "cp", State: 0})
	}
	b, _ := json.Marshal(struct {
		List      []row `json:"list"`
		TotalRows int   `json:"total_rows"`
		IsHasMore bool  `json:"is_has_more"`
		RowsLimit int   `json:"rows_limit"`
	}{list, total, hasMore, 20})
	return string(b)
}

// TestRunListCrossPostings_TruncationWarning is the CLI half of the
// emitList convention: a single page with is_has_more=true exits 0, emits
// data on stdout, and warns on stderr naming total_rows, rows shown, --all.
func TestRunListCrossPostings_TruncationWarning(t *testing.T) {
	srv := stubPagedServer(t, "/cross-posting", map[string]string{
		"1": crossPostingsPage(1, 20, 40, true),
	})
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runListCrossPostings(context.Background(), c, &out, &errOut, 0, false)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — a truncated page is a complete answer, not an error; stderr=%s", code, errOut.String())
	}
	want := "40 cross-posting connections total — showing 20. Use --all to fetch every page."
	if !strings.Contains(errOut.String(), want) {
		t.Fatalf("stderr = %q, want it to contain %q", errOut.String(), want)
	}
	var env struct {
		List      []json.RawMessage `json:"list"`
		TotalRows int               `json:"total_rows"`
		IsHasMore bool              `json:"is_has_more"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
	if len(env.List) != 20 {
		t.Errorf("stdout list len = %d, want 20", len(env.List))
	}
}

// TestRunListCrossPostings_AllWalksEveryPage is the --all guard: two pages,
// 40 rows total; --all walks both and emits an AllListEnvelope with all 40.
func TestRunListCrossPostings_AllWalksEveryPage(t *testing.T) {
	srv := stubPagedServer(t, "/cross-posting", map[string]string{
		"1": crossPostingsPage(1, 20, 40, true),
		"2": crossPostingsPage(21, 20, 40, false),
	})
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runListCrossPostings(context.Background(), c, &out, &errOut, 0, true)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errOut.String())
	}
	var env struct {
		List []struct {
			ID int `json:"id"`
		} `json:"list"`
		TotalRows int  `json:"total_rows"`
		IsHasMore bool `json:"is_has_more"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
	if len(env.List) != 40 {
		t.Errorf("--all walked %d rows, want 40 (20+20 across two pages)", len(env.List))
	}
	if env.IsHasMore {
		t.Error("AllListEnvelope must pin is_has_more=false for a complete --all walk")
	}
}

// TestRunShowCrossPosting_EmitsDecodedEnumNames is the CLI half of the
// "decode, do not translate away" surface: show <id> emits the full raw body
// with decoded enum names injected alongside the raw integers.
func TestRunShowCrossPosting_EmitsDecodedEnumNames(t *testing.T) {
	// Build a 95-key /edit body with real enum values.
	var base map[string]json.RawMessage
	if err := json.Unmarshal([]byte(hooppyCrossPostingEditFixture), &base); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]int{
		"search_mode": 3, "determine_best_by": 4, "check_interval": 6,
	} {
		b, _ := json.Marshal(v)
		base[k] = b
	}
	body, _ := json.Marshal(base)

	srv := stubCrossPostingEditServer(t, 2899, string(body))
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runShowCrossPosting(context.Background(), c, &out, &errOut, 2899)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errOut.String())
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
	if string(m["search_mode"]) != "3" {
		t.Errorf("search_mode raw = %s, want 3 (raw integer must survive)", m["search_mode"])
	}
	if string(m["search_mode_name"]) != `"best"` {
		t.Errorf("search_mode_name = %s, want \"best\" (decoded name must be injected)", m["search_mode_name"])
	}
	if string(m["determine_best_by_name"]) != `"views"` {
		t.Errorf("determine_best_by_name = %s, want \"views\"", m["determine_best_by_name"])
	}
	if string(m["check_interval_name"]) != `"daily"` {
		t.Errorf("check_interval_name = %s, want \"daily\"", m["check_interval_name"])
	}
	// All 95 original keys preserved (plus the injected *_name keys).
	if len(m) < 95 {
		t.Errorf("emitted %d keys, want >= 95 (the full raw body must be preserved)", len(m))
	}
}

// TestRunShowCrossPosting_ZeroIDRefused is the fail-closed guard: id=0
// exits 1 without a request.
func TestRunShowCrossPosting_ZeroIDRefused(t *testing.T) {
	c := newDoctorTestClient(t, stubCrossPostingEditServer(t, 0, "{}"))
	var out, errOut bytes.Buffer
	code := runShowCrossPosting(context.Background(), c, &out, &errOut, 0)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for id=0", code)
	}
}

// TestRunCrossPostingStats_AllZeroIsRealData is the CLI half of F4: an
// all-zero statistics array is emitted as data (not an error), with no
// "absent data" note.
func TestRunCrossPostingStats_AllZeroIsRealData(t *testing.T) {
	srv := stubCrossPostingStatsServer(t, 2899, `{"statistics":[{"date":"25.07.2026","posts_found_amount":0,"posts_filtered_amount":0,"posts_duplicates_amount":0,"posts_taken_amount":0,"errors":0}]}`)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runCrossPostingStats(context.Background(), c, &out, &errOut, 2899)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if strings.Contains(errOut.String(), "absent data") {
		t.Errorf("stderr must NOT say 'absent data' for a non-empty all-zero array — that is a real measurement: %s", errOut.String())
	}
	var resp hooppy.CrossPostingStatisticsResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("stdout not valid JSON: %v", err)
	}
	if len(resp.Statistics) != 1 {
		t.Errorf("statistics len = %d, want 1", len(resp.Statistics))
	}
}

// TestRunCrossPostingStats_EmptyIsAbsentData is the CLI half of F4: an
// empty statistics array is emitted with a stderr note so an operator does
// not read it as "checked, found nothing".
func TestRunCrossPostingStats_EmptyIsAbsentData(t *testing.T) {
	srv := stubCrossPostingStatsServer(t, 2899, `{"statistics":[]}`)
	defer srv.Close()
	c := newDoctorTestClient(t, srv)

	var out, errOut bytes.Buffer
	code := runCrossPostingStats(context.Background(), c, &out, &errOut, 2899)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (absent data is not an error); stderr=%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "absent data") {
		t.Errorf("stderr must note 'absent data' for an empty statistics array: %s", errOut.String())
	}
}
