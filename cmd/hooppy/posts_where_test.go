package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
)

// TestRunPostsWhere_PrintsPublishedURL drives the CLI core `hooppy posts
// where 4471647 --source 1` end to end against a stub and asserts on the
// printed JSON: each page is listed with its public URL.
func TestRunPostsWhere_PrintsPublishedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items/social/pages" || r.URL.Query().Get("item_id") != "4471647" || r.URL.Query().Get("source_id") != "1" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		w.Write([]byte(`{"social_pages_by_accounts":[{"account":{"id":78453,"source_id":1,"name":"Иван"},"pages":[{"id":822454,"name":"Рецепты","link":"https://vk.ru/recipes_test"}]}],"posts_published_links":{"822454":"https://vk.ru/wall-75076231_34364"},"posts_published_planned":{},"posts_errors":{}}`))
	}))
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runPostsWhere(context.Background(), c, &out, &errOut, 4471647, ops.PostWhereInput{SourceID: pint(1)}); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	var res struct {
		Pages []struct {
			PageID       int    `json:"page_id"`
			PublishedURL string `json:"published_url"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(res.Pages) != 1 || res.Pages[0].PageID != 822454 || res.Pages[0].PublishedURL != "https://vk.ru/wall-75076231_34364" {
		t.Errorf("printed pages = %+v\n%s", res.Pages, out.String())
	}
}

func TestRunPostsWhere_ValidationIsAUsageStyleRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	}))
	defer srv.Close()
	c, _ := hooppy.NewClient(hooppy.Config{Token: "t", BaseURL: srv.URL})
	var out, errOut bytes.Buffer
	if code := runPostsWhere(context.Background(), c, &out, &errOut, 5, ops.PostWhereInput{SourceID: pint(0)}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "hooppy: posts where: --source must be a positive") {
		t.Errorf("stderr = %s", errOut.String())
	}
}
