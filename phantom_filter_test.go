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
// of SearchPostsFilter and ListPostsFilter via reflection and checks each
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
// added to either filter struct MUST be classified here or the sweep is
// incomplete by construction.
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
			"SourceID":            {phantom: true, setVal: 1},
			"SourceResourceID":    {phantom: true, setVal: 1},
			"OwnerID":             {phantom: true, setVal: 1},
			"Page":                {wireParam: "page", expectWire: "2", setVal: 2},
			"SortBy":              {wireParam: "sort_by", expectWire: "likes", setVal: "likes"},
			"SortDirection":       {wireParam: "sort_direction", expectWire: "asc", setVal: "asc"},
			"MinLikes":            {phantom: true, setVal: 1},
			"MinViews":            {phantom: true, setVal: 1},
			"MinComments":         {phantom: true, setVal: 1},
			"MinReposts":          {phantom: true, setVal: 1},
			"MinInvolvement":      {phantom: true, setVal: 1.5},
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
			"AccountID":       {phantom: true, setVal: 1},
			"PageID":          {phantom: true, setVal: 1},
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
