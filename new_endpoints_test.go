package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/users/me" {
			t.Errorf("GET /users/me, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"user":{"id":12345,"email":"user@example.com","plan_type":1}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.GetUser(context.Background())
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if resp.User.ID != 12345 {
		t.Errorf("ID = %d, want 12345", resp.User.ID)
	}
	if resp.User.Email != "user@example.com" {
		t.Errorf("Email = %q, want user@example.com", resp.User.Email)
	}
}

func TestWatermarksCRUD(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			if len(body) > 0 {
				_ = json.Unmarshal(body, &capturedBody)
			}
		}
		// Respond based on method
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"list":[{"id":1,"name":"WM1","file":"f.png","space":0,"position":0,"opacity":0,"size":0}],"total_rows":1,"is_has_more":false,"rows_limit":12}`))
		case http.MethodPost:
			w.Write([]byte(`{"id":2,"watermarks":[{"id":2,"name":"New WM","file":"","space":0,"position":0,"opacity":0,"size":0}]}`))
		case http.MethodPut:
			w.Write([]byte(`{"success":true,"watermarks":[{"id":2,"name":"Updated","file":"","space":1,"position":2,"opacity":50,"size":100}]}`))
		case http.MethodDelete:
			w.Write([]byte(`{"success":true,"watermarks":[]}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	// List
	resp, err := c.ListWatermarks(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListWatermarks: %v", err)
	}
	if capturedMethod != http.MethodGet || capturedPath != "/watermarks" {
		t.Errorf("List: %s %s, want GET /watermarks", capturedMethod, capturedPath)
	}
	if len(resp.List) != 1 || resp.List[0].ID != 1 {
		t.Errorf("List result = %+v", resp.List)
	}

	// Create
	payload := WatermarkPayload{Name: "New WM", File: "", Space: 0, Position: 0, Opacity: 0, Size: 0}
	createResp, err := c.CreateWatermark(context.Background(), payload)
	if err != nil {
		t.Fatalf("CreateWatermark: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("Create: %s, want POST", capturedMethod)
	}
	if createResp.ID != 2 {
		t.Errorf("Create ID = %d, want 2", createResp.ID)
	}

	// Update
	updResp, err := c.UpdateWatermark(context.Background(), 2, WatermarkPayload{Name: "Updated", Space: 1, Position: 2, Opacity: 50, Size: 100})
	if err != nil {
		t.Fatalf("UpdateWatermark: %v", err)
	}
	if capturedMethod != http.MethodPut || capturedPath != "/watermarks/2" {
		t.Errorf("Update: %s %s, want PUT /watermarks/2", capturedMethod, capturedPath)
	}
	if !updResp.Success {
		t.Error("Update Success = false")
	}

	// Delete
	delResp, err := c.DeleteWatermark(context.Background(), 2)
	if err != nil {
		t.Fatalf("DeleteWatermark: %v", err)
	}
	if capturedMethod != http.MethodDelete || capturedPath != "/watermarks/2" {
		t.Errorf("Delete: %s %s, want DELETE /watermarks/2", capturedMethod, capturedPath)
	}
	if !delResp.Success {
		t.Error("Delete Success = false")
	}
}

func TestProxiesCRUD(t *testing.T) {
	var capturedMethod, capturedPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"list":[{"id":1,"name":"P1","ip":"1.1.1.1","port":"80","login":"u","password":"p"}],"total_rows":1,"is_has_more":false,"rows_limit":12}`))
		case http.MethodPost:
			w.Write([]byte(`{"id":2,"proxies":[{"id":2,"name":"New","ip":"2.2.2.2","port":"90","login":"u2","password":"p2"}]}`))
		case http.MethodPut:
			w.Write([]byte(`{"success":true,"proxies":[{"id":2,"name":"Updated","ip":"3.3.3.3","port":"90","login":"u3","password":"p3"}]}`))
		case http.MethodDelete:
			w.Write([]byte(`{"success":true,"proxies":[]}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	// List
	resp, err := c.ListProxies(context.Background())
	if err != nil {
		t.Fatalf("ListProxies: %v", err)
	}
	if capturedMethod != http.MethodGet || capturedPath != "/proxies" {
		t.Errorf("List: %s %s, want GET /proxies", capturedMethod, capturedPath)
	}
	if len(resp.List) != 1 {
		t.Errorf("List count = %d, want 1", len(resp.List))
	}

	// Create
	payload := ProxyPayload{Name: "New", IP: "2.2.2.2", Port: "90", Login: "u2", Password: "p2"}
	createResp, err := c.CreateProxy(context.Background(), payload)
	if err != nil {
		t.Fatalf("CreateProxy: %v", err)
	}
	if createResp.ID != 2 {
		t.Errorf("Create ID = %d, want 2", createResp.ID)
	}

	// Update
	updResp, err := c.UpdateProxy(context.Background(), 2, ProxyPayload{Name: "Updated", IP: "3.3.3.3", Port: "90", Login: "u3", Password: "p3"})
	if err != nil {
		t.Fatalf("UpdateProxy: %v", err)
	}
	if capturedMethod != http.MethodPut || capturedPath != "/proxies/2" {
		t.Errorf("Update: %s %s, want PUT /proxies/2", capturedMethod, capturedPath)
	}
	if !updResp.Success {
		t.Error("Update Success = false")
	}

	// Delete
	delResp, err := c.DeleteProxy(context.Background(), 2)
	if err != nil {
		t.Fatalf("DeleteProxy: %v", err)
	}
	if capturedMethod != http.MethodDelete || capturedPath != "/proxies/2" {
		t.Errorf("Delete: %s %s, want DELETE /proxies/2", capturedMethod, capturedPath)
	}
	if !delResp.Success {
		t.Error("Delete Success = false")
	}
}

func TestListNotifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/notifications" {
			t.Errorf("GET /notifications, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"list":[{"id":1,"data":"test","is_error":1}],"total_rows":1,"is_has_more":false,"rows_limit":12}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListNotifications(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(resp.List) != 1 || resp.List[0].ID != 1 {
		t.Errorf("List = %+v", resp.List)
	}
}

func TestCreateProject(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":100,"projects":[{"id":100,"name":"Test"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	payload := NewProjectPayload("Test Project", 123456)
	resp, err := c.CreateProject(context.Background(), payload)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if capturedMethod != http.MethodPost || capturedPath != "/posts/projects" {
		t.Errorf("Create: %s %s, want POST /posts/projects", capturedMethod, capturedPath)
	}
	if capturedBody["name"] != "Test Project" {
		t.Errorf("body name = %v", capturedBody["name"])
	}
	if resp.ID != 100 {
		t.Errorf("ID = %d, want 100", resp.ID)
	}
}

func TestNewProjectPayload_Defaults(t *testing.T) {
	p := NewProjectPayload("My Project", 12345)
	if p.Name != "My Project" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.PublicationWhereType != 1 {
		t.Errorf("PublicationWhereType = %d, want 1", p.PublicationWhereType)
	}
	if len(p.SelectedPagesIDs) != 1 || p.SelectedPagesIDs[0] != 12345 {
		t.Errorf("SelectedPagesIDs = %v, want [12345]", p.SelectedPagesIDs)
	}
}

func TestUpdatePost(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	payload := PostPublishNowPayload{
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "updated", SourceID: 0}},
		Attachments:         []Attachment{},
	}
	resp, err := c.UpdatePost(context.Background(), 12345, payload)
	if err != nil {
		t.Fatalf("UpdatePost: %v", err)
	}
	if capturedMethod != http.MethodPut || capturedPath != "/posts/12345" {
		t.Errorf("Update: %s %s, want PUT /posts/12345", capturedMethod, capturedPath)
	}
	if !resp.Success {
		t.Error("Success = false")
	}
}

func TestDisconnectPage(t *testing.T) {
	var capturedMethod, capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.DisconnectPage(context.Background(), 99999)
	if err != nil {
		t.Fatalf("DisconnectPage: %v", err)
	}
	if capturedMethod != http.MethodDelete || capturedPath != "/accounts/pages/99999" {
		t.Errorf("Disconnect: %s %s, want DELETE /accounts/pages/99999", capturedMethod, capturedPath)
	}
	if !resp.Success {
		t.Error("Success = false")
	}
}

func TestCrossPostModes(t *testing.T) {
	modes := []struct {
		name string
		fn   func(*Client, context.Context, interface{}) (*PostIDResponse, error)
	}{
		{"search", (*Client).SearchPosts},
		{"copy", (*Client).CopyPost},
		{"sources", (*Client).SourcesPost},
		{"import", (*Client).ImportPost},
		{"crosspost", (*Client).CrossPost},
		{"rewrite", (*Client).RewritePost},
		{"translate", (*Client).TranslatePost},
		{"queue", (*Client).QueuePost},
		{"drafts", (*Client).DraftPost},
		{"templates", (*Client).TemplatePost},
		{"rss", (*Client).RSSPost},
		{"feeds", (*Client).FeedPost},
		{"tags", (*Client).TagPost},
		{"watermarks", (*Client).WatermarkPost},
		{"batch", (*Client).BatchPost},
	}

	for _, m := range modes {
		var capturedMethod, capturedPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedMethod = r.Method
			capturedPath = r.URL.Path
			w.Write([]byte(`{"id":99999}`))
		}))

		cli, _ := NewClient(Config{Token: "test", BaseURL: srv.URL})

		payload := PostPublishNowPayload{
			PublicationWhenType: 1,
			PublicationHowType:  1,
			SelectedPagesIDs:    []int{1},
			Texts:               []PostText{{Text: "x", SourceID: 0}},
			Attachments:         []Attachment{},
		}

		resp, err := m.fn(cli, context.Background(), payload)
		srv.Close()

		if err != nil {
			t.Errorf("%s: %v", m.name, err)
			continue
		}
		if capturedMethod != http.MethodPut {
			t.Errorf("%s: method = %s, want PUT", m.name, capturedMethod)
		}
		expectedPath := "/posts/" + m.name
		if capturedPath != expectedPath {
			t.Errorf("%s: path = %s, want %s", m.name, capturedPath, expectedPath)
		}
		if resp.ID != 99999 {
			t.Errorf("%s: ID = %d, want 99999", m.name, resp.ID)
		}
	}
}

func TestCrossPostWithMode(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.CrossPostWithMode(context.Background(), CrossPostModeSearch, PostPublishNowPayload{
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{1},
		Texts:               []PostText{{Text: "test", SourceID: 0}},
		Attachments:         []Attachment{},
	})
	if err != nil {
		t.Fatalf("CrossPostWithMode: %v", err)
	}
	if capturedPath != "/posts/search" {
		t.Errorf("path = %s, want /posts/search", capturedPath)
	}
	if resp.ID != 42 {
		t.Errorf("ID = %d, want 42", resp.ID)
	}
}
