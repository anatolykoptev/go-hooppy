package hooppy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// --- ScheduleEditResponse decode tests ---

// TestGetScheduleEdit_DecodeTimes decodes a GET /posts/schedules/{id}/edit
// response whose times array carries minutes as a JSON NUMBER in one slot
// and a JSON STRING ("00") in another, in the SAME fixture. A bare int
// field aborts the whole decode on the string form — the bug this repo has
// shipped five times. FlexInt handles both; this is the regression guard.
func TestGetScheduleEdit_DecodeTimes(t *testing.T) {
	body := `{
		"id": 42,
		"name": "ПН/СР/ЧТ",
		"times": [
			[{"hours":12,"minutes":25},{"hours":14,"minutes":25},{"hours":16,"minutes":"00"},{"hours":17,"minutes":35}],
			[],
			[{"hours":12,"minutes":25},{"hours":14,"minutes":25}],
			[],
			[],
			[],
			[]
		],
		"posts_hashtags": {"tag1": "value1"},
		"posts_links": {"link1": "url1"},
		"project_id": 7,
		"projects": [{"id": 7, "name": "Project A"}],
		"selected_pages_by_source_ids": {"1": [100, 200]},
		"selected_albums_by_source_ids": {"1": [300]},
		"social_pages_by_accounts": {"123": [{"id": 100, "source_id": 1, "social_page_name": "Page A"}]},
		"social_albums_by_pages": {"100": [300]},
		"watermarks": [{"id": 1, "name": "WM A"}]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/posts/schedules/42/edit" {
			t.Errorf("GET /posts/schedules/42/edit, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	edit, err := c.GetScheduleEdit(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetScheduleEdit: %v\n— a wrong field type aborts the whole decode (the minutes=int bug class)", err)
	}
	if len(edit.Times) != 7 {
		t.Fatalf("Times: %d weekdays, want 7", len(edit.Times))
	}
	// Day 0 (Mon): 4 slots. Slot 2 has minutes as string "00", slot 0 as number 25.
	if len(edit.Times[0]) != 4 {
		t.Fatalf("Times[0]: %d slots, want 4", len(edit.Times[0]))
	}
	if got, want := edit.Times[0][0].Minutes.Int64(), int64(25); got != want {
		t.Errorf("Times[0][0].Minutes = %d, want %d (number form)", got, want)
	}
	if got, want := edit.Times[0][2].Minutes.Int64(), int64(0); got != want {
		t.Errorf("Times[0][2].Minutes = %d, want %d (string form \"00\")", got, want)
	}
	if got, want := edit.Times[0][2].Hours.Int64(), int64(16); got != want {
		t.Errorf("Times[0][2].Hours = %d, want %d", got, want)
	}
	// Day 1 (Tue): empty (no slots).
	if len(edit.Times[1]) != 0 {
		t.Errorf("Times[1]: %d slots, want 0 (empty day)", len(edit.Times[1]))
	}
	// Day 2 (Wed): 2 slots.
	if len(edit.Times[2]) != 2 {
		t.Errorf("Times[2]: %d slots, want 2", len(edit.Times[2]))
	}
	// project_id is int.
	if edit.ProjectID != 7 {
		t.Errorf("ProjectID = %d, want 7", edit.ProjectID)
	}
	// projects is []Project.
	if len(edit.Projects) != 1 || edit.Projects[0].ID != 7 {
		t.Errorf("Projects = %+v, want one project with id 7", edit.Projects)
	}
	// watermarks is []Watermark.
	if len(edit.Watermarks) != 1 || edit.Watermarks[0].ID != 1 {
		t.Errorf("Watermarks = %+v, want one watermark with id 1", edit.Watermarks)
	}
}

// TestScheduleEdit_DecodeCredentialHygiene verifies that
// social_pages_by_accounts carrying OAuth tokens (access_token, bot_token,
// password) cannot reach the marshalled ScheduleEditResponse output. The
// field reuses the narrow Page struct, which models only id/source/social-
// ids/name/photo — the token fields are silently dropped at decode and
// therefore absent from any re-marshal. This is the RED-on-revert test for
// the credential-hygiene invariant on the schedule-edit decode path.
func TestScheduleEdit_DecodeCredentialHygiene(t *testing.T) {
	body := `{
		"id": 42,
		"name": "S",
		"times": [[]],
		"social_pages_by_accounts": {
			"123": [{
				"id": 100, "source_id": 1, "account_id": 123,
				"social_page_id": "999", "social_page_name": "test page",
				"social_page_photo": "https://pp.vk.me/p.jpg",
				"access_token": "SECRET_ACCESS_TOKEN_VALUE",
				"bot_token": "SECRET_BOT_TOKEN_VALUE",
				"password": "SECRET_PASSWORD_VALUE",
				"access_token_secret": "SECRET_ACCESS_TOKEN_SECRET_VALUE"
			}]
		}
	}`
	var edit ScheduleEditResponse
	if err := json.Unmarshal([]byte(body), &edit); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(edit)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	secretValues := []string{
		"SECRET_ACCESS_TOKEN_VALUE",
		"SECRET_BOT_TOKEN_VALUE",
		"SECRET_PASSWORD_VALUE",
		"SECRET_ACCESS_TOKEN_SECRET_VALUE",
	}
	for _, secret := range secretValues {
		if strings.Contains(got, secret) {
			t.Errorf("marshalled ScheduleEditResponse leaked credential value %q:\n%s", secret, got)
		}
	}
	// The modelled page fields must still be present.
	if !strings.Contains(got, `"social_page_name":"test page"`) {
		t.Errorf("marshalled ScheduleEditResponse lost the safe page field social_page_name:\n%s", got)
	}
}

// --- Slot reporting tests ---

// TestImportSearchPost_SlotReported verifies that a single import into a
// schedule (when_type=3) reads back the assigned slot from GET /posts/{id}/edit
// and populates PublicationDate + ScheduleID on the response.
func TestImportSearchPost_SlotReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			w.Write([]byte(`{
				"id": 92820377,
				"publication_when_type": 3,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"publication_date": {"date":"31.07.2026","hours":"14","minutes":"25"},
				"created_by": 1,
				"texts": [],
				"attachments": [],
				"selected_pages_by_source_ids": {},
				"all_pages_ids_by_source_ids": {},
				"schedule_id": 55,
				"project_id": 0
			}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost: %v", err)
	}
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377", resp.ID)
	}
	if resp.PublicationDate == nil {
		t.Fatal("PublicationDate = nil, want the slot from the read-back")
	}
	if got, want := resp.PublicationDate.Date, "31.07.2026"; got != want {
		t.Errorf("PublicationDate.Date = %q, want %q", got, want)
	}
	if got, want := resp.PublicationDate.Hours, "14"; got != want {
		t.Errorf("PublicationDate.Hours = %q, want %q", got, want)
	}
	if got, want := resp.PublicationDate.Minutes, "25"; got != want {
		t.Errorf("PublicationDate.Minutes = %q, want %q", got, want)
	}
	if resp.ScheduleID != 55 {
		t.Errorf("ScheduleID = %d, want 55", resp.ScheduleID)
	}
	if resp.SlotLookupError != "" {
		t.Errorf("SlotLookupError = %q, want empty", resp.SlotLookupError)
	}
}

// TestImportSearchPost_BatchSlotOneCall verifies that a batch import into a
// schedule (when_type=3) resolves all slots in ONE ListPosts call (not N
// GetPostEdit calls), and matches all created ids to the right slots.
func TestImportSearchPost_BatchSlotOneCall(t *testing.T) {
	var listCalls int32
	var editCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			// Server returns the first id plus the full ids array for the batch.
			w.Write([]byte(`{"id":92820377,"ids":[92820377,92820378,92820379]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			atomic.AddInt32(&listCalls, 1)
			// Return all posts in the schedule, each with its slot.
			w.Write([]byte(`{
				"list": [
					{"id":92820377,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
					{"id":92820378,"publication_date":{"date":"29 Июля","time":"16:25","timestamp":1753777500,"source_timestamp":1753781100},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
					{"id":92820379,"publication_date":{"date":"30 Июля","time":"12:00","timestamp":1753856400,"source_timestamp":1753860000},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],
				"total_rows": 3,
				"is_has_more": false,
				"rows_limit": 20
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			atomic.AddInt32(&editCalls, 1)
			w.Write([]byte(`{"id":92820377,"publication_date":{"date":"31.07.2026","hours":"14","minutes":"25"},"schedule_id":55}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001, 2002, 2003},
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch: %v", err)
	}
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377", resp.ID)
	}
	// ONE list call, not three GetPostEdit calls.
	if got := atomic.LoadInt32(&listCalls); got != 1 {
		t.Errorf("ListPosts calls = %d, want 1 (batch resolves all slots in ONE call)", got)
	}
	if got := atomic.LoadInt32(&editCalls); got != 0 {
		t.Errorf("GetPostEdit calls = %d, want 0 (all ids matched in the list — no per-id fallback)", got)
	}
	// All three slots matched to the right ids.
	if len(resp.Slots) != 3 {
		t.Fatalf("Slots = %d entries, want 3", len(resp.Slots))
	}
	slotByID := make(map[int]*PublicationDate, 3)
	for i := range resp.Slots {
		slotByID[resp.Slots[i].ID] = resp.Slots[i].PublicationDate
	}
	// 92820377 → 14:25
	pd, ok := slotByID[92820377]
	if !ok || pd == nil {
		t.Fatal("slot for 92820377 missing or nil")
	}
	if pd.Hours != "14" || pd.Minutes != "25" {
		t.Errorf("slot 92820377: hours=%q minutes=%q, want 14/25", pd.Hours, pd.Minutes)
	}
	// 92820378 → 16:25
	pd, ok = slotByID[92820378]
	if !ok || pd == nil {
		t.Fatal("slot for 92820378 missing or nil")
	}
	if pd.Hours != "16" || pd.Minutes != "25" {
		t.Errorf("slot 92820378: hours=%q minutes=%q, want 16/25", pd.Hours, pd.Minutes)
	}
	// 92820379 → 12:00
	pd, ok = slotByID[92820379]
	if !ok || pd == nil {
		t.Fatal("slot for 92820379 missing or nil")
	}
	if pd.Hours != "12" || pd.Minutes != "00" {
		t.Errorf("slot 92820379: hours=%q minutes=%q, want 12/00", pd.Hours, pd.Minutes)
	}
	if resp.SlotLookupError != "" {
		t.Errorf("SlotLookupError = %q, want empty", resp.SlotLookupError)
	}
}

// TestImportSearchPost_SlotLookupFails verifies that a failed read-back
// (stub 500) does NOT fail the import: the id is still returned,
// SlotLookupError is set, and the method returns nil error (exit zero).
func TestImportSearchPost_SlotLookupFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"server error"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost: a failed read-back must not fail the import, got error: %v", err)
	}
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377 (the id must still be returned)", resp.ID)
	}
	if resp.PublicationDate != nil {
		t.Errorf("PublicationDate = %+v, want nil (read-back failed)", resp.PublicationDate)
	}
	if resp.SlotLookupError == "" {
		t.Error("SlotLookupError = empty, want a non-empty error message")
	}
}

// TestImportSearchPost_NoSlotForNonSchedule verifies that when when_type is
// NOT 3, no read-back is attempted — the response carries only the id.
func TestImportSearchPost_NoSlotForNonSchedule(t *testing.T) {
	var readBackCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			atomic.AddInt32(&readBackCalls, 1)
			w.Write([]byte(`{"id":92820377,"publication_date":{"date":"31.07.2026","hours":"14","minutes":"25"},"schedule_id":55}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			atomic.AddInt32(&readBackCalls, 1)
			w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		PublicationWhenType: 1, // publish now, not by schedule
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost: %v", err)
	}
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377", resp.ID)
	}
	if got := atomic.LoadInt32(&readBackCalls); got != 0 {
		t.Errorf("read-back calls = %d, want 0 (when_type != 3 → no read-back attempted)", got)
	}
	if resp.PublicationDate != nil {
		t.Errorf("PublicationDate = %+v, want nil (no read-back for when_type != 3)", resp.PublicationDate)
	}
	if resp.SlotLookupError != "" {
		t.Errorf("SlotLookupError = %q, want empty", resp.SlotLookupError)
	}
}

// TestCopySearchPost_SlotReported verifies the slot read-back fires for
// CopySearchPost too (single-post, when_type=3).
func TestCopySearchPost_SlotReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/copy":
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			w.Write([]byte(`{"id":92820377,"publication_date":{"date":"31.07.2026","hours":"14","minutes":"25"},"schedule_id":55}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		PublicationWhenType: 3,
		PublicationHowType:  1,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("CopySearchPost: %v", err)
	}
	if resp.PublicationDate == nil || resp.PublicationDate.Hours != "14" {
		t.Errorf("PublicationDate = %+v, want hours=14 from the read-back", resp.PublicationDate)
	}
	if resp.ScheduleID != 55 {
		t.Errorf("ScheduleID = %d, want 55", resp.ScheduleID)
	}
}

// TestRewriteSearchPost_SlotReported verifies the slot read-back fires for
// RewriteSearchPost (single-post, when_type=3).
func TestRewriteSearchPost_SlotReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			w.Write([]byte(`{"id":92820377,"publication_date":{"date":"31.07.2026","hours":"14","minutes":"25"},"schedule_id":55}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		PublicationWhenType: 3,
		PublicationHowType:  1,
		SchedulesIDs:        []int{55},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("RewriteSearchPost: %v", err)
	}
	if resp.PublicationDate == nil || resp.PublicationDate.Hours != "14" {
		t.Errorf("PublicationDate = %+v, want hours=14 from the read-back", resp.PublicationDate)
	}
}

// TestPostPubDateToPublicationDate_Conversion verifies the conversion from
// the list-surface PostPublicationDate ({date, time, timestamp}) to the
// {date, hours, minutes} PublicationDate shape.
func TestPostPubDateToPublicationDate_Conversion(t *testing.T) {
	ppd := &PostPublicationDate{
		Date:      "29 Июля",
		Time:      "14:25",
		Timestamp: FlexInt{},
	}
	// Set timestamp via JSON unmarshal (the normal path).
	_ = json.Unmarshal([]byte(`1753770300`), &ppd.Timestamp)

	pd := postPubDateToPublicationDate(ppd)
	if pd == nil {
		t.Fatal("postPubDateToPublicationDate returned nil")
	}
	if pd.Hours != "14" {
		t.Errorf("Hours = %q, want 14", pd.Hours)
	}
	if pd.Minutes != "25" {
		t.Errorf("Minutes = %q, want 25", pd.Minutes)
	}
	// Date from timestamp formatted as dd.mm.yyyy in UTC.
	// 1753770300 = 2025-07-29 06:25:00 UTC → "29.07.2025"
	if pd.Date != "29.07.2025" {
		t.Errorf("Date = %q, want 29.07.2025 (from timestamp 1753770300 in UTC)", pd.Date)
	}
}

// TestImportSearchPost_BatchPerIDFallback verifies the per-id fallback: when
// the list does not return one of the created ids, that id is fetched via
// GetPostEdit. The list path is still ONE call; the fallback is per-missing-
// id only.
func TestImportSearchPost_BatchPerIDFallback(t *testing.T) {
	var listCalls int32
	var editCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			w.Write([]byte(`{"id":92820377,"ids":[92820377,92820378,92820399]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			atomic.AddInt32(&listCalls, 1)
			// Return only 2 of the 3 created ids — 92820399 is missing.
			w.Write([]byte(`{
				"list": [
					{"id":92820377,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
					{"id":92820378,"publication_date":{"date":"29 Июля","time":"16:25","timestamp":1753777500,"source_timestamp":1753781100},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],
				"total_rows": 2,
				"is_has_more": false,
				"rows_limit": 20
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820399/edit":
			atomic.AddInt32(&editCalls, 1)
			w.Write([]byte(`{"id":92820399,"publication_date":{"date":"01.08.2026","hours":"09","minutes":"00"},"schedule_id":55}`))
		default:
			// Don't fail on unexpected paths — the list path may probe for
			// other ids; we only care about the one we expect.
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001, 2002, 2003},
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch: %v", err)
	}
	// ONE list call.
	if got := atomic.LoadInt32(&listCalls); got != 1 {
		t.Errorf("ListPosts calls = %d, want 1", got)
	}
	// ONE GetPostEdit call — for the missing id only.
	if got := atomic.LoadInt32(&editCalls); got != 1 {
		t.Errorf("GetPostEdit calls = %d, want 1 (per-id fallback for the one missing id)", got)
	}
	// All three slots resolved.
	if len(resp.Slots) != 3 {
		t.Fatalf("Slots = %d entries, want 3 (2 from list + 1 from fallback)", len(resp.Slots))
	}
	slotByID := make(map[int]*PublicationDate, 3)
	for i := range resp.Slots {
		slotByID[resp.Slots[i].ID] = resp.Slots[i].PublicationDate
	}
	// The fallback id got its slot from GetPostEdit.
	pd, ok := slotByID[92820399]
	if !ok || pd == nil {
		t.Fatal("slot for 92820399 (fallback id) missing or nil")
	}
	if pd.Hours != "09" || pd.Minutes != "00" {
		t.Errorf("fallback slot 92820399: hours=%q minutes=%q, want 09/00", pd.Hours, pd.Minutes)
	}
}

// TestImportSearchPost_BatchListFailsAllFallback verifies that when the
// ListPosts call fails entirely, the per-id fallback is used for ALL ids.
func TestImportSearchPost_BatchListFailsAllFallback(t *testing.T) {
	var listCalls int32
	var editCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			w.Write([]byte(`{"id":92820377,"ids":[92820377,92820378]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			atomic.AddInt32(&listCalls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"server error"}`))
		case r.Method == http.MethodGet && (r.URL.Path == "/posts/92820377/edit" || r.URL.Path == "/posts/92820378/edit"):
			atomic.AddInt32(&editCalls, 1)
			w.Write([]byte(`{"id":` + r.URL.Path[len("/posts/"):len("/posts/")+8] + `,"publication_date":{"date":"31.07.2026","hours":"14","minutes":"25"},"schedule_id":55}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001, 2002},
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch: %v", err)
	}
	if got := atomic.LoadInt32(&listCalls); got != 1 {
		t.Errorf("ListPosts calls = %d, want 1 (the failed call)", got)
	}
	// Both ids fetched via per-id fallback.
	if got := atomic.LoadInt32(&editCalls); got != 2 {
		t.Errorf("GetPostEdit calls = %d, want 2 (all ids via fallback after list failure)", got)
	}
	// SlotLookupError is set (the list failure is reported).
	if resp.SlotLookupError == "" {
		t.Error("SlotLookupError = empty, want a message about the list failure + fallback")
	}
	// Both slots resolved despite the list failure.
	if len(resp.Slots) != 2 {
		t.Fatalf("Slots = %d entries, want 2 (both via fallback)", len(resp.Slots))
	}
}
