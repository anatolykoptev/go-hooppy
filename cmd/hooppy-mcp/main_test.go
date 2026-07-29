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
// element, naming the offending token.
//
// RED-on-revert: route search_post_ids back through parseIntListStr and the
// "partial drop" cases (abc, empty-mid, trailing-comma) return nil error
// with a silently-truncated slice → these tests fail.
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
