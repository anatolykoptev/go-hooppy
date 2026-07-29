package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// TestPhantomFilterSweep is the single gate that prevents a fourth issue
// in the phantom-filter series (#63, #67, #73) shipping unclassified. It
// enumerates EVERY field
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
// Two groups, explicitly labelled. A field is in the FIRST group only if
// a differential run was recorded (with/without/different valid value,
// judged by RETURNED ROW CONTENT, not total_rows — see method note 1).
// Anything without recorded differential evidence goes in the SECOND
// group by definition: it reaches the wire (the sweep asserts that) but
// was NOT measured differentially, so a `works` classification there is
// an assumption, not a measurement. Downgrade the claim; do not upgrade
// the prose. The live API is not called from this repo, so anything
// without recorded evidence stays in the second group until a
// differential run is recorded and pasted here.
//
// ## measured (differential run, row content compared)
//
// /posts-search (SearchPostsFilter):
//   - source_id=7 (Instagram) returns rows whose own source_id is 1 → phantom.
//   - source_resource_id=2228 (Instagram-only) returns rows with
//     source_id: [1] → phantom.
//   - owner_id=<real> returns four different owners → phantom.
//   - source_type=2 (RSS) returns 0 rows vs source_type=1 returns rows → works.
//   - DateFrom/DateTo: no filter oldest=18.06.2024;
//     date_from=20.07.2026 oldest=19.07.2026 10:00;
//     date_to=10.07.2026 newest=10.07.2026 09:50 → works. The window is
//     offset (asking from the 20th yields rows from the 19th at 10:00) —
//     that is the separate defect tracked in #62 (an off-by-one in the
//     date window), NOT a phantom.
//   - photos_amount: 1→9294, 5→566, 6→742, 10→2172, 99→2172 (saturates,
//     "N or more", not "exactly N") → works. See posts_search.go:157-164.
//   - video_duration: 0→4194, 1→710, 2→159, 3→3525, 4→4036, 5→4128,
//     6→4161, 7→644, 8→677; 9,10 server error → works. See
//     posts_search.go:140-155.
//
// /posts (ListPostsFilter):
//   - account_id=<impossible> returns the full collection → phantom.
//   - page_id=<impossible> returns the full collection → phantom.
//
// /accounts (ListAccountsFilter):
//   - source_id=1 → rows all carry source_id 1; source_id=7 → rows all
//     carry source_id 7 → works.
//
// /accounts/pages (ListPagesFilter):
//   - account_id=<a> → rows all carry that account_id → works.
//   - source_id=3 → rows all carry source_id 3 → works.
//
// ## assumed — reaches the wire only, NOT differentially measured
//
// These fields reach the query string (the sweep asserts the wire value)
// but no differential run was recorded, so classifying them `works` is an
// assumption, not a measurement. They are NOT phantom (a phantom is
// refused by the guard and never reaches the wire); they are
// unmeasured-working. A differential run would promote any of these to
// the measured group above.
//
// /posts-search (SearchPostsFilter):
//   - Text, SortBy, SortDirection, ContentTypes, ContentTypesExclude, Page
//
// /posts (ListPostsFilter):
//   - IsPublished, PublicationDate, SourceID, ScheduleID, ProjectID, Page
//
// /accounts (ListAccountsFilter):
//   - Page
//
// /accounts/pages (ListPagesFilter):
//   - Page
//
// Note the cross-endpoint pair worth stating: account_id is a WORKING
// filter on /accounts/pages (measured) and a PHANTOM on /posts (measured)
// — two endpoints, one name, opposite behaviour. This is the second such
// pair in the repo after source_id (works on /posts [assumed], phantom on
// /posts-search [measured]). The fix is per-endpoint, never per-name.
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
			"SourceID":            {phantom: true, wireParam: "source_id", setVal: 1, negVal: -1},
			"SourceResourceID":    {phantom: true, wireParam: "source_resource_id", setVal: 1, negVal: -1},
			"OwnerID":             {phantom: true, wireParam: "owner_id", setVal: 1, negVal: -1},
			"Page":                {wireParam: "page", expectWire: "2", setVal: 2},
			"SortBy":              {wireParam: "sort_by", expectWire: "likes", setVal: "likes"},
			"SortDirection":       {wireParam: "sort_direction", expectWire: "asc", setVal: "asc"},
			"MinLikes":            {phantom: true, wireParam: "min_likes", setVal: 1, negVal: -1},
			"MinViews":            {phantom: true, wireParam: "min_views", setVal: 1, negVal: -1},
			"MinComments":         {phantom: true, wireParam: "min_comments", setVal: 1, negVal: -1},
			"MinReposts":          {phantom: true, wireParam: "min_reposts", setVal: 1, negVal: -1},
			"MinInvolvement":      {phantom: true, wireParam: "min_involvement", setVal: 1.5, negVal: -1.5},
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
			"AccountID":       {phantom: true, wireParam: "account_id", setVal: 1, negVal: -1},
			"PageID":          {phantom: true, wireParam: "page_id", setVal: 1, negVal: -1},
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
//	The phantom arm runs BOTH signs — setVal (positive) and
//	negVal (negative) — so the gate catches a guard weakened
//	from != 0 to > 0 for every current and future phantom
//	field, not just the five hand-written cases in
//	TestListPosts_NegativeRejected / TestListSearchPosts_IDPageNegative.
//	A negative taking neither branch (no error, no parameter,
//	an unfiltered result that looks filtered) is issue #65
//	item 1 verbatim and reachable from the shipped CLI (pflag
//	IntVar is signed). negVal must be the negation of setVal
//	for numeric fields; for non-numeric phantom fields leave
//	it unset (the arm skips the negative leg when negVal == nil).
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
	negVal     interface{}
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
			t.Errorf("field %q on %s is not listed in the phantom sweep table — every field must be classified as works or phantom (this test prevents a fourth issue in the phantom-filter series shipping unclassified, #67/#73)", name, typ.Name())
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
			// A spec that cannot assert anything must be a test FAILURE,
			// never a silent pass: the gate is defeated by omission, not
			// by a wrong value. wireParam is required for BOTH arms (the
			// phantom arm asserts the error names it; the works arm
			// asserts the wire carries it). expectWire is required for
			// the works arm only. Without these checks a spec that omits
			// a map key passes vacuously — a field can be classified
			// `works` with NO wire assertion, or `phantom` with no
			// error-names-parameter assertion (the hand-rolled contains
			// short-circuited len(substr)==0 to true, evaporating the
			// check). That is the per-struct blindness of the prior round
			// reproduced per-field.
			if spec.wireParam == "" {
				t.Fatalf("spec for %q has an empty wireParam — the gate cannot assert anything without it; a spec that omits it must fail, not pass (the gate is defeated by omission, not by a wrong value)", name)
			}
			if !spec.phantom && spec.expectWire == "" {
				t.Fatalf("spec for working filter %q has an empty expectWire — the works arm cannot assert the wire value without it; a spec that omits it must fail, not pass", name)
			}

			if spec.phantom {
				// Run BOTH signs: setVal (positive) and negVal (negative).
				// The phantom guard fires on != 0 today, so both must error
				// before any request. The negative leg catches a guard
				// weakened from != 0 to > 0 — a negative then takes neither
				// branch (no error, no parameter, an unfiltered result that
				// looks filtered), which is issue #65 item 1 verbatim and
				// reachable from the shipped CLI (pflag IntVar is signed).
				// This makes the coverage structural for every current and
				// future phantom field, not just the five hand-written
				// cases in TestListPosts_NegativeRejected /
				// TestListSearchPosts_IDPageNegative.
				values := []struct {
					label string
					v     interface{}
				}{
					{"positive", spec.setVal},
				}
				if spec.negVal != nil {
					values = append(values, struct {
						label string
						v     interface{}
					}{"negative", spec.negVal})
				}
				for _, vc := range values {
					t.Run(vc.label, func(t *testing.T) {
						fv := reflect.New(typ).Elem()
						fv.FieldByName(name).Set(reflect.ValueOf(vc.v))
						_, reached, err := callFn(fv.Interface())
						if err == nil {
							t.Fatalf("expected an error refusing the phantom parameter %q (%s), got nil — the API accepts and silently ignores it, returning an unfiltered result set that looks filtered", name, vc.label)
						}
						if reached {
							t.Fatalf("the refusal guard issued a request before erroring for phantom parameter %q (%s) — refusal MUST happen before any request is issued", name, vc.label)
						}
						// The error must name the field's wire parameter,
						// not just any error — an unrelated early failure
						// (e.g. a transport error) would otherwise satisfy
						// the phantom arm.
						if !strings.Contains(err.Error(), spec.wireParam) {
							t.Fatalf("phantom parameter %q (%s): error does not name the field (want %q in message, got: %v) — an unrelated early failure must not satisfy the phantom arm", name, vc.label, spec.wireParam, err)
						}
					})
				}
				return
			}

			fv := reflect.New(typ).Elem()
			fv.FieldByName(name).Set(reflect.ValueOf(spec.setVal))
			filter := fv.Interface()
			q, reached, err := callFn(filter)
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
