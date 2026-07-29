package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

// TestPhantomFilterSweep is the single gate that prevents a fourth issue
// in the phantom-filter series (#63, #67, #73). It enumerates EVERY field
// of all four list-filter structs — SearchPostsFilter, ListPostsFilter,
// ListAccountsFilter, and ListPagesFilter — via reflection and checks each
// against a table that marks it `works` or `phantom`:
//
//   - `phantom` fields produce an error and issue NO request (the API
//     accepts and silently ignores the parameter, returning an unfiltered
//     result set that looks filtered — see the per-endpoint doc comments
//     in posts_search.go and posts.go).
//   - `works` fields reach the query string unchanged.
//
// A field NOT listed in the table is a test FAILURE, not a silent omission
// — adding a new filter field without registering it here makes this test
// RED. That is the property that closes this issue class: the next field
// added to any of the four filter structs MUST be classified here or the
// sweep is incomplete by construction.
//
// # What the gate guarantees — and what it does not
//
// The gate forces a CLASSIFICATION, not a VERIFICATION. A new field can be
// marked `works` with a trivially-passing wire assertion (the value reaches
// the query string) and still be phantom in production — the server
// accepts and drops it. What the gate guarantees is that no new field
// ships UNCLASSIFIED. The classification itself must be earned by a
// differential measurement: run the endpoint WITH the filter, WITHOUT it,
// and with a DIFFERENT valid value, then judge by RETURNED ROW CONTENT
// (not total_rows — see method note 1). A field is `works` only if the
// three runs return three distinguishable result sets; otherwise it is
// `phantom`.
//
// # Per-endpoint measurement evidence
//
// /posts-search (SearchPostsFilter) — measured by row content:
//   - source_id=7 (Instagram) returns rows whose own source_id is 1 → phantom.
//   - source_resource_id=2228 (Instagram-only) returns rows with
//     source_id: [1] → phantom.
//   - owner_id=<real> returns four different owners → phantom.
//   - source_type=2 (RSS) returns 0 rows vs source_type=1 returns rows → works.
//   - text, sort_by, sort_direction, photos_amount, video_duration,
//     content_types, content_types_exclude, page: all reach the wire → works.
//
// /posts (ListPostsFilter) — measured by row content:
//   - account_id=<impossible> returns the full collection → phantom.
//   - page_id=<impossible> returns the full collection → phantom.
//   - source_id, schedule_id, project_id, page, is_published,
//     publication_date: all reach the wire → works.
//
// /accounts (ListAccountsFilter) — measured by row content:
//   - source_id=1 → rows all carry source_id 1; source_id=7 → rows all
//     carry source_id 7 → works.
//   - page → works (pagination).
//
// /accounts/pages (ListPagesFilter) — measured by row content:
//   - account_id=<a> → rows all carry that account_id → works.
//   - source_id=3 → rows all carry source_id 3 → works.
//   - page → works (pagination).
//
// Note the cross-endpoint pair worth stating: account_id is a WORKING
// filter on /accounts/pages and a PHANTOM on /posts — two endpoints, one
// name, opposite behaviour. This is the second such pair in the repo after
// source_id (works on /posts, phantom on /posts-search). The fix is
// per-endpoint, never per-name.
//
// DateFrom/DateTo on /posts-search were classified `works` on no evidence;
// the classification is now confirmed by differential measurement:
//   - no filter:        oldest of 20 rows → 18.06.2024
//   - date_from=20.07.2026: oldest → 19.07.2026 10:00
//   - date_to=10.07.2026:   newest → 10.07.2026 09:50
//
// Both genuinely filter. The window is offset — asking from the 20th
// yields rows from the 19th at 10:00 — which is the separate defect
// tracked in #62 (an off-by-one in the date window), NOT a phantom.
//
// # Method notes — read before re-probing (both cost a wrong answer)
//
//  1. total_rows CAPS AT 10000. A filter over a large collection looks
//     phantom because both the filtered and unfiltered sides read the cap.
//     Judge by RETURNED ROW CONTENT, not total_rows.
//
//  2. An impossible enum value is NOT a probe. source_type=9 returns
//     everything because the server ignores an unrecognised enum rather
//     than matching nothing — indistinguishable from a phantom. Use a
//     different VALID value: source_type=2 returns 0 rows and proves the
//     filter works.
//
// These two notes are why this issue took three rounds to characterise.
func TestPhantomFilterSweep(t *testing.T) {
	t.Run("SearchPostsFilter", func(t *testing.T) {
		specs := map[string]fieldSpec{
			"Text":                {wireParam: "text", expectWire: "probe", setVal: "probe"},
			"DateFrom":            {wireParam: "date_from", expectWire: "01.01.2026", setVal: "01.01.2026"},
			"DateTo":              {wireParam: "date_to", expectWire: "31.01.2026", setVal: "31.01.2026"},
			"SourceType":          {wireParam: "source_type", expectWire: "1", setVal: 1},
			"SourceID":            {phantom: true, wireParam: "source_id", setVal: 1},
			"SourceResourceID":    {phantom: true, wireParam: "source_resource_id", setVal: 1},
			"OwnerID":             {phantom: true, wireParam: "owner_id", setVal: 1},
			"Page":                {wireParam: "page", expectWire: "2", setVal: 2},
			"SortBy":              {wireParam: "sort_by", expectWire: "likes", setVal: "likes"},
			"SortDirection":       {wireParam: "sort_direction", expectWire: "asc", setVal: "asc"},
			"MinLikes":            {phantom: true, wireParam: "min_likes", setVal: 1},
			"MinViews":            {phantom: true, wireParam: "min_views", setVal: 1},
			"MinComments":         {phantom: true, wireParam: "min_comments", setVal: 1},
			"MinReposts":          {phantom: true, wireParam: "min_reposts", setVal: 1},
			"MinInvolvement":      {phantom: true, wireParam: "min_involvement", setVal: 1.5},
			"PhotosAmount":        {wireParam: "photos_amount", expectWire: "3", setVal: 3},
			"VideoDuration":       {wireParam: "video_duration", expectWire: "2", setVal: 2},
			"ContentTypes":        {wireParam: "content_types", expectWire: "photos", setVal: "photos"},
			"ContentTypesExclude": {wireParam: "content_types_exclude", expectWire: "videos", setVal: "videos"},
		}
		assertFilterSweep(t, specs, reflect.TypeOf(SearchPostsFilter{}), func(f interface{}) (url.Values, bool, error) {
			reached := false
			var q url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				q = r.URL.Query()
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			_, err := c.ListSearchPosts(context.Background(), f.(SearchPostsFilter))
			return q, reached, err
		})
	})

	t.Run("ListPostsFilter", func(t *testing.T) {
		pub := true
		specs := map[string]fieldSpec{
			"IsPublished":     {wireParam: "is_published", expectWire: "1", setVal: &pub},
			"PublicationDate": {wireParam: "publication_date", expectWire: "15.06.2026", setVal: "15.06.2026"},
			"SourceID":        {wireParam: "source_id", expectWire: "6", setVal: 6},
			"AccountID":       {phantom: true, wireParam: "account_id", setVal: 1},
			"PageID":          {phantom: true, wireParam: "page_id", setVal: 1},
			"ScheduleID":      {wireParam: "schedule_id", expectWire: "300", setVal: 300},
			"ProjectID":       {wireParam: "project_id", expectWire: "400", setVal: 400},
			"Page":            {wireParam: "page", expectWire: "2", setVal: 2},
		}
		assertFilterSweep(t, specs, reflect.TypeOf(ListPostsFilter{}), func(f interface{}) (url.Values, bool, error) {
			reached := false
			var q url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				q = r.URL.Query()
				w.Write([]byte(`{"total_rows":0,"list":[]}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			_, err := c.ListPosts(context.Background(), f.(ListPostsFilter))
			return q, reached, err
		})
	})

	t.Run("ListAccountsFilter", func(t *testing.T) {
		// Measured by row content: source_id=1 → rows all carry source_id 1;
		// source_id=7 → rows all carry source_id 7. Both fields work.
		specs := map[string]fieldSpec{
			"SourceID": {wireParam: "source_id", expectWire: "7", setVal: 7},
			"Page":     {wireParam: "page", expectWire: "2", setVal: 2},
		}
		assertFilterSweep(t, specs, reflect.TypeOf(ListAccountsFilter{}), func(f interface{}) (url.Values, bool, error) {
			reached := false
			var q url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				q = r.URL.Query()
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			_, err := c.ListAccounts(context.Background(), f.(ListAccountsFilter))
			return q, reached, err
		})
	})

	t.Run("ListPagesFilter", func(t *testing.T) {
		// Measured by row content: account_id=<a> → rows all carry that
		// account_id; source_id=3 → rows all carry source_id 3. All three
		// fields work. Note: account_id WORKS here but is a PHANTOM on
		// /posts (ListPostsFilter) — same name, two endpoints, opposite
		// behaviour, the second such pair after source_id.
		specs := map[string]fieldSpec{
			"SourceID":  {wireParam: "source_id", expectWire: "3", setVal: 3},
			"AccountID": {wireParam: "account_id", expectWire: "5", setVal: 5},
			"Page":      {wireParam: "page", expectWire: "2", setVal: 2},
		}
		assertFilterSweep(t, specs, reflect.TypeOf(ListPagesFilter{}), func(f interface{}) (url.Values, bool, error) {
			reached := false
			var q url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				q = r.URL.Query()
				w.Write([]byte(`{"list":[],"total_rows":0,"is_has_more":false,"rows_limit":20}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			_, err := c.ListPages(context.Background(), f.(ListPagesFilter))
			return q, reached, err
		})
	})
}

// fieldSpec describes one filter field's expected behaviour.
// phantom=true  → the field is a phantom parameter: setting it to a
//
//	non-zero value must produce an error and issue NO
//	request (the API accepts and silently ignores it).
//
// phantom=false → the field is a working filter: setting it must reach
//
//	the query string with the exact value in expectWire,
//	under the key in wireParam, with no error.
type fieldSpec struct {
	phantom    bool
	wireParam  string
	expectWire string
	setVal     interface{}
}

// assertFilterSweep verifies that every field of the filter struct is
// listed in specs (and vice versa), then runs one sub-test per field.
// callFn builds a fresh server+client, calls the endpoint with the
// populated filter, and returns the captured query values, whether the
// server was reached, and the error.
func assertFilterSweep(t *testing.T, specs map[string]fieldSpec, typ reflect.Type, callFn func(f interface{}) (q url.Values, reached bool, err error)) {
	t.Helper()

	// Completeness: every struct field must be listed.
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := specs[name]; !ok {
			t.Errorf("field %q on %s is not listed in the phantom sweep table — every field must be classified as works or phantom (this test prevents a fourth issue in the phantom-filter series, #67/#73)", name, typ.Name())
		}
	}
	// Reverse: no stale table entries for non-existent fields.
	for name := range specs {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("spec for %q but %s has no such field — stale table entry", name, typ.Name())
		}
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			fv := reflect.New(typ).Elem()
			fv.FieldByName(name).Set(reflect.ValueOf(spec.setVal))
			filter := fv.Interface()

			q, reached, err := callFn(filter)

			if spec.phantom {
				if err == nil {
					t.Fatalf("expected an error refusing the phantom parameter %q, got nil — the API accepts and silently ignores it, returning an unfiltered result set that looks filtered", name)
				}
				if reached {
					t.Fatalf("the refusal guard issued a request before erroring for phantom parameter %q — refusal MUST happen before any request is issued", name)
				}
				// The error must name the field's wire parameter, not just
				// any error — an unrelated early failure (e.g. a transport
				// error) would otherwise satisfy the phantom arm.
				if !contains(err.Error(), spec.wireParam) {
					t.Fatalf("phantom parameter %q: error does not name the field (want %q in message, got: %v) — an unrelated early failure must not satisfy the phantom arm", name, spec.wireParam, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected pass-through for working filter %q, got error: %v — this parameter must reach the wire unchanged", name, err)
			}
			if !reached {
				t.Fatalf("server was not reached for working filter %q — the request must be issued", name)
			}
			got := q.Get(spec.wireParam)
			if got != spec.expectWire {
				t.Fatalf("working filter %q: %s on wire = %q, want %q — a working filter must reach the query string unchanged", name, spec.wireParam, got, spec.expectWire)
			}
		})
	}
}
