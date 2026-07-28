package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestListSchedules_PageParam(t *testing.T) {
	tests := []struct {
		name      string
		page      int
		wantParam string
	}{
		{"page 0 (default)", 0, ""},
		{"page 1", 1, "page=1"},
		{"page 2", 2, "page=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedURL = r.URL.String()
				w.Write([]byte(`{"schedules":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			_, err := c.ListSchedules(context.Background(), tt.page)
			if err != nil {
				t.Fatalf("ListSchedules: %v", err)
			}
			if tt.wantParam == "" {
				if contains(capturedURL, "page=") {
					t.Errorf("page 0 should not add page param, got: %s", capturedURL)
				}
			} else {
				if !contains(capturedURL, tt.wantParam) {
					t.Errorf("URL should contain %s, got: %s", tt.wantParam, capturedURL)
				}
			}
		})
	}
}

func TestListProjects_PageParam(t *testing.T) {
	tests := []struct {
		name      string
		page      int
		wantParam string
	}{
		{"page 0 (default)", 0, ""},
		{"page 1", 1, "page=1"},
		{"page 2", 2, "page=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedURL = r.URL.String()
				w.Write([]byte(`{"projects":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			_, err := c.ListProjects(context.Background(), tt.page)
			if err != nil {
				t.Fatalf("ListProjects: %v", err)
			}
			if tt.wantParam == "" {
				if contains(capturedURL, "page=") {
					t.Errorf("page 0 should not add page param, got: %s", capturedURL)
				}
			} else {
				if !contains(capturedURL, tt.wantParam) {
					t.Errorf("URL should contain %s, got: %s", tt.wantParam, capturedURL)
				}
			}
		})
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
