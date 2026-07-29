package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Mode invariant guard (issue #66) ---

// TestSchedulePayload_Validate_ModeInvariant is the RED-on-revert test for
// the fail-closed guard on CreateSchedule. The Hooppy server enforces a
// mode-dependent requirement:
//   - publication_how_type=1 (manual): selected_pages_by_source_ids must be
//     non-empty.
//   - publication_how_type=2 (by project): project_id must be non-zero.
//
// NewSchedulePayload defaults to how_type=1 with NO pages — its output is
// unsatisfiable by construction. Before #66, every CreateSchedule call 500'd
// with "Undefined index: <key>" (teaching the caller nothing). The guard
// refuses locally, naming the invariant and what would satisfy it.
//
// RED-on-revert: remove the Validate() call from CreateSchedule (or delete
// the Validate method) → the unsatisfiable-payload case sends a request to
// the server instead of failing locally → the test fails because
// CreateSchedule did not return the expected validation error.
func TestSchedulePayload_Validate_ModeInvariant(t *testing.T) {
	t.Run("how_type_1_no_pages_refused", func(t *testing.T) {
		// NewSchedulePayload default: how_type=1, no pages — unsatisfiable.
		payload := NewSchedulePayload("test")
		err := payload.Validate()
		if err == nil {
			t.Fatal("Validate: expected error for how_type=1 with no pages, got nil — the default NewSchedulePayload output is unsatisfiable and must be refused")
		}
		if !strings.Contains(err.Error(), "publication_how_type=1") {
			t.Errorf("error must name the failed invariant, got: %v", err)
		}
		if !strings.Contains(err.Error(), "selected_pages_by_source_ids") {
			t.Errorf("error must name the field that would satisfy the invariant, got: %v", err)
		}
	})

	t.Run("how_type_1_with_pages_accepted", func(t *testing.T) {
		payload := NewSchedulePayload("test")
		payload.SelectedPagesBySourceIDs = map[int][]int{1: {100, 200}}
		if err := payload.Validate(); err != nil {
			t.Fatalf("Validate: how_type=1 with pages should pass, got: %v", err)
		}
	})

	t.Run("how_type_2_no_project_refused", func(t *testing.T) {
		payload := NewSchedulePayload("test")
		payload.PublicationHowType = 2
		err := payload.Validate()
		if err == nil {
			t.Fatal("Validate: expected error for how_type=2 with project_id=0, got nil")
		}
		if !strings.Contains(err.Error(), "publication_how_type=2") {
			t.Errorf("error must name the failed invariant, got: %v", err)
		}
		if !strings.Contains(err.Error(), "project_id") {
			t.Errorf("error must name the field that would satisfy the invariant, got: %v", err)
		}
	})

	t.Run("how_type_2_with_project_accepted", func(t *testing.T) {
		payload := NewSchedulePayload("test")
		payload.PublicationHowType = 2
		payload.ProjectID = 7
		if err := payload.Validate(); err != nil {
			t.Fatalf("Validate: how_type=2 with project_id should pass, got: %v", err)
		}
	})

	t.Run("unknown_how_type_refused", func(t *testing.T) {
		payload := NewSchedulePayload("test")
		payload.PublicationHowType = 99
		err := payload.Validate()
		if err == nil {
			t.Fatal("Validate: expected error for how_type=99, got nil")
		}
		if !strings.Contains(err.Error(), "99") {
			t.Errorf("error must name the unrecognised mode, got: %v", err)
		}
	})
}

// TestCreateSchedule_UnsatisfiablePayloadRefused is the RED-on-revert test
// for the guard wired into CreateSchedule. NewSchedulePayload's default
// (how_type=1, no pages) is unsatisfiable — CreateSchedule must refuse
// locally, WITHOUT sending a request to the server. The stub asserts NO
// request reaches /posts/schedules; if the guard is removed, a POST is sent
// and the test fails on the request-count assertion.
//
// RED-on-revert: remove the Validate() call from CreateSchedule → a POST
// request reaches the server → requestReceived is true → test fails.
func TestCreateSchedule_UnsatisfiablePayloadRefused(t *testing.T) {
	var requestReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		t.Errorf("unexpected request to %s %s — CreateSchedule must refuse locally before any request when the mode invariant is unmet", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	payload := NewSchedulePayload("unsatisfiable") // how_type=1, no pages
	_, err := c.CreateSchedule(context.Background(), payload)
	if err == nil {
		t.Fatal("CreateSchedule: expected validation error for unsatisfiable payload, got nil")
	}
	if !strings.Contains(err.Error(), "publication_how_type=1") {
		t.Errorf("error must name the failed invariant, got: %v", err)
	}
	if requestReceived {
		t.Fatal("a request reached the server — the guard must refuse BEFORE any request")
	}
}

// --- Read-modify-write byte-identity round trip (issue #66) ---

// scheduleEditFullResponse is a realistic 72-key GET /posts/schedules/{id}/edit
// response fixture. It includes:
//   - The 13 keys ScheduleEditResponse models (id, name, times, posts_hashtags,
//     posts_links, project_id, projects, selected_pages_by_source_ids,
//     selected_albums_by_source_ids, social_pages_by_accounts,
//     social_albums_by_pages, watermarks).
//   - The 23 keys SchedulePayload models that ScheduleEditResponse does NOT
//     model (state, publication_how_type, publication_where_type,
//     watermark_id, utm_tags, is_unique_content, ... publish_as_carousel).
//   - Additional keys neither struct models (user_id, position, start_date,
//     stop_date, is_deleted, is_random_content, is_posts_repeated,
//     posts_caption, posts_comment, posts_location, etc.).
//   - The KNOWN-HOSTILE fields publish_as_story_source_ids and
//     share_stories_to_feed_source_ids as comma-separated STRINGS ("1,2,7,9"),
//     which is how the live API carries them — typed int in SchedulePayload
//     but the server coerces. The round trip through map[string]json.RawMessage
//     must preserve them byte-identically; a Go struct round trip would
//     mangle them (int 0 vs. the original string).
//
// Every KEY and every VALUE TYPE is as the server sends them. IDs are small
// integers; names are "A"/"B"; URLs are example.invalid.
const scheduleEditFullResponse = `{
	"id": 42,
	"name": "A",
	"user_id": 1,
	"position": 0,
	"state": 1,
	"is_deleted": 0,
	"start_date": 0,
	"stop_date": 0,
	"publication_how_type": 1,
	"publication_where_type": 1,
	"watermark_id": 0,
	"utm_tags": "",
	"is_unique_content": 0,
	"is_posts_repeated": 0,
	"is_random_content": 0,
	"is_comments_disabled": 0,
	"publish_as_story": 0,
	"publish_as_story_source_ids": "1,2,7,9",
	"publish_as_reels": 0,
	"publish_as_clips": 0,
	"publish_as_shorts": 0,
	"publish_as_article": 0,
	"publish_as_article_by_link": 0,
	"publish_in_channel": 0,
	"share_stories_to_feed": 0,
	"share_stories_to_feed_source_ids": "1,2,7,9",
	"share_reels_to_feed": 0,
	"share_clips_to_feed": 0,
	"share_clips_to_feed_with_text": 0,
	"share_clips_to_feed_if_no_video": 0,
	"share_channel_to_feed": 0,
	"expand_clips_title": 0,
	"publish_as_user": 0,
	"add_link_to_user": 0,
	"message_to_community": 0,
	"message_to_channel": 0,
	"download_vk_videos": 0,
	"save_vk_videos_names": 0,
	"plan_by_network": 0,
	"publish_as_carousel": 0,
	"posts_caption": 0,
	"posts_comment": 0,
	"posts_location": 0,
	"posts_location_vk": 0,
	"posts_photo": 0,
	"posts_photo_always": 0,
	"posts_hashtags": 0,
	"posts_links": 0,
	"posts_rewrite": 0,
	"times": [
		[{"hours":12,"minutes":25},{"hours":14,"minutes":25},{"hours":16,"minutes":"00"},{"hours":17,"minutes":35}],
		[],
		[{"hours":12,"minutes":25},{"hours":14,"minutes":25}],
		[],
		[],
		[],
		[]
	],
	"posts_hashtags_obj": {"tag1": "value1"},
	"posts_links_obj": {"link1": "https://example.invalid/x"},
	"project_id": 7,
	"projects": [
		{"id": 7, "user_id": 1, "position": 0, "name": "A", "is_deleted": 0, "publication_where_type": 1, "posts_count": 3, "watermark_id": 0, "utm_tags": ""}
	],
	"selected_pages_by_source_ids": {"1": [100, 200]},
	"selected_albums_by_source_ids": {"1": [300]},
	"social_pages_by_accounts": [
		{
			"account": {"id": 123, "social_id": "3251", "source_id": 6, "name": "A", "photo": "https://example.invalid/x", "link": "https://example.invalid/x"},
			"pages": [
				{"id": 100, "social_id": "100", "type": "board", "name": "A", "alias": "", "photo": "", "link": "https://example.invalid/x"},
				{"id": 101, "social_id": "101", "type": "board", "name": "B", "alias": "", "photo": "", "link": "https://example.invalid/x"}
			]
		}
	],
	"social_albums_by_pages": [],
	"watermarks": [
		{"id": 1, "user_id": 1, "name": "A", "file": "https://example.invalid/x", "space": 10, "position": 1, "opacity": 80, "size": 50}
	]
}`

// TestUpdateScheduleFromEdit_ByteIdentity is THE critical test for issue #66.
// It verifies that a round trip through UpdateScheduleFromEdit with a change
// to one field (name) leaves every other field byte-identical on the wire.
//
// The fixture (scheduleEditFullResponse) carries 72 keys, including many
// SchedulePayload does NOT model (times, posts_hashtags_obj, projects,
// social_pages_by_accounts, social_albums_by_pages, watermarks, user_id,
// position, start_date, stop_date, is_deleted, posts_caption, posts_comment,
// posts_location, etc.) and the KNOWN-HOSTILE string fields
// (publish_as_story_source_ids="1,2,7,9", share_stories_to_feed_source_ids).
//
// The test decodes the PUT request body as map[string]interface{} and asserts
// EVERY key from the original fixture is present with the same value — except
// the overridden key ("name"). It does NOT assert on the Go struct, which by
// definition cannot show the fields it does not model.
//
// RED-on-revert: drop one unmodelled field from the read-modify-write body
// (e.g. revert UpdateScheduleFromEdit to decode into ScheduleEditResponse and
// re-marshal, which drops the 36 unmodelled keys) → the test fails, naming
// every dropped key. The test is designed so that even a SINGLE dropped key
// produces a failure with the key name.
func TestUpdateScheduleFromEdit_ByteIdentity(t *testing.T) {
	// Decode the fixture once to get the set of keys + values to compare against.
	var fixtureMap map[string]interface{}
	if err := json.Unmarshal([]byte(scheduleEditFullResponse), &fixtureMap); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	var capturedPUTBody []byte
	var putReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/42/edit":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(scheduleEditFullResponse))
		case r.Method == http.MethodPut && r.URL.Path == "/posts/schedules/42":
			putReceived = true
			body, _ := io.ReadAll(r.Body)
			capturedPUTBody = body
			w.Write([]byte(`{"schedules":[{"id":42,"name":"New Name"}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	overrides, err := ScheduleOverride("name", "New Name")
	if err != nil {
		t.Fatalf("ScheduleOverride: %v", err)
	}
	resp, err := c.UpdateScheduleFromEdit(context.Background(), 42, overrides)
	if err != nil {
		t.Fatalf("UpdateScheduleFromEdit: %v", err)
	}
	if !putReceived {
		t.Fatal("no PUT request was sent")
	}
	if resp.Schedules[0].Name != "New Name" {
		t.Errorf("response name = %q, want New Name", resp.Schedules[0].Name)
	}

	// Decode the PUT body as a generic map — NOT as a Go struct, which by
	// definition cannot show the fields it does not model.
	var putMap map[string]interface{}
	if err := json.Unmarshal(capturedPUTBody, &putMap); err != nil {
		t.Fatalf("decode PUT body: %v", err)
	}

	// Assert EVERY key from the fixture is present in the PUT body.
	// This is the byte-identity check: no unmodelled field may be dropped.
	var missingKeys []string
	for key := range fixtureMap {
		if _, ok := putMap[key]; !ok {
			missingKeys = append(missingKeys, key)
		}
	}
	if len(missingKeys) > 0 {
		t.Errorf("PUT body is missing %d key(s) from the /edit response — the read-modify-write helper dropped unmodelled fields: %v", len(missingKeys), missingKeys)
	}

	// Assert every key has the SAME VALUE as the fixture, except the overridden
	// key ("name"). Compare via re-marshalled JSON bytes to avoid float64/map
	// comparison issues.
	for key, expectedVal := range fixtureMap {
		if key == "name" {
			continue // this is the field we overrode
		}
		gotVal, ok := putMap[key]
		if !ok {
			continue // already reported in missingKeys
		}
		expectedBytes, _ := json.Marshal(expectedVal)
		gotBytes, _ := json.Marshal(gotVal)
		if string(expectedBytes) != string(gotBytes) {
			t.Errorf("PUT body key %q = %s, want %s (byte-identity violated — the read-modify-write helper altered an unmodelled field)", key, gotBytes, expectedBytes)
		}
	}

	// The overridden key must have the new value.
	if putMap["name"] != "New Name" {
		t.Errorf("PUT body name = %v, want \"New Name\" (the override was not applied)", putMap["name"])
	}

	// The KNOWN-HOSTILE string fields must be preserved byte-identically.
	// These are typed int in SchedulePayload but the server carries them as
	// comma-separated strings. A Go struct round trip would mangle them
	// (int 0 vs. "1,2,7,9"). The map[string]json.RawMessage path preserves them.
	if putMap["publish_as_story_source_ids"] != "1,2,7,9" {
		t.Errorf("publish_as_story_source_ids = %v, want \"1,2,7,9\" (KNOWN-HOSTILE string field mangled by round trip)", putMap["publish_as_story_source_ids"])
	}
	if putMap["share_stories_to_feed_source_ids"] != "1,2,7,9" {
		t.Errorf("share_stories_to_feed_source_ids = %v, want \"1,2,7,9\" (KNOWN-HOSTILE string field mangled by round trip)", putMap["share_stories_to_feed_source_ids"])
	}
}

// TestUpdateScheduleFromEdit_PreservesAllKeys_Count is a focused count check
// that the PUT body has exactly the same number of keys as the /edit response.
// If the helper drops or adds any key, the count mismatches.
func TestUpdateScheduleFromEdit_PreservesAllKeys_Count(t *testing.T) {
	var fixtureMap map[string]interface{}
	if err := json.Unmarshal([]byte(scheduleEditFullResponse), &fixtureMap); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	expectedKeyCount := len(fixtureMap)

	var capturedPUTBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/42/edit":
			w.Write([]byte(scheduleEditFullResponse))
		case r.Method == http.MethodPut && r.URL.Path == "/posts/schedules/42":
			capturedPUTBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"schedules":[{"id":42,"name":"X"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	overrides, _ := ScheduleOverride("state", 1)
	_, err := c.UpdateScheduleFromEdit(context.Background(), 42, overrides)
	if err != nil {
		t.Fatalf("UpdateScheduleFromEdit: %v", err)
	}

	var putMap map[string]interface{}
	if err := json.Unmarshal(capturedPUTBody, &putMap); err != nil {
		t.Fatalf("decode PUT body: %v", err)
	}
	if len(putMap) != expectedKeyCount {
		t.Errorf("PUT body key count = %d, want %d (the /edit response key count) — the helper dropped or added keys", len(putMap), expectedKeyCount)
	}
}

// TestUpdateScheduleFromEdit_NoOverridesRefused verifies the helper refuses
// an empty override map rather than sending a no-op PUT (which would still
// be a full-state write with no changes — wasteful and potentially harmful
// if the server re-derives any fields).
func TestUpdateScheduleFromEdit_NoOverridesRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — empty overrides must be refused locally", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.UpdateScheduleFromEdit(context.Background(), 42, map[string]json.RawMessage{})
	if err == nil {
		t.Fatal("expected error for empty overrides, got nil")
	}
	if !strings.Contains(err.Error(), "at least one override") {
		t.Errorf("error must name the requirement, got: %v", err)
	}
}

// TestUpdateScheduleFromEdit_ZeroIDRefused verifies the helper refuses id=0
// rather than sending a request to /posts/schedules/0/edit.
func TestUpdateScheduleFromEdit_ZeroIDRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — id=0 must be refused locally", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	overrides, _ := ScheduleOverride("name", "X")
	_, err := c.UpdateScheduleFromEdit(context.Background(), 0, overrides)
	if err == nil {
		t.Fatal("expected error for id=0, got nil")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error must name the requirement, got: %v", err)
	}
}
