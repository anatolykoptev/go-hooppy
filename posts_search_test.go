package hooppy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
// SourceID, SourceResourceID, and OwnerID ARE included here even though
// they are phantom parameters (issues #67, #73): the phantom guard fires
// on != 0 today, so a negative is refused by it — but that is a property
// of the CURRENT guard. The observable these cases assert (a negative
// value errors before any request) stays true and stays worth asserting
// regardless of which internal guard produces the refusal. They are the
// only thing that notices if the phantom guard is weakened from != 0 to
// > 0: a negative would then take neither branch — no error, no
// parameter, an unfiltered result that looks filtered — which is issue
// #65 item 1 verbatim and reachable from the shipped CLI. The structural
// sweep in TestPhantomFilterSweep now also runs both signs on every
// phantom field (see its negVal arm), so this is belt-and-braces with
// that gate.
func TestListSearchPosts_IDPageNegative(t *testing.T) {
	cases := []struct {
		name string
		f    SearchPostsFilter
	}{
		{"SourceType negative", SearchPostsFilter{SourceType: -1}},
		{"SourceID negative", SearchPostsFilter{SourceID: -1}},
		{"SourceResourceID negative", SearchPostsFilter{SourceResourceID: -1}},
		{"OwnerID negative", SearchPostsFilter{OwnerID: -1}},
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

	resp, err := c.ListSourceResources(context.Background(), 0)
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

// TestListSourceResources_DecodesPagingFields pins the issue #98 fix: the
// server sends total_rows/is_has_more/rows_limit on /posts-search/source-
// resources, and SourceResourcesResponse now models them (previously only
// List was modelled, so the signal was dropped at decode and a truncation
// above 20 source resources was undetectable).
//
// RED-on-revert: remove any of the three paging fields from
// SourceResourcesResponse and the corresponding assertion fails (zero value
// instead of the wire value).
func TestListSourceResources_DecodesPagingFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"list":[{"id":1,"name":"S","source_type":1,"search_type":1,"source_id":1,"data":"","hashtag":"","link":""}],"total_rows":25,"is_has_more":true,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ListSourceResources(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListSourceResources: %v", err)
	}
	if resp.TotalRows != 25 {
		t.Errorf("TotalRows = %d, want 25 — the field must be modelled so a truncation warning can be computed (issue #98)", resp.TotalRows)
	}
	if !resp.IsHasMore {
		t.Errorf("IsHasMore = false, want true — the field must be modelled (issue #98)")
	}
	if resp.RowsLimit != 20 {
		t.Errorf("RowsLimit = %d, want 20 (issue #98)", resp.RowsLimit)
	}
}

// TestListAllSourceResources_TwoPages verifies the source-resources walker
// accumulates both pages and terminates on is_has_more=false. This is the
// library half of the issue #103 --all fix for `search sources`.
//
// RED-on-revert: break the ListAllSourceResourcesWithTotal walk (or revert
// ListSourceResources to take no page param) and len(all) < 4 fails.
func TestListAllSourceResources_TwoPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`{"list":[{"id":3,"name":"C","source_type":1,"search_type":1,"source_id":1,"data":"","hashtag":"","link":""},{"id":4,"name":"D","source_type":1,"search_type":1,"source_id":1,"data":"","hashtag":"","link":""}],"total_rows":4,"is_has_more":false,"rows_limit":20}`))
		default:
			w.Write([]byte(`{"list":[{"id":1,"name":"A","source_type":1,"search_type":1,"source_id":1,"data":"","hashtag":"","link":""},{"id":2,"name":"B","source_type":1,"search_type":1,"source_id":1,"data":"","hashtag":"","link":""}],"total_rows":4,"is_has_more":true,"rows_limit":20}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, total, err := c.ListAllSourceResourcesWithTotal(context.Background())
	if err != nil {
		t.Fatalf("ListAllSourceResourcesWithTotal: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("len(all) = %d, want 4 (two 2-row pages)", len(all))
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("page params = %v, want [1 2] — the walk must start at page 1", pages)
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

// TestStartParsing_WireDateFormat asserts that MarshalJSON emits date_from/
// date_to as dd.mm.yyyy strings ("" = any), NOT the int timestamps the struct
// fields declare. Decoding the received JSON into map[string]interface{} and
// comparing the values catches a type change: an int would decode as float64,
// not string.
func TestStartParsing_WireDateFormat(t *testing.T) {
	// Fixed unix timestamp at a known UTC date: 2026-07-21 00:00:00 UTC.
	fromUnix := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC).Unix()
	wantFromUnix := "21.07.2026"

	tests := []struct {
		name       string
		payload    ParsingStartPayload
		wantFrom   string
		wantTo     string
		wantFromIs string // "string" or "number" — asserts the JSON type
		wantToIs   string
	}{
		{
			name: "both Day fields set",
			payload: ParsingStartPayload{
				SourceResourceID: 1, DateFromDay: "21.07.2026", DateToDay: "28.07.2026",
			},
			wantFrom: "21.07.2026", wantTo: "28.07.2026",
			wantFromIs: "string", wantToIs: "string",
		},
		{
			name: "one-sided only DateFromDay",
			payload: ParsingStartPayload{
				SourceResourceID: 1, DateFromDay: "21.07.2026",
			},
			wantFrom: "21.07.2026", wantTo: "",
			wantFromIs: "string", wantToIs: "string",
		},
		{
			name: "neither set",
			payload: ParsingStartPayload{
				SourceResourceID: 1,
			},
			wantFrom: "", wantTo: "",
			wantFromIs: "string", wantToIs: "string",
		},
		{
			name: "int DateFrom set Day empty",
			payload: ParsingStartPayload{
				SourceResourceID: 1, DateFrom: int(fromUnix),
			},
			wantFrom: wantFromUnix, wantTo: "",
			wantFromIs: "string", wantToIs: "string",
		},
		{
			name: "Day set AND int set Day wins",
			payload: ParsingStartPayload{
				SourceResourceID: 1,
				DateFromDay:      "21.07.2026", DateFrom: int(fromUnix),
				DateToDay: "28.07.2026", DateTo: 999999999,
			},
			wantFrom: "21.07.2026", wantTo: "28.07.2026",
			wantFromIs: "string", wantToIs: "string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedBody map[string]interface{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &capturedBody)
				w.Write([]byte(`{"success":true}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)

			resp, err := c.StartParsing(context.Background(), tt.payload)
			if err != nil {
				t.Fatalf("StartParsing: %v", err)
			}
			if !resp.Success {
				t.Fatalf("Success = false, want true")
			}
			gotFrom, ok := capturedBody["date_from"]
			if !ok {
				t.Fatalf("date_from missing from wire body; body=%v", capturedBody)
			}
			gotTo, ok := capturedBody["date_to"]
			if !ok {
				t.Fatalf("date_to missing from wire body; body=%v", capturedBody)
			}
			// Assert the JSON type: string, not float64 (int).
			if _, isStr := gotFrom.(string); !isStr {
				t.Errorf("date_from type = %T (%v), want string %q", gotFrom, gotFrom, tt.wantFrom)
			}
			if _, isStr := gotTo.(string); !isStr {
				t.Errorf("date_to type = %T (%v), want string %q", gotTo, gotTo, tt.wantTo)
			}
			if gotFrom != tt.wantFrom {
				t.Errorf("date_from = %v, want %q", gotFrom, tt.wantFrom)
			}
			if gotTo != tt.wantTo {
				t.Errorf("date_to = %v, want %q", gotTo, tt.wantTo)
			}
		})
	}
}

// TestStartParsing_MalformedDateRejectsBeforeRequest asserts that a malformed
// DateFromDay/DateToDay is rejected client-side BEFORE any HTTP request is
// issued. The requirement is "zero requests reach the stub" — not merely
// that an error is returned.
func TestStartParsing_MalformedDateRejectsBeforeRequest(t *testing.T) {
	tests := []struct {
		name    string
		payload ParsingStartPayload
	}{
		{
			name: "malformed DateFromDay",
			payload: ParsingStartPayload{
				SourceResourceID: 1, DateFromDay: "2026-07-21",
			},
		},
		{
			name: "malformed DateToDay",
			payload: ParsingStartPayload{
				SourceResourceID: 1, DateToDay: "not-a-date",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqCount int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&reqCount, 1)
				w.Write([]byte(`{"success":true}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)

			_, err := c.StartParsing(context.Background(), tt.payload)
			// Assert ZERO requests first — "fails before the request" is the
			// actual requirement, not merely that an error came back.
			if got := atomic.LoadInt32(&reqCount); got != 0 {
				t.Errorf("request count = %d, want 0 (validation must reject before any HTTP request)", got)
			}
			if err == nil {
				t.Errorf("expected error for malformed date, got nil")
			}
		})
	}
}

// TestStopParsing pins the exact path, which is the whole behaviour of this
// call. The server has a suffix-less sibling that accepts the same DELETE and
// answers {"success":true} without cancelling anything (issue #94), so an
// assertion on the status code or the response body passes for both the
// working and the broken path. Only the path distinguishes them.
//
// This test previously asserted /posts-search/parsing and was green while the
// client could not cancel a parse at all.
func TestStopParsing(t *testing.T) {
	var reqCount atomic.Int64
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if err := c.StopParsing(context.Background()); err != nil {
		t.Fatalf("StopParsing: %v", err)
	}
	if got := reqCount.Load(); got != 1 {
		t.Fatalf("StopParsing issued %d requests, want 1 — an early return before the request would leave the path assertion below comparing empty strings", got)
	}
	if gotMethod != http.MethodDelete || gotPath != "/posts-search/parsing/stop" {
		t.Errorf("want DELETE /posts-search/parsing/stop, got %s %s — /posts-search/parsing is the suffix-less sibling that answers success and cancels nothing", gotMethod, gotPath)
	}
}

// TestStopParsing_RetriedToTheSamePath pins the retryAllowed declaration at the
// call site, which TestRetryPolicySweep cannot do (it checks a declaration
// exists, not that it matches the literal passed to doDELETE — issue #93). It
// is also the only DELETE retry pin in the suite; the behavioural
// _Retried/_NotRetried pairs in retry_policy_test.go cover PUT only.
//
// Two things must hold. A transient 500 has to be retried, because a cancel
// that silently gives up leaves a scraping job running for its full duration
// (measured at 256.9s). And every attempt has to land on the SAME path — a
// retry that fell through to the suffix-less sibling would answer success
// while cancelling nothing, which is the exact regression this file's sibling
// test exists to prevent.
//
// Retrying a cancel is safe: three consecutive DELETEs against an idle live
// account each answered {"success":true} and left is_parsing_in_progress
// false (measured 2026-07-30). The residual case the endpoint cannot protect
// against is a retry landing after a NEW job was started inside the backoff
// window; that is a caller-level race, not a property of this call.
func TestStopParsing_RetriedToTheSamePath(t *testing.T) {
	var calls atomic.Int64
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.StopParsing(context.Background()); err != nil {
		t.Fatalf("StopParsing: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("StopParsing issued %d requests, want 2 — a transient 500 on a cancel MUST be retried (setting doDELETE retryable=false drops this to 1)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, p := range paths {
		if p != "DELETE /posts-search/parsing/stop" {
			t.Errorf("attempt %d went to %q, want %q — a retry must not fall through to the sibling that answers success without cancelling", i+1, p, "DELETE /posts-search/parsing/stop")
		}
	}
}

func TestRewriteSearchPost(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "2001",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "original", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)
			w.Write([]byte(`{"id":6001}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
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
		t.Errorf("texts[0].text = %v, want 'Rewritten text' (payload.Texts overrides resolved text)", textMap["text"])
	}
}

func TestRewriteSearchPost_Scheduled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "2002",
				"publication_when_type": 2,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "original", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			w.Write([]byte(`{"id":6002}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
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

// TestImportSearchPost asserts the wire shape of a successful ImportSearchPost
// request: POST /posts with as_copy=1, the hardcoded
// publication_where_type=1, ids as the string form of the single search-post
// id. ImportSearchPost now resolves via GET /posts-search/{id}/edit first,
// then publishes via POST /posts. The resolved text from GET /edit is kept
// (import = keep original); payload.Attachments is ignored.
func TestImportSearchPost(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "2003",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "original", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedBody)
			w.Write([]byte(`{"id":7001}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
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
		t.Errorf("publication_where_type = %v, want 1 (hardcoded by PublishPost)", capturedBody["publication_where_type"])
	}
	if capturedBody["ids"] != "2003" {
		t.Errorf("ids = %v, want '2003' (string form of SearchPostID)", capturedBody["ids"])
	}
	// ImportSearchPost ignores payload.Attachments — attachments come from
	// the resolve step. The GET /edit response has empty attachments, so the
	// POST body has attachments: [].
	attachments, ok := capturedBody["attachments"].([]interface{})
	if !ok || len(attachments) != 0 {
		t.Fatalf("attachments = %v, want empty array (resolve step provides attachments; payload.Attachments is ignored)", capturedBody["attachments"])
	}
}

// TestImportSearchPost_ScheduleDrivenNoSchedules verifies the fail-closed
// guard: a schedule-driven import (publication_when_type=3) targeted at an
// EMPTY schedules list issues NO POST /posts and returns an error, rather than
// sending a POST with schedules_ids=[] — a schedule-driven import targeted at
// nothing. The resolve GET /edit happens first (before the guard in
// PublishPost), so the stub handles it; the guard prevents the POST.
func TestImportSearchPost_ScheduleDrivenNoSchedules(t *testing.T) {
	var postRequestMade bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "2003",
				"publication_when_type": 3,
				"publication_how_type": 2,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "x", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			postRequestMade = true
			w.Write([]byte(`{"id":7001}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
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
	if postRequestMade {
		t.Fatal("ImportSearchPost issued a POST /posts despite when_type=3 + empty schedules — the guard must fail before the publish")
	}
	if !strings.Contains(err.Error(), "schedule") {
		t.Errorf("error must explain the schedule requirement, got: %v", err)
	}
}

// TestRewriteSearchPost_ScheduleDrivenNoSchedules mirrors the guard for the
// rewrite endpoint: when_type=3 + empty schedules must fail closed. The
// resolve GET /edit happens first; the guard in PublishPost prevents the POST.
func TestRewriteSearchPost_ScheduleDrivenNoSchedules(t *testing.T) {
	var postRequestMade bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "2005",
				"publication_when_type": 3,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "x", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			postRequestMade = true
			w.Write([]byte(`{"id":7003}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
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
	if postRequestMade {
		t.Fatal("RewriteSearchPost issued a POST /posts despite when_type=3 + empty schedules")
	}
}

// TestRewriteSearchPost_BatchIDsOrder verifies the batch form: a slice of
// SearchPostIDs results in N separate POST /posts calls, each with a single
// id in the ids field, in the CALLER's order. The batch is now N independent
// resolve+publish pairs (client-side), NOT one server-side batch.
//
// The fixture is deliberately NON-MONOTONIC with a REPEAT ({2003, 2001, 2002,
// 2001}): an ascending-distinct fixture makes a sort and a dedupe both no-ops,
// so the test stays green even if the loop silently skips or reorders ids.
// This fixture discriminates: each POST must carry the exact id in caller order.
func TestRewriteSearchPost_BatchIDsOrder(t *testing.T) {
	var postCount int32
	var capturedIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "x",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "original", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			atomic.AddInt32(&postCount, 1)
			body, _ := io.ReadAll(r.Body)
			var b map[string]interface{}
			_ = json.Unmarshal(body, &b)
			capturedIDs = append(capturedIDs, b["ids"].(string))
			w.Write([]byte(`{"id":6003}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2003, 2001, 2002, 2001},
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{123456},
		// Texts is intentionally NOT set — a batch+Texts is a broadcast guard
		// (MAJOR 3): one text array overwrites all N posts' resolved text.
		// Each post keeps its own resolved text (per-post, the whole point of
		// the client-side loop).
	})
	if err != nil {
		t.Fatalf("RewriteSearchPost batch: %v", err)
	}
	// Batch is N separate POST /posts calls, each with a single id.
	if got := atomic.LoadInt32(&postCount); got != 4 {
		t.Fatalf("POST /posts count = %d, want 4 (one per id in the batch)", got)
	}
	wantIDs := []string{"2003", "2001", "2002", "2001"}
	if len(capturedIDs) != len(wantIDs) {
		t.Fatalf("captured ids count = %d, want %d", len(capturedIDs), len(wantIDs))
	}
	for i, want := range wantIDs {
		if capturedIDs[i] != want {
			t.Errorf("POST %d ids = %q, want %q (caller order + duplicates preserved)", i+1, capturedIDs[i], want)
		}
	}
	if resp.ID != 6003 {
		t.Errorf("ID = %d, want 6003 (first created post id)", resp.ID)
	}
}

// TestImportSearchPost_BatchIDsOrder mirrors the rewrite batch test for the
// import endpoint: a slice of SearchPostIDs results in N separate POST /posts
// calls, each with a single id in caller order. The fixture is non-monotonic
// with a repeat ({3003, 3001, 3002, 3001}).
func TestImportSearchPost_BatchIDsOrder(t *testing.T) {
	var postCount int32
	var capturedIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "x",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "original", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			atomic.AddInt32(&postCount, 1)
			body, _ := io.ReadAll(r.Body)
			var b map[string]interface{}
			_ = json.Unmarshal(body, &b)
			capturedIDs = append(capturedIDs, b["ids"].(string))
			w.Write([]byte(`{"id":7002}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
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
	if got := atomic.LoadInt32(&postCount); got != 4 {
		t.Fatalf("POST /posts count = %d, want 4 (one per id in the batch)", got)
	}
	wantIDs := []string{"3003", "3001", "3002", "3001"}
	if len(capturedIDs) != len(wantIDs) {
		t.Fatalf("captured ids count = %d, want %d", len(capturedIDs), len(wantIDs))
	}
	for i, want := range wantIDs {
		if capturedIDs[i] != want {
			t.Errorf("POST %d ids = %q, want %q (caller order + duplicates preserved)", i+1, capturedIDs[i], want)
		}
	}
	if resp.ID != 7002 {
		t.Errorf("ID = %d, want 7002 (first created post id)", resp.ID)
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
// with a multi-id batch must error before any POST. The resolve GET /edit
// happens first for the first id; the guard in PublishPost prevents the POST.
func TestRewriteSearchPost_BatchScheduleGuard(t *testing.T) {
	var postRequestMade bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "x",
				"publication_when_type": 3,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "x", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			postRequestMade = true
			w.Write([]byte(`{"id":6006}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
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
	if postRequestMade {
		t.Fatal("RewriteSearchPost issued a POST /posts despite batch when_type=3 + empty schedules — the guard must fail before the publish")
	}
}

// TestImportSearchPost_BatchScheduleGuard mirrors the batch schedule guard
// for the import endpoint.
func TestImportSearchPost_BatchScheduleGuard(t *testing.T) {
	var postRequestMade bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "x",
				"publication_when_type": 3,
				"publication_how_type": 2,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "x", "source_id": 0}],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			postRequestMade = true
			w.Write([]byte(`{"id":7005}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
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
	if postRequestMade {
		t.Fatal("ImportSearchPost issued a POST /posts despite batch when_type=3 + empty schedules — the guard must fail before the publish")
	}
}

// searchPostRows builds a JSON page body for /posts-search with `count` rows
// of sequential ids starting at `start`, the given total_rows, is_has_more,
// and rows_limit=20. Only the `id` field is populated — the cap-detection
// walker under test only needs ids to prove the collected rows are returned
// (not discarded); the rest of SearchPost is irrelevant to the result-window
// logic.
func searchPostRows(start, count, total int, hasMore bool) string {
	type sp struct {
		ID int `json:"id"`
	}
	list := make([]sp, 0, count)
	for i := 0; i < count; i++ {
		list = append(list, sp{ID: start + i})
	}
	b, _ := json.Marshal(struct {
		List      []sp `json:"list"`
		TotalRows int  `json:"total_rows"`
		IsHasMore bool `json:"is_has_more"`
		RowsLimit int  `json:"rows_limit"`
	}{list, total, hasMore, 20})
	return string(b)
}

// resultWindowErrorBody is the Elasticsearch max_result_window rejection,
// shaped exactly as the live Hooppy API returns it: HTTP 500 with the reason
// nested under an "error" object (so newAPIError's string-extraction falls
// through to the raw body, where "Result window is too large" lives). This
// is the shape isResultWindowError must recognise via the Body fallback.
const resultWindowErrorBody = `{"error":{"type":"illegal_argument_exception","reason":"Result window is too large, from + size must be less than or equal to: [10000] but was [10060]"},"status":500}`

// TestListAllSearchPosts_ResultWindowCap_ReturnsCollectedRows is the
// RED-then-GREEN guard for the result-window cap. The server serves 3 pages
// (6 rows) with is_has_more=true and total_rows=10000 (the ES ceiling), then
// returns the max_result_window 500 on page 4. The walker MUST stop at the
// reachable window and return the 6 rows it already collected, marked Capped
// — NOT discard them to the error (the prior behaviour, which lost 10000
// successfully-fetched rows on the live account).
//
// RED-on-revert: revert the isResultWindowError branch in the walker and it
// returns (nil, err) — res == nil / len(res.List) == 0 fails, and the
// "collected rows are returned, not discarded" assertion (len == 6) fails.
// A test asserting only "no error" would accept a walker that returns nothing;
// this one asserts the 6 rows survive.
func TestListAllSearchPosts_ResultWindowCap_ReturnsCollectedRows(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		pg := r.URL.Query().Get("page")
		switch pg {
		case "1", "2", "3":
			w.Header().Set("Content-Type", "application/json")
			start, _ := strconv.Atoi(pg)
			w.Write([]byte(searchPostRows((start-1)*2+1, 2, 10000, true)))
		default: // page 4 — the server wall
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(resultWindowErrorBody))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.ListAllSearchPostsWithFirstAndLastTotal(context.Background(), SearchPostsFilter{})
	if err != nil {
		t.Fatalf("ListAllSearchPostsWithFirstAndLastTotal: unexpected error %v — a result-window cap after collecting rows MUST return the partial result, not discard it", err)
	}
	if res == nil {
		t.Fatal("res == nil — the walker returned no result on a capped walk; the 6 collected rows were discarded (the defect this test exists to catch)")
	}
	if !res.Capped {
		t.Errorf("res.Capped = false, want true — a walk that stopped at the result-window wall MUST be marked capped, not presented as complete")
	}
	if len(res.List) != 6 {
		t.Fatalf("len(res.List) = %d, want 6 — the 6 rows collected before the wall MUST be returned, not discarded (pages fetched=%v)", len(res.List), pages)
	}
	// The collected rows must be the actual ids 1..6, in order — proving the
	// walker returned what it collected, not an empty or synthetic slice.
	for i, p := range res.List {
		if p.ID != i+1 {
			t.Errorf("res.List[%d].ID = %d, want %d — collected rows must be the rows actually served, in order", i, p.ID, i+1)
		}
	}
	if res.FirstTotalRows != 10000 {
		t.Errorf("FirstTotalRows = %d, want 10000 (the ceiling, reported on page 1)", res.FirstTotalRows)
	}
	if res.LastTotalRows != 10000 {
		t.Errorf("LastTotalRows = %d, want 10000 (the ceiling does not decrease)", res.LastTotalRows)
	}
	// The walker MUST have attempted the page that hits the wall — otherwise
	// the cap is undetectable (is_has_more never clears at the ceiling). 4
	// requests = pages 1,2,3 (data) + page 4 (the 500 that signals the cap).
	if len(pages) != 4 {
		t.Fatalf("handler received %d requests, want 4 (pages 1-3 data + page 4 the wall); pages=%v — the walker must attempt the page that crosses the window to detect the cap", len(pages), pages)
	}
	if pages[3] != "4" {
		t.Errorf("4th request page = %q, want \"4\" (the page that hits the result-window wall)", pages[3])
	}
}

// TestListAllSearchPosts_MidWalkGeneric500_FailsLoud is the companion guard:
// a 500 that is NOT the result-window error MUST still fail loud. The walker
// must not turn every server error into a partial success — that would mask
// real failures (a genuine backend crash, an exhausted 429 surfaced as 500,
// a proxy fault). Page 1 serves 2 rows with is_has_more=true; page 2 returns
// a generic 500 (no "Result window is too large" phrase). The walk MUST
// return an error and NO result.
//
// RED-on-revert: if the cap detection is broadened to treat any 500 as a cap
// (or any error after len(all)>0), err == nil / res != nil fails.
func TestListAllSearchPosts_MidWalkGeneric500_FailsLoud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(searchPostRows(1, 2, 10000, true)))
		default: // page 2 — a GENERIC 500, not the result-window wall
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"internal server error","status":500}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.ListAllSearchPostsWithFirstAndLastTotal(context.Background(), SearchPostsFilter{})
	if err == nil {
		t.Fatalf("expected a loud error for a generic mid-walk 500, got nil — the walker must NOT turn a non-result-window server error into a partial success (res=%+v)", res)
	}
	if res != nil {
		t.Errorf("res = %+v, want nil — a genuine mid-walk failure must return NO result (no partial list), only the error", res)
	}
	if strings.Contains(err.Error(), "CAPPED") || strings.Contains(err.Error(), "reachable window") {
		t.Errorf("error %q mentions a cap, but a generic 500 is not the result-window wall — it must not be mislabelled as capped", err.Error())
	}
}

// =========================================================================
// F1-F5: falsification tests for the resolve+publish collapse (spec §Falsification)
// =========================================================================

// F1 — a video attachment survives resolve→publish. Break the photos-fold → RED.
//
// The edit endpoint returns a video as {type:"video", data:{...}} (singular,
// object data). The POST /posts write endpoint has NO "videos" case in its
// getFormData switch — videos ride inside the {type:"photos"} group with
// array data. Today the client drops videos silently (spec fact 4). This test
// asserts the video survives the resolve→publish path: ResolveSearchPost maps
// it into the photos group, and PublishPost sends it in that group on the wire.
//
// RED-on-revert: drop the "video" case from mapEditAttachmentsToWriteShape
// (or put videos in a separate group) → the POST body has no video data →
// this test fails on the attachments assertion.
func TestF1_VideoSurvivesResolvePublish(t *testing.T) {
	var postBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "7077730",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "video post", "source_id": 0}],
				"attachments": [
					{"type": "video", "data": {"id": 456254173, "owner_id": -26270763, "post_id": "1435865", "preview": "https://example.com/v.jpg", "title": "test video", "description": "", "duration": 12, "access_key": "787c8c3a56b60ef3c8", "type": "video", "source_id": 1}}
				]
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &postBody)
			w.Write([]byte(`{"id":93058170}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	content, err := c.ResolveSearchPost(context.Background(), 7077730)
	if err != nil {
		t.Fatalf("ResolveSearchPost: %v", err)
	}
	// The video must be in a {type: "photos"} group with array data.
	if len(content.Attachments) != 1 {
		t.Fatalf("content.Attachments = %d entries, want 1 (the photos group); got %+v", len(content.Attachments), content.Attachments)
	}
	if content.Attachments[0].Type != "photos" {
		t.Fatalf("attachment type = %q, want \"photos\" (videos ride inside the photos group — spec fact 4)", content.Attachments[0].Type)
	}
	photosData, ok := content.Attachments[0].Data.([]interface{})
	if !ok || len(photosData) != 1 {
		t.Fatalf("photos group data = %v, want array of 1 (the video item)", content.Attachments[0].Data)
	}

	_, err = c.PublishPost(context.Background(), content, PublishTarget{
		PublicationWhenType: 1, PublicationHowType: 1,
		SelectedPagesIDs: []int{123456},
	})
	if err != nil {
		t.Fatalf("PublishPost: %v", err)
	}
	// Assert the POST body carries the video inside the photos group.
	atts, ok := postBody["attachments"].([]interface{})
	if !ok || len(atts) != 1 {
		t.Fatalf("POST body attachments = %v, want array of 1 (the photos group)", postBody["attachments"])
	}
	attMap := atts[0].(map[string]interface{})
	if attMap["type"] != "photos" {
		t.Fatalf("POST body attachment type = %v, want \"photos\"", attMap["type"])
	}
	attData, ok := attMap["data"].([]interface{})
	if !ok || len(attData) != 1 {
		t.Fatalf("POST body photos data = %v, want array of 1 (the video)", attMap["data"])
	}
	videoItem, ok := attData[0].(map[string]interface{})
	if !ok {
		t.Fatalf("POST body photos[0] = %v, want the video object", attData[0])
	}
	if videoItem["type"] != "video" {
		t.Errorf("POST body photos[0].type = %v, want \"video\" (the item's own type field identifies it as a video inside the photos group)", videoItem["type"])
	}
	if videoItem["id"] != float64(456254173) {
		t.Errorf("POST body photos[0].id = %v, want 456254173 (the video's id must survive the round trip)", videoItem["id"])
	}
}

// F2 — an unknown attachment kind returns an error AND creates no post.
// Assert the request count is zero, not merely the exit code.
//
// The vendor's getFormData switch knows a fixed set of kinds (photos, audios,
// documents, link, ad, poll, repost, source, comment, title,
// telegram_buttons, location, settings). An attachment kind NOT in that set
// must be an error at resolve time — fact 4 (silently dropped videos) is the
// whole reason this mapping exists; a mapping that fails open reproduces it.
//
// RED-on-revert: remove the unknown-kind error from mapEditAttachmentsToWriteShape
// → ResolveSearchPost succeeds → PublishPost is called → POST count > 0.
func TestF2_UnknownAttachmentKindErrorsAndCreatesNoPost(t *testing.T) {
	var postCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "9999",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "has mystery attachment", "source_id": 0}],
				"attachments": [
					{"type": "mystery_kind", "data": {"foo": "bar"}}
				]
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			atomic.AddInt32(&postCount, 1)
			w.Write([]byte(`{"id":12345}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ResolveSearchPost(context.Background(), 9999)
	if err == nil {
		t.Fatal("ResolveSearchPost with unknown attachment kind: expected an error, got nil — a mapping that fails open reproduces the silent video-drop (spec fact 4)")
	}
	if !strings.Contains(err.Error(), "mystery_kind") {
		t.Errorf("error %q does not name the unknown kind \"mystery_kind\" — the operator must be told WHAT was rejected", err.Error())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("POST /posts count = %d, want 0 — an unknown attachment kind must error at RESOLVE time, before any publish request is issued (asserting the request count, not merely the exit code — spec F2)", got)
	}
}

// F4 — the resolve call carries as_copy=1. Omitting it is the historical bug
// that yields empty posts, so assert the query parameter itself.
//
// as_copy=1 is a GET-side resolver (spec fact 1): the server returns the
// resolved content only when this parameter is present. Without it, the edit
// endpoint returns an empty form (no texts, no attachments) — the historical
// bug that created empty posts. This test asserts the query parameter is on
// the wire.
//
// RED-on-revert: remove the params.Set("as_copy", "1") from GetSearchPostEdit
// → the query param assertion fails.
func TestF4_ResolveCarriesAsCopy1(t *testing.T) {
	var gotAsCopy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit") {
			gotAsCopy = r.URL.Query().Get("as_copy")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "6453701",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "x", "source_id": 0}],
				"attachments": []
			}`))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ResolveSearchPost(context.Background(), 6453701)
	if err != nil {
		t.Fatalf("ResolveSearchPost: %v", err)
	}
	if gotAsCopy != "1" {
		t.Fatalf("as_copy query param = %q, want \"1\" — omitting it is the historical bug that yields empty posts (spec fact 1, F4)", gotAsCopy)
	}
}

// F5 — a create whose resolved content is empty is refused. Today the client
// can create an empty post and return 0; that must become impossible.
//
// The server accepts a POST /posts with empty texts and empty attachments and
// creates a post with no content. This is the defect behind the empty-posts
// ticket. PublishPost must refuse before any request.
//
// RED-on-revert: remove the empty-content guard from PublishPost → the POST
// is sent → postCount > 0 → this test fails.
func TestF5_EmptyResolvedContentRefused(t *testing.T) {
	var postCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "8888",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [],
				"attachments": []
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			atomic.AddInt32(&postCount, 1)
			w.Write([]byte(`{"id":12345}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	content, err := c.ResolveSearchPost(context.Background(), 8888)
	if err != nil {
		t.Fatalf("ResolveSearchPost: %v (the resolve itself should succeed — the empty content is valid resolved data; the PUBLISH must refuse it)", err)
	}
	if len(content.Texts) != 0 || len(content.Attachments) != 0 {
		t.Fatalf("resolved content is not empty: texts=%d attachments=%d — the test fixture must produce empty content to exercise the guard", len(content.Texts), len(content.Attachments))
	}
	_, err = c.PublishPost(context.Background(), content, PublishTarget{
		PublicationWhenType: 1, PublicationHowType: 1,
		SelectedPagesIDs: []int{123456},
	})
	if err == nil {
		t.Fatal("PublishPost with empty resolved content: expected an error, got nil — the server would accept it and create an empty post (spec F5)")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q does not name the empty-content cause — the operator must be told WHY the publish was refused", err.Error())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("POST /posts count = %d, want 0 — an empty-content publish must be refused BEFORE any request (spec F5)", got)
	}
}

// =========================================================================
// F11-F14: falsification tests for the review followups (BLOCKER 1 + MAJOR 3)
// =========================================================================

// F11 — a batch where post 2 of 3 fails returns a result whose IDs contain
// post 1, AND a non-nil error. Assert the id is present in the return value,
// not just that an error came back — a test that only checks the error
// reproduces the blocker (the result was nil, the id was lost).
//
// RED-on-revert: revert resolvePublishBatch to the early-return shape
// (`return nil, fmt.Errorf(...)` on a per-post failure) → resp is nil → the
// IDs assertion fails (nil pointer or empty), even though err is non-nil.
func TestF11_BatchPartialFailureReturnsPopulatedResult(t *testing.T) {
	var postCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"x","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			n := atomic.AddInt32(&postCount, 1)
			if n == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message":"boom"}`))
				return
			}
			w.Write([]byte(fmt.Sprintf(`{"id":9000%d}`, n))) // post 1 → 90001, post 3 → 90003
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{11, 22, 33},
		PublicationWhenType: 1, PublicationHowType: 1,
		SelectedPagesIDs: []int{1},
	})
	// err MUST be non-nil — a post failed.
	if err == nil {
		t.Fatal("ImportSearchPost batch with post 2 failing: expected a non-nil error, got nil — a per-post failure must surface as an error (PartialPostError)")
	}
	// resp MUST be non-nil — the blocker is that it was nil, discarding post 1.
	if resp == nil {
		t.Fatal("resp is nil — a batch partial failure MUST return a populated result, not discard every post already created (BLOCKER 1: post 90001 is live and unrecoverable from a nil return)")
	}
	// The critical assertion: post 1's id (90001) MUST be in resp.IDs.
	// A test that only checks err != nil reproduces the blocker.
	found := false
	for _, id := range resp.IDs {
		if id == 90001 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("resp.IDs = %v, want 90001 present — post 1 was created (POST count=%d) and its id MUST be in the returned result, not discarded (BLOCKER 1)", resp.IDs, atomic.LoadInt32(&postCount))
	}
	// The error must be a *PartialPostError carrying the failed post.
	var ppe *PartialPostError
	if !errors.As(err, &ppe) {
		t.Fatalf("error is not *PartialPostError (got %T): %v — a batch partial failure must return the typed error so callers can distinguish partial from total failure", err, err)
	}
	if len(ppe.Failed) != 1 {
		t.Fatalf("ppe.Failed = %d entries, want 1 (post 2 failed)", len(ppe.Failed))
	}
	if ppe.Failed[0].SearchPostID != 22 {
		t.Errorf("ppe.Failed[0].SearchPostID = %d, want 22 (the failed post)", ppe.Failed[0].SearchPostID)
	}
}

// F12 — a direct library call with SearchPostIDs set and Texts non-empty
// errors before any request (assert request count zero). The guard lives in
// PublishPost (the single choke point), so a direct RewriteSearchPost caller
// passing both is refused before any POST /posts.
//
// RED-on-revert: remove the batch+Texts guard from PublishPost → the POST is
// sent → postCount > 0 → this test fails.
func TestF12_BatchWithTextsErrorsBeforeAnyRequest(t *testing.T) {
	var postCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			// The resolve step runs before the guard in PublishPost — but the
			// guard fires on the FIRST PublishPost call, so the edit fetch for
			// post 1 MAY happen. The assertion is on POST count (the publish),
			// not GET count (the resolve). However, the guard should fire
			// BEFORE any resolve too — the batch+Texts broadcast is a payload
			// invariant, checkable before any request. Assert GET count is
			// also zero to prove the guard is pre-request.
			atomic.AddInt32(&postCount, 1) // reuse counter for ANY request
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"x","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			atomic.AddInt32(&postCount, 1)
			w.Write([]byte(`{"id":999}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{11, 22, 33},
		Texts:               []PostText{{Text: "broadcast text", SourceID: 0}},
		PublicationWhenType: 1, PublicationHowType: 1,
		SelectedPagesIDs: []int{1},
	})
	if err == nil {
		t.Fatal("RewriteSearchPost with SearchPostIDs + Texts: expected an error (batch+Texts is a broadcast that overwrites all N posts with one text), got nil")
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("request count = %d, want 0 — a batch+Texts broadcast must error BEFORE any request (the guard is a payload invariant, checkable pre-request); the broadcast would silently overwrite all N posts' text with no error (MAJOR 3)", got)
	}
}

// F13 — the restored CopySearchPost shim produces a post with text AND
// attachments, i.e. it no longer creates the empty post that made it broken.
// The shim delegates to ResolveSearchPost + PublishPost, so it carries the
// resolved text and attachments into the write — the exact thing the old
// PUT /posts/copy path failed to do (it created empty posts).
//
// RED-on-revert: revert CopySearchPost to the old PUT /posts/copy shape (or
// make it a no-op that returns an empty PostIDResponse) → the POST body has
// no texts/attachments → this test fails.
func TestF13_CopySearchPostShimProducesTextAndAttachments(t *testing.T) {
	var postBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "7077730",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "copied post text", "source_id": 0}],
				"attachments": [
					{"type": "photo", "data": {"id": 123, "url": "https://example.com/p.jpg"}}
				]
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &postBody)
			w.Write([]byte(`{"id":88001}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        7077730,
		PublicationWhenType: 1, PublicationHowType: 1,
		SelectedPagesIDs: []int{1},
	})
	if err != nil {
		t.Fatalf("CopySearchPost shim: %v", err)
	}
	if resp.ID != 88001 {
		t.Errorf("resp.ID = %d, want 88001", resp.ID)
	}
	// Assert the POST body carries the resolved text (not empty).
	texts, ok := postBody["texts"].([]interface{})
	if !ok || len(texts) != 1 {
		t.Fatalf("POST body texts = %v, want array of 1 — the shim must carry the resolved text (the old PUT /posts/copy created empty posts)", postBody["texts"])
	}
	textMap, ok := texts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("POST body texts[0] = %v, want a map", texts[0])
	}
	if textMap["text"] != "copied post text" {
		t.Errorf("POST body texts[0].text = %v, want \"copied post text\" (the resolved text must reach the wire)", textMap["text"])
	}
	// Assert the POST body carries the resolved attachments (not empty).
	atts, ok := postBody["attachments"].([]interface{})
	if !ok || len(atts) != 1 {
		t.Fatalf("POST body attachments = %v, want array of 1 (the photos group) — the shim must carry the resolved attachments (the old PUT /posts/copy created empty posts)", postBody["attachments"])
	}
	attMap, ok := atts[0].(map[string]interface{})
	if !ok || attMap["type"] != "photos" {
		t.Errorf("POST body attachments[0] = %v, want {type:\"photos\"} (the photo must be mapped into the photos group)", atts[0])
	}
}

// F14 — an unrecognised read-side attachment kind still fails closed after
// the vocabulary fix. The fix changes the allowlist to speak the read
// vocabulary (singular: photo, video, audio, document, ...) but must NOT
// turn fail-open — an unknown kind must still error.
//
// RED-on-revert: if the vocabulary fix accidentally removes the fail-closed
// guard (e.g. by making the map empty or the check a no-op), ResolveSearchPost
// succeeds → no error → this test fails.
func TestF14_UnknownAttachmentKindStillFailsClosedAfterVocabFix(t *testing.T) {
	var postCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "9999",
				"publication_when_type": 1,
				"publication_how_type": 1,
				"publication_where_type": 1,
				"created_by": 7,
				"texts": [{"text": "has mystery attachment", "source_id": 0}],
				"attachments": [
					{"type": "mystery_kind", "data": {"foo": "bar"}}
				]
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			atomic.AddInt32(&postCount, 1)
			w.Write([]byte(`{"id":12345}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ResolveSearchPost(context.Background(), 9999)
	if err == nil {
		t.Fatal("ResolveSearchPost with unknown attachment kind: expected an error, got nil — the vocabulary fix must NOT turn the fail-closed guard fail-open (an unknown kind must still error)")
	}
	if !strings.Contains(err.Error(), "mystery_kind") {
		t.Errorf("error %q does not name the unknown kind \"mystery_kind\" — the operator must be told WHAT was rejected", err.Error())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("POST /posts count = %d, want 0 — an unknown attachment kind must error at RESOLVE time, before any publish request (the vocabulary fix must preserve fail-closed)", got)
	}
}
