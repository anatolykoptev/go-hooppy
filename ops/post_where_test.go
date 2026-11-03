package ops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postWhereDocsFixture is modelled on the official reference's example
// response for «Где опубликован пост» (GET /items/social/pages): one VK
// account, two pages, a public URL for the first, an error for the second.
const postWhereDocsFixture = `{
  "social_pages_by_accounts": [
    {
      "account": {"id": 78453, "source_id": 1, "name": "Иван Петров"},
      "pages": [
        {"id": 822454, "name": "Кулинарные рецепты", "link": "https://vk.ru/recipes_test", "photo": "https://example.com/p.jpg"},
        {"id": 822455, "name": "Вторая группа", "link": "https://vk.ru/second", "photo": ""}
      ]
    }
  ],
  "posts_published_links": {"822454": "https://vk.ru/wall-75076231_34364"},
  "posts_published_planned": {},
  "posts_errors": {"822455": ["Нет прав для публикации в данную группу."]}
}`

func whereServer(t *testing.T, status int, body string, gotQuery *map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/items/social/pages" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if gotQuery != nil {
			q := map[string]string{}
			for k := range r.URL.Query() {
				q[k] = r.URL.Query().Get(k)
			}
			*gotQuery = q
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

// TestPostWhere_ReportsPublishedURLPerPage is F1: the owner runs
// `hooppy posts where <id>` and must see each page WITH its public URL.
//
// RED-on-revert: drop the PublishedURL join in PostWhere (or the
// posts_published_links field from the decoded response) → the page that has
// a published copy comes back with an empty published_url and this fails.
func TestPostWhere_ReportsPublishedURLPerPage(t *testing.T) {
	var q map[string]string
	srv := whereServer(t, 200, postWhereDocsFixture, &q)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	res, err := PostWhere(context.Background(), c, 4471647, PostWhereInput{})
	if err != nil {
		t.Fatalf("PostWhere: %v", err)
	}
	if q["items_type"] != "posts" || q["item_id"] != "4471647" || q["source_id"] != "1" {
		t.Errorf("query = %v, want items_type=posts item_id=4471647 source_id=1 (VK default)", q)
	}
	if res.PostID != 4471647 || res.SourceID != 1 || res.SourceName != "vkontakte" {
		t.Errorf("header = %+v", res)
	}
	if len(res.Pages) != 2 {
		t.Fatalf("pages = %d, want 2: %+v", len(res.Pages), res.Pages)
	}
	p := res.Pages[0]
	if p.PageID != 822454 || p.Name != "Кулинарные рецепты" || p.PageURL != "https://vk.ru/recipes_test" || p.AccountName != "Иван Петров" {
		t.Errorf("page 0 identity = %+v", p)
	}
	if p.PublishedURL != "https://vk.ru/wall-75076231_34364" {
		t.Errorf("page 0 published_url = %q, want the public wall URL from posts_published_links", p.PublishedURL)
	}
	q1 := res.Pages[1]
	if q1.PublishedURL != "" || len(q1.Errors) != 1 || !strings.Contains(q1.Errors[0], "Нет прав") {
		t.Errorf("page 1 (unpublished, with error) = %+v", q1)
	}
	// The JSON the CLI/MCP print must carry the URL under published_url.
	raw, _ := json.Marshal(res)
	if !strings.Contains(string(raw), `"published_url":"https://vk.ru/wall-75076231_34364"`) {
		t.Errorf("marshalled result lacks published_url: %s", raw)
	}
}

func TestPostWhere_SourceFlagIsSent(t *testing.T) {
	var q map[string]string
	srv := whereServer(t, 200, postWhereDocsFixture, &q)
	defer srv.Close()
	res, err := PostWhere(context.Background(), newTestClient(t, srv.URL), 5, PostWhereInput{SourceID: pint(9)})
	if err != nil {
		t.Fatal(err)
	}
	if q["source_id"] != "9" || res.SourceID != 9 || res.SourceName != "telegram" {
		t.Errorf("source_id query=%q result=%d/%s, want 9/telegram", q["source_id"], res.SourceID, res.SourceName)
	}
}

// Empty maps arrive as `[]` from PHP; one of them must not abort the decode
// and lose the pages list beside it.
func TestPostWhere_EmptyListFormMapsDecode(t *testing.T) {
	body := `{"social_pages_by_accounts":[{"account":{"id":1,"source_id":1,"name":"A"},"pages":[{"id":7,"name":"G","link":"https://vk.ru/g"}]}],"posts_published_links":[],"posts_published_planned":[],"posts_errors":[]}`
	srv := whereServer(t, 200, body, nil)
	defer srv.Close()
	res, err := PostWhere(context.Background(), newTestClient(t, srv.URL), 5, PostWhereInput{})
	if err != nil {
		t.Fatalf("PostWhere: %v", err)
	}
	if len(res.Pages) != 1 || res.Pages[0].PageID != 7 || res.Pages[0].PublishedURL != "" {
		t.Errorf("pages = %+v", res.Pages)
	}
}

// A key in the URL/error maps that the pages list does not carry must still
// surface: dropping it would hide a published copy.
func TestPostWhere_KeysOutsidePagesListAreKept(t *testing.T) {
	body := `{"social_pages_by_accounts":[],"posts_published_links":{"901":"https://vk.ru/wall-1_2"},"posts_published_planned":{},"posts_errors":{"902":["boom"]}}`
	srv := whereServer(t, 200, body, nil)
	defer srv.Close()
	res, err := PostWhere(context.Background(), newTestClient(t, srv.URL), 5, PostWhereInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pages) != 2 || res.Pages[0].PageID != 901 || res.Pages[0].PublishedURL != "https://vk.ru/wall-1_2" || res.Pages[1].PageID != 902 || res.Pages[1].Errors[0] != "boom" {
		t.Errorf("pages = %+v", res.Pages)
	}
}

func TestPostWhere_NoPagesIsAnEmptyAnswerNotAFailure(t *testing.T) {
	srv := whereServer(t, 500, `{"message":"No pages for this object."}`, nil)
	defer srv.Close()
	res, err := PostWhere(context.Background(), newTestClient(t, srv.URL), 5, PostWhereInput{})
	if err != nil {
		t.Fatalf("PostWhere: %v (a post without pages in this network is a normal answer)", err)
	}
	if len(res.Pages) != 0 || res.Note == "" {
		t.Errorf("result = %+v, want empty pages with a note", res)
	}
}

func TestPostWhere_OtherServerErrorsPropagate(t *testing.T) {
	srv := whereServer(t, 500, `{"message":"This type of object is not exists."}`, nil)
	defer srv.Close()
	_, err := PostWhere(context.Background(), newTestClient(t, srv.URL), 5, PostWhereInput{})
	if err == nil {
		t.Fatal("a different HTTP 500 must be an error, not an empty result")
	}
}

func TestPostWhere_ValidationRefusesBeforeAnyRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s: invalid input must be refused locally", r.URL)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	for name, tc := range map[string]struct {
		id  int
		in  PostWhereInput
		sub string
	}{
		"zero id":         {0, PostWhereInput{}, "id is required"},
		"negative id":     {-3, PostWhereInput{}, "id is required"},
		"zero source":     {5, PostWhereInput{SourceID: pint(0)}, "--source must be a positive"},
		"negative source": {5, PostWhereInput{SourceID: pint(-1)}, "--source must be a positive"},
	} {
		_, err := PostWhere(context.Background(), c, tc.id, tc.in)
		var ve *ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), tc.sub) {
			t.Errorf("%s: err = %v, want *ValidationError containing %q", name, err, tc.sub)
		}
	}
}
