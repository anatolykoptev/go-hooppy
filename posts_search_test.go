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
		if q.Get("source_resource_id") != "123" {
			t.Errorf("source_resource_id = %q, want 123", q.Get("source_resource_id"))
		}
		if q.Get("text") != "test query" {
			t.Errorf("text = %q, want test query", q.Get("text"))
		}
		w.Write([]byte(`{
			"list":[{
				"id":1001,
				"is_attachments_in_process":0,
				"source_id":1,
				"publication_date":"28.07.2026, 10:07",
				"text":"Test post text",
				"photos":[{"id":1,"owner_id":-100,"url":"https://example.com/p.jpg","info":""}],
				"videos":[],"audios":[],"documents":[],
				"owner":{"id":"100","type":"page","name":"test_page","alias":"test_page","photo":"","link":"https://vk.ru/public100"},
				"link":"https://vk.ru/wall-100_1",
				"likes":"1","reposts":"3","views":"864","comments":"0","involvement":"0.463",
				"video_duration":0,"is_used":0
			}],
			"total_rows":1,"is_has_more":false,"rows_limit":20
		}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{
		SourceResourceID: 123,
		Text:             "test query",
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
	if p.ID != 1001 {
		t.Errorf("ID = %d, want 1001", p.ID)
	}
	if p.PublicationDate != "28.07.2026, 10:07" {
		t.Errorf("PublicationDate = %q", p.PublicationDate)
	}
	if p.Likes != "1" {
		t.Errorf("Likes = %q, want 1", p.Likes)
	}
	if p.Owner.Name != "test_page" {
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
			{"id":123,"user_id":456,"name":"Test Source","source_type":1,"search_type":1,"source_id":1,"data":"https://vk.com/test","hashtag":"","link":""}
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
	if s.ID != 123 || s.Name != "Test Source" || s.SourceID != 1 {
		t.Errorf("Source = %+v", s)
	}
}

func TestGetParsingForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/posts-search/parsing/form" {
			t.Errorf("GET /posts-search/parsing/form, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{
			"source_resources":[{"id":123,"name":"Test","source_type":1,"search_type":1,"source_id":1,"data":"https://vk.com/test"}],
			"social_accounts":[{"id":999,"source_id":1,"name":"Test Account"}],
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
	if len(resp.SocialAccounts) != 1 || resp.SocialAccounts[0].ID != 999 {
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
		SourceResourceID:          123,
		SocialAccountForParsingID: 999,
		DateFrom:                  0,
		DateTo:                    0,
	})
	if err != nil {
		t.Fatalf("StartParsing: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false, want true")
	}
	if capturedBody["source_resource_id"].(float64) != 123 {
		t.Errorf("source_resource_id = %v, want 123", capturedBody["source_resource_id"])
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

func TestCopySearchPost(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/posts/copy" {
			t.Errorf("PUT /posts/copy, got %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":5001}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
	})
	if err != nil {
		t.Fatalf("CopySearchPost: %v", err)
	}
	if resp.ID != 5001 {
		t.Errorf("ID = %d, want 5001", resp.ID)
	}
	if capturedBody["search_post_id"].(float64) != 1001 {
		t.Errorf("search_post_id = %v, want 1001", capturedBody["search_post_id"])
	}
	if capturedBody["publication_when_type"].(float64) != 1 {
		t.Errorf("publication_when_type = %v, want 1", capturedBody["publication_when_type"])
	}
}

func TestCopySearchPost_NilSlicesInitialized(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":5002}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	// Pass no slices at all — verify they're initialized to [] not null
	_, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1002,
		PublicationWhenType: 1,
		PublicationHowType:  1,
	})
	if err != nil {
		t.Fatalf("CopySearchPost: %v", err)
	}
	// Server expects arrays, not null
	if capturedBody["texts"] == nil {
		t.Errorf("texts = null, want []")
	}
	if capturedBody["attachments"] == nil {
		t.Errorf("attachments = null, want []")
	}
	if capturedBody["selected_pages_ids"] == nil {
		t.Errorf("selected_pages_ids = null, want []")
	}
}

func TestCopySearchPost_Scheduled(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":5003}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1003,
		PublicationWhenType: 2,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		PublicationDate: &PublicationDate{
			Date:    "01.02.2026",
			Hours:   "14",
			Minutes: "30",
		},
	})
	if err != nil {
		t.Fatalf("CopySearchPost: %v", err)
	}
	if resp.ID != 5003 {
		t.Errorf("ID = %d, want 5003", resp.ID)
	}
	if capturedBody["publication_when_type"].(float64) != 2 {
		t.Errorf("publication_when_type = %v, want 2", capturedBody["publication_when_type"])
	}
	pubDate, ok := capturedBody["publication_date"].(map[string]interface{})
	if !ok {
		t.Fatalf("publication_date not a map: %T", capturedBody["publication_date"])
	}
	if pubDate["date"] != "01.02.2026" || pubDate["hours"] != "14" || pubDate["minutes"] != "30" {
		t.Errorf("publication_date = %+v", pubDate)
	}
}
