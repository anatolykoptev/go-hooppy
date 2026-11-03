package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestPostWhereTool_ReturnsPublishedURL drives the real hooppy_post_where
// handler end to end (in-memory MCP transport → handler → ops.PostWhere →
// GET /items/social/pages stub) and asserts the tool result carries each
// page's public URL.
func TestPostWhereTool_ReturnsPublishedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items/social/pages" || r.URL.Query().Get("item_id") != "4471647" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		w.Write([]byte(`{"social_pages_by_accounts":[{"account":{"id":1,"source_id":1,"name":"A"},"pages":[{"id":822454,"name":"Рецепты","link":"https://vk.ru/recipes_test"}]}],"posts_published_links":{"822454":"https://vk.ru/wall-75076231_34364"},"posts_published_planned":{},"posts_errors":{}}`))
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)

	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "hooppy_post_where", Arguments: map[string]any{"id": 4471647}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", toolResultText(res))
	}
	if txt := toolResultText(res); !strings.Contains(txt, `"published_url": "https://vk.ru/wall-75076231_34364"`) {
		t.Errorf("tool result lacks the page's public URL:\n%s", txt)
	}
}

func TestPostWhereTool_RefusesMissingID(t *testing.T) {
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", "http://127.0.0.1:1")
	cs := newMCPClientSession(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "hooppy_post_where", Arguments: map[string]any{"id": 0}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(toolResultText(res), "id is required") {
		t.Errorf("want an id-required tool error, got IsError=%v %q", res.IsError, toolResultText(res))
	}
}
