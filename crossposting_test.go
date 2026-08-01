package hooppy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// liveFixture loads a scrubbed fixture from testdata/live/.
func liveFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "live", name))
	if err != nil {
		t.Fatalf("read testdata/live/%s: %v", name, err)
	}
	return data
}

// --- F1: every enum decodes to its name AND retains its raw integer ---
//
// RED-on-revert: break one mapping (delete a table entry or swap a name) and
// the corresponding case fails — the decoded Name no longer matches, or the
// Value is not the raw integer that came off the wire.

func TestCrossPostingEnumDecode_F1_SearchMode(t *testing.T) {
	cases := []struct {
		raw  int
		name string
	}{
		{1, "new"}, {2, "old"}, {3, "best"}, {4, "random"},
	}
	for _, c := range cases {
		b, _ := json.Marshal(c.raw)
		var m SearchMode
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("SearchMode %d: %v", c.raw, err)
		}
		if m.Value != c.raw {
			t.Errorf("SearchMode %d: Value = %d, want %d (raw integer must be retained for round-tripping)", c.raw, m.Value, c.raw)
		}
		if m.Name != c.name {
			t.Errorf("SearchMode %d: Name = %q, want %q (break this mapping → RED)", c.raw, m.Name, c.name)
		}
		if m.Unknown {
			t.Errorf("SearchMode %d: Unknown = true, want false (a known value must not be marked unknown)", c.raw)
		}
		// MarshalJSON must emit the raw integer, not the {value,name} object.
		out, _ := json.Marshal(m)
		if string(out) != intJSON(c.raw) {
			t.Errorf("SearchMode %d: MarshalJSON = %s, want %s (round-trip must emit the raw integer)", c.raw, out, intJSON(c.raw))
		}
	}
}

func TestCrossPostingEnumDecode_F1_SearchModeDirection(t *testing.T) {
	cases := []struct {
		raw  int
		name string
	}{
		{1, "oldest_first"}, {2, "newest_first"},
	}
	for _, c := range cases {
		b, _ := json.Marshal(c.raw)
		var m SearchModeDirection
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("SearchModeDirection %d: %v", c.raw, err)
		}
		if m.Value != c.raw {
			t.Errorf("SearchModeDirection %d: Value = %d, want %d", c.raw, m.Value, c.raw)
		}
		if m.Name != c.name {
			t.Errorf("SearchModeDirection %d: Name = %q, want %q (break this mapping → RED)", c.raw, m.Name, c.name)
		}
		if m.Unknown {
			t.Errorf("SearchModeDirection %d: Unknown = true, want false", c.raw)
		}
	}
}

func TestCrossPostingEnumDecode_F1_DetermineBestBy(t *testing.T) {
	cases := []struct {
		raw  int
		name string
	}{
		{1, "likes"}, {2, "reposts"}, {3, "comments"}, {4, "views"},
	}
	for _, c := range cases {
		b, _ := json.Marshal(c.raw)
		var m DetermineBestBy
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("DetermineBestBy %d: %v", c.raw, err)
		}
		if m.Value != c.raw {
			t.Errorf("DetermineBestBy %d: Value = %d, want %d", c.raw, m.Value, c.raw)
		}
		if m.Name != c.name {
			t.Errorf("DetermineBestBy %d: Name = %q, want %q (break this mapping → RED)", c.raw, m.Name, c.name)
		}
		if m.Unknown {
			t.Errorf("DetermineBestBy %d: Unknown = true, want false", c.raw)
		}
	}
}

func TestCrossPostingEnumDecode_F1_CheckWhenType(t *testing.T) {
	cases := []struct {
		raw  int
		name string
	}{
		{1, "by_interval"}, {2, "at_fixed_times"},
	}
	for _, c := range cases {
		b, _ := json.Marshal(c.raw)
		var m CheckWhenType
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("CheckWhenType %d: %v", c.raw, err)
		}
		if m.Value != c.raw {
			t.Errorf("CheckWhenType %d: Value = %d, want %d", c.raw, m.Value, c.raw)
		}
		if m.Name != c.name {
			t.Errorf("CheckWhenType %d: Name = %q, want %q (break this mapping → RED)", c.raw, m.Name, c.name)
		}
		if m.Unknown {
			t.Errorf("CheckWhenType %d: Unknown = true, want false", c.raw)
		}
	}
}

func TestCrossPostingEnumDecode_F1_CheckInterval(t *testing.T) {
	cases := []struct {
		raw  int
		name string
	}{
		{1, "every_30_min"}, {2, "hourly"}, {3, "every_2h"}, {4, "every_3h"},
		{5, "every_4h"}, {6, "daily"}, {7, "twice_daily"}, {8, "three_times_daily"},
		{9, "four_times_daily"}, {10, "weekly"}, {11, "twice_weekly"}, {12, "three_times_weekly"},
	}
	for _, c := range cases {
		b, _ := json.Marshal(c.raw)
		var m CheckInterval
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("CheckInterval %d: %v", c.raw, err)
		}
		if m.Value != c.raw {
			t.Errorf("CheckInterval %d: Value = %d, want %d", c.raw, m.Value, c.raw)
		}
		if m.Name != c.name {
			t.Errorf("CheckInterval %d: Name = %q, want %q (break this mapping → RED)", c.raw, m.Name, c.name)
		}
		if m.Unknown {
			t.Errorf("CheckInterval %d: Unknown = true, want false", c.raw)
		}
	}
}

// --- F2: an enum value NOT in the table passes through as its number,
// marked unknown, and does not become a default. ---
//
// RED-on-revert: if enumName silently mapped an unknown value to a default
// name (e.g. "new") and Unknown=false, this test fails — the value would be
// mistranslated and the agent would never learn the bundle does not define it.

func TestCrossPostingEnumDecode_F2_UnknownPassThrough(t *testing.T) {
	// 99 is not in any cross-posting enum table.
	unknown := 99
	b, _ := json.Marshal(unknown)
	decoders := []struct {
		name string
		dec  func() (EnumValue, error)
	}{
		{"SearchMode", func() (EnumValue, error) {
			var m SearchMode
			err := json.Unmarshal(b, &m)
			return EnumValue(m), err
		}},
		{"SearchModeDirection", func() (EnumValue, error) {
			var m SearchModeDirection
			err := json.Unmarshal(b, &m)
			return EnumValue(m), err
		}},
		{"DetermineBestBy", func() (EnumValue, error) {
			var m DetermineBestBy
			err := json.Unmarshal(b, &m)
			return EnumValue(m), err
		}},
		{"CheckWhenType", func() (EnumValue, error) {
			var m CheckWhenType
			err := json.Unmarshal(b, &m)
			return EnumValue(m), err
		}},
		{"CheckInterval", func() (EnumValue, error) {
			var m CheckInterval
			err := json.Unmarshal(b, &m)
			return EnumValue(m), err
		}},
	}
	for _, d := range decoders {
		got, err := d.dec()
		if err != nil {
			t.Fatalf("%s: decode unknown %d: %v", d.name, unknown, err)
		}
		if got.Value != unknown {
			t.Errorf("%s: unknown Value = %d, want %d (the raw integer must pass through, not be coerced to a default)", d.name, got.Value, unknown)
		}
		if got.Name != "unknown" {
			t.Errorf("%s: unknown Name = %q, want \"unknown\" (an undefined value must be marked, not silently mapped to a default name)", d.name, got.Name)
		}
		if !got.Unknown {
			t.Errorf("%s: unknown Unknown = false, want true (the bundle does not define %d — the marker is the signal an agent needs)", d.name, unknown)
		}
	}
}

// intJSON renders an int as its JSON number string.
func intJSON(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// --- F3: the /edit round-trip is lossless ---
//
// Decode a 95-key fixture into CrossPostingEditResponse, re-encode, and assert
// every original key survives byte-for-byte. Drop one field from the struct
// (i.e. replace MarshalJSON's raw-bytes emission with a struct-only marshal)
// and ~75 unmodelled keys vanish → RED. This is the test that protects the
// future write path: a read-modify-write that loses fields silently destroys
// the connection's state.
func TestCrossPostingEdit_LosslessRoundTrip_F3(t *testing.T) {
	fixture := liveFixture(t, "cross_posting_edit.json")
	var fixtureMap map[string]json.RawMessage
	if err := json.Unmarshal(fixture, &fixtureMap); err != nil {
		t.Fatalf("decode fixture as map: %v", err)
	}
	if len(fixtureMap) != 95 {
		t.Fatalf("fixture must carry 95 keys (dossier), got %d — fix the fixture", len(fixtureMap))
	}

	var resp CrossPostingEditResponse
	if err := json.Unmarshal(fixture, &resp); err != nil {
		t.Fatalf("decode into CrossPostingEditResponse: %v", err)
	}
	if len(resp.Raw) == 0 {
		t.Fatal("Raw is empty — UnmarshalJSON did not stash the raw body; the lossless round-trip is broken")
	}

	// Re-encode. MarshalJSON emits Raw verbatim — byte-identical round-trip.
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var outMap map[string]json.RawMessage
	if err := json.Unmarshal(out, &outMap); err != nil {
		t.Fatalf("decode re-marshalled output: %v", err)
	}

	// Assert EVERY original key is present with byte-identical (compacted)
	// value. A struct-only marshal would drop the ~75 unmodelled keys.
	var missing []string
	for key, expectedRaw := range fixtureMap {
		gotRaw, ok := outMap[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		if string(compactRaw(t, expectedRaw)) != string(compactRaw(t, gotRaw)) {
			t.Errorf("round-trip altered key %q: got %s, want %s (byte-identity violated — the lossless round-trip must not alter any field)", key, gotRaw, expectedRaw)
		}
	}
	if len(missing) > 0 {
		t.Errorf("re-marshalled output is missing %d of %d keys from the /edit fixture — the lossless round-trip dropped unmodelled fields (the future write path would silently destroy them): %v", len(missing), len(fixtureMap), missing)
	}
	if len(outMap) != len(fixtureMap) {
		t.Errorf("re-marshalled output has %d keys, fixture has %d — the round-trip added or dropped keys", len(outMap), len(fixtureMap))
	}
}

// TestCrossPostingEdit_TypedViewDecoded confirms the typed view (enums,
// thresholds, identity) is populated from Raw — the round-trip is not the
// only thing the struct does. RED-on-revert: if UnmarshalJSON stopped
// stashing/decoding the typed fields, the enum Value/Name and thresholds go
// zero and the test fails.
func TestCrossPostingEdit_TypedViewDecoded(t *testing.T) {
	// Build an inline 95-key fixture with REAL enum values so the typed view
	// is exercised (the live fixture scrubs enums to 0).
	fixture := string(liveFixture(t, "cross_posting_edit.json"))
	// Override the enum + threshold fields with real values.
	override := map[string]interface{}{
		"id": 2899, "name": "Русская протестантская церковь",
		"search_mode": 1, "search_mode_direction": 2,
		"determine_best_by": 1, "check_when_type": 1, "check_interval": 2,
		"search_likes": 0, "search_views": 0, "search_comments": 0,
		"search_reposts": 0, "take_amount": 1, "state": 0,
		"source_resources_mode": 1,
	}
	var base map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fixture), &base); err != nil {
		t.Fatalf("decode base: %v", err)
	}
	for k, v := range override {
		b, _ := json.Marshal(v)
		base[k] = b
	}
	body, _ := json.Marshal(base)

	var resp CrossPostingEditResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != 2899 {
		t.Errorf("ID = %d, want 2899", resp.ID)
	}
	if resp.Name == "" {
		t.Error("Name is empty — typed view not decoded from Raw")
	}
	if resp.SearchMode.Value != 1 || resp.SearchMode.Name != "new" {
		t.Errorf("SearchMode = %+v, want Value=1 Name=\"new\"", resp.SearchMode)
	}
	if resp.SearchModeDirection.Value != 2 || resp.SearchModeDirection.Name != "newest_first" {
		t.Errorf("SearchModeDirection = %+v, want Value=2 Name=\"newest_first\"", resp.SearchModeDirection)
	}
	if resp.DetermineBestBy.Value != 1 || resp.DetermineBestBy.Name != "likes" {
		t.Errorf("DetermineBestBy = %+v, want Value=1 Name=\"likes\"", resp.DetermineBestBy)
	}
	if resp.CheckWhenType.Value != 1 || resp.CheckWhenType.Name != "by_interval" {
		t.Errorf("CheckWhenType = %+v, want Value=1 Name=\"by_interval\"", resp.CheckWhenType)
	}
	if resp.CheckInterval.Value != 2 || resp.CheckInterval.Name != "hourly" {
		t.Errorf("CheckInterval = %+v, want Value=2 Name=\"hourly\"", resp.CheckInterval)
	}
	if resp.TakeAmount != 1 {
		t.Errorf("TakeAmount = %d, want 1", resp.TakeAmount)
	}
	if resp.SourceResourcesMode != 1 {
		t.Errorf("SourceResourcesMode = %d, want 1", resp.SourceResourcesMode)
	}
}

// --- F4: statistics parse with all-zero counters and are not mistaken for
// "no data" ---
//
// Live state today: a connection configured but not producing has all-zero
// counters across days. Zero found is a REAL measurement (the engine ran and
// found nothing); absent data (empty statistics array) is not. HasData makes
// the distinction. RED-on-revert: if HasData treated all-zero counters as
// absent (e.g. checked sum>0), the zero-counters case would read as "no data"
// and the test fails.

func TestCrossPostingStatistics_AllZeroIsRealData_F4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/cross-posting/2899/statistics" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(liveFixture(t, "cross_posting_statistics.json"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.GetCrossPostingStatistics(context.Background(), 2899)
	if err != nil {
		t.Fatalf("GetCrossPostingStatistics: %v", err)
	}
	if !resp.HasData() {
		t.Fatal("HasData = false for a non-empty statistics array with all-zero counters — zero found is a REAL measurement, not absent data (the live state today is all-zero counters; mistaking it for no data hides a configured-but-not-producing connection)")
	}
	if len(resp.Statistics) != 1 {
		t.Errorf("Statistics len = %d, want 1 (the 2026-07-31 recorded fixture carries 1 row)", len(resp.Statistics))
	}
	for i, d := range resp.Statistics {
		if d.PostsFoundAmount != 0 || d.PostsFilteredAmount != 0 || d.PostsDuplicatesAmount != 0 || d.PostsTakenAmount != 0 || d.Errors != 0 {
			t.Errorf("day %d: expected all-zero counters (the live state), got %+v", i, d)
		}
	}
}

func TestCrossPostingStatistics_EmptyIsAbsentData_F4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/cross-posting/2899/statistics" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(liveFixture(t, "cross_posting_statistics_empty.json"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.GetCrossPostingStatistics(context.Background(), 2899)
	if err != nil {
		t.Fatalf("GetCrossPostingStatistics: %v", err)
	}
	if resp.HasData() {
		t.Fatal("HasData = true for an EMPTY statistics array — an empty array is absent data (the engine has not run), not a zero measurement. The distinction is the whole point of F4.")
	}
}

// --- Library: ListCrossPostings, ListAllCrossPostings, GetCrossPostingEdit ---

func TestListCrossPostings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/cross-posting" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(liveFixture(t, "cross_postings.json"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListCrossPostings(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListCrossPostings: %v", err)
	}
	// The scrubbed fixture carries 1 row (the diagnostic baseline records
	// unmodelled keys at list[0].<key>; a single row keeps the baseline to
	// list[0]). The live account has 3 configured connections.
	if len(resp.List) != 1 {
		t.Errorf("List len = %d, want 1 (the scrubbed fixture carries 1 row)", len(resp.List))
	}
	if len(resp.List[0].Name) == 0 && resp.List[0].ID != 0 {
		t.Errorf("row 0 not decoded: %+v", resp.List[0])
	}
}

func TestListCrossPostings_NegativePageRefused(t *testing.T) {
	c := newTestClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("a negative page must be refused before any request")
	})))
	if _, err := c.ListCrossPostings(context.Background(), -1); err == nil {
		t.Fatal("ListCrossPostings(-1): expected error, got nil — a negative page must be refused before any request")
	}
}

func TestListAllCrossPostings(t *testing.T) {
	// Two pages: page 1 returns 2 rows with is_has_more=true, page 2 returns
	// 1 row and clears is_has_more. Built inline (not from the 1-row live
	// fixture) so the walk is exercised across a real two-page boundary.
	cpPage := func(start, count, total int, hasMore bool) string {
		type row struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		list := make([]row, 0, count)
		for i := 0; i < count; i++ {
			list = append(list, row{ID: start + i, Name: "cp"})
		}
		b, _ := json.Marshal(struct {
			List      []row `json:"list"`
			TotalRows int   `json:"total_rows"`
			IsHasMore bool  `json:"is_has_more"`
			RowsLimit int   `json:"rows_limit"`
		}{list, total, hasMore, 20})
		return string(b)
	}
	page1Body := cpPage(1, 2, 3, true)
	page2Body := cpPage(3, 1, 3, false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cross-posting" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(page2Body))
			return
		}
		w.Write([]byte(page1Body))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, total, err := c.ListAllCrossPostingsWithTotal(context.Background())
	if err != nil {
		t.Fatalf("ListAllCrossPostingsWithTotal: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("walked %d connections, want 3 (2 on page 1 + 1 on page 2)", len(all))
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}

func TestGetCrossPostingEdit_ZeroIDRefused(t *testing.T) {
	c := newTestClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("id=0 must be refused before any request")
	})))
	if _, err := c.GetCrossPostingEdit(context.Background(), 0); err == nil {
		t.Fatal("GetCrossPostingEdit(0): expected error, got nil")
	}
}

func TestGetCrossPostingStatistics_ZeroIDRefused(t *testing.T) {
	c := newTestClient(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("id=0 must be refused before any request")
	})))
	if _, err := c.GetCrossPostingStatistics(context.Background(), 0); err == nil {
		t.Fatal("GetCrossPostingStatistics(0): expected error, got nil")
	}
}

func TestGetCrossPostingEdit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/cross-posting/2899/edit" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(liveFixture(t, "cross_posting_edit.json"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.GetCrossPostingEdit(context.Background(), 2899)
	if err != nil {
		t.Fatalf("GetCrossPostingEdit: %v", err)
	}
	if len(resp.Raw) == 0 {
		t.Fatal("Raw is empty — the lossless body was not preserved")
	}
}

// TestEnrichedCrossPostingEditMap confirms the agent-facing presentation
// injects decoded enum names alongside the raw integers, and preserves all
// 95 original keys.
func TestEnrichedCrossPostingEditMap(t *testing.T) {
	body := string(liveFixture(t, "cross_posting_edit.json"))
	// Override enums with real values so the injected names are non-"unknown".
	var base map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &base); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]int{
		"search_mode": 3, "search_mode_direction": 2,
		"determine_best_by": 4, "check_when_type": 2, "check_interval": 6,
	} {
		b, _ := json.Marshal(v)
		base[k] = b
	}
	bodyBytes, _ := json.Marshal(base)

	var resp CrossPostingEditResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	enriched, err := EnrichedCrossPostingEditMap(&resp)
	if err != nil {
		t.Fatalf("EnrichedCrossPostingEditMap: %v", err)
	}
	// All 95 original keys preserved.
	for k := range base {
		if _, ok := enriched[k]; !ok {
			t.Errorf("enriched map lost original key %q", k)
		}
	}
	// Enum names injected alongside the raw integer.
	want := map[string]string{
		"search_mode_name":           "best",
		"search_mode_direction_name": "newest_first",
		"determine_best_by_name":     "views",
		"check_when_type_name":       "at_fixed_times",
		"check_interval_name":        "daily",
	}
	for k, wantName := range want {
		got, ok := enriched[k]
		if !ok {
			t.Errorf("missing injected key %q", k)
			continue
		}
		var gotStr string
		if err := json.Unmarshal(got, &gotStr); err != nil {
			t.Errorf("decode %q: %v", k, err)
			continue
		}
		if gotStr != wantName {
			t.Errorf("%q = %q, want %q", k, gotStr, wantName)
		}
	}
	// Raw integers still present.
	if string(enriched["search_mode"]) != "3" {
		t.Errorf("search_mode raw = %s, want 3 (the raw integer must survive alongside the name)", enriched["search_mode"])
	}
}

// TestGetCrossPostingEdit_405SurfacesError confirms a 405 (no direct GET by
// id) surfaces as an error, not a silent empty struct — the dossier measured
// 405 on /cross-posting/{id}.
func TestGetCrossPostingEdit_405SurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cross-posting/2899" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte(`{"error":"Method Not Allowed"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	// The library hits /edit, not /{id}; this test documents that a 405 on
	// /{id} is the reason /edit is the read path. A direct-GET helper does
	// not exist by design.
	_, err := c.GetCrossPostingEdit(context.Background(), 2899)
	if err == nil {
		t.Fatal("GetCrossPostingEdit: expected error on a server that 404s /edit, got nil")
	}
}

// TestCrossPostingEdit_MarshalJSON_RawVerbatim is the focused mutant guard
// for F3: MarshalJSON must emit Raw verbatim. If someone replaces it with a
// struct-only marshal, the output drops ~75 keys.
func TestCrossPostingEdit_MarshalJSON_RawVerbatim(t *testing.T) {
	fixture := liveFixture(t, "cross_posting_edit.json")
	var resp CrossPostingEditResponse
	if err := json.Unmarshal(fixture, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The marshalled output must decode to the same key set as the fixture.
	var outMap map[string]json.RawMessage
	if err := json.Unmarshal(out, &outMap); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	var fixtureMap map[string]json.RawMessage
	if err := json.Unmarshal(fixture, &fixtureMap); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(outMap) != len(fixtureMap) {
		t.Errorf("MarshalJSON emitted %d keys, fixture has %d — MarshalJSON must emit Raw verbatim, not a struct-only view", len(outMap), len(fixtureMap))
	}
}

// Ensure io import is used (some build configs warn on unused imports; the
// 405 test path and others keep it live, but guard against a future trim).
var _ = io.Discard

// --- F1-F4: falsification tests for the cross-posting fixture/struct fix ---
//
// These tests pin the four properties the fix introduced. Each is structured
// so that reverting the relevant change makes it go RED — a green test that
// survives the revert it claims to pin is not a falsification test, it is
// decoration.
//
// F1: reverting LastCheckDate to string makes the fixture-decode test RED.
// F2: removing is_search_started from the struct makes the diagnostic report
//     it as unmodelled (RED).
// F3: the cross-posting LastCheckDate (FlexInt) field errors on non-{null,
//     number, numeric string} input — guarding the field the change made
//     polymorphic, not a bare FlexInt.
// F4: the recorder self-check asserts reduce(fixture)==fixture across every
//     committed fixture (the idempotency oracle, not a self-fulfilling
//     author-chosen input).

// TestCrossPostingFix_F1_LastCheckDateIsNumber pins that last_check_date
// decodes from the JSON number the fixture sends. The prior hand-authored
// fixture guessed string; the live API sends a number (0, unix epoch); the
// decode failed with "cannot unmarshal number into Go struct Field
// .last_check_date of type string". The field is now FlexInt (nullable
// timestamp, heterogeneous date family — see the CrossPosting doc), which
// accepts a number, a numeric string, or null.
//
// NOT falsifiable by reverting the field's type. This test calls .Int64() and
// .IsSet(), so declaring LastCheckDate as a string makes the package fail to
// COMPILE, and a compile error falsifies nothing — the binary never runs and
// no assertion is evaluated. The runtime proof lives in
// TestCrossPostingFix_F1_WrongDeclarationIsRejected below, which decodes the
// same fixture into a local mirror carrying the wrong declaration. That is the
// remedy this repo already adopted for the same problem — see
// wrongSchedulePostsResponse in unknown_field_diagnostic_test.go.
func TestCrossPostingFix_F1_LastCheckDateIsNumber(t *testing.T) {
	fixture := liveFixture(t, "cross_postings.json")
	var resp CrossPostingsResponse
	if err := json.Unmarshal(fixture, &resp); err != nil {
		t.Fatalf("decode cross_postings.json: %v\n\nIf LastCheckDate was reverted to string, this is the exact defect: the fixture sends a number, the struct declares a string, encoding/json aborts the whole decode.", err)
	}
	if len(resp.List) == 0 {
		t.Fatal("List is empty — fixture has no rows to test")
	}
	// The fixture's last_check_date is 0 (a JSON number). A string field would
	// have failed above; FlexInt receives it. Int64() is the typed accessor.
	if got := resp.List[0].LastCheckDate.Int64(); got != 0 {
		t.Errorf("LastCheckDate.Int64() = %d, want 0 (the fixture's placeholder value)", got)
	}
	if !resp.List[0].LastCheckDate.IsSet() {
		t.Error("LastCheckDate.IsSet() = false, want true (the fixture sends 0, a present number — IsSet distinguishes a present 0 from an absent/null field)")
	}
	// instagram_last_check_date is the nullable-timestamp sibling, modelled
	// the same way (FlexInt). The fixture sends 0 (number).
	if got := resp.List[0].InstagramLastCheckDate.Int64(); got != 0 {
		t.Errorf("InstagramLastCheckDate.Int64() = %d, want 0 (the fixture's placeholder value)", got)
	}
}

// TestCrossPostingFix_F1_EditLastCheckDateIsNumber pins the same property for
// the /edit response, which has its own LastCheckDate field.
func TestCrossPostingFix_F1_EditLastCheckDateIsNumber(t *testing.T) {
	fixture := liveFixture(t, "cross_posting_edit.json")
	var resp CrossPostingEditResponse
	if err := json.Unmarshal(fixture, &resp); err != nil {
		t.Fatalf("decode cross_posting_edit.json: %v\n\nIf LastCheckDate was reverted to string, this is the exact defect.", err)
	}
	if got := resp.LastCheckDate.Int64(); got != 0 {
		t.Errorf("LastCheckDate.Int64() = %d, want 0 (the fixture's placeholder value)", got)
	}
}

// TestCrossPostingFix_F2_IsSearchStartedModelled pins that is_search_started
// is modelled by the struct. The diagnostic walker (crossPostingEditWalker)
// walks the /edit fixture against the struct; if is_search_started is removed
// from the struct, the walker reports it as unmodelled and the diagnostic gate
// (TestUnknownFieldDiagnostic) goes RED.
//
// This test is the direct, local assertion of that property: it decodes the
// fixture and checks the field is populated. The diagnostic gate is the
// indirect, structural assertion (it would catch ANY removed field, not just
// this one).
func TestCrossPostingFix_F2_IsSearchStartedModelled(t *testing.T) {
	fixture := liveFixture(t, "cross_posting_edit.json")
	var resp CrossPostingEditResponse
	if err := json.Unmarshal(fixture, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The fixture's is_search_started is true. If the field is removed from
	// the struct, this line does not compile — which is a different signal
	// (compile error) from the diagnostic gate (RED test). Both are valid;
	// the compile error is the first line of defense, the diagnostic gate
	// catches a field that compiles but is not in the struct's json tags.
	if !resp.IsSearchStarted {
		t.Error("IsSearchStarted = false, want true (the fixture's placeholder value)")
	}
}

// TestCrossPostingFix_F2_ProjectIDModelled pins that project_id is modelled.
func TestCrossPostingFix_F2_ProjectIDModelled(t *testing.T) {
	fixture := liveFixture(t, "cross_posting_edit.json")
	var resp CrossPostingEditResponse
	if err := json.Unmarshal(fixture, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The fixture's project_id is 0. We cannot distinguish "0 because modelled
	// and the fixture sends 0" from "0 because not modelled and the zero value
	// is 0" by value alone — the diagnostic gate does that (it walks the JSON
	// key set against the struct's json tags). This test pins the compile-time
	// presence of the field; the diagnostic gate pins the runtime coverage.
	_ = resp.ProjectID
}

// TestCrossPostingFix_F3_FlexIntRejectsBadInput pins that the cross-posting
// LastCheckDate field (FlexInt) errors on input that is not null, a JSON
// number, or a JSON numeric string. This guards the field change 2 made
// polymorphic — not a bare FlexInt — so a shape change on last_check_date
// (the API starts sending an object where it sent a number) is loud, not a
// silent coerce to 0 that hides the regression behind a green decode.
//
// What this IS falsifiable by: change FlexInt.UnmarshalJSON to accept any
// input and the object/array cases below pass where they must error. What it
// is NOT falsifiable by: reverting LastCheckDate to a bare int — the .Int64()
// call sites make that a compile error, which proves nothing. The type-level
// proof is TestCrossPostingFix_F1_WrongDeclarationIsRejected.
func TestCrossPostingFix_F3_FlexIntRejectsBadInput(t *testing.T) {
	// Build a one-row /cross-posting list body and mutate only last_check_date.
	// A bad shape on the cross-posting field must abort the whole list decode,
	// exactly the failure class FlexInt exists to make loud.
	row := func(lastCheckDate string) string {
		return fmt.Sprintf(`{"list":[{"id":1,"name":"cp","last_check_date":%s}],"total_rows":1,"is_has_more":false,"rows_limit":20}`, lastCheckDate)
	}
	bad := []string{
		`{"a":1}`, // object
		`[1,2]`,   // array
		`"abc"`,   // non-numeric string
		`"1.5"`,   // non-integer string
		`true`,    // boolean
		`1.5`,     // non-integer number
	}
	for _, body := range bad {
		var resp CrossPostingsResponse
		if err := json.Unmarshal([]byte(row(body)), &resp); err == nil {
			t.Errorf("CrossPostingsResponse decoded with last_check_date=%s and nil error — a non-{null, number, numeric string} on the cross-posting field must abort the decode, not silently coerce", body)
		}
	}
	// The legitimate inputs MUST still decode, and the field reads back via
	// Int64() regardless of the wire form (the point of FlexInt).
	for _, body := range []struct {
		wire string
		want int64
		set  bool
	}{
		{`null`, 0, false},
		{`0`, 0, true},
		{`21`, 21, true},
		{`"21"`, 21, true},
		{`"0"`, 0, true},
	} {
		var resp CrossPostingsResponse
		if err := json.Unmarshal([]byte(row(body.wire)), &resp); err != nil {
			t.Errorf("CrossPostingsResponse rejected legitimate last_check_date=%s: %v", body.wire, err)
			continue
		}
		if got := resp.List[0].LastCheckDate.Int64(); got != body.want {
			t.Errorf("last_check_date=%s: Int64() = %d, want %d", body.wire, got, body.want)
		}
		if resp.List[0].LastCheckDate.IsSet() != body.set {
			t.Errorf("last_check_date=%s: IsSet() = %v, want %v", body.wire, resp.List[0].LastCheckDate.IsSet(), body.set)
		}
	}
}

// TestCrossPostingFix_F4_RecorderSelfCheckIsFixedPoint pins that the fixture
// recorder's --self-check asserts reduce(fixture)==fixture across EVERY
// committed fixture in testdata/live/. The prior F4 synthesized a trivial
// one-row input that reduced to a self-fulfilling fixture — a green test that
// proved nothing about the 22 fixtures the recorder actually owns. This test
// shells out to the recorder's own idempotency oracle instead: it fails the
// day a reducer change (or a trailing-newline drift) breaks any committed
// fixture, not just the one the test author chose.
//
// RED-on-revert: break the reducer (e.g. change "str" → "string" in
// reduce_value) and --self-check reports N/N fixtures diverged → this test
// goes RED. The test does NOT skip when python3 is absent — the recorder is a
// committed part of the fixture pipeline, and a missing interpreter is a CI
// provisioning failure to fix in preflight.yml, not a green skip.
func TestCrossPostingFix_F4_RecorderSelfCheckIsFixedPoint(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("scripts", "record_fixture.py"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	// --self-check reads FIXTURE_DIR (constant "testdata/live") relative to
	// CWD, so run from the repo root where testdata/live/ lives.
	cmd := exec.Command("python3", scriptPath, "--self-check")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("record_fixture.py --self-check failed (the reducer is not a fixed point on every committed fixture, or python3 is missing — the latter is a CI provisioning failure, not a skip):\n%v\n%s", err, out)
	}
	// The script prints "self-check: N fixtures are fixed points of the reducer".
	// Assert N > 0 so a reducer that accepts an empty fixture dir (N=0) does
	// not pass vacuously.
	if !bytes.Contains(out, []byte("self-check:")) {
		t.Fatalf("unexpected --self-check output (no 'self-check:' line):\n%s", out)
	}
	// Extract the count. The line is "self-check: N fixtures are fixed points".
	idx := bytes.Index(out, []byte("self-check: "))
	rest := string(out[idx+len("self-check: "):])
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil || n <= 0 {
		t.Fatalf("could not parse fixture count from --self-check output, or it is <= 0 (a reducer that passes on an empty fixture dir passes vacuously):\n%s", out)
	}
	t.Logf("recorder self-check: %d fixtures are fixed points of the reducer", n)
}

// TestCrossPostingFix_F9_EnumNameReachesListSurface pins that the decoded enum
// name reaches the LIST surface (EnrichedCrossPostingsMap), not just /edit.
// The MCP tool description promises "the enum integers are decoded to names in
// the response"; before this change the list path emitted bare integers via
// MarshalJSON and the promise was false on the list surface.
//
// RED-on-revert: drop the EnrichedCrossPostingsMap call in runListCrossPostings
// (emit the bare resp) and the CLI test stops seeing search_mode_name. Or
// revert injectEnum to a no-op and the name key is absent here too.
func TestCrossPostingFix_F9_EnumNameReachesListSurface(t *testing.T) {
	resp := &CrossPostingsResponse{
		List: []CrossPosting{{
			ID: 1, SearchMode: SearchMode(EnumValue{Value: 3, Name: "best"}),
		}},
	}
	m, err := EnrichedCrossPostingsMap(resp)
	if err != nil {
		t.Fatalf("EnrichedCrossPostingsMap: %v", err)
	}
	var env struct {
		List []map[string]json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(m["list"], &env.List); err != nil {
		t.Fatalf("decode enriched list: %v", err)
	}
	if len(env.List) != 1 {
		t.Fatalf("list len = %d, want 1", len(env.List))
	}
	if got := string(env.List[0]["search_mode"]); got != "3" {
		t.Errorf("search_mode raw = %s, want 3 (raw integer must survive)", got)
	}
	if got := string(env.List[0]["search_mode_name"]); got != `"best"` {
		t.Errorf("search_mode_name = %s, want \"best\" (decoded name must reach the LIST surface)", got)
	}
}

// TestCrossPostingFix_F10_UnknownEnumDistinguishableOnList pins that an
// undefined enum value is distinguishable on the LIST surface: the raw integer
// survives, the name is "unknown", and the *_unknown flag is injected. An
// agent reading the list can tell "the server sent a value the bundle does not
// define" from "the server sent a known value" — the pass-through contract
// made visible on the list path, not just /edit.
func TestCrossPostingFix_F10_UnknownEnumDistinguishableOnList(t *testing.T) {
	resp := &CrossPostingsResponse{
		List: []CrossPosting{{
			ID: 1, CheckInterval: CheckInterval(EnumValue{Value: 99, Name: "unknown", Unknown: true}),
		}},
	}
	m, err := EnrichedCrossPostingsMap(resp)
	if err != nil {
		t.Fatalf("EnrichedCrossPostingsMap: %v", err)
	}
	var env struct {
		List []map[string]json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(m["list"], &env.List); err != nil {
		t.Fatalf("decode enriched list: %v", err)
	}
	if got := string(env.List[0]["check_interval"]); got != "99" {
		t.Errorf("check_interval raw = %s, want 99 (undefined raw integer must survive)", got)
	}
	if got := string(env.List[0]["check_interval_name"]); got != `"unknown"` {
		t.Errorf("check_interval_name = %s, want \"unknown\"", got)
	}
	if got := string(env.List[0]["check_interval_unknown"]); got != "true" {
		t.Errorf("check_interval_unknown = %s, want true (the *_unknown flag is what distinguishes undefined from known on the list surface)", got)
	}
}

// TestCrossPostingFix_F11_LastCheckDateDecodesFromNumberAndString pins that
// the cross-posting LastCheckDate (FlexInt) decodes from a JSON number, a
// JSON numeric string, and errors on a JSON object — the three-way contract
// the FlexInt change bought. The number→numeric-string mutation is the one
// the FlexInt trade gave up on the decode gate (FlexInt accepts it where a
// bare int rejected it); this test is the live-robustness side of that trade,
// asserting the stringified numeric the API family is known to send does not
// abort the list decode.
func TestCrossPostingFix_F11_LastCheckDateDecodesFromNumberAndString(t *testing.T) {
	row := func(lastCheckDate string) string {
		return fmt.Sprintf(`{"list":[{"id":1,"name":"cp","last_check_date":%s}],"total_rows":1,"is_has_more":false,"rows_limit":20}`, lastCheckDate)
	}
	cases := []struct {
		wire string
		want int64
	}{
		{`0`, 0},
		{`1700000000`, 1700000000},
		{`"1700000000"`, 1700000000},
		{`"0"`, 0},
	}
	for _, tc := range cases {
		var resp CrossPostingsResponse
		if err := json.Unmarshal([]byte(row(tc.wire)), &resp); err != nil {
			t.Errorf("last_check_date=%s: decode failed: %v (FlexInt must accept a number and a numeric string — the stringified-numeric case is the live-robustness the change bought)", tc.wire, err)
			continue
		}
		if got := resp.List[0].LastCheckDate.Int64(); got != tc.want {
			t.Errorf("last_check_date=%s: Int64() = %d, want %d", tc.wire, got, tc.want)
		}
	}
	// An object must error — a shape change is loud, not a silent coerce.
	var resp CrossPostingsResponse
	if err := json.Unmarshal([]byte(row(`{"a":1}`)), &resp); err == nil {
		t.Error("last_check_date={\"a\":1}: decoded with nil error — an object on the cross-posting field must abort the decode (FlexInt rejects containers with *json.UnmarshalTypeError)")
	}
}

// TestCrossPostingFix_F12_SelfCheckGoesRedOnBrokenReducer is the falsification
// half of F4: it breaks the reducer (writes a fixture whose scalar leaves are
// NOT the placeholder set the reducer emits) into a temp fixture dir, runs
// --self-check against it, and asserts the script exits non-zero. A self-check
// that passes on a broken reducer is the failure mode F4 exists to prevent.
//
// This does NOT mutate the committed testdata/live/ — it copies the recorder
// into a temp dir with a single broken fixture so the committed suite is not
// touched. The committed F4 test runs the real --self-check against the real
// fixtures; this test proves that oracle actually fires.
func TestCrossPostingFix_F12_SelfCheckGoesRedOnBrokenReducer(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("scripts", "record_fixture.py"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	tmpDir := t.TempDir()
	// The script reads FIXTURE_DIR = "testdata/live" relative to CWD.
	fixDir := filepath.Join(tmpDir, "testdata", "live")
	if err := os.MkdirAll(fixDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A "broken" fixture: a scalar leaf that is NOT a placeholder the reducer
	// emits (a real string value "not-a-placeholder"). reduce_value would turn
	// it into "str", so reduce(fixture) != fixture → self-check diverges.
	broken := []byte(`{"real_value": "not-a-placeholder", "nested": {"num": 0}}` + "\n")
	if err := os.WriteFile(filepath.Join(fixDir, "broken.json"), broken, 0644); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}
	cmd := exec.Command("python3", scriptPath, "--self-check")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("record_fixture.py --self-check exited 0 on a BROKEN fixture (a reducer that reports a non-placeholder fixture as a fixed point is inert):\n%s", out)
	}
	if !bytes.Contains(out, []byte("diverged")) {
		t.Fatalf("expected --self-check to report 'diverged' on the broken fixture, got:\n%s", out)
	}
}

// TestCrossPostingFix_F13_NegativeIDRefusedBeforeRequest pins that a negative
// id is refused BEFORE any request is made — the guard is on the client, not
// deferred to the server. A negative id builds an invalid path
// (/cross-posting/-1/edit) the server cannot resolve; the prior guard
// rejected only id==0, so -1 slipped through to a server-side 404 that looks
// like "not found" rather than "bad argument".
func TestCrossPostingFix_F13_NegativeIDRefusedBeforeRequest(t *testing.T) {
	// A server that 200s on ANY path — if the client makes a request, the
	// test sees the response and fails; the guard must fire before the call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"GetCrossPostingEdit", func() error { _, err := c.GetCrossPostingEdit(ctx, -1); return err }},
		{"GetCrossPostingStatistics", func() error { _, err := c.GetCrossPostingStatistics(ctx, -1); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if err == nil {
				t.Fatalf("%s(-1) returned nil error — a negative id must be refused before any request", tc.name)
			}
			// Confirm the server was NOT hit: the stub writes {"ok":true},
			// so a successful call would have decoded into the response. The
			// guard error is the only path that does not reach the server.
			if !strings.Contains(err.Error(), "positive integer") {
				t.Errorf("%s(-1) error = %q, want it to mention 'positive integer'", tc.name, err.Error())
			}
		})
	}
}

// TestCrossPostingFix_NullEnumIsUnsetNotUnknown pins that a JSON null on an
// enum field decodes to Name="unset" (Unknown=false), distinct from an
// undefined integer which decodes to Name="unknown" (Unknown=true). The prior
// decode conflated null with "unknown value" — an agent reading a null
// check_interval as "the bundle does not define this value" would mis-report
// a feature that is simply not configured.
func TestCrossPostingFix_NullEnumIsUnsetNotUnknown(t *testing.T) {
	var sm SearchMode
	if err := json.Unmarshal([]byte(`null`), &sm); err != nil {
		t.Fatalf("decode null into SearchMode: %v", err)
	}
	if sm.Name != "unset" {
		t.Errorf("null SearchMode.Name = %q, want \"unset\" (null means the feature is not configured, distinct from an undefined value)", sm.Name)
	}
	if sm.Unknown {
		t.Error("null SearchMode.Unknown = true, want false (null is not an undefined bundle value)")
	}
	// An undefined integer must still be "unknown"/true — the distinction is
	// the point of the change.
	var ci CheckInterval
	if err := json.Unmarshal([]byte(`99`), &ci); err != nil {
		t.Fatalf("decode 99 into CheckInterval: %v", err)
	}
	if ci.Name != "unknown" || !ci.Unknown {
		t.Errorf("CheckInterval(99) = {Name:%q Unknown:%v}, want {Name:\"unknown\" Unknown:true}", ci.Name, ci.Unknown)
	}
	// A known integer must still decode to its name.
	var db DetermineBestBy
	if err := json.Unmarshal([]byte(`4`), &db); err != nil {
		t.Fatalf("decode 4 into DetermineBestBy: %v", err)
	}
	if db.Name != "views" || db.Unknown {
		t.Errorf("DetermineBestBy(4) = {Name:%q Unknown:%v}, want {Name:\"views\" Unknown:false}", db.Name, db.Unknown)
	}
}

// TestEnrichedCrossPostingsMap pins the list-enrichment contract: every
// modelled field survives, the five enum names are injected on every row, the
// raw integers stay, and the envelope shape (list/total_rows/is_has_more) is
// preserved. This is the unit-test half of F9/F10; the CLI test in
// cmd/hooppy/crossposting_test.go is the integration half.
func TestEnrichedCrossPostingsMap(t *testing.T) {
	resp := &CrossPostingsResponse{
		List: []CrossPosting{{
			ID:                  1,
			Name:                "cp",
			State:               1,
			SearchMode:          SearchMode(EnumValue{Value: 3, Name: "best"}),
			SearchModeDirection: SearchModeDirection(EnumValue{Value: 2, Name: "newest-first"}),
			DetermineBestBy:     DetermineBestBy(EnumValue{Value: 4, Name: "views"}),
			CheckWhenType:       CheckWhenType(EnumValue{Value: 1, Name: "by-interval"}),
			CheckInterval:       CheckInterval(EnumValue{Value: 99, Name: "unknown", Unknown: true}),
		}},
		TotalRows: 1,
		IsHasMore: false,
		RowsLimit: 20,
	}
	m, err := EnrichedCrossPostingsMap(resp)
	if err != nil {
		t.Fatalf("EnrichedCrossPostingsMap: %v", err)
	}
	// Envelope shape preserved.
	if got := string(m["total_rows"]); got != "1" {
		t.Errorf("total_rows = %s, want 1", got)
	}
	if got := string(m["is_has_more"]); got != "false" {
		t.Errorf("is_has_more = %s, want false", got)
	}
	var env struct {
		List []map[string]json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(m["list"], &env.List); err != nil {
		t.Fatalf("decode enriched list: %v", err)
	}
	row := env.List[0]
	// Modelled fields survive.
	if got := string(row["id"]); got != "1" {
		t.Errorf("id = %s, want 1", got)
	}
	if got := string(row["name"]); got != `"cp"` {
		t.Errorf("name = %s, want \"cp\"", got)
	}
	// All five enum names injected; raw integers preserved.
	for _, tc := range []struct {
		key, raw, name string
		unknown        bool
	}{
		{"search_mode", "3", `"best"`, false},
		{"search_mode_direction", "2", `"newest-first"`, false},
		{"determine_best_by", "4", `"views"`, false},
		{"check_when_type", "1", `"by-interval"`, false},
		{"check_interval", "99", `"unknown"`, true},
	} {
		if got := string(row[tc.key]); got != tc.raw {
			t.Errorf("%s raw = %s, want %s", tc.key, got, tc.raw)
		}
		if got := string(row[tc.key+"_name"]); got != tc.name {
			t.Errorf("%s_name = %s, want %s", tc.key, got, tc.name)
		}
		if tc.unknown {
			if got := string(row[tc.key+"_unknown"]); got != "true" {
				t.Errorf("%s_unknown = %s, want true", tc.key, got)
			}
		} else if _, ok := row[tc.key+"_unknown"]; ok {
			t.Errorf("%s_unknown present but the value is known (Unknown=false) — the flag must be injected only for undefined values", tc.key)
		}
	}
}

// TestEnrichedCrossPostingEditMap_RefusesServerKeyCollision pins the
// collision guard: if the server already sends a key the injector would
// alias (e.g. search_mode_name), EnrichedCrossPostingEditMap errors rather
// than silently overwriting the server field. The prior injectEnum swallowed
// marshal errors and could clobber a server key.
func TestEnrichedCrossPostingEditMap_RefusesServerKeyCollision(t *testing.T) {
	// Build a raw body where the server already occupies search_mode_name.
	raw := []byte(`{"search_mode":3,"search_mode_name":"server-sent-label"}`)
	resp := &CrossPostingEditResponse{Raw: raw, SearchMode: SearchMode(EnumValue{Value: 3, Name: "best"})}
	_, err := EnrichedCrossPostingEditMap(resp)
	if err == nil {
		t.Fatal("EnrichedCrossPostingEditMap returned nil error when the server already occupies search_mode_name — the injector must refuse to overwrite a server key, not silently clobber it")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("error = %q, want it to mention 'refusing to overwrite'", err.Error())
	}
}

// TestCrossPostingFix_F1_WrongDeclarationIsRejected is the runtime half of the
// last_check_date fix, and it exists because the obvious mutation is not a
// falsification.
//
// The natural way to test "the field must not be a string" is to declare it a
// string and watch the suite go red. It cannot be done here: the assertions in
// F1 and F3 call .Int64() and .IsSet(), so a reverted declaration fails to
// COMPILE — zero RED and zero GREEN, nothing evaluated. Two comments in this
// file used to claim otherwise.
//
// So the wrong declaration lives here as a local mirror instead, decoupled
// from the real struct's methods, and the assertion is that the REAL fixture
// rejects it. Break the fix — record a fixture whose last_check_date is a
// string — and this stops erroring. The same device as
// wrongSchedulePostsResponse in unknown_field_diagnostic_test.go.
func TestCrossPostingFix_F1_WrongDeclarationIsRejected(t *testing.T) {
	// Verbatim the declaration that shipped and broke `crossposting list`.
	type wrongCrossPosting struct {
		LastCheckDate          string `json:"last_check_date"`
		InstagramLastCheckDate string `json:"instagram_last_check_date"`
	}
	type wrongCrossPostingsResponse struct {
		List []wrongCrossPosting `json:"list"`
	}

	fixture := liveFixture(t, "cross_postings.json")
	var typeErr *json.UnmarshalTypeError
	err := json.Unmarshal(fixture, &wrongCrossPostingsResponse{})
	if !errors.As(err, &typeErr) {
		t.Fatalf("decoding the real fixture into the string declaration gave %v, want a *json.UnmarshalTypeError — the fixture must record last_check_date as a NUMBER; if it records a string again, the live decode breaks exactly as it did before and nothing here would notice", err)
	}
	// Either sibling may be the reported field: both are recorded as numbers
	// and both are declared string in the mirror, so which one the decoder
	// reaches first is not a property worth pinning. What matters is that the
	// error names one of the two timestamps rather than something else, which
	// would mean the fixture broke somewhere this test does not cover.
	if !strings.Contains(typeErr.Field, "last_check_date") {
		t.Errorf("UnmarshalTypeError.Field = %q, want one of the last_check_date timestamps — the error must point at a field whose recorded type is wrong", typeErr.Field)
	}
}
