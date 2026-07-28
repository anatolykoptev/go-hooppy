package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListPosts_NilIsPublished(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListPosts(context.Background(), ListPostsFilter{IsPublished: nil})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	// is_published should NOT be in the query string when nil.
	if contains(capturedURL, "is_published") {
		t.Errorf("URL should not contain is_published, got: %s", capturedURL)
	}
}

func TestListPosts_TrueIsPublished(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	pub := true
	_, err := c.ListPosts(context.Background(), ListPostsFilter{IsPublished: &pub})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if !contains(capturedURL, "is_published=1") {
		t.Errorf("URL should contain is_published=1, got: %s", capturedURL)
	}
}

func TestListPosts_FalseIsPublished(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	pub := false
	_, err := c.ListPosts(context.Background(), ListPostsFilter{IsPublished: &pub})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if !contains(capturedURL, "is_published=0") {
		t.Errorf("URL should contain is_published=0, got: %s", capturedURL)
	}
}

func TestListPosts_ZeroValuesSkipped(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListPosts(context.Background(), ListPostsFilter{
		SourceID:  0,
		AccountID: 0,
		PageID:    0,
		Page:      0,
	})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if contains(capturedURL, "source_id") || contains(capturedURL, "account_id") || contains(capturedURL, "page_id") {
		t.Errorf("zero values should be skipped, got: %s", capturedURL)
	}
}

func TestListPosts_PublicationDate(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListPosts(context.Background(), ListPostsFilter{PublicationDate: "01.01.2026"})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if !contains(capturedURL, "publication_date=01.01.2026") {
		t.Errorf("URL should contain publication_date, got: %s", capturedURL)
	}
}

func TestListPosts_AllFilters(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	pub := true
	_, err := c.ListPosts(context.Background(), ListPostsFilter{
		IsPublished:     &pub,
		PublicationDate: "15.06.2026",
		SourceID:        6,
		AccountID:       100,
		PageID:          200,
		ScheduleID:      300,
		ProjectID:       400,
		Page:            2,
	})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	for _, param := range []string{"is_published=1", "publication_date=15.06.2026", "source_id=6", "account_id=100", "page_id=200", "schedule_id=300", "project_id=400", "page=2"} {
		if !contains(capturedURL, param) {
			t.Errorf("URL should contain %s, got: %s", param, capturedURL)
		}
	}
}

func TestCreatePost_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":12345,"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.CreatePost(context.Background(), PostPublishNowPayload{
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{1, 2},
		Texts:               []PostText{{Text: "hello", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if resp.ID != 12345 {
		t.Errorf("ID = %d, want 12345", resp.ID)
	}
}

func TestDeletePost_Success(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.DeletePost(context.Background(), 42)
	if err != nil {
		t.Fatalf("DeletePost: %v", err)
	}
	if !contains(capturedPath, "/42") {
		t.Errorf("path should contain /42, got: %s", capturedPath)
	}
}

func TestBatchDeletePosts_EmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.BatchDeletePosts(context.Background(), []int{})
	if err != nil {
		t.Fatalf("BatchDeletePosts: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestBatchDeletePosts_SingleID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.BatchDeletePosts(context.Background(), []int{42})
	if err != nil {
		t.Fatalf("BatchDeletePosts: %v", err)
	}
}

func TestBatchDeletePosts_MultipleIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.BatchDeletePosts(context.Background(), []int{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatalf("BatchDeletePosts: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
