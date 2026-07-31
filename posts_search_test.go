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
		w.Write([]byte(`{"success":true}`))
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
		w.Write([]byte(`{"success":true}`))
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

// TestStopParsingAndConfirm_Oracle pins the OBSERVED-state contract of
// StopParsingAndConfirm (issue #114): the result reflects
// GetParsingForm.is_parsing_in_progress, NOT the DELETE's own success body.
// Three arms: the parse is still running (Stopped=false, no ConfirmErr), the
// parse is idle (Stopped=true), and the DELETE succeeded but the oracle
// re-read failed (ConfirmErr set, Stopped=false — never claim success). A
// fourth arm covers a 2xx {"success":false} on the DELETE caught by the
// universal gate (the same family as PR #134): the method returns an error,
// not a result with Stopped=true.
func TestStopParsingAndConfirm_Oracle(t *testing.T) {
	t.Run("still running → not stopped", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				w.Write([]byte(`{"success":true}`))
				return
			}
			w.Write([]byte(`{"is_parsing_in_progress":true,"source_resources":[],"social_accounts":[]}`))
		}))
		defer srv.Close()
		res, err := newTestClient(t, srv).StopParsingAndConfirm(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v — a 2xx success:true DELETE must not error", err)
		}
		if res.IsParsingInProgress != true {
			t.Errorf("IsParsingInProgress = %v, want true (the oracle is the source of truth, not the DELETE body)", res.IsParsingInProgress)
		}
		if res.Stopped() {
			t.Errorf("Stopped() = true, want false — the parse is still running; claiming stopped is the #114 defect")
		}
		if res.ConfirmErr != "" {
			t.Errorf("ConfirmErr = %q, want empty — the oracle read succeeded", res.ConfirmErr)
		}
	})
	t.Run("idle → stopped", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				w.Write([]byte(`{"success":true}`))
				return
			}
			w.Write([]byte(`{"is_parsing_in_progress":false,"source_resources":[],"social_accounts":[]}`))
		}))
		defer srv.Close()
		res, err := newTestClient(t, srv).StopParsingAndConfirm(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Stopped() {
			t.Errorf("Stopped() = false, want true — the oracle observed idle")
		}
	})
	t.Run("DELETE success:false → gate error, not a result", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"success":false,"message":"no active parse"}`))
		}))
		defer srv.Close()
		res, err := newTestClient(t, srv).StopParsingAndConfirm(context.Background())
		if err == nil {
			t.Fatalf("expected a *SuccessFalseError for a 2xx {\"success\":false} — the universal gate must fire now that StopParsing reads the body (PR #134 family)")
		}
		if res != nil {
			t.Errorf("res = %+v, want nil — a DELETE failure must not return a result the caller could read as stopped", res)
		}
	})
	t.Run("oracle re-read fails → ConfirmErr, not stopped", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				w.Write([]byte(`{"success":true}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		res, err := newTestClient(t, srv).StopParsingAndConfirm(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v — the DELETE succeeded; only the confirm read failed, so the method returns a result with ConfirmErr, not an error", err)
		}
		if res.ConfirmErr == "" {
			t.Errorf("ConfirmErr empty, want the confirm-read failure — the DELETE succeeded but the oracle could not be read")
		}
		if res.Stopped() {
			t.Errorf("Stopped() = true, want false — never claim stopped when the confirm read failed (issue #114)")
		}
	})
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
	if !strings.Contains(err.Error(), "schedule") {
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
	if !strings.Contains(err.Error(), "RewriteSearchPost") || !strings.Contains(err.Error(), "ImportSearchPost") {
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

// TestCopySearchPost_SchedulesWithoutWhenType3 is the CONVERSE of the
// ScheduleDrivenNoSchedules guard: schedules_ids IS set but when_type is NOT 3.
// The existing guard only refuses when_type=3 + empty schedules; without the
// converse, a library consumer calling CopySearchPost directly with
// SchedulesIDs + when_type=1 sends the schedules onto the wire under a
// publish-now intent — the exact mechanism the CLI now guards against, one
// layer down. This is a public Go module; the CLI guard does not protect an
// external consumer.
//
// RED-on-revert: remove the converse guard from CopySearchPost and the stub is
// reached (requestMade=true) with err == nil → both assertions fail.
func TestCopySearchPost_SchedulesWithoutWhenType3(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7010}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2006,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SchedulesIDs:        []int{10, 11},
		SelectedPagesIDs:    []int{123456},
	})
	if err == nil {
		t.Fatal("expected fail-closed error for schedules_ids with when_type!=3, got nil — a public library consumer would publish under a contradictory intent")
	}
	if requestMade {
		t.Fatal("CopySearchPost issued a request despite schedules_ids + when_type!=3 — must fail before any request (the schedules would marshal onto the wire under a publish-now intent)")
	}
	if !strings.Contains(err.Error(), "schedules_ids") || !strings.Contains(err.Error(), "publication_when_type") {
		t.Errorf("error must name schedules_ids and publication_when_type, got: %v", err)
	}
}

// TestRewriteSearchPost_SchedulesWithoutWhenType3 mirrors the copy converse
// guard for the rewrite endpoint. RewriteSearchPost marshals the payload
// wholesale onto POST /posts, so SchedulesIDs + when_type!=3 reaches the wire.
//
// RED-on-revert: remove the converse guard from RewriteSearchPost and the stub
// is reached (requestMade=true) with err == nil → both assertions fail.
func TestRewriteSearchPost_SchedulesWithoutWhenType3(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7011}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2007,
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SchedulesIDs:        []int{10, 11},
		SelectedPagesIDs:    []int{123456},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected fail-closed error for schedules_ids with when_type!=3, got nil")
	}
	if requestMade {
		t.Fatal("RewriteSearchPost issued a request despite schedules_ids + when_type!=3 — must fail before any request")
	}
	if !strings.Contains(err.Error(), "schedules_ids") || !strings.Contains(err.Error(), "publication_when_type") {
		t.Errorf("error must name schedules_ids and publication_when_type, got: %v", err)
	}
}

// F10 — ImportSearchPost called DIRECTLY (not via the CLI) with SchedulesIDs
// set and when_type=1 must error BEFORE any request. The assertion is on the
// REQUEST COUNT (zero), not merely that an error came back — a guard that
// errors after the request would still "return an error" while having
// published. Import is the worst of the three: it assigns SchedulesIDs in the
// payload literal with no switch, so the schedules reach the wire unconditionally
// under whatever when_type the caller set.
//
// RED-on-revert: remove the converse guard from ImportSearchPost and the stub
// is reached (requestMade=true) with err == nil → both assertions fail. This
// exact mutation is green today (the converse guard does not exist yet).
func TestImportSearchPost_SchedulesWithoutWhenType3_F10(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.Write([]byte(`{"id":7012}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        2008,
		PublicationWhenType: 1,
		PublicationHowType:  2,
		SchedulesIDs:        []int{10, 11},
		Texts:               []PostText{{Text: "x", SourceID: 0}},
	})
	if err == nil {
		t.Fatal("expected fail-closed error for schedules_ids with when_type=1, got nil — ImportSearchPost marshals schedules onto the wire under a publish-now intent")
	}
	if requestMade {
		t.Fatal("ImportSearchPost issued a request despite schedules_ids + when_type=1 — must fail BEFORE any request (F10: assert request count is zero, not merely that an error came back)")
	}
	if !strings.Contains(err.Error(), "schedules_ids") || !strings.Contains(err.Error(), "publication_when_type") {
		t.Errorf("error must name schedules_ids and publication_when_type, got: %v", err)
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
