package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"testing"
)

func TestCreateSchedule(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"schedules":[{"id":123,"name":"Test Schedule","state":1}]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	payload := NewSchedulePayload("Test Schedule")
	payload.PublishAsStory = 1
	resp, err := c.CreateSchedule(context.Background(), payload)
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedMethod)
	}
	if capturedPath != "/posts/schedules" {
		t.Errorf("path = %s, want /posts/schedules", capturedPath)
	}
	if capturedBody["name"] != "Test Schedule" {
		t.Errorf("body name = %v, want Test Schedule", capturedBody["name"])
	}
	if capturedBody["state"] != float64(1) {
		t.Errorf("body state = %v, want 1", capturedBody["state"])
	}
	if capturedBody["publish_as_story"] != float64(1) {
		t.Errorf("body publish_as_story = %v, want 1", capturedBody["publish_as_story"])
	}
	// Verify all 34 required fields are present
	requiredFields := []string{
		"name", "state", "publication_how_type", "publication_where_type",
		"watermark_id", "utm_tags", "is_unique_content", "is_posts_repeated",
		"is_random_content", "is_comments_disabled", "publish_as_story",
		"publish_as_story_source_ids", "publish_as_reels", "publish_as_clips",
		"publish_as_shorts", "publish_as_article", "publish_as_article_by_link",
		"publish_in_channel", "share_stories_to_feed", "share_stories_to_feed_source_ids",
		"share_reels_to_feed", "share_clips_to_feed", "share_clips_to_feed_with_text",
		"share_clips_to_feed_if_no_video", "share_channel_to_feed", "expand_clips_title",
		"publish_as_user", "add_link_to_user", "message_to_community",
		"message_to_channel", "download_vk_videos", "save_vk_videos_names",
		"plan_by_network", "publish_as_carousel",
	}
	for _, field := range requiredFields {
		if _, ok := capturedBody[field]; !ok {
			t.Errorf("missing required field %q in request body", field)
		}
	}
	if len(resp.Schedules) != 1 || resp.Schedules[0].ID != 123 {
		t.Errorf("resp = %+v, want schedule ID 123", resp)
	}
}

func TestNewSchedulePayload_Defaults(t *testing.T) {
	p := NewSchedulePayload("My Schedule")
	if p.Name != "My Schedule" {
		t.Errorf("Name = %q, want My Schedule", p.Name)
	}
	if p.State != 1 {
		t.Errorf("State = %d, want 1 (active)", p.State)
	}
	if p.PublicationHowType != 1 {
		t.Errorf("PublicationHowType = %d, want 1 (manual)", p.PublicationHowType)
	}
	if p.PublicationWhereType != 1 {
		t.Errorf("PublicationWhereType = %d, want 1 (pages)", p.PublicationWhereType)
	}
	// All flags should default to 0
	if p.PublishAsStory != 0 || p.PublishAsClips != 0 || p.IsCommentsDisabled != 0 {
		t.Errorf("flags should default to 0, got story=%d clips=%d comments=%d",
			p.PublishAsStory, p.PublishAsClips, p.IsCommentsDisabled)
	}
}

func TestUpdateSchedule(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Write([]byte(`{"schedules":[{"id":55608,"name":"Updated"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	payload := NewSchedulePayload("Updated")
	resp, err := c.UpdateSchedule(context.Background(), 55608, payload)
	if err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	if capturedMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", capturedMethod)
	}
	if capturedPath != "/posts/schedules/55608" {
		t.Errorf("path = %s, want /posts/schedules/55608", capturedPath)
	}
	if resp.Schedules[0].Name != "Updated" {
		t.Errorf("name = %q, want Updated", resp.Schedules[0].Name)
	}
}

func TestDeleteSchedule(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Write([]byte(`{"success":true,"schedules":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.DeleteSchedule(context.Background(), 55608)
	if err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", capturedMethod)
	}
	if capturedPath != "/posts/schedules/55608" {
		t.Errorf("path = %s, want /posts/schedules/55608", capturedPath)
	}
	if !resp.Success {
		t.Error("resp.Success = false, want true")
	}
}

func TestUpdateProject(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.UpdateProject(context.Background(), 17711, "New Name")
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if capturedMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", capturedMethod)
	}
	if capturedPath != "/posts/projects/17711" {
		t.Errorf("path = %s, want /posts/projects/17711", capturedPath)
	}
	if capturedBody["name"] != "New Name" {
		t.Errorf("body name = %v, want New Name", capturedBody["name"])
	}
	if !resp.Success {
		t.Error("resp.Success = false, want true")
	}
}

func TestDeleteProject(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.DeleteProject(context.Background(), 17711)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", capturedMethod)
	}
	if capturedPath != "/posts/projects/17711" {
		t.Errorf("path = %s, want /posts/projects/17711", capturedPath)
	}
	if !resp.Success {
		t.Error("resp.Success = false, want true")
	}
}

func TestCreateSchedule_AllFieldsSerialized(t *testing.T) {
	// Verify that ALL 34 fields are serialized (no omitempty) — the API
	// requires every field to be present.
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"schedules":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	payload := NewSchedulePayload("test")
	_, _ = c.CreateSchedule(context.Background(), payload)

	// All fields must be present (no omitempty on SchedulePayload)
	expectedFields := 34
	if len(capturedBody) != expectedFields {
		t.Errorf("serialized field count = %d, want %d (all fields must be present, no omitempty)", len(capturedBody), expectedFields)
		t.Logf("fields: %v", capturedBody)
	}
}

// TestListSchedules_PageParam verifies the page param mapping AND that
// consecutive page inputs emit DISTINCT page= query values. The API is
// 1-indexed: page 0 (or omit) hits server page 1, page 1 sends page=1,
// page 2 sends page=2. A client iterating 0,1,2,3 must NOT receive
// page1,page1,page2,page3 — the distinctness assertion catches any mapping
// that collapses 0 and 1 (the double-fetch this PR fixed), and the absolute
// assertions catch a boundary translation (apiPage = userPage + 1) that would
// silently shift every caller's page by one.
func TestListSchedules_PageParam(t *testing.T) {
	pages := []int{0, 1, 2}
	wantAbs := map[int]string{0: "", 1: "1", 2: "2"}
	got := make([]string, len(pages))
	for i, p := range pages {
		var capturedURL string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedURL = r.URL.String()
			w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
		}))
		c := newTestClient(t, srv)
		if _, err := c.ListSchedules(context.Background(), p); err != nil {
			srv.Close()
			t.Fatalf("ListSchedules(%d): %v", p, err)
		}
		srv.Close()
		got[i] = pageParamFromURL(capturedURL)
	}
	// Absolute mapping: page 0 -> no param, page N -> "N" for N>=1.
	for i, p := range pages {
		if got[i] != wantAbs[p] {
			t.Errorf("page %d emitted page=%q, want %q", p, got[i], wantAbs[p])
		}
	}
	// Distinctness: consecutive pages must emit distinct page= values, so a
	// walk iterating 0,1,2 cannot fetch the same server page twice.
	if got[0] == got[1] {
		t.Errorf("page 0 and page 1 both emit page=%q — double-fetch (server page 1 fetched twice)", got[0])
	}
	if got[1] == got[2] {
		t.Errorf("page 1 and page 2 both emit page=%q — distinct pages collapsed", got[1])
	}
}

// pageParamFromURL extracts the raw "page" query value ("" if absent) from a
// request URL string. Used by the page-param distinctness tests.
func pageParamFromURL(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("page")
}

// TestListProjects_PageParam mirrors TestListSchedules_PageParam: verifies
// the absolute page mapping AND that consecutive page inputs emit DISTINCT
// page= values (no double-fetch). See TestListSchedules_PageParam for the
// rationale.
func TestListProjects_PageParam(t *testing.T) {
	pages := []int{0, 1, 2}
	wantAbs := map[int]string{0: "", 1: "1", 2: "2"}
	got := make([]string, len(pages))
	for i, p := range pages {
		var capturedURL string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedURL = r.URL.String()
			w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
		}))
		c := newTestClient(t, srv)
		if _, err := c.ListProjects(context.Background(), p); err != nil {
			srv.Close()
			t.Fatalf("ListProjects(%d): %v", p, err)
		}
		srv.Close()
		got[i] = pageParamFromURL(capturedURL)
	}
	for i, p := range pages {
		if got[i] != wantAbs[p] {
			t.Errorf("page %d emitted page=%q, want %q", p, got[i], wantAbs[p])
		}
	}
	if got[0] == got[1] {
		t.Errorf("page 0 and page 1 both emit page=%q — double-fetch", got[0])
	}
	if got[1] == got[2] {
		t.Errorf("page 1 and page 2 both emit page=%q — distinct pages collapsed", got[1])
	}
}

func TestListSchedules_IsHasMore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schedules":[{"id":1,"name":"S1"}],"total_rows":53,"is_has_more":true,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.ListSchedules(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if !resp.IsHasMore {
		t.Error("IsHasMore = false, want true (truncated list)")
	}
	if resp.TotalRows != 53 {
		t.Errorf("TotalRows = %d, want 53", resp.TotalRows)
	}
	if resp.RowsLimit != 20 {
		t.Errorf("RowsLimit = %d, want 20", resp.RowsLimit)
	}
}

// TestListAllSchedules_TwoPages verifies the --all walk starts at page 1
// (not 0), accumulates both pages, and produces no duplicate IDs. Without
// the fix (walk starting at page 0) the first page is fetched twice because
// page=0 and page=1 are byte-identical on the server, yielding duplicates
// and a length that exceeds total_rows.
func TestListAllSchedules_TwoPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`{"list":[{"id":3}],"total_rows":3,"is_has_more":false,"rows_limit":20}`))
		default: // page 1 (and the buggy page 0 which omits the param)
			w.Write([]byte(`{"list":[{"id":1},{"id":2}],"total_rows":3,"is_has_more":true,"rows_limit":20}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, err := c.ListAllSchedules(context.Background())
	if err != nil {
		t.Fatalf("ListAllSchedules: %v", err)
	}

	// Length must equal the server's total_rows.
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3 (server total_rows)", len(all))
	}
	// No duplicate IDs.
	seen := map[int]bool{}
	for _, s := range all {
		if seen[s.ID] {
			t.Errorf("duplicate schedule ID %d in accumulated result", s.ID)
		}
		seen[s.ID] = true
	}
	// Handler received each page exactly once, starting at page=1.
	if len(pages) != 2 {
		t.Fatalf("handler received %d requests, want 2 (pages=%v)", len(pages), pages)
	}
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want \"1\"", pages[0])
	}
	if pages[1] != "2" {
		t.Errorf("second request page = %q, want \"2\"", pages[1])
	}
}

// TestListAllProjects_TwoPages mirrors TestListAllSchedules_TwoPages for
// the projects endpoint.
func TestListAllProjects_TwoPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`{"list":[{"id":3,"name":"P3"}],"total_rows":3,"is_has_more":false,"rows_limit":20}`))
		default:
			w.Write([]byte(`{"list":[{"id":1,"name":"P1"},{"id":2,"name":"P2"}],"total_rows":3,"is_has_more":true,"rows_limit":20}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, err := c.ListAllProjects(context.Background())
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}
	seen := map[int]bool{}
	for _, p := range all {
		if seen[p.ID] {
			t.Errorf("duplicate project ID %d", p.ID)
		}
		seen[p.ID] = true
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("pages = %v, want [1 2]", pages)
	}
}

// TestListAllSchedules_SanityCap verifies the walk returns an error
// instead of looping forever when is_has_more never goes false.
func TestListAllSchedules_SanityCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"list":[{"id":1}],"total_rows":1000000,"is_has_more":true,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListAllSchedules(context.Background())
	if err == nil {
		t.Fatal("expected error when is_has_more never goes false, got nil")
	}
	if !contains(err.Error(), "exceeded") {
		t.Errorf("expected cap error mentioning 'exceeded', got: %v", err)
	}
}

// TestListAllSchedules_TruncatedWalkErrors verifies that when the server
// clears is_has_more early while total_rows still exceeds the rows served,
// the walk returns the short list + the server's totalRows with err == nil,
// and NewAllListEnvelope catches the mismatch and errors — instead of
// letting a truncated walk pass as complete with total_rows substituted by
// len(list).
//
// Without the envelope mismatch check: NewAllListEnvelope returns
// {list: short, total_rows: len(short), is_has_more: false} with err == nil,
// and a truncated walk is indistinguishable from a complete one.
func TestListAllSchedules_TruncatedWalkErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Single page: 2 rows served, is_has_more already false, but the
		// server's total_rows=5 contradicts the 2 rows it served.
		w.Write([]byte(`{"list":[{"id":1},{"id":2}],"total_rows":5,"is_has_more":false,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, total, err := c.ListAllSchedulesWithTotal(context.Background())
	if err != nil {
		t.Fatalf("walk itself should succeed (API OK): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2 (rows served)", len(all))
	}
	if total != 5 {
		t.Fatalf("totalRows = %d, want 5 (server's last-seen total_rows)", total)
	}
	// The envelope is the fail-loud gate: unique ids {1,2}=2 != totalRows=5 -> error.
	if _, err := NewAllListEnvelope(all, total, func(s Schedule) int { return s.ID }); err == nil {
		t.Fatal("NewAllListEnvelope must error when unique id count != totalRows (truncated walk), got nil")
	}
}

// TestNewAllListEnvelope_DuplicateIDErrors verifies that counting UNIQUE
// ids (rather than raw length) catches a duplicate row served across two
// pages that masks a missing row. A raw-length check would pass here
// (len == total_rows), but the unique-id count does not.
//
// Without the unique-id check (revert to raw len): len(list)=3 ==
// totalRows=3 passes and err is nil — the duplicate masks the missing row.
func TestNewAllListEnvelope_DuplicateIDErrors(t *testing.T) {
	// Three entries, but id 2 is duplicated — raw len=3, unique=2.
	// total_rows=3 matches the raw length, so a raw-length check passes.
	list := []Post{{ID: 1}, {ID: 2}, {ID: 2}}
	_, err := NewAllListEnvelope(list, 3, func(p Post) int { return p.ID })
	if err == nil {
		t.Fatal("expected error for duplicate id with total_rows matching raw length, got nil — unique-id check must catch the duplicate that a raw-length check misses")
	}
}
