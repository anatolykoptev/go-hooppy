package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListSearchPosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/posts-search" {
			t.Errorf("GET /posts-search, got %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("source_resource_id") != "578" {
			t.Errorf("source_resource_id = %q, want 578", q.Get("source_resource_id"))
		}
		if q.Get("text") != "Петербург" {
			t.Errorf("text = %q, want Петербург", q.Get("text"))
		}
		w.Write([]byte(`{
			"list":[{
				"id":6445575,
				"is_attachments_in_process":0,
				"source_id":1,
				"publication_date":"28.07.2026, 10:07",
				"text":"Flow Fest возвращается",
				"photos":[{"id":1,"owner_id":-26270763,"url":"https://example.com/p.jpg","info":""}],
				"videos":[],"audios":[],"documents":[],
				"owner":{"id":"26270763","type":"page","name":"blog_fiesta","alias":"blog_fiesta","photo":"","link":"https://vk.ru/public26270763"},
				"link":"https://vk.ru/wall-26270763_1435615",
				"likes":"1","reposts":"3","views":"864","comments":"0","involvement":"0.463",
				"video_duration":0,"is_used":0
			}],
			"total_rows":1,"is_has_more":false,"rows_limit":20
		}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{
		SourceResourceID: 578,
		Text:             "Петербург",
	})
	if err != nil {
		t.Fatalf("ListSearchPosts: %v", err)
	}
	if resp.TotalRows != 1 {
		t.Errorf("TotalRows = %d, want 1", resp.TotalRows)
	}
	if len(resp.List) != 1 {
		t.Fatalf("List len = %d, want 1", len(resp.List))
	}
	p := resp.List[0]
	if p.ID != 6445575 {
		t.Errorf("ID = %d, want 6445575", p.ID)
	}
	if p.PublicationDate != "28.07.2026, 10:07" {
		t.Errorf("PublicationDate = %q", p.PublicationDate)
	}
	if p.Likes != "1" {
		t.Errorf("Likes = %q, want 1", p.Likes)
	}
	if p.Owner.Name != "blog_fiesta" {
		t.Errorf("Owner.Name = %q", p.Owner.Name)
	}
	if len(p.Photos) != 1 || p.Photos[0].URL != "https://example.com/p.jpg" {
		t.Errorf("Photos = %+v", p.Photos)
	}
}

func TestListSourceResources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/posts-search/source-resources" {
			t.Errorf("GET /posts-search/source-resources, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"list":[
			{"id":578,"user_id":5751,"name":"Питер паблики вк","source_type":1,"search_type":1,"source_id":1,"data":"https://vk.com/piter","hashtag":"","link":""}
		]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSourceResources(context.Background())
	if err != nil {
		t.Fatalf("ListSourceResources: %v", err)
	}
	if len(resp.List) != 1 {
		t.Fatalf("List len = %d, want 1", len(resp.List))
	}
	s := resp.List[0]
	if s.ID != 578 || s.Name != "Питер паблики вк" || s.SourceID != 1 {
		t.Errorf("Source = %+v", s)
	}
}

func TestGetParsingForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/posts-search/parsing/form" {
			t.Errorf("GET /posts-search/parsing/form, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{
			"source_resources":[{"id":578,"name":"Питер","source_type":1,"search_type":1,"source_id":1,"data":"https://vk.com/piter"}],
			"social_accounts":[{"id":94294,"source_id":1,"name":"Екатерина Вторая"}],
			"is_parsing_in_progress":false
		}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.GetParsingForm(context.Background())
	if err != nil {
		t.Fatalf("GetParsingForm: %v", err)
	}
	if resp.IsParsingInProgress {
		t.Errorf("IsParsingInProgress = true, want false")
	}
	if len(resp.SocialAccounts) != 1 || resp.SocialAccounts[0].ID != 94294 {
		t.Errorf("SocialAccounts = %+v", resp.SocialAccounts)
	}
}

func TestStartParsing(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts-search/parsing/start" {
			t.Errorf("POST /posts-search/parsing/start, got %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.StartParsing(context.Background(), ParsingStartPayload{
		SourceType:                1,
		SearchType:                1,
		SourceID:                  1,
		SourceResourceID:          578,
		SocialAccountForParsingID: 94294,
		DateFrom:                  0,
		DateTo:                    0,
	})
	if err != nil {
		t.Fatalf("StartParsing: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true")
	}
	if capturedBody["source_resource_id"].(float64) != 578 {
		t.Errorf("source_resource_id = %v, want 578", capturedBody["source_resource_id"])
	}
}

func TestStopParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/posts-search/parsing" {
			t.Errorf("DELETE /posts-search/parsing, got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if err := c.StopParsing(context.Background()); err != nil {
		t.Fatalf("StopParsing: %v", err)
	}
}
