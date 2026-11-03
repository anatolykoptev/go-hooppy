package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPageKeyed_AcceptsObjectAndEmptyListOnly(t *testing.T) {
	var m PageKeyed[string]
	if err := m.UnmarshalJSON([]byte(`{"1":"a"}`)); err != nil || m["1"] != "a" {
		t.Fatalf("object form: %v %v", m, err)
	}
	if err := m.UnmarshalJSON([]byte(`[]`)); err != nil || m == nil || len(m) != 0 {
		t.Fatalf("empty list form: %v %v (want non-nil empty map)", m, err)
	}
	if err := m.UnmarshalJSON([]byte(`null`)); err != nil || m == nil {
		t.Fatalf("null form: %v %v", m, err)
	}
	if err := m.UnmarshalJSON([]byte(`["x"]`)); err == nil {
		t.Fatal("a populated list is a real shape change and must be an error")
	}
}

func TestGetPostPublishedPages_RefusesImpossibleIDsLocally(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetPostPublishedPages(context.Background(), 0, 1); err == nil || !strings.Contains(err.Error(), "postID") {
		t.Errorf("postID 0: %v", err)
	}
	if _, err := c.GetPostPublishedPages(context.Background(), 5, 0); err == nil || !strings.Contains(err.Error(), "sourceID") {
		t.Errorf("sourceID 0: %v", err)
	}
}
