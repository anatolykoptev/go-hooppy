package hooppy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestListSearchPosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/posts-search" {
			t.Errorf("GET /posts-search, got %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("source_type") != "1" {
			t.Errorf("source_type = %q, want 1", q.Get("source_type"))
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
		SourceType: 1,
		Text:       "test query",
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

func TestListSearchPosts_SortingAndContent(t *testing.T) {
	var gotSortBy, gotSortDir, gotContentTypes string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotSortBy = q.Get("sort_by")
		gotSortDir = q.Get("sort_direction")
		gotContentTypes = q.Get("content_types")
		w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{
		SortBy:        "likes",
		SortDirection: "desc",
		ContentTypes:  "photos,videos",
	})
	if err != nil {
		t.Fatalf("ListSearchPosts: %v", err)
	}
	if gotSortBy != "likes" {
		t.Errorf("sort_by = %q, want likes", gotSortBy)
	}
	if gotSortDir != "desc" {
		t.Errorf("sort_direction = %q, want desc", gotSortDir)
	}
	if gotContentTypes != "photos,videos" {
		t.Errorf("content_types = %q, want photos,videos", gotContentTypes)
	}
}

// TestListSearchPosts_MetricFiltersRejected covers issue #63 (a): the five
// min_* metric threshold flags (min_likes, min_views, min_comments,
// min_reposts, min_involvement) are NOT server-side filters — the API
// silently ignores them and returns an unfiltered result set. The library
// refuses them before any request is issued, pointing the caller at
// --sort-by, which does work server-side. The flags stay registered (a flag
// that errors with an explanation is strictly better than one that lies),
// so this is source-compatible but BEHAVIOUR-CHANGING: a caller that
// previously passed MinViews: 100 got a result set and now gets an error
// (see CHANGELOG). The guard fires on any non-zero value, including
// negatives — a computed threshold like avg-stddev going negative must not
// silently fall through to an unfiltered result (issue #65 item 4).
//
// The load-bearing property — refusal happens BEFORE any request is issued
// — is pinned by a reached flag in the stub handler: a refactor that issues
// the GET and then errors keeps err != nil but trips the reached assertion
// (issue #65 item 5).
func TestListSearchPosts_MetricFiltersRejected(t *testing.T) {
	cases := []struct {
		name string
		f    SearchPostsFilter
	}{
		{"MinLikes", SearchPostsFilter{MinLikes: 5000}},
		{"MinViews", SearchPostsFilter{MinViews: 1000000}},
		{"MinComments", SearchPostsFilter{MinComments: 10}},
		{"MinReposts", SearchPostsFilter{MinReposts: 5}},
		{"MinInvolvement", SearchPostsFilter{MinInvolvement: 10.5}},
		// Negative thresholds must be refused too: a caller passing -1
		// (directly, or from a computed threshold like avg-stddev going
		// negative) took neither branch of the old > 0 guard — no error, no
		// parameter, an unfiltered result while the help promised the flag
		// errors. Same shape as the original defect.
		{"MinLikes negative", SearchPostsFilter{MinLikes: -1}},
		{"MinViews negative", SearchPostsFilter{MinViews: -100}},
		{"MinComments negative", SearchPostsFilter{MinComments: -10}},
		{"MinReposts negative", SearchPostsFilter{MinReposts: -5}},
		{"MinInvolvement negative", SearchPostsFilter{MinInvolvement: -0.5}},
	}
	// A server that, if ever reached, would lie that the filter was applied.
	// reached MUST stay false for every case — refusal is before any request.
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			_, err := c.ListSearchPosts(context.Background(), tc.f)
			if err == nil {
				t.Fatalf("ListSearchPosts with %s: expected an error refusing the metric threshold filter, got nil — the API has no such server-side parameter and would silently return an unfiltered result", tc.name)
			}
			if reached {
				t.Fatalf("ListSearchPosts with %s: the refusal guard issued a request before erroring — refusal MUST happen before any request is issued (issue #65 item 5)", tc.name)
			}
		})
	}
}

// TestListSearchPosts_VideoDurationNegative covers issue #65 item 2:
// VideoDuration is a bucket-key filter gated on `> 0` — the same hole
// this PR closed for the min_* fields. A negative value took neither
// branch: no error, no parameter, an unfiltered result that looks
// filtered. The guard now rejects negatives before any request; zero
// stays the unset sentinel. Positive values are passed through verbatim
// (see TestListSearchPosts_VideoDurationPassThrough) — the prior guard
// hardcoded a 1..4 enum from a measurement that only tried 1..4, then a
// wider measurement found keys 5-8 are real, so the enum was removed.
func TestListSearchPosts_VideoDurationNegative(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{VideoDuration: -1})
	if err == nil {
		t.Fatal("ListSearchPosts with VideoDuration=-1: expected an error, got nil — a negative bucket key must be rejected before any request (issue #65 item 2)")
	}
	if reached {
		t.Fatal("ListSearchPosts with VideoDuration=-1: the guard issued a request before erroring — rejection MUST happen before any request is issued")
	}
}

// TestListSearchPosts_VideoDurationPassThrough covers issue #65 item 2:
// the prior guard hardcoded video_duration to a 1..4 enum. A wider live
// measurement found keys 5-8 are real and each returns a distinct result
// set (5 → 4128; 6 → 4161; 7 → 644; 8 → 677), so the enum hard-errored
// on four working filters. The guard now passes any non-negative value
// through verbatim and lets the server answer — do not re-introduce a
// hardcoded upper bound (9 and 10 error today, but the vendor may add
// them). This test asserts each measured-working key (5,6,7,8) reaches
// the wire as-is; reverting to the 1..4 enum makes it RED.
func TestListSearchPosts_VideoDurationPassThrough(t *testing.T) {
	for _, key := range []int{5, 6, 7, 8} {
		t.Run(fmt.Sprintf("key=%d", key), func(t *testing.T) {
			var gotVD string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotVD = r.URL.Query().Get("video_duration")
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)

			_, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{VideoDuration: key})
			if err != nil {
				t.Fatalf("ListSearchPosts with VideoDuration=%d: expected pass-through, got error: %v — keys 5-8 are measured to work; the 1..4 enum must not be re-introduced (issue #65 item 2)", key, err)
			}
			if gotVD != strconv.Itoa(key) {
				t.Fatalf("ListSearchPosts with VideoDuration=%d: video_duration on wire = %q, want %q — pass-through must send the value verbatim", key, gotVD, strconv.Itoa(key))
			}
		})
	}
}

// TestListSearchPosts_PhotosAmountNegative covers issue #65 item 2: the
// pre-existing PhotosAmount `> 0` guard had the same silent-negative hole
// as VideoDuration — a negative value fell through to an unfiltered result
// with no error. The guard now rejects negatives before any request; zero
// stays the unset sentinel and positive values are passed through (the
// upper bound is not confirmed — 5 was measured to filter).
func TestListSearchPosts_PhotosAmountNegative(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{PhotosAmount: -1})
	if err == nil {
		t.Fatal("ListSearchPosts with PhotosAmount=-1: expected an error, got nil — a negative bucket key must be rejected before any request (issue #65 item 2)")
	}
	if reached {
		t.Fatal("ListSearchPosts with PhotosAmount=-1: the guard issued a request before erroring — rejection MUST happen before any request is issued")
	}
}

// TestListSearchPosts_PhotosAmountPassThrough covers issue #65 item 3: the
// no-hardcoded-enum policy is guarded for VideoDuration (re-adding
// `|| f.VideoDuration > 4` goes RED), but PhotosAmount had no equivalent
// pass-through test — adding `|| f.PhotosAmount > 5` stayed GREEN across
// the full suite. Nothing stopped the same enum mistake being remade on
// the field whose own measurement table is the proof a ceiling would be
// wrong. This test asserts keys 6, 10 and 99 reach the wire verbatim;
// reverting to a `> 5` (or any upper-bound) guard makes it RED. Key 10
// and 99 return identical counts (saturation — "N or more", not "exactly
// N"), so both are included to pin the saturation semantics too.
func TestListSearchPosts_PhotosAmountPassThrough(t *testing.T) {
	for _, key := range []int{6, 10, 99} {
		t.Run(fmt.Sprintf("key=%d", key), func(t *testing.T) {
			var gotPA string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPA = r.URL.Query().Get("photos_amount")
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)

			_, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{PhotosAmount: key})
			if err != nil {
				t.Fatalf("ListSearchPosts with PhotosAmount=%d: expected pass-through, got error: %v — the valid key space is not enumerable client-side; a hardcoded upper bound must not be re-introduced (issue #65 item 3)", key, err)
			}
			if gotPA != strconv.Itoa(key) {
				t.Fatalf("ListSearchPosts with PhotosAmount=%d: photos_amount on wire = %q, want %q — pass-through must send the value verbatim", key, gotPA, strconv.Itoa(key))
			}
		})
	}
}

// TestListSearchPosts_IDPageNegative covers issue #65 item 1: the
// ID/page filters that are still WORKING filters (SourceType, Page) were
// gated on `> 0` — the same silent-negative hole this PR closed for the
// min_* and bucket-key fields. A negative took neither branch: no error,
// no parameter, an unfiltered result that looks filtered. Reachable from
// the shipped CLI (--source-type -1, --page -1 via pflag's signed IntVar).
// The guard now rejects negatives before any request; zero stays the
// unset sentinel. Each case is isolated to one field so a regression in
// any single guard is visible.
//
// SourceID, SourceResourceID, and OwnerID are NOT here: they are phantom
// parameters (issues #67, #73) whose non-zero guard fires on != 0 (so a
// negative is refused by the phantom guard, not the negative guard). Their
// negative path is covered by TestPhantomFilterSweep, which sets a positive
// value — the same guard fires for both signs. Keeping them here would
// read as negative-path coverage while exercising only the phantom guard.
func TestListSearchPosts_IDPageNegative(t *testing.T) {
	cases := []struct {
		name string
		f    SearchPostsFilter
	}{
		{"SourceType negative", SearchPostsFilter{SourceType: -1}},
		{"Page negative", SearchPostsFilter{Page: -1}},
	}
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			_, err := c.ListSearchPosts(context.Background(), tc.f)
			if err == nil {
				t.Fatalf("ListSearchPosts with %s: expected an error, got nil — a negative ID/page value must be rejected before any request (issue #65 item 1)", tc.name)
			}
			if reached {
				t.Fatalf("ListSearchPosts with %s: the guard issued a request before erroring — rejection MUST happen before any request is issued", tc.name)
			}
		})
	}
}

// TestListSearchPosts_FilterVocabularyPinned covers issue #63 (c): the
// /posts-search endpoint publishes its real filter vocabulary in every
// response's filters_plug array (one entry per valid filter, keyed by
// `slug`). This test captures a realistic filters_plug fixture, drives
// ListSearchPosts with every VALID filter populated, and asserts that every
// parameter the function puts on the wire appears as a slug in the
// descriptor — excepting the three sort/pagination parameters (page,
// sort_by, sort_direction), which the descriptor does not list but which
// demonstrably work. This is the test that would have caught the five
// invented min_* names on the day they were written, and keeps catching a
// vendor rename.
//
// This test asserts SLUGS, not VALUES, on purpose. The descriptor's
// `values` arrays are advisory, not authoritative: measured against a live
// response, `documents` is a working content_types value the descriptor
// omits, and photos_amount/video_duration ship values:[] (empty) yet accept
// arguments. Asserting values here would couple the test to a non-exhaustive
// list and force a false "improvement" — do NOT tighten this into a value
// check. See the content-filters comment in posts_search.go for the measured
// evidence.
func TestListSearchPosts_FilterVocabularyPinned(t *testing.T) {
	// Realistic filters_plug fixture: the complete descriptor measured from
	// the live API. No account identifiers — only the vendor's filter schema.
	const filtersPlugFixture = `[
		{"slug":"text","type":"input","name":"Text"},
		{"slug":"date_from","type":"date","name":"Date from"},
		{"slug":"date_to","type":"date","name":"Date to"},
		{"slug":"source_type","type":"select","name":"Source type","values":[{"key":"1","name":"Social"},{"key":"2","name":"RSS"}]},
		{"slug":"source_id","type":"select","name":"Source","values":[{"key":"1","name":"VK"},{"key":"7","name":"Instagram"}]},
		{"slug":"source_resource_id","type":"select","name":"Resource","values":[]},
		{"slug":"owner_id","type":"select","name":"Owner","values":[]},
		{"slug":"content_types","type":"checkbox","name":"Content types","values":[{"key":"photos","name":"Photos"},{"key":"videos","name":"Videos"},{"key":"audios","name":"Audios"},{"key":"documents","name":"Documents"},{"key":"links","name":"Links"}]},
		{"slug":"content_types_exclude","type":"checkbox","name":"Exclude content types","values":[{"key":"photos","name":"Photos"},{"key":"videos","name":"Videos"}]},
		{"slug":"photos_amount","type":"select","name":"Photos amount","values":[{"key":"1","name":"1"},{"key":"2","name":"2"},{"key":"3","name":"3+"}]},
		{"slug":"video_duration","type":"select","name":"Video duration","values":[{"key":"1","name":"Short"},{"key":"2","name":"Medium"},{"key":"3","name":"Long"}]}
	]`
	type plugEntry struct {
		Slug string `json:"slug"`
	}
	var plug []plugEntry
	if err := json.Unmarshal([]byte(filtersPlugFixture), &plug); err != nil {
		t.Fatalf("decode filters_plug fixture: %v", err)
	}
	validSlugs := make(map[string]bool, len(plug))
	for _, e := range plug {
		validSlugs[e.Slug] = true
	}

	// sort/pagination params the descriptor does NOT list but which work.
	sortPagParams := map[string]bool{"page": true, "sort_by": true, "sort_direction": true}

	var capturedKeys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k := range r.URL.Query() {
			capturedKeys = append(capturedKeys, k)
		}
		w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	// Populate every VALID filter (no metric thresholds — those are refused
	// by the guard and never reach the wire; see TestListSearchPosts_MetricFiltersRejected;
	// no source_id/source_resource_id/owner_id — those are phantom on
	// /posts-search and refused, see TestPhantomFilterSweep).
	_, err := c.ListSearchPosts(context.Background(), SearchPostsFilter{
		Text:                "query",
		DateFrom:            "01.01.2026",
		DateTo:              "31.01.2026",
		SourceType:          1,
		Page:                2,
		SortBy:              "likes",
		SortDirection:       "desc",
		PhotosAmount:        3,
		VideoDuration:       2,
		ContentTypes:        "photos,videos",
		ContentTypesExclude: "audios",
	})
	if err != nil {
		t.Fatalf("ListSearchPosts: %v", err)
	}
	if len(capturedKeys) == 0 {
		t.Fatal("no query parameters captured — server handler was not reached or sent no params")
	}

	seen := make(map[string]bool, len(capturedKeys))
	for _, k := range capturedKeys {
		seen[k] = true
	}
	// video_duration is the one real filter we previously omitted (issue #63 b).
	// Asserting it is on the wire makes this test RED if the addition is reverted.
	if !seen["video_duration"] {
		t.Errorf("video_duration not on the wire — captured keys: %v", capturedKeys)
	}

	for _, k := range capturedKeys {
		if sortPagParams[k] {
			continue
		}
		if !validSlugs[k] {
			t.Errorf("parameter %q is on the wire but is NOT a slug in the filters_plug descriptor — the API does not recognize this filter and would silently ignore it (false-confidence bug class)", k)
		}
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

func TestRewriteSearchPost(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts" {
			t.Errorf("POST /posts, got %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":6001}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2001,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "Rewritten text", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("RewriteSearchPost: %v", err)
	}
	if resp.ID != 6001 {
		t.Errorf("ID = %d, want 6001", resp.ID)
	}
	if capturedBody["as_copy"].(float64) != 1 {
		t.Errorf("as_copy = %v, want 1", capturedBody["as_copy"])
	}
	if capturedBody["ids"] != "2001" {
		t.Errorf("ids = %v, want '2001'", capturedBody["ids"])
	}
	texts, ok := capturedBody["texts"].([]interface{})
	if !ok || len(texts) != 1 {
		t.Fatalf("texts = %v, want array of 1", capturedBody["texts"])
	}
	textMap := texts[0].(map[string]interface{})
	if textMap["text"] != "Rewritten text" {
		t.Errorf("texts[0].text = %v, want 'Rewritten text'", textMap["text"])
	}
}

func TestRewriteSearchPost_Scheduled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts" {
			t.Errorf("POST /posts, got %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"id":6002}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2002,
		PublicationWhenType: 2,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		PublicationDate: &PublicationDate{
			Date:    "15.08.2026",
			Hours:   "09",
			Minutes: "15",
		},
		Texts: []PostText{{Text: "Scheduled rewrite", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("RewriteSearchPost: %v", err)
	}
	if resp.ID != 6002 {
		t.Errorf("ID = %d, want 6002", resp.ID)
	}
}

func TestGetSearchPostEdit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/posts-search/6453701/edit" {
			t.Errorf("path = %s, want /posts-search/6453701/edit", r.URL.Path)
		}
		if r.URL.Query().Get("as_copy") != "1" {
			t.Errorf("as_copy = %q, want '1'", r.URL.Query().Get("as_copy"))
		}
		w.Write([]byte(`{
			"id": "6453701",
			"publication_when_type": 1,
			"publication_how_type": 1,
			"publication_where_type": 1,
			"created_by": 7,
			"texts": [{"text": "Original text", "source_id": 0}],
			"attachments": [
				{"id": 13803886, "result_id": 6453701, "type": "photo", "data": {"id": "abc123", "url": "https://example.com/photo.jpg", "type": "photo", "source_id": 1}}
			]
		}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	edit, err := c.GetSearchPostEdit(context.Background(), 6453701)
	if err != nil {
		t.Fatalf("GetSearchPostEdit: %v", err)
	}
	if edit.ID != "6453701" {
		t.Errorf("ID = %q, want '6453701'", edit.ID)
	}
	if len(edit.Attachments) != 1 {
		t.Fatalf("Attachments = %d, want 1", len(edit.Attachments))
	}
	if edit.Attachments[0].Type != "photo" {
		t.Errorf("Attachments[0].Type = %q, want 'photo'", edit.Attachments[0].Type)
	}
}

func TestSearchPostPhotos(t *testing.T) {
	edit := &SearchPostEditResponse{
		Attachments: []Attachment{
			{Type: "photo", Data: map[string]interface{}{"id": "photo1", "url": "https://example.com/1.jpg"}},
			{Type: "video", Data: map[string]interface{}{"id": "video1", "url": "https://example.com/1.mp4"}},
			{Type: "link", Data: "https://example.com"},
		},
	}
	att := SearchPostPhotos(edit)
	if att == nil {
		t.Fatal("SearchPostPhotos returned nil")
	}
	if att.Type != "photos" {
		t.Errorf("Type = %q, want 'photos'", att.Type)
	}
	photos, ok := att.Data.([]interface{})
	if !ok {
		t.Fatalf("Data = %T, want []interface{}", att.Data)
	}
	if len(photos) != 2 {
		t.Errorf("len(photos) = %d, want 2 (photo + video, not link)", len(photos))
	}
}

func TestSearchPostNonPhotoAttachments(t *testing.T) {
	edit := &SearchPostEditResponse{
		Attachments: []Attachment{
			{Type: "photo", Data: map[string]interface{}{"id": "photo1"}},
			{Type: "copyright", Data: "https://vk.com/wall-123_456"},
			{Type: "link", Data: "https://example.com"},
			{Type: "video", Data: map[string]interface{}{"id": "video1"}},
		},
	}
	result := SearchPostNonPhotoAttachments(edit)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2 (copyright + link, not photo/video)", len(result))
	}
	if result[0].Type != "copyright" {
		t.Errorf("result[0].Type = %q, want 'copyright'", result[0].Type)
	}
	if result[1].Type != "link" {
		t.Errorf("result[1].Type = %q, want 'link'", result[1].Type)
	}
}

func TestAttachmentHelpers(t *testing.T) {
	tests := []struct {
		name     string
		att      Attachment
		wantType string
	}{
		{"LinkAttachment", LinkAttachment("https://example.com"), "link"},
		{"SourceAttachment", SourceAttachment("https://vk.com/wall-1_2"), "source"},
		{"CopyrightAttachment", CopyrightAttachment("https://vk.com/wall-1_2"), "copyright"},
		{"TitleAttachment", TitleAttachment("My Title"), "title"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.att.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", tc.att.Type, tc.wantType)
			}
		})
	}
}

func TestPollAttachment(t *testing.T) {
	att := PollAttachment(Poll{
		Question:    "Best city?",
		Answers:     []PollAnswer{{Text: "SPB"}, {Text: "MSK"}},
		IsAnonymous: true,
	})
	if att.Type != "poll" {
		t.Errorf("Type = %q, want 'poll'", att.Type)
	}
	poll, ok := att.Data.(Poll)
	if !ok {
		t.Fatalf("Data = %T, want Poll", att.Data)
	}
	if poll.Question != "Best city?" {
		t.Errorf("Question = %q", poll.Question)
	}
	if len(poll.Answers) != 2 {
		t.Errorf("Answers = %d, want 2", len(poll.Answers))
	}
	if !poll.IsAnonymous {
		t.Errorf("IsAnonymous = false, want true")
	}
}

func TestRepostAttachment(t *testing.T) {
	att := RepostAttachment("https://vk.com/wall-1_2", "Original Post")
	if att.Type != "repost" {
		t.Errorf("Type = %q, want 'repost'", att.Type)
	}
	r, ok := att.Data.(Repost)
	if !ok {
		t.Fatalf("Data = %T, want Repost", att.Data)
	}
	if r.Link != "https://vk.com/wall-1_2" {
		t.Errorf("Link = %q", r.Link)
	}
	if r.Title != "Original Post" {
		t.Errorf("Title = %q", r.Title)
	}
}

func TestTelegramButtonsAttachment(t *testing.T) {
	att := TelegramButtonsAttachment([]TelegramButton{
		{Name: "Website", Link: "https://example.com"},
		{Name: "Telegram", Link: "https://t.me/example"},
	})
	if att.Type != "telegram_buttons" {
		t.Errorf("Type = %q, want 'telegram_buttons'", att.Type)
	}
	tb, ok := att.Data.(TelegramButtons)
	if !ok {
		t.Fatalf("Data = %T, want TelegramButtons", att.Data)
	}
	if len(tb.List) != 2 {
		t.Errorf("List = %d, want 2", len(tb.List))
	}
}

func TestScrapedPhotoAttachment(t *testing.T) {
	// ScrapedPhotoAttachment is deprecated — scraped VK photo IDs can't be
	// attached to your own post (VK doesn't allow cross-group references).
	// The helper is kept for reference; tests verify it still builds the
	// correct structure in case it's useful for non-VK sources in the future.
	photos := []SearchPostPhoto{
		{ID: 111, OwnerID: -222, URL: "https://example.com/1.jpg"},
		{ID: 333, OwnerID: -222, URL: "https://example.com/2.jpg"},
	}
	att := ScrapedPhotoAttachment(photos)
	if att.Type != "photos" {
		t.Errorf("Type = %q, want 'photos'", att.Type)
	}
	items, ok := att.Data.([]map[string]interface{})
	if !ok {
		t.Fatalf("Data = %T, want []map[string]interface{}", att.Data)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0]["id"] != "111" {
		t.Errorf("items[0].id = %v, want '111'", items[0]["id"])
	}
	if items[0]["owner_id"] != -222 {
		t.Errorf("items[0].owner_id = %v, want -222", items[0]["owner_id"])
	}
	if items[0]["type"] != "photo" {
		t.Errorf("items[0].type = %v, want 'photo'", items[0]["type"])
	}
}

// TestImportSearchPost asserts the wire shape of a successful ImportSearchPost
// request: PUT /posts/import with as_copy=1, the hardcoded
// publication_where_type=1, ids as the string form of the single search-post
// id, and the attachments passed through as one {type: "photos"} entry.
// Mirrors TestCopySearchPost (the nearer sibling) but decodes the body rather
// than substring-matching, and covers the import-specific fields (as_copy,
// publication_where_type, ids) that CopySearchPost does not send.
func TestImportSearchPost(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/posts/import" {
			t.Errorf("PUT /posts/import, got %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":7001}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2003,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "Imported text", SourceID: 0}},
		Attachments: []Attachment{
			{Type: "photos", Data: []interface{}{
				map[string]interface{}{"id": "photo1", "url": "https://example.com/1.jpg"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost: %v", err)
	}
	if resp.ID != 7001 {
		t.Errorf("ID = %d, want 7001", resp.ID)
	}
	if capturedBody["as_copy"].(float64) != 1 {
		t.Errorf("as_copy = %v, want 1", capturedBody["as_copy"])
	}
	if capturedBody["publication_where_type"].(float64) != 1 {
		t.Errorf("publication_where_type = %v, want 1 (hardcoded by ImportSearchPost)", capturedBody["publication_where_type"])
	}
	if capturedBody["ids"] != "2003" {
		t.Errorf("ids = %v, want '2003' (string form of SearchPostID)", capturedBody["ids"])
	}
	attachments, ok := capturedBody["attachments"].([]interface{})
	if !ok || len(attachments) != 1 {
		t.Fatalf("attachments = %v, want array of 1 {type: \"photos\"} entry", capturedBody["attachments"])
	}
	attMap := attachments[0].(map[string]interface{})
	if attMap["type"] != "photos" {
		t.Errorf("attachments[0].type = %v, want 'photos'", attMap["type"])
	}
}

// TestImportSearchPost_ScheduleDrivenNoSchedules verifies the fail-closed
// guard: a schedule-driven import (publication_when_type=3) targeted at an
// EMPTY schedules list issues NO request and returns an error, rather than
// sending a PUT with schedules_ids=[] — a schedule-driven import targeted at
// nothing.
//
// Without the guard: ImportSearchPost normalises nil schedules to []int{}
// and issues the PUT, which the server may accept and silently publish to
// nothing.
func TestImportSearchPost_ScheduleDrivenNoSchedules(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7001}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2003,
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        nil, // empty — the bug default
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected fail-closed error for when_type=3 with empty schedules, got nil")
	}
	if requestMade {
		t.Fatal("ImportSearchPost issued a request despite when_type=3 + empty schedules — must fail before any request")
	}
	if !contains(err.Error(), "schedule") {
		t.Errorf("error must explain the schedule requirement, got: %v", err)
	}
}

// TestCopySearchPost_ScheduleDrivenNoSchedules mirrors the import guard for
// the copy endpoint: when_type=3 + empty schedules must fail closed.
func TestCopySearchPost_ScheduleDrivenNoSchedules(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7002}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2004,
		PublicationWhenType: 3,
		PublicationHowType:  1,
		SchedulesIDs:        []int{}, // explicit empty
	})
	if err == nil {
		t.Fatal("expected fail-closed error for when_type=3 with empty schedules, got nil")
	}
	if requestMade {
		t.Fatal("CopySearchPost issued a request despite when_type=3 + empty schedules")
	}
}

// TestCopySearchPost_RejectsBatchSlice verifies the BLOCKER fix at the library
// surface: CopySearchPost REFUSES a non-empty SearchPostIDs before any request.
// PUT /posts/copy takes a singular search_post_id int and silently ignores
// search_post_ids; this method marshals the payload wholesale, so without the
// guard a library consumer that sets SearchPostIDs gets the slice on the wire
// (json:"search_post_ids,omitempty") with err == nil — a phantom batch. The
// CLI --post-ids removal closed one caller; this closes the published module
// surface (the CLI is one of several). The error must name the batch-capable
// endpoints (RewriteSearchPost/ImportSearchPost) so the consumer reaches them.
//
// RED-on-revert: drop the `len(payload.SearchPostIDs) > 0` guard from
// CopySearchPost and the stub is reached (requestMade=true) with err == nil →
// both assertions fail.
func TestCopySearchPost_RejectsBatchSlice(t *testing.T) {
	requestMade := false
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":7006}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		SearchPostIDs:       []int{2001, 2002, 2003},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
	})
	if err == nil {
		t.Fatal("CopySearchPost with SearchPostIDs: expected an error refusing the batch slice, got nil — PUT /posts/copy takes a singular search_post_id and silently ignores search_post_ids (phantom batch)")
	}
	if requestMade {
		t.Fatal("CopySearchPost issued a request despite a non-empty SearchPostIDs — must fail before any request (the slice would otherwise marshal onto the wire with err == nil)")
	}
	if !contains(err.Error(), "RewriteSearchPost") || !contains(err.Error(), "ImportSearchPost") {
		t.Errorf("error must name the batch-capable endpoints RewriteSearchPost/ImportSearchPost, got: %v", err)
	}
	// The scalar must stay valid on its own (no batch slice) — sanity-check
	// the guard does not over-fire on the legacy single-post path.
	requestMade = false
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7007}`))
	}))
	defer srv2.Close()
	c2 := newTestClient(t, srv2)
	if _, err := c2.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        1001,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
	}); err != nil {
		t.Fatalf("CopySearchPost scalar path broke: %v", err)
	}
	if !requestMade {
		t.Fatal("CopySearchPost scalar path did not issue a request — the batch guard must not over-fire when SearchPostIDs is empty")
	}
}

// TestRewriteSearchPost_ScheduleDrivenNoSchedules mirrors the guard for the
// rewrite endpoint: when_type=3 + empty schedules must fail closed.
func TestRewriteSearchPost_ScheduleDrivenNoSchedules(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7003}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2005,
		PublicationWhenType: 3,
		PublicationHowType:  1,
		SchedulesIDs:        nil,
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected fail-closed error for when_type=3 with empty schedules, got nil")
	}
	if requestMade {
		t.Fatal("RewriteSearchPost issued a request despite when_type=3 + empty schedules")
	}
}

// TestRewriteSearchPost_BatchIDsOrder verifies the batch form: a slice of
// SearchPostIDs reaches the wire as a comma-joined ids string in the
// CALLER's order. The server assigns schedule slots in the order it receives
// ids, so order preservation is load-bearing. Decodes the body (does not
// substring-match) and asserts the exact ids string.
//
// The fixture is deliberately NON-MONOTONIC with a REPEAT ({2003, 2001, 2002,
// 2001}): an ascending-distinct fixture makes a sort and a dedupe both no-ops,
// so the test stays green even if copySearchPostIDs silently sorts or dedupes
// the slice — proven by mutation (injecting sort.Ints + a dedupe pass left the
// suite green under the old {2001,2002,2003} fixture). This fixture
// discriminates against both at once: a sort yields "2001,2001,2002,2003", a
// dedupe yields "2003,2001,2002", and either mutation now goes RED here.
func TestRewriteSearchPost_BatchIDsOrder(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts" {
			t.Errorf("POST /posts, got %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":6003}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2003, 2001, 2002, 2001},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "Batch rewrite", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("RewriteSearchPost batch: %v", err)
	}
	if resp.ID != 6003 {
		t.Errorf("ID = %d, want 6003", resp.ID)
	}
	// Decoded assertion, not substring: the exact joined string in caller order.
	// Non-monotonic + repeat: a sort or dedupe mutation changes this string.
	if got, want := capturedBody["ids"], "2003,2001,2002,2001"; got != want {
		t.Errorf("ids = %v, want %q (caller order + duplicates preserved)", got, want)
	}
	if capturedBody["as_copy"].(float64) != 1 {
		t.Errorf("as_copy = %v, want 1", capturedBody["as_copy"])
	}
}

// TestImportSearchPost_BatchIDsOrder mirrors the rewrite batch test for the
// import endpoint: a slice of SearchPostIDs reaches PUT /posts/import as a
// comma-joined ids string in caller order. The fixture is non-monotonic with
// a repeat ({3003, 3001, 3002, 3001}) so a silent sort or dedupe in
// copySearchPostIDs goes RED here (same rationale as the rewrite test).
func TestImportSearchPost_BatchIDsOrder(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/posts/import" {
			t.Errorf("PUT /posts/import, got %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":7002}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{3003, 3001, 3002, 3001},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "Batch import", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch: %v", err)
	}
	if resp.ID != 7002 {
		t.Errorf("ID = %d, want 7002", resp.ID)
	}
	if got, want := capturedBody["ids"], "3003,3001,3002,3001"; got != want {
		t.Errorf("ids = %v, want %q (caller order + duplicates preserved)", got, want)
	}
}

// TestRewriteSearchPost_BothEmpty verifies the precedence guard: when both
// SearchPostIDs (empty/nil) and SearchPostID (zero) are unset, the wrapper
// errors before issuing any request — there is nothing to copy.
func TestRewriteSearchPost_BothEmpty(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":6004}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected error for both SearchPostIDs and SearchPostID empty, got nil")
	}
	if requestMade {
		t.Fatal("RewriteSearchPost issued a request despite both id fields empty — must fail before any request")
	}
}

// TestImportSearchPost_BothEmpty mirrors the both-empty guard for import.
func TestImportSearchPost_BothEmpty(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7003}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected error for both SearchPostIDs and SearchPostID empty, got nil")
	}
	if requestMade {
		t.Fatal("ImportSearchPost issued a request despite both id fields empty — must fail before any request")
	}
}

// TestRewriteSearchPost_BothSet verifies the precedence guard: setting both
// SearchPostIDs and SearchPostID is ambiguous and must error before any
// request, rather than silently preferring one.
func TestRewriteSearchPost_BothSet(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":6005}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2001,
		SearchPostIDs:       []int{2002, 2003},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected error for both SearchPostIDs and SearchPostID set, got nil")
	}
	if requestMade {
		t.Fatal("RewriteSearchPost issued a request despite both id fields set — must fail before any request")
	}
}

// TestImportSearchPost_BothSet mirrors the both-set guard for import.
func TestImportSearchPost_BothSet(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7004}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        3001,
		SearchPostIDs:       []int{3002, 3003},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected error for both SearchPostIDs and SearchPostID set, got nil")
	}
	if requestMade {
		t.Fatal("ImportSearchPost issued a request despite both id fields set — must fail before any request")
	}
}

// TestRewriteSearchPost_BatchScheduleGuard verifies the fail-closed schedule
// guard fires for the BATCH form too: when_type=3 + an empty schedule list
// with a multi-id batch must error before any request. A batch of twenty
// posts targeted at no schedule is twenty times the damage of one.
func TestRewriteSearchPost_BatchScheduleGuard(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":6006}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001, 2002, 2003},
		PublicationWhenType: 3,
		PublicationHowType:  1,
		SchedulesIDs:        nil, // empty — the trap
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected fail-closed error for batch when_type=3 with empty schedules, got nil")
	}
	if requestMade {
		t.Fatal("RewriteSearchPost issued a request despite batch when_type=3 + empty schedules — must fail before any request")
	}
}

// TestImportSearchPost_BatchScheduleGuard mirrors the batch schedule guard
// for the import endpoint.
func TestImportSearchPost_BatchScheduleGuard(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7005}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{3001, 3002, 3003},
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{}, // explicit empty
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected fail-closed error for batch when_type=3 with empty schedules, got nil")
	}
	if requestMade {
		t.Fatal("ImportSearchPost issued a request despite batch when_type=3 + empty schedules — must fail before any request")
	}
}

// TestCopySearchPostIDs_NonPositiveRejects verifies the batch validation the
// scalar path has but the batch path lacked: a zero or negative id in
// SearchPostIDs is rejected with the offending INDEX, before any request.
// The scalar path rejects SearchPostID == 0 (nothing to copy); without this
// guard the batch path joined "0,-5,2001" onto the wire with err=nil.
//
// RED-on-revert: drop the `id <= 0` check from copySearchPostIDs and this
// test fails at the requestMade + err assertions.
func TestCopySearchPostIDs_NonPositiveRejects(t *testing.T) {
	cases := []struct {
		name string
		ids  []int
		// wantIndex is the offending slice index named in the error.
		wantIndex int
	}{
		{"zero at head", []int{0, 2001, 2002}, 0},
		{"negative mid-list", []int{2001, -5, 2002}, 1},
		{"zero tail", []int{2001, 2002, 0}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requestMade := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestMade = true
				w.Write([]byte(`{"id":6007}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)

			_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
				SearchPostIDs:       tc.ids,
				PublicationWhenType: 1,
				PublicationHowType:  1,
				SelectedPagesIDs:    []int{123456},
				Texts:               []PostText{{Text: "x", SourceID: 0}},
			})
			if err == nil {
				t.Fatal("expected error for non-positive SearchPostIDs element, got nil")
			}
			if requestMade {
				t.Fatal("RewriteSearchPost issued a request despite a non-positive id — must fail before any request")
			}
			want := fmt.Sprintf("SearchPostIDs[%d]", tc.wantIndex)
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name the offending index %q", err.Error(), want)
			}
		})
	}
}

// TestCopySearchPostIDs_ScalarNegativeRejects verifies finding 5a: the scalar
// arm of copySearchPostIDs previously sent a negative SearchPostID straight
// onto the wire (the old `if payload.SearchPostID != 0` took any non-zero,
// including -5), while the batch arm rejected id <= 0. The doc claimed the
// batch matched "the scalar path which rejects SearchPostID == 0" — a guard
// that did not exist (0 is the unset sentinel, not a rejection). The scalar
// arm now rejects a negative; 0 stays the unset sentinel (the both-empty
// guard fires when both fields are 0/empty).
//
// RED-on-revert: drop the `payload.SearchPostID < 0` guard and the stub is
// reached (requestMade=true) with err == nil → both assertions fail.
func TestCopySearchPostIDs_ScalarNegativeRejects(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":6010}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        -5,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected error for negative scalar SearchPostID, got nil — a negative scraped-post id is never real and must be rejected before any request")
	}
	if requestMade {
		t.Fatal("RewriteSearchPost issued a request despite a negative SearchPostID — must fail before any request")
	}
	if !strings.Contains(err.Error(), "SearchPostID = -5") {
		t.Errorf("error must name the offending scalar value, got: %v", err)
	}
}

// TestCopySearchPostIDs_DuplicatesKept verifies the duplicate policy: the
// same source post in two schedule slots may be intentional, so duplicates
// are preserved on the wire (NOT deduped). The order test fixtures above
// ({2003,2001,2002,2001}) already cover this on the wire; this test pins the
// policy at the helper level and documents the decision.
//
// RED-on-revert: if a dedupe pass is added to copySearchPostIDs, the captured
// ids string loses the repeat and this test fails.
func TestCopySearchPostIDs_DuplicatesKept(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Write([]byte(`{"id":6008}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001, 2001, 2002},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("duplicates must be accepted, got error: %v", err)
	}
	if got, want := capturedBody["ids"], "2001,2001,2002"; got != want {
		t.Errorf("ids = %v, want %q (duplicates preserved, not deduped)", got, want)
	}
}

// TestCopySearchPostIDs_NoSliceMutation verifies that copySearchPostIDs does
// NOT mutate the caller's slice — the payload is passed by value but the
// slice header shares backing storage with the caller's array, and a library
// that reorders a caller's slice in place is a nasty surprise. The caller's
// slice must be byte-identical after the call.
//
// RED-on-revert: if copySearchPostIDs sorts/dedupes payload.SearchPostIDs in
// place, the caller's slice changes and this test fails.
func TestCopySearchPostIDs_NoSliceMutation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":6009}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	original := []int{2003, 2001, 2002, 2001}
	originalCopy := append([]int(nil), original...)
	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       original,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("RewriteSearchPost: %v", err)
	}
	if !reflect.DeepEqual(original, originalCopy) {
		t.Errorf("copySearchPostIDs mutated the caller's slice: got %v, want %v (slice must be read-only)", original, originalCopy)
	}
}
