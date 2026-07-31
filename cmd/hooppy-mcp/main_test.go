package main

import (
	"strings"
	"testing"
)

// TestParseOrderedIDListStr_Strict verifies the MCP strict parser for
// order-significant id lists (search_post_ids on rewrite). The lenient
// parseIntListStr skips unparseable entries by design — on an ordered list,
// "2001,abc,2003" became [2001,2003]: one post silently not copied and every
// subsequent post's schedule slot shifted by one. The fully-invalid case
// errors via the both-empty guard, so only the partial drop was silent — the
// worse half. parseOrderedIDListStr must reject any unparseable or empty
// element, naming the offending token, and (per finding 5c) must reject a
// non-positive id — its own error text promises "positive IDs" but the parser
// previously accepted 0 and -5.
//
// NOTE: this test guards the PARSER in isolation only. It does NOT guard the
// call site — an earlier version of this comment claimed "route search_post_ids
// back through parseIntListStr and these tests fail", which was measurably
// FALSE: reverting the call site to the lenient helper built and left this
// suite green, because the parser was never wired in here. The call site is
// guarded separately by TestBuildRewriteSearchPostPayload_StrictParse, which
// drives the real builder and DOES fail on that revert.
func TestParseOrderedIDListStr_Strict(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []int
		wantErr bool
		errSub  string
	}{
		{"empty string → nil, no error", "", nil, false, ""},
		{"single valid", "2001", []int{2001}, false, ""},
		{"multiple valid preserves order", "2003,2001,2002", []int{2003, 2001, 2002}, false, ""},
		{"whitespace trimmed", " 2001 , 2002 ", []int{2001, 2002}, false, ""},
		{"unparseable mid-list → error naming token", "2001,abc,2003", nil, true, "abc"},
		{"unparseable head → error naming token", "abc,2001", nil, true, "abc"},
		{"empty element mid-list → error", "2001,,2003", nil, true, "empty element"},
		{"trailing comma → empty element error", "2001,2002,", nil, true, "empty element"},
		{"leading comma → empty element error", ",2001,2002", nil, true, "empty element"},
		{"all invalid → error", "abc,def", nil, true, "abc"},
		// Finding 5c: the error text promises "positive IDs" but the parser
		// accepted 0 and -5. Both must now error.
		{"zero id → error (not positive)", "2001,0,2003", nil, true, "0"},
		{"negative id → error (not positive)", "2001,-5,2003", nil, true, "-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOrderedIDListStr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseOrderedIDListStr(%q) = %v, nil — want an error (lenient drop would silently shift later slots)", tc.in, got)
				}
				if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error %q does not name the offending token %q", err.Error(), tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOrderedIDListStr(%q) unexpected error: %v", tc.in, err)
			}
			if !sliceEq(got, tc.want) {
				t.Errorf("parseOrderedIDListStr(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseIntListStr_LenientStillDrops verifies the lenient helper is
// UNCHANGED — it still skips unparseable entries (its contract for
// non-ordered fields like schedules_ids/selected_pages_ids). The fix added a
// STRICT sibling for ordered id lists; it did not alter the lenient path.
// This pins the divergence so a future "unify the two parsers" change does
// not silently regress either contract.
func TestParseIntListStr_LenientStillDrops(t *testing.T) {
	got := parseIntListStr("2001,abc,2003")
	if want := []int{2001, 2003}; !sliceEq(got, want) {
		t.Errorf("parseIntListStr lenient path changed: got %v, want %v (lenient drop is the contract for non-ordered fields)", got, want)
	}
}

func sliceEq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBuildRewriteSearchPostPayload_StrictParse guards the CALL SITE, not the
// parser. The parser test above (TestParseOrderedIDListStr_Strict) guards
// parseOrderedIDListStr in isolation; an earlier RED-on-revert claim there was
// measurably false because reverting the call site to the lenient
// parseIntListStr left the suite green — the parser was never wired into the
// test. This test drives the real builder (buildRewriteSearchPostPayload) so
// that the strict parse is enforced at the point of use: an unparseable id in
// an order-significant list must error naming the token, not silently drop
// and shift every later post's schedule slot by one.
//
// RED-on-revert: route search_post_ids back through parseIntListStr inside
// buildRewriteSearchPostPayload and the "partial drop" cases return a payload
// with err == nil (the bad token dropped) → these assertions fail.
func TestBuildRewriteSearchPostPayload_StrictParse(t *testing.T) {
	cases := []struct {
		name   string
		in     rewriteSearchPostInput
		errSub string
	}{
		{
			"unparseable mid-list errors at the call site",
			rewriteSearchPostInput{SearchPostIDs: "2001,abc,2003", PublicationWhenType: 1},
			"abc",
		},
		{
			"empty element mid-list errors at the call site",
			rewriteSearchPostInput{SearchPostIDs: "2001,,2003", PublicationWhenType: 1},
			"empty element",
		},
		{
			"trailing comma errors at the call site",
			rewriteSearchPostInput{SearchPostIDs: "2001,2003,", PublicationWhenType: 1},
			"empty element",
		},
		// Finding 5c: positivity is enforced at the call site too.
		{
			"zero id errors at the call site",
			rewriteSearchPostInput{SearchPostIDs: "2001,0,2003", PublicationWhenType: 1},
			"0",
		},
		{
			"negative id errors at the call site",
			rewriteSearchPostInput{SearchPostIDs: "2001,-5,2003", PublicationWhenType: 1},
			"-5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildRewriteSearchPostPayload(tc.in)
			if err == nil {
				t.Fatalf("buildRewriteSearchPostPayload(%+v) = _, nil — want an error (lenient drop at the call site would silently shift later schedule slots)", tc.in)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error %q does not name the offending token %q", err.Error(), tc.errSub)
			}
		})
	}
}

// TestBuildRewriteSearchPostPayload_BatchTextRefusal verifies finding 4 at
// the MCP surface: batch rewrite cannot express per-post text, so text with
// search_post_ids errors; batch alone sends an empty Texts slice (the server
// keeps each post's original text, like import); single-post requires text.
func TestBuildRewriteSearchPostPayload_BatchTextRefusal(t *testing.T) {
	t.Run("batch + text → error", func(t *testing.T) {
		_, err := buildRewriteSearchPostPayload(rewriteSearchPostInput{
			SearchPostIDs: "2001,2002", Text: "hello", PublicationWhenType: 1,
		})
		if err == nil {
			t.Fatal("expected error for batch + text, got nil — batch rewrite cannot express per-post text")
		}
		if !strings.Contains(err.Error(), "not allowed with search_post_ids") {
			t.Errorf("error must name the batch+text refusal, got: %v", err)
		}
	})

	t.Run("batch alone → empty Texts slice (no override)", func(t *testing.T) {
		p, err := buildRewriteSearchPostPayload(rewriteSearchPostInput{
			SearchPostIDs: "2003,2001,2002", PublicationWhenType: 1,
		})
		if err != nil {
			t.Fatalf("batch alone: %v", err)
		}
		if got, want := p.SearchPostIDs, []int{2003, 2001, 2002}; !sliceEq(got, want) {
			t.Errorf("SearchPostIDs = %v, want %v (caller order)", got, want)
		}
		if p.Texts == nil {
			t.Fatal("Texts = nil, want []PostText{} (empty non-nil) — server keeps original text")
		}
		if len(p.Texts) != 0 {
			t.Errorf("Texts = %v, want [] (no text override for batch)", p.Texts)
		}
	})

	t.Run("single + no text → error", func(t *testing.T) {
		_, err := buildRewriteSearchPostPayload(rewriteSearchPostInput{
			SearchPostID: 2001, PublicationWhenType: 1,
		})
		if err == nil {
			t.Fatal("expected error for single-post without text, got nil — single rewrite requires text")
		}
		if !strings.Contains(err.Error(), "text is required for search_post_id") {
			t.Errorf("error must name the single-post text requirement, got: %v", err)
		}
	})

	t.Run("single + text → override payload", func(t *testing.T) {
		p, err := buildRewriteSearchPostPayload(rewriteSearchPostInput{
			SearchPostID: 2001, Text: "hello", PublicationWhenType: 1,
		})
		if err != nil {
			t.Fatalf("single + text: %v", err)
		}
		if p.SearchPostID != 2001 {
			t.Errorf("SearchPostID = %d, want 2001", p.SearchPostID)
		}
		if len(p.Texts) != 1 || p.Texts[0].Text != "hello" {
			t.Errorf("Texts = %v, want [{hello 0}]", p.Texts)
		}
	})
}

// TestBuildCopySearchPostPayload_SchedulesWhenTypeGuard and
// TestBuildRewriteSearchPostPayload_SchedulesWhenTypeGuard are the table tests
// that close finding 2: all four MCP contradiction guards (copy + rewrite,
// each the when_type=3+empty and the schedules+non-3 pair) were ungated —
// deleting both contradiction guards left `go build ./...` clean and
// `go test ./cmd/hooppy-mcp/` green (proven by mutation). The copy handler's
// validation was inline and unreachable from a test; it is now extracted into
// buildCopySearchPostPayload so it can be tested at all.
//
// Each table covers BOTH directions so neither guard can be satisfied by
// breaking its pair:
//   - when_type 1 + schedules → error (the contradiction guard)
//   - when_type 3 + schedules → SchedulesIDs populated (the _OK pair)
//
// F8 RED-on-revert: delete the contradiction guards from both builders and the
// "when_type 1 + schedules → error" cases return a payload with err == nil →
// these assertions fail. (This exact mutation is green today.)

func TestBuildCopySearchPostPayload_SchedulesWhenTypeGuard(t *testing.T) {
	cases := []struct {
		name      string
		in        copySearchPostInput
		wantErr   bool
		errSub    string
		wantSched []int
	}{
		{
			"when_type 1 + schedules → error (contradiction guard)",
			copySearchPostInput{SearchPostID: 2001, PublicationWhenType: 1, SchedulesIDs: "10,11"},
			true, "when-type 3", nil,
		},
		{
			"when_type 3 + schedules → SchedulesIDs populated (_OK pair)",
			copySearchPostInput{SearchPostID: 2001, PublicationWhenType: 3, SchedulesIDs: "10,11"},
			false, "", []int{10, 11},
		},
		{
			"when_type 3 + no schedules → error (existing converse guard)",
			copySearchPostInput{SearchPostID: 2001, PublicationWhenType: 3, SchedulesIDs: ""},
			true, "publication_when_type=3", nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := buildCopySearchPostPayload(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("buildCopySearchPostPayload(%+v) = %+v, nil — want an error (the contradiction guard must refuse schedules with a non-schedule when-type; F8 mutation is green without this)", tc.in, p)
				}
				if !strings.Contains(err.Error(), "schedules") {
					t.Errorf("error must name schedules, got: %v", err)
				}
				if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error must contain %q, got: %v", tc.errSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildCopySearchPostPayload(%+v): %v", tc.in, err)
			}
			if !sliceEq(p.SchedulesIDs, tc.wantSched) {
				t.Errorf("SchedulesIDs = %v, want %v — schedules must reach the payload under when-type 3 (the _OK pair stops the contradiction guard from refusing ALL schedules)", p.SchedulesIDs, tc.wantSched)
			}
		})
	}
}

func TestBuildRewriteSearchPostPayload_SchedulesWhenTypeGuard(t *testing.T) {
	cases := []struct {
		name      string
		in        rewriteSearchPostInput
		wantErr   bool
		errSub    string
		wantSched []int
	}{
		{
			"when_type 1 + schedules → error (contradiction guard)",
			rewriteSearchPostInput{SearchPostID: 2001, Text: "hello", PublicationWhenType: 1, SchedulesIDs: "10,11"},
			true, "when-type 3", nil,
		},
		{
			"when_type 3 + schedules → SchedulesIDs populated (_OK pair)",
			rewriteSearchPostInput{SearchPostID: 2001, Text: "hello", PublicationWhenType: 3, SchedulesIDs: "10,11"},
			false, "", []int{10, 11},
		},
		{
			"when_type 3 + no schedules → error (existing converse guard)",
			rewriteSearchPostInput{SearchPostID: 2001, Text: "hello", PublicationWhenType: 3, SchedulesIDs: ""},
			true, "publication_when_type=3", nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := buildRewriteSearchPostPayload(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("buildRewriteSearchPostPayload(%+v) = %+v, nil — want an error (the contradiction guard must refuse schedules with a non-schedule when-type; F8 mutation is green without this)", tc.in, p)
				}
				if !strings.Contains(err.Error(), "schedules") {
					t.Errorf("error must name schedules, got: %v", err)
				}
				if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error must contain %q, got: %v", tc.errSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildRewriteSearchPostPayload(%+v): %v", tc.in, err)
			}
			if !sliceEq(p.SchedulesIDs, tc.wantSched) {
				t.Errorf("SchedulesIDs = %v, want %v — schedules must reach the payload under when-type 3", p.SchedulesIDs, tc.wantSched)
			}
		})
	}
}
