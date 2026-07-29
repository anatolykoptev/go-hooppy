package hooppy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
		"social_pages_by_accounts": [{"account": {"id": 123, "social_id": "3251", "source_id": 1, "name": "A", "photo": "https://example.invalid/x", "link": "https://example.invalid/x"}, "pages": [{"id": 100, "social_id": "100", "type": "board", "name": "Page A", "alias": "", "photo": "", "link": "https://example.invalid/x"}]}],
		"social_albums_by_pages": [],
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
// refresh_token, password, wp_app_password, access_token_secret) cannot
// reach the marshalled ScheduleEditResponse output. The field is an array
// of {account, pages}; BOTH the account sub-object and the page sub-object
// are modelled by narrow structs (SocialPagesAccount / SocialPagesPage)
// that list only the safe fields measured on the wire — the token fields
// are silently dropped at decode and therefore absent from any re-marshal.
// This is the RED-on-revert test for the credential-hygiene invariant on
// the schedule-edit decode path: if either sub-object were widened to
// map[string]interface{} or json.RawMessage, the token values would leak
// through to stdout via printJSON.
func TestScheduleEdit_DecodeCredentialHygiene(t *testing.T) {
	body := `{
		"id": 42,
		"name": "S",
		"times": [[]],
		"social_pages_by_accounts": [{
			"account": {
				"id": 123, "social_id": "3251", "source_id": 1, "name": "A",
				"photo": "https://example.invalid/x", "link": "https://example.invalid/x",
				"access_token": "SECRET_ACCESS_TOKEN_VALUE",
				"bot_token": "SECRET_BOT_TOKEN_VALUE",
				"refresh_token": "SECRET_REFRESH_TOKEN_VALUE",
				"password": "SECRET_PASSWORD_VALUE",
				"wp_app_password": "SECRET_WP_APP_PASSWORD_VALUE",
				"access_token_secret": "SECRET_ACCESS_TOKEN_SECRET_VALUE"
			},
			"pages": [{
				"id": 100, "social_id": "100", "type": "board", "name": "test page",
				"alias": "", "photo": "", "link": "https://example.invalid/x",
				"access_token": "SECRET_ACCESS_TOKEN_VALUE",
				"bot_token": "SECRET_BOT_TOKEN_VALUE",
				"refresh_token": "SECRET_REFRESH_TOKEN_VALUE",
				"password": "SECRET_PASSWORD_VALUE",
				"wp_app_password": "SECRET_WP_APP_PASSWORD_VALUE",
				"access_token_secret": "SECRET_ACCESS_TOKEN_SECRET_VALUE"
			}]
		}]
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
		"SECRET_REFRESH_TOKEN_VALUE",
		"SECRET_PASSWORD_VALUE",
		"SECRET_WP_APP_PASSWORD_VALUE",
		"SECRET_ACCESS_TOKEN_SECRET_VALUE",
	}
	for _, secret := range secretValues {
		if strings.Contains(got, secret) {
			t.Errorf("marshalled ScheduleEditResponse leaked credential value %q:\n%s", secret, got)
		}
	}
	// The modelled page fields must still be present (the narrow struct
	// kept the safe fields while dropping the tokens).
	if !strings.Contains(got, `"name":"test page"`) {
		t.Errorf("marshalled ScheduleEditResponse lost the safe page field name:\n%s", got)
	}
	// The account sub-object's safe fields must also survive.
	if len(edit.SocialPagesByAccounts) != 1 {
		t.Fatalf("SocialPagesByAccounts = %d entries, want 1", len(edit.SocialPagesByAccounts))
	}
	if got, want := edit.SocialPagesByAccounts[0].Account.ID, 123; got != want {
		t.Errorf("Account.ID = %d, want %d", got, want)
	}
	if got, want := edit.SocialPagesByAccounts[0].Account.SocialID, "3251"; got != want {
		t.Errorf("Account.SocialID = %q, want %q (string, not int)", got, want)
	}
	if len(edit.SocialPagesByAccounts[0].Pages) != 1 {
		t.Fatalf("Pages = %d, want 1", len(edit.SocialPagesByAccounts[0].Pages))
	}
	if got, want := edit.SocialPagesByAccounts[0].Pages[0].ID, 100; got != want {
		t.Errorf("Pages[0].ID = %d, want %d", got, want)
	}
	if got, want := edit.SocialPagesByAccounts[0].Pages[0].SocialID, "100"; got != want {
		t.Errorf("Pages[0].SocialID = %q, want %q (string, not int)", got, want)
	}
}

// TestGetScheduleEdit_DecodeRealCapture is the regression guard built from
// the captured GET /posts/schedules/{id}/edit response shape
// (testdata/schedule_edit.json), not a hand-written inline fixture. IDs →
// small integers, names → "A"/"B"/"C", URLs → example.invalid, but every
// KEY and every VALUE TYPE is exactly as the server sends them — including:
//
//   - social_pages_by_accounts is an ARRAY (the crash field: typed as a map
//     it aborted the whole decode with "cannot unmarshal array into Go struct
//     field ... of type map[int][]hooppy.Page"). This is the sixth
//     name-implies-wrong-shape instance on this API.
//   - the number-vs-string id/social_id split inside each account and page
//     sub-object (id is a number, social_id is a string).
//   - the polymorphic minutes (number 25 in one slot, string "00" in
//     another, in the SAME times array).
//   - the empty social_albums_by_pages array.
//
// This SINGLE capture cannot lie about the shape because it was recorded
// from the wire. It is the RED-on-revert proof for the
// social_pages_by_accounts type: revert the field to map[int][]Page and
// this test fails at unmarshal with the exact error the live command
// produced. The gate was green while the command was broken, so the gate
// is not the evidence — this fixture decoding is.
func TestGetScheduleEdit_DecodeRealCapture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "schedule_edit.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var edit ScheduleEditResponse
	if err := json.Unmarshal(raw, &edit); err != nil {
		t.Fatalf("unmarshal real capture: %v\n— a wrong field type aborts the whole decode (the social_pages_by_accounts=map bug class)", err)
	}
	// times: 7 weekdays, day 0 has 4 slots, day 2 has 2, the rest empty.
	if len(edit.Times) != 7 {
		t.Fatalf("Times: %d weekdays, want 7", len(edit.Times))
	}
	if len(edit.Times[0]) != 4 {
		t.Errorf("Times[0]: %d slots, want 4", len(edit.Times[0]))
	}
	// Polymorphic minutes: slot 0 number 25, slot 2 string "00".
	if got, want := edit.Times[0][0].Minutes.Int64(), int64(25); got != want {
		t.Errorf("Times[0][0].Minutes = %d, want %d (number form)", got, want)
	}
	if got, want := edit.Times[0][2].Minutes.Int64(), int64(0); got != want {
		t.Errorf("Times[0][2].Minutes = %d, want %d (string form \"00\")", got, want)
	}
	// project_id is int.
	if edit.ProjectID != 7 {
		t.Errorf("ProjectID = %d, want 7", edit.ProjectID)
	}
	// projects: full project objects decode into the narrow Project type.
	if len(edit.Projects) != 1 || edit.Projects[0].ID != 7 || edit.Projects[0].Name != "A" {
		t.Errorf("Projects = %+v, want one {id:7,name:A}", edit.Projects)
	}
	// social_pages_by_accounts is an ARRAY of {account, pages} — the crash
	// field. Two elements in the fixture.
	if len(edit.SocialPagesByAccounts) != 2 {
		t.Fatalf("SocialPagesByAccounts = %d entries, want 2 (array, not map)", len(edit.SocialPagesByAccounts))
	}
	// First element: account id 123 (number), social_id "3251" (string).
	acc := edit.SocialPagesByAccounts[0].Account
	if acc.ID != 123 {
		t.Errorf("Account[0].ID = %d, want 123 (number)", acc.ID)
	}
	if acc.SocialID != "3251" {
		t.Errorf("Account[0].SocialID = %q, want \"3251\" (string, not int)", acc.SocialID)
	}
	if acc.SourceID != 6 {
		t.Errorf("Account[0].SourceID = %d, want 6", acc.SourceID)
	}
	// First element's pages: 2 pages, id number, social_id string.
	if len(edit.SocialPagesByAccounts[0].Pages) != 2 {
		t.Fatalf("Pages[0] = %d, want 2", len(edit.SocialPagesByAccounts[0].Pages))
	}
	pg := edit.SocialPagesByAccounts[0].Pages[0]
	if pg.ID != 100 {
		t.Errorf("Pages[0][0].ID = %d, want 100 (number)", pg.ID)
	}
	if pg.SocialID != "100" {
		t.Errorf("Pages[0][0].SocialID = %q, want \"100\" (string, not int)", pg.SocialID)
	}
	if pg.Type != "board" {
		t.Errorf("Pages[0][0].Type = %q, want \"board\"", pg.Type)
	}
	// watermarks: array of narrow watermark objects.
	if len(edit.Watermarks) != 1 || edit.Watermarks[0].ID != 1 {
		t.Errorf("Watermarks = %+v, want one with id 1", edit.Watermarks)
	}
	// social_albums_by_pages: empty array in every sample; RawMessage
	// captures it without guessing the element shape.
	if !strings.Contains(string(edit.SocialAlbumsByPages), "[") {
		t.Errorf("SocialAlbumsByPages = %s, want the fixture's empty array \"[]\" (element shape unobserved)", edit.SocialAlbumsByPages)
	}
}

// --- Slot reporting tests ---

// TestImportSearchPost_SlotReported verifies that a single import into a
// schedule (when_type=3) recovers the assigned slot via the schedule
// snapshot-diff over the list surface — the SAME mechanism the batch path
// uses. A single create is a batch of one: the before snapshot is empty,
// the after snapshot carries the one created post, the diff recovers it,
// and the slot (hours/minutes from the time field, date from the timestamp
// at the account offset) is reported.
//
// The stub asserts NO call reaches /posts/{id}/edit on the single path —
// that is the regression guard. GET /posts/{id}/edit returns 403 "post is
// processing" for ~1 min after a create; the list surface returns the slot
// immediately. A test that merely checked the slot appears would pass with
// the old /edit path restored (against a stub that answers /edit), so the
// no-/edit-call assertion is what makes this a real guard.
//
// RED-on-revert: restore the single-path GetPostEdit branch and the stub's
// /edit handler will be hit → editCalls != 0 → test fails.
func TestImportSearchPost_SlotReported(t *testing.T) {
	var listCalls int32
	var editCalls int32
	var settingsCalls int32
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			// Single create: server returns {"id": ...}.
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			atomic.AddInt32(&settingsCalls, 1)
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[{"id":101,"name":"(GMT+03:00) SPb"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			atomic.AddInt32(&listCalls, 1)
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before snapshot: empty schedule.
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				// After snapshot: 1 created post.
				// 1753770300 = 2025-07-29 06:25:00 UTC → +3 = 09:25 local → date 29.07.2025.
				// time field "14:25" → hours 14, minutes 25 (independent of timestamp).
				w.Write([]byte(`{"list":[
					{"id":92820377,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],"total_rows":1,"is_has_more":false,"rows_limit":20}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			// MUST NOT be called — the single path uses the list snapshot-diff.
			atomic.AddInt32(&editCalls, 1)
			t.Errorf("unexpected /posts/92820377/edit call — single path must use the list snapshot-diff, not GET /posts/{id}/edit")
			w.Write([]byte(`{"id":92820377,"publication_date":{"date":"31.07.2026","hours":"14","minutes":"25"},"schedule_id":55}`))
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
	// ID set (server-returned id, confirmed by the diff).
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377", resp.ID)
	}
	// IDs carries exactly one id (the single create recovered as a batch of one).
	if len(resp.IDs) != 1 || resp.IDs[0] != 92820377 {
		t.Errorf("IDs = %v, want [92820377] (single create = batch of one)", resp.IDs)
	}
	// Slot reported from the list snapshot.
	if resp.PublicationDate == nil {
		t.Fatal("PublicationDate = nil, want the slot from the list snapshot-diff")
	}
	if got, want := resp.PublicationDate.Hours, "14"; got != want {
		t.Errorf("PublicationDate.Hours = %q, want %q", got, want)
	}
	if got, want := resp.PublicationDate.Minutes, "25"; got != want {
		t.Errorf("PublicationDate.Minutes = %q, want %q", got, want)
	}
	// Date from the timestamp at offset +3 (29.07.2025, not UTC 29.07.2025 —
	// same calendar day here, but the offset path is exercised).
	if got, want := resp.PublicationDate.Date, "29.07.2025"; got != want {
		t.Errorf("PublicationDate.Date = %q, want %q (timestamp 1753770300 at offset +3)", got, want)
	}
	if resp.ScheduleID != 55 {
		t.Errorf("ScheduleID = %d, want 55", resp.ScheduleID)
	}
	if resp.SlotLookupError != "" {
		t.Errorf("SlotLookupError = %q, want empty", resp.SlotLookupError)
	}
	// Regression guard: NO /posts/{id}/edit call on the single path.
	if got := atomic.LoadInt32(&editCalls); got != 0 {
		t.Errorf("GetPostEdit calls = %d, want 0 (single path uses list snapshot-diff, not /posts/{id}/edit)", got)
	}
	// At least 2 list calls (before + after snapshots).
	if got := atomic.LoadInt32(&listCalls); got < 2 {
		t.Errorf("ListPosts calls = %d, want >= 2 (before + after snapshots)", got)
	}
	// ONE settings call.
	if got := atomic.LoadInt32(&settingsCalls); got != 1 {
		t.Errorf("GetSettings calls = %d, want 1 (offset fetched once)", got)
	}
}

// TestImportSearchPost_BatchSlotSnapshotDiff verifies that a batch import
// into a schedule (when_type=3) recovers the created ids via a
// snapshot-diff (the server returns {"success": true} for a batch — no id,
// no ids). The before snapshot (taken before the create) has 2 pre-existing
// posts; the after snapshot has those 2 plus 3 newly created posts. The
// diff recovers exactly the 3 created ids, ordered by publication timestamp,
// with no GetPostEdit calls (the snapshot-diff replaces the old per-id
// fallback).
//
// RED-on-revert: if the batch path trusts resp.ID again (the old guard
// `resp.ID == 0 → return early`), no ids are recovered, Slots is empty, and
// the test fails at the Slots length check.
func TestImportSearchPost_BatchSlotSnapshotDiff(t *testing.T) {
	var listCalls int32
	var editCalls int32
	var settingsCalls int32
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			// Server returns {"success": true} for a batch — NO id, NO ids.
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			atomic.AddInt32(&settingsCalls, 1)
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[{"id":101,"name":"(GMT+03:00) Санкт-Петербург"}],"api_token":"SECRET","gpt_key":"SECRET","ru_captcha_key":"SECRET"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			atomic.AddInt32(&listCalls, 1)
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before snapshot: 2 pre-existing posts.
				w.Write([]byte(`{
					"list": [
						{"id":10000001,"publication_date":{"date":"28 Июля","time":"09:00","timestamp":1753670400,"source_timestamp":1753674000},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":10000002,"publication_date":{"date":"28 Июля","time":"12:00","timestamp":1753681200,"source_timestamp":1753684800},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
					],
					"total_rows": 2,
					"is_has_more": false,
					"rows_limit": 20
				}`))
			} else {
				// After snapshot: 2 pre-existing + 3 created.
				w.Write([]byte(`{
					"list": [
						{"id":10000001,"publication_date":{"date":"28 Июля","time":"09:00","timestamp":1753670400,"source_timestamp":1753674000},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":10000002,"publication_date":{"date":"28 Июля","time":"12:00","timestamp":1753681200,"source_timestamp":1753684800},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":92820377,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":92820378,"publication_date":{"date":"29 Июля","time":"16:25","timestamp":1753777500,"source_timestamp":1753781100},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":92820379,"publication_date":{"date":"30 Июля","time":"12:00","timestamp":1753856400,"source_timestamp":1753860000},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
					],
					"total_rows": 5,
					"is_has_more": false,
					"rows_limit": 20
				}`))
			}
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
	// ID set to the first recovered id (ordered by timestamp — 92820377
	// has the smallest timestamp of the three created).
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377 (first recovered id by timestamp)", resp.ID)
	}
	// IDs contains all recovered ids, ordered by timestamp.
	if len(resp.IDs) != 3 {
		t.Fatalf("IDs = %v, want 3 recovered ids", resp.IDs)
	}
	if resp.IDs[0] != 92820377 || resp.IDs[1] != 92820378 || resp.IDs[2] != 92820379 {
		t.Errorf("IDs = %v, want [92820377, 92820378, 92820379] (ordered by timestamp)", resp.IDs)
	}
	// No GetPostEdit calls — the snapshot-diff replaces per-id fallback.
	if got := atomic.LoadInt32(&editCalls); got != 0 {
		t.Errorf("GetPostEdit calls = %d, want 0 (snapshot-diff recovers ids, no per-id fallback)", got)
	}
	// At least 2 list calls (before + after snapshots).
	if got := atomic.LoadInt32(&listCalls); got < 2 {
		t.Errorf("ListPosts calls = %d, want >= 2 (before + after snapshots)", got)
	}
	// ONE settings call for the whole batch.
	if got := atomic.LoadInt32(&settingsCalls); got != 1 {
		t.Errorf("GetSettings calls = %d, want 1 (offset fetched once per batch)", got)
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

// TestImportSearchPost_SlotLookupFails verifies that a failed snapshot-diff
// on a SINGLE create (stub 500 on the after-snapshot list) does NOT fail
// the import: the server-returned id is still returned, SlotLookupError is
// set, and the method returns nil error (exit zero). The posts exist; this
// is reporting.
//
// RED-on-revert: this is the single-path failure contract under the unified
// snapshot-diff. The stub asserts NO /posts/{id}/edit call — the failure
// comes from the list, not /edit.
func TestImportSearchPost_SlotLookupFails(t *testing.T) {
	var editCalls int32
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before snapshot succeeds (empty).
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				// After snapshot fails (500).
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"server error"}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			atomic.AddInt32(&editCalls, 1)
			t.Errorf("unexpected /posts/92820377/edit call — single path uses the list snapshot-diff")
			w.WriteHeader(http.StatusInternalServerError)
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
	// The server-returned id is still returned (the create succeeded).
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377 (the id must still be returned)", resp.ID)
	}
	if resp.PublicationDate != nil {
		t.Errorf("PublicationDate = %+v, want nil (snapshot-diff failed)", resp.PublicationDate)
	}
	if resp.SlotLookupError == "" {
		t.Error("SlotLookupError = empty, want a non-empty error message")
	}
	// No /edit call — the failure came from the list.
	if got := atomic.LoadInt32(&editCalls); got != 0 {
		t.Errorf("GetPostEdit calls = %d, want 0 (single path uses list snapshot-diff)", got)
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

// TestCopySearchPost_SlotReported verifies the slot is reported for a
// single CopySearchPost (when_type=3) via the list snapshot-diff — the same
// mechanism as ImportSearchPost. The stub asserts NO /posts/{id}/edit call.
func TestCopySearchPost_SlotReported(t *testing.T) {
	var editCalls int32
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/copy":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				w.Write([]byte(`{"list":[
					{"id":92820377,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],"total_rows":1,"is_has_more":false,"rows_limit":20}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			atomic.AddInt32(&editCalls, 1)
			t.Errorf("unexpected /posts/92820377/edit call — CopySearchPost single path uses the list snapshot-diff")
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
		t.Errorf("PublicationDate = %+v, want hours=14 from the list snapshot-diff", resp.PublicationDate)
	}
	if resp.ScheduleID != 55 {
		t.Errorf("ScheduleID = %d, want 55", resp.ScheduleID)
	}
	if got := atomic.LoadInt32(&editCalls); got != 0 {
		t.Errorf("GetPostEdit calls = %d, want 0 (CopySearchPost single path uses list snapshot-diff)", got)
	}
}

// TestRewriteSearchPost_SlotReported verifies the slot is reported for a
// single RewriteSearchPost (when_type=3) via the list snapshot-diff. The
// stub asserts NO /posts/{id}/edit call.
func TestRewriteSearchPost_SlotReported(t *testing.T) {
	var editCalls int32
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			// POST /posts is the create (the list is GET /posts, below).
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"id":92820377}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				w.Write([]byte(`{"list":[
					{"id":92820377,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],"total_rows":1,"is_has_more":false,"rows_limit":20}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/posts/92820377/edit":
			atomic.AddInt32(&editCalls, 1)
			t.Errorf("unexpected /posts/92820377/edit call — RewriteSearchPost single path uses the list snapshot-diff")
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
		t.Errorf("PublicationDate = %+v, want hours=14 from the list snapshot-diff", resp.PublicationDate)
	}
	if got := atomic.LoadInt32(&editCalls); got != 0 {
		t.Errorf("GetPostEdit calls = %d, want 0 (RewriteSearchPost single path uses list snapshot-diff)", got)
	}
}

// TestPostPubDateToPublicationDate_Conversion verifies the conversion from
// the list-surface PostPublicationDate ({date, time, timestamp}) to the
// {date, hours, minutes} PublicationDate shape. With offset=0 the date is
// formatted in UTC (the baseline); offset behaviour is covered by
// TestPostPubDateToPublicationDate_OffsetShift.
func TestPostPubDateToPublicationDate_Conversion(t *testing.T) {
	ppd := &PostPublicationDate{
		Date:      "29 Июля",
		Time:      "14:25",
		Timestamp: FlexInt{},
	}
	// Set timestamp via JSON unmarshal (the normal path).
	_ = json.Unmarshal([]byte(`1753770300`), &ppd.Timestamp)

	pd, malformed := postPubDateToPublicationDate(ppd, 0)
	if pd == nil {
		t.Fatal("postPubDateToPublicationDate returned nil")
	}
	if malformed != "" {
		t.Errorf("malformed = %q, want empty (time 14:25 is valid)", malformed)
	}
	if pd.Hours != "14" {
		t.Errorf("Hours = %q, want 14", pd.Hours)
	}
	if pd.Minutes != "25" {
		t.Errorf("Minutes = %q, want 25", pd.Minutes)
	}
	// Date from timestamp formatted as dd.mm.yyyy at offset 0 (UTC).
	// 1753770300 = 2025-07-29 06:25:00 UTC → "29.07.2025"
	if pd.Date != "29.07.2025" {
		t.Errorf("Date = %q, want 29.07.2025 (from timestamp 1753770300 at offset 0/UTC)", pd.Date)
	}
}

// TestPostPubDateToPublicationDate_OffsetShift verifies that the date is
// formatted at the account's timezone offset, not UTC. A positive offset
// shifts the date forward; a negative offset shifts it back. The timestamps
// are chosen so UTC and the offset land on DIFFERENT calendar days — a
// test that passes either way proves nothing.
func TestPostPubDateToPublicationDate_OffsetShift(t *testing.T) {
	// Positive offset (+3): 23:30 UTC → 02:30 local (next day).
	// 1753831800 = 2025-07-29 23:30:00 UTC → UTC date "29.07.2025",
	// UTC+3 date "30.07.2025".
	var tsPos FlexInt
	_ = json.Unmarshal([]byte(`1753831800`), &tsPos)
	ppdPos := &PostPublicationDate{Time: "23:30", Timestamp: tsPos}
	pdPos, _ := postPubDateToPublicationDate(ppdPos, 3)
	if pdPos == nil {
		t.Fatal("postPubDateToPublicationDate returned nil for offset+3")
	}
	if pdPos.Date != "30.07.2025" {
		t.Errorf("offset+3: Date = %q, want 30.07.2025 (23:30 UTC + 3h = 02:30 next day)", pdPos.Date)
	}

	// Negative offset (-5): 02:00 UTC → 21:00 previous day local.
	// 1753754400 = 2025-07-29 02:00:00 UTC → UTC date "29.07.2025",
	// UTC-5 date "28.07.2025".
	var tsNeg FlexInt
	_ = json.Unmarshal([]byte(`1753754400`), &tsNeg)
	ppdNeg := &PostPublicationDate{Time: "02:00", Timestamp: tsNeg}
	pdNeg, _ := postPubDateToPublicationDate(ppdNeg, -5)
	if pdNeg == nil {
		t.Fatal("postPubDateToPublicationDate returned nil for offset-5")
	}
	if pdNeg.Date != "28.07.2025" {
		t.Errorf("offset-5: Date = %q, want 28.07.2025 (02:00 UTC - 5h = 21:00 previous day)", pdNeg.Date)
	}
}

// TestImportSearchPost_BatchMultiPage verifies that the snapshot-diff walks
// ALL pages of the schedule's post list, not just the first. The fixture
// has 25 pre-existing posts (page 1 = 20, page 2 = 5) before the create,
// and 28 after (page 1 = 20, page 2 = 8). A single-page snapshot would
// miss 5 pre-existing posts on page 2, mis-attributing them as "created"
// and recovering 8 ids instead of 3 → the count guard fires.
//
// RED-on-revert: if the walk uses single-page ListPosts instead of
// ListAllPostsWithTotal, the before snapshot sees only 20 of 25 pre-existing
// posts, the diff recovers 8 (3 real + 5 mis-attributed), the count guard
// fires (8 != 3), and SlotLookupError is non-empty — the test fails at the
// SlotLookupError check.
func TestImportSearchPost_BatchMultiPage(t *testing.T) {
	var createCalled int32

	// Build fixtures: 25 pre-existing posts (IDs 10000001-10000025), 3
	// created posts (IDs 92820377-92820379). Page size = 20.
	makePost := func(id int, ts int64) string {
		return fmt.Sprintf(`{"id":%d,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":%d,"source_timestamp":%d},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}`, id, ts, ts+3600)
	}

	buildList := func(ids []int, total int) string {
		var rows []string
		for _, id := range ids {
			rows = append(rows, makePost(id, int64(1753600000+id)))
		}
		// is_has_more is true when the page is full (rows_limit=20) —
		// the server signals "might have more" when the page is at capacity.
		hasMore := len(ids) == 20
		return fmt.Sprintf(`{"list":[%s],"total_rows":%d,"is_has_more":%s,"rows_limit":20}`, strings.Join(rows, ","), total, strconv.FormatBool(hasMore))
	}

	preExisting := make([]int, 25)
	for i := range preExisting {
		preExisting[i] = 10000001 + i
	}
	created := []int{92820377, 92820378, 92820379}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			page := r.URL.Query().Get("page")
			if page == "" {
				page = "1"
			}
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before: 25 pre-existing posts across 2 pages.
				if page == "1" {
					w.Write([]byte(buildList(preExisting[:20], 25)))
				} else {
					w.Write([]byte(buildList(preExisting[20:], 25)))
				}
			} else {
				// After: 25 pre-existing + 3 created = 28 across 2 pages.
				all := append(append([]int{}, preExisting...), created...)
				if page == "1" {
					w.Write([]byte(buildList(all[:20], 28)))
				} else {
					w.Write([]byte(buildList(all[20:], 28)))
				}
			}
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
		t.Fatalf("ImportSearchPost batch multi-page: %v", err)
	}
	// Exactly 3 ids recovered (not 8 — the walk saw all 25 pre-existing).
	if len(resp.IDs) != 3 {
		t.Errorf("IDs = %v, want 3 recovered ids (multi-page walk must see all pre-existing)", resp.IDs)
	}
	// No count-mismatch error (the walk was complete).
	if resp.SlotLookupError != "" {
		t.Errorf("SlotLookupError = %q, want empty (multi-page walk should recover exactly 3)", resp.SlotLookupError)
	}
	if len(resp.Slots) != 3 {
		t.Errorf("Slots = %d entries, want 3", len(resp.Slots))
	}
}

// TestImportSearchPost_BatchMalformedTime verifies that a malformed time
// field on a created post's list row populates SlotLookupError with the
// malformed value, and the slot's hours/minutes are empty (not silently
// wrong). The date is still populated from the timestamp.
func TestImportSearchPost_BatchMalformedTime(t *testing.T) {
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				// One created post with a malformed time field (no colon).
				w.Write([]byte(`{"list":[
					{"id":92820377,"publication_date":{"date":"29 Июля","time":"1425","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],"total_rows":1,"is_has_more":false,"rows_limit":20}`))
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001},
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch malformed time: %v", err)
	}
	// The id is recovered.
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377", resp.ID)
	}
	if len(resp.Slots) != 1 {
		t.Fatalf("Slots = %d entries, want 1", len(resp.Slots))
	}
	pd := resp.Slots[0].PublicationDate
	if pd == nil {
		t.Fatal("Slots[0].PublicationDate = nil")
	}
	// Hours/minutes are empty (malformed time).
	if pd.Hours != "" || pd.Minutes != "" {
		t.Errorf("Hours=%q Minutes=%q, want empty (malformed time 1425 has no colon)", pd.Hours, pd.Minutes)
	}
	// Date is still populated from the timestamp.
	if pd.Date == "" {
		t.Error("Date = empty, want the date from timestamp (malformed time does not affect date)")
	}
	// SlotLookupError names the malformed value.
	if resp.SlotLookupError == "" {
		t.Error("SlotLookupError = empty, want a message about the malformed time field")
	}
	if !strings.Contains(resp.SlotLookupError, "1425") {
		t.Errorf("SlotLookupError = %q, want it to mention the malformed value 1425", resp.SlotLookupError)
	}
}

// TestImportSearchPost_BatchCountMismatch verifies the count guard: when
// the snapshot-diff recovers a different number of ids than were sent
// (simulating a concurrent create by another client, or a post still
// processing and not yet in the list), SlotLookupError names both counts,
// the recovered ids are emitted in IDs, but no slot attribution is done.
// The create is NOT failed — exit zero, the posts exist.
func TestImportSearchPost_BatchCountMismatch(t *testing.T) {
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before: 2 pre-existing posts.
				w.Write([]byte(`{"list":[
					{"id":10000001,"publication_date":{"date":"28 Июля","time":"09:00","timestamp":1753670400,"source_timestamp":1753674000},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
					{"id":10000002,"publication_date":{"date":"28 Июля","time":"12:00","timestamp":1753681200,"source_timestamp":1753684800},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],"total_rows":2,"is_has_more":false,"rows_limit":20}`))
			} else {
				// After: 2 pre-existing + only 2 of the 3 created (one
				// is still processing, not in the list yet).
				w.Write([]byte(`{"list":[
					{"id":10000001,"publication_date":{"date":"28 Июля","time":"09:00","timestamp":1753670400,"source_timestamp":1753674000},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
					{"id":10000002,"publication_date":{"date":"28 Июля","time":"12:00","timestamp":1753681200,"source_timestamp":1753684800},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
					{"id":92820377,"publication_date":{"date":"29 Июля","time":"14:25","timestamp":1753770300,"source_timestamp":1753773900},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
					{"id":92820378,"publication_date":{"date":"29 Июля","time":"16:25","timestamp":1753777500,"source_timestamp":1753781100},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],"total_rows":4,"is_has_more":false,"rows_limit":20}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001, 2002, 2003}, // 3 sent
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch: a count mismatch must not fail the import, got: %v", err)
	}
	// SlotLookupError names both counts.
	if resp.SlotLookupError == "" {
		t.Error("SlotLookupError = empty, want a message about the count mismatch")
	}
	if !strings.Contains(resp.SlotLookupError, "2") || !strings.Contains(resp.SlotLookupError, "3") {
		t.Errorf("SlotLookupError = %q, want it to name both counts (recovered 2, sent 3)", resp.SlotLookupError)
	}
	// Recovered ids are emitted in IDs (2 of 3).
	if len(resp.IDs) != 2 {
		t.Errorf("IDs = %v, want 2 recovered ids", resp.IDs)
	}
	// No slot attribution (count mismatch → do not guess).
	if len(resp.Slots) != 0 {
		t.Errorf("Slots = %d entries, want 0 (count mismatch → no slot attribution)", len(resp.Slots))
	}
}

// TestImportSearchPost_BatchAfterSnapshotFails verifies that when the
// after-snapshot ListPosts call fails, no ids are recovered,
// SlotLookupError is set, and the create is NOT failed (exit zero). The
// posts exist; this is reporting.
func TestImportSearchPost_BatchAfterSnapshotFails(t *testing.T) {
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before snapshot succeeds.
				w.Write([]byte(`{"list":[
					{"id":10000001,"publication_date":{"date":"28 Июля","time":"09:00","timestamp":1753670400,"source_timestamp":1753674000},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
				],"total_rows":1,"is_has_more":false,"rows_limit":20}`))
			} else {
				// After snapshot fails.
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"server error"}`))
			}
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
		t.Fatalf("ImportSearchPost batch: an after-snapshot failure must not fail the import, got: %v", err)
	}
	// No ids recovered.
	if len(resp.IDs) != 0 {
		t.Errorf("IDs = %v, want empty (after-snapshot failed)", resp.IDs)
	}
	if resp.ID != 0 {
		t.Errorf("ID = %d, want 0 (no ids recovered)", resp.ID)
	}
	// SlotLookupError is set.
	if resp.SlotLookupError == "" {
		t.Error("SlotLookupError = empty, want a message about the after-snapshot failure")
	}
}

// TestImportSearchPost_BatchDateAtAccountOffset verifies that the batch path
// formats the publication date at the account's timezone offset (from
// GET /users/settings), not UTC. The fixture uses a post whose timestamp
// falls at 23:30 UTC — at UTC the date is 29.07.2025, at UTC+3 it is
// 30.07.2025 (the next day).
//
// RED-on-revert: if the offset is ignored (format in UTC), the batch date
// is 29.07.2025, not 30.07.2025 → the assertion fails.
func TestImportSearchPost_BatchDateAtAccountOffset(t *testing.T) {
	var settingsCalls int32
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			atomic.AddInt32(&settingsCalls, 1)
			w.Write([]byte(`{"timezone_id":101,"timezone_offset":3,"timezones":[{"id":101,"name":"(GMT+03:00) SPb"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before: empty schedule.
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				// After: 3 created posts.
				// 1753831800 = 2025-07-29 23:30:00 UTC → UTC date 29.07.2025,
				// UTC+3 date 30.07.2025. The first post carries this timestamp.
				w.Write([]byte(`{
					"list": [
						{"id":92820377,"publication_date":{"date":"30 Июля","time":"02:30","timestamp":1753831800,"source_timestamp":1753842600},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":92820378,"publication_date":{"date":"30 Июля","time":"10:00","timestamp":1753856400,"source_timestamp":1753867200},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":92820379,"publication_date":{"date":"30 Июля","time":"14:00","timestamp":1753870800,"source_timestamp":1753881600},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
					],
					"total_rows": 3,
					"is_has_more": false,
					"rows_limit": 20
				}`))
			}
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
	if len(resp.Slots) != 3 {
		t.Fatalf("Slots = %d entries, want 3", len(resp.Slots))
	}
	// The first slot's date must be 30.07.2025 (UTC+3), NOT 29.07.2025 (UTC).
	pd := resp.Slots[0].PublicationDate
	if pd == nil {
		t.Fatal("Slots[0].PublicationDate = nil")
	}
	if pd.Date != "30.07.2025" {
		t.Errorf("Slots[0].Date = %q, want 30.07.2025 (23:30 UTC at offset+3 = 02:30 next day) — if this is 29.07.2025 the offset was ignored (UTC bug)", pd.Date)
	}
	// Settings called once for the batch of three, not three times.
	if got := atomic.LoadInt32(&settingsCalls); got != 1 {
		t.Errorf("GetSettings calls = %d, want 1 (offset fetched once per batch)", got)
	}
	if resp.SlotLookupError != "" {
		t.Errorf("SlotLookupError = %q, want empty (settings lookup succeeded)", resp.SlotLookupError)
	}
}

// TestImportSearchPost_BatchDateNegativeOffset verifies the negative-offset
// case: a post at 02:00 UTC with timezone_offset -5 → the date is the
// PREVIOUS day (28.07.2025, not 29.07.2025 UTC).
func TestImportSearchPost_BatchDateNegativeOffset(t *testing.T) {
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.Write([]byte(`{"timezone_id":5,"timezone_offset":-5,"timezones":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				// 1753754400 = 2025-07-29 02:00:00 UTC → UTC date 29.07.2025,
				// UTC-5 date 28.07.2025 (previous day).
				w.Write([]byte(`{
					"list": [
						{"id":92820377,"publication_date":{"date":"28 Июля","time":"21:00","timestamp":1753754400,"source_timestamp":1753736400},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":92820378,"publication_date":{"date":"29 Июля","time":"10:00","timestamp":1753790400,"source_timestamp":1753772400},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
					],
					"total_rows": 2,
					"is_has_more": false,
					"rows_limit": 20
				}`))
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
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
	if len(resp.Slots) != 2 {
		t.Fatalf("Slots = %d entries, want 2", len(resp.Slots))
	}
	pd := resp.Slots[0].PublicationDate
	if pd == nil {
		t.Fatal("Slots[0].PublicationDate = nil")
	}
	if pd.Date != "28.07.2025" {
		t.Errorf("Slots[0].Date = %q, want 28.07.2025 (02:00 UTC at offset-5 = 21:00 previous day)", pd.Date)
	}
}

// TestImportSearchPost_BatchSettingsLookupFails verifies that a failed
// settings lookup (stub 500) does NOT fail the import: the ids are still
// recovered from the snapshot-diff, exit zero, SlotLookupError records the
// offset was unavailable, and the publication dates for recovered ids are
// OMITTED (empty) — a stated-unknown date is better than a silently-wrong
// one. Hours/minutes are still correct (from the time field, not the
// timestamp).
func TestImportSearchPost_BatchSettingsLookupFails(t *testing.T) {
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/settings":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"server error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			} else {
				w.Write([]byte(`{
					"list": [
						{"id":92820377,"publication_date":{"date":"30 Июля","time":"14:25","timestamp":1753831800,"source_timestamp":1753842600},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]},
						{"id":92820378,"publication_date":{"date":"30 Июля","time":"16:25","timestamp":1753839000,"source_timestamp":1753849800},"is_published":0,"is_ad":0,"is_repeated":0,"is_attachments_in_process":0,"is_planned_by_networks":0,"is_planning_by_networks_needed":0,"views":null,"likes":null,"comments":null,"reposts":null,"text":"","link":"","source_link":"","repost_link":"","repost_title":"","photos_amount":0,"created_by":1,"errors_for_source_ids":[]}
					],
					"total_rows": 2,
					"is_has_more": false,
					"rows_limit": 20
				}`))
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
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
		t.Fatalf("ImportSearchPost: a failed settings lookup must not fail the import, got error: %v", err)
	}
	// The ids are still recovered (exit zero).
	if resp.ID != 92820377 {
		t.Errorf("ID = %d, want 92820377 (recovered from snapshot-diff)", resp.ID)
	}
	if len(resp.IDs) != 2 {
		t.Errorf("IDs = %v, want 2 recovered ids", resp.IDs)
	}
	// SlotLookupError records the offset was unavailable.
	if resp.SlotLookupError == "" {
		t.Error("SlotLookupError = empty, want a message about the unavailable timezone offset")
	}
	if !strings.Contains(resp.SlotLookupError, "timezone offset unavailable") {
		t.Errorf("SlotLookupError = %q, want it to mention the unavailable timezone offset", resp.SlotLookupError)
	}
	// Both slots resolved (diff matched), but dates are OMITTED (empty).
	if len(resp.Slots) != 2 {
		t.Fatalf("Slots = %d entries, want 2", len(resp.Slots))
	}
	for i, s := range resp.Slots {
		if s.PublicationDate == nil {
			t.Errorf("Slots[%d].PublicationDate = nil, want non-nil with hours/minutes", i)
			continue
		}
		if s.PublicationDate.Date != "" {
			t.Errorf("Slots[%d].Date = %q, want empty (offset unavailable — date omitted, not guessed at UTC)", i, s.PublicationDate.Date)
		}
		// Hours/minutes are still correct (from the time field).
		if s.PublicationDate.Hours == "" {
			t.Errorf("Slots[%d].Hours = empty, want the time from the list row", i)
		}
	}
}

// TestSettings_DecodeCredentialHygiene verifies that GET /users/settings
// carrying api_token, gpt_key, and ru_captcha_key cannot reach the
// marshalled SettingsResponse output. The narrow struct models only
// timezone_id, timezone_offset, and the timezones array — the credential
// fields are silently dropped at decode and absent from any re-marshal.
// This is the RED-on-revert test for the credential-hygiene invariant on
// the settings decode path: if SettingsResponse were widened to
// map[string]interface{} or json.RawMessage, the credential values would
// leak through to stdout via printJSON.
func TestSettings_DecodeCredentialHygiene(t *testing.T) {
	body := `{
		"timezone_id": 101,
		"timezone_offset": 3,
		"timezones": [{"id": 101, "name": "(GMT+03:00) Санкт-Петербург"}],
		"api_token": "SECRET_API_TOKEN_VALUE",
		"gpt_key": "SECRET_GPT_KEY_VALUE",
		"ru_captcha_key": "SECRET_RU_CAPTCHA_KEY_VALUE"
	}`
	var settings SettingsResponse
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	secretValues := []string{
		"SECRET_API_TOKEN_VALUE",
		"SECRET_GPT_KEY_VALUE",
		"SECRET_RU_CAPTCHA_KEY_VALUE",
	}
	for _, secret := range secretValues {
		if strings.Contains(got, secret) {
			t.Errorf("marshalled SettingsResponse leaked credential value %q:\n%s", secret, got)
		}
	}
	// The modelled fields must still be present.
	if settings.TimezoneID != 101 {
		t.Errorf("TimezoneID = %d, want 101", settings.TimezoneID)
	}
	if settings.TimezoneOffset != 3 {
		t.Errorf("TimezoneOffset = %d, want 3", settings.TimezoneOffset)
	}
	if len(settings.Timezones) != 1 || settings.Timezones[0].ID != 101 {
		t.Errorf("Timezones = %+v, want one entry with id 101", settings.Timezones)
	}
	if !strings.Contains(got, `"timezone_id":101`) {
		t.Errorf("marshalled SettingsResponse lost timezone_id:\n%s", got)
	}
}
