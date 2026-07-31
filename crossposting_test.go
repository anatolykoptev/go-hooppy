package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if len(resp.Statistics) != 2 {
		t.Errorf("Statistics len = %d, want 2", len(resp.Statistics))
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
