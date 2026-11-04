package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

func pbool(v bool) *bool   { return &v }
func pids(v ...int) *[]int { return &v }

// ---------------------------------------------------------------------------
// VK wiki-markup strip + advertising-disclosure detection — moved verbatim
// from cmd/hooppy/import_text_test.go when the functions moved into the op
// (they are part of request building and result shaping, so the op owns them).
// ---------------------------------------------------------------------------

func TestStripVKMarkup(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single link converts to display text",
			in:   "See [https://vk.com/x|Заголовок] for details",
			want: "See Заголовок for details",
		},
		{
			name: "multiple links in one text all convert",
			in:   "[https://vk.com/a|First] and [https://vk.com/b|Second]",
			want: "First and Second",
		},
		{
			name: "pipe inside link text preserved (split on first pipe only)",
			in:   "[https://vk.com/x|text|with|pipes]",
			want: "text|with|pipes",
		},
		{
			name: "unterminated bracket left untouched",
			in:   "Text [https://vk.com/x|Заголовок without close",
			want: "Text [https://vk.com/x|Заголовок without close",
		},
		{
			name: "bracket with no pipe left untouched (not a wiki-link)",
			in:   "See [https://vk.com/x] here",
			want: "See [https://vk.com/x] here",
		},
		{
			name: "empty brackets left untouched",
			in:   "See [] here",
			want: "See [] here",
		},
		{
			name: "double-bracket internal page with display converts to display",
			in:   "See [[page_name|Display Text]] here",
			want: "See Display Text here",
		},
		{
			name: "double-bracket internal page without pipe left untouched",
			in:   "See [[page_name]] here",
			want: "See [[page_name]] here",
		},
		{
			name: "unterminated double-bracket left untouched",
			in:   "See [[page_name|Display without close",
			want: "See [[page_name|Display without close",
		},
		{
			name: "no markup passes through byte-identical",
			in:   "Plain text with no markup at all.\nSecond line.",
			want: "Plain text with no markup at all.\nSecond line.",
		},
		{
			name: "no markup with leading/trailing spaces preserved byte-identical",
			in:   "  spaced text  ",
			want: "  spaced text  ",
		},
		{
			name: "adjacent links with no gap",
			in:   "[https://vk.com/a|One][https://vk.com/b|Two]",
			want: "OneTwo",
		},
		{
			name: "link text containing brackets in display is handled by first-close rule",
			in:   "[https://vk.com/x|text] stuff]",
			want: "text stuff]",
		},
		// F1: a malformed marker must pass through byte-identical, including
		// when valid-looking markup appears later in the same text. The old
		// unterminated-[[ branch emitted only the first "[" and advanced by
		// one, so the second "[" re-entered the single-bracket parser, found
		// a later "]" and "|", and transformed text that was never a valid
		// marker. Each case below was mangled before the fix; all must now be
		// byte-identical.
		{
			name: "unterminated [[ with later ] and | passes through byte-identical",
			in:   "[[a|b]|c]",
			want: "[[a|b]|c]",
		},
		{
			name: "unterminated [[ with later ] and space passes through byte-identical",
			in:   "[[page|Display] text]",
			want: "[[page|Display] text]",
		},
		{
			name: "nested broken [[ with inner [ passes through byte-identical",
			in:   "[[a|[c|d]]]",
			want: "[[a|[c|d]]]",
		},
		// F5: a "[" inside a [url|text] inner is nested/broken markup — leave
		// it byte-untouched so strip is idempotent (the old first-close rule
		// turned "[url|[x|y]]" into "[x|y]" on pass 1 and "y" on pass 2).
		{
			name: "single bracket with inner [ passes through byte-identical (idempotence)",
			in:   "[url|[x|y]]",
			want: "[url|[x|y]]",
		},
		// A "[" inside the display text is NOT only nested/broken markup — it can
		// be a legitimate link whose display contains "[". stripVKMarkup leaves
		// such a marker byte-untouched DELIBERATELY to stay idempotent and avoid
		// the nested-bracket corruption class, at the cost of not converting this
		// rare valid form. This case pins the trade-off in the suite: the marker
		// reaches the wire byte-identical, not mangled and not converted.
		{
			name: "link whose display text contains [ is left byte-untouched (trade-off pinned)",
			in:   "[https://vk.com/x|see [note]]",
			want: "[https://vk.com/x|see [note]]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripVKMarkup(tc.in)
			if got != tc.want {
				t.Errorf("stripVKMarkup(%q) =\n  %q\nwant\n  %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripVKMarkup_Idempotent pins strip(strip(x)) == strip(x) over the whole
// table, so the idempotence property is asserted rather than incidental. A
// non-idempotent strip is a corruption vector on a live publishing queue: a
// caller that pre-transforms and then re-transforms would get a different
// result, and the first-close rule could leave a "[" in the output that
// re-parses on the second pass.
func TestStripVKMarkup_Idempotent(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"single link", "See [https://vk.com/x|Заголовок] for details"},
		{"multiple links", "[https://vk.com/a|First] and [https://vk.com/b|Second]"},
		{"pipe in display", "[https://vk.com/x|text|with|pipes]"},
		{"unterminated single", "Text [https://vk.com/x|Заголовок without close"},
		{"no pipe", "See [https://vk.com/x] here"},
		{"empty brackets", "See [] here"},
		{"double-bracket display", "See [[page_name|Display Text]] here"},
		{"double-bracket no pipe", "See [[page_name]] here"},
		{"unterminated double-bracket", "See [[page_name|Display without close"},
		{"plain text", "Plain text with no markup at all.\nSecond line."},
		{"spaced", "  spaced text  "},
		{"adjacent links", "[https://vk.com/a|One][https://vk.com/b|Two]"},
		{"first-close display", "[https://vk.com/x|text] stuff]"},
		{"F1 unterminated [[ with later pipe", "[[a|b]|c]"},
		{"F1 unterminated [[ with space", "[[page|Display] text]"},
		{"F1 nested broken [[", "[[a|[c|d]]]"},
		{"F5 single bracket with inner [", "[url|[x|y]]"},
		{"display text contains [ (trade-off)", "[https://vk.com/x|see [note]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			once := stripVKMarkup(tc.in)
			twice := stripVKMarkup(once)
			if once != twice {
				t.Errorf("strip not idempotent on %q:\n  pass1=%q\n  pass2=%q", tc.in, once, twice)
			}
		})
	}
}

func TestDetectVKMarkup(t *testing.T) {
	t.Run("finds single link", func(t *testing.T) {
		hits := detectVKMarkup("See [https://vk.com/x|Заголовок] here")
		if len(hits) != 1 || hits[0] != "[https://vk.com/x|Заголовок]" {
			t.Errorf("detectVKMarkup = %v, want [\"[https://vk.com/x|Заголовок]\"]", hits)
		}
	})
	t.Run("finds multiple links", func(t *testing.T) {
		hits := detectVKMarkup("[https://vk.com/a|A] [https://vk.com/b|B]")
		if len(hits) != 2 {
			t.Errorf("detectVKMarkup = %v, want 2 hits", hits)
		}
	})
	t.Run("no markup returns empty", func(t *testing.T) {
		hits := detectVKMarkup("plain text")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty", hits)
		}
	})
	t.Run("unterminated bracket not reported", func(t *testing.T) {
		hits := detectVKMarkup("[https://vk.com/x|no close")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (unterminated is not a marker)", hits)
		}
	})
	t.Run("double-bracket page without pipe is reported", func(t *testing.T) {
		// [[page]] has no display text, but it is still VK wiki markup (an
		// internal page reference) — report it so the operator knows it is
		// there, even though strip leaves it untouched.
		hits := detectVKMarkup("See [[page_name]] here")
		if len(hits) != 1 || hits[0] != "[[page_name]]" {
			t.Errorf("detectVKMarkup = %v, want [\"[[page_name]]\"]", hits)
		}
	})
	t.Run("single bracket without pipe not reported", func(t *testing.T) {
		// [url] with no pipe is not a wiki-link — do not report it.
		hits := detectVKMarkup("See [https://vk.com/x] here")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (no pipe = not a wiki-link)", hits)
		}
	})
	t.Run("unterminated double-bracket not reported", func(t *testing.T) {
		hits := detectVKMarkup("See [[page_name|Display without close")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (unterminated [[ is not a marker)", hits)
		}
	})
	// A "[" inside a marker's inner means nested/broken markup — not a valid
	// VK marker. stripVKMarkup leaves it byte-untouched, so detection MUST
	// NOT report it either: reporting it would warn "VK markup found" and
	// suggest --strip-vk-markup, a flag that changes nothing. Detection and
	// strip must agree on what is a marker.
	t.Run("malformed nested double-bracket not reported (strip leaves it, detection agrees)", func(t *testing.T) {
		hits := detectVKMarkup("[[a|[c|d]]]")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (inner [ = malformed, strip leaves it, detection must agree)", hits)
		}
	})
	t.Run("malformed single bracket with inner [ not reported (strip leaves it, detection agrees)", func(t *testing.T) {
		hits := detectVKMarkup("[url|[x|y]]")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (inner [ = malformed, strip leaves it, detection must agree)", hits)
		}
	})
}

func TestDetectAdMarkers(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantLines []string
	}{
		{
			name:      "Erid marker matched",
			in:        "Some post text\nErid: 2VtzqwxyzAB\nmore text",
			wantLines: []string{"Erid: 2VtzqwxyzAB"},
		},
		{
			// The real-world token is written "erid:" in lower case at least
			// as often as "Erid:". Erid is a Latin token and the disclosure
			// body is Cyrillic, so the lower-case form cannot appear inside a
			// Russian word — case-insensitive matching is safe here.
			name:      "lower-case erid marker matched (case-insensitive)",
			in:        "erid: 2VtzqwxyzAB",
			wantLines: []string{"erid: 2VtzqwxyzAB"},
		},
		{
			// Regression guard: "Реклама." stays case-sensitive. The lower-case
			// "реклама." is an ordinary Russian word ("this is an ad. a good
			// one.") and would fire on non-disclosure text.
			name:      "lower-case реклама. NOT matched (Cyrillic word, false-positive risk)",
			in:        "это реклама. хорошая.",
			wantLines: nil,
		},
		{
			name:      "Реклама. marker matched",
			in:        "Реклама. ООО «Ромашка».",
			wantLines: []string{"Реклама. ООО «Ромашка»."},
		},
		{
			name:      "ИНН marker matched",
			in:        "ИНН 1234567890",
			wantLines: []string{"ИНН 1234567890"},
		},
		{
			// F4: "ИНН" is a Cyrillic ABBREVIATION, not a word — the lower-case
			// "инн" is not an ordinary Russian word, so folding has no
			// false-positive cost (a hit is one extra warning, never
			// corruption), and the miss is real: "инн 1234567890" in a sloppy
			// source escapes detection when the marker is case-sensitive.
			name:      "lower-case инн matched (Cyrillic abbreviation, fold-safe)",
			in:        "инн 1234567890",
			wantLines: []string{"инн 1234567890"},
		},
		{
			name: "full advertising block matched as one line",
			in:   "Реклама. ООО «Ромашка». ИНН 1234567. Erid: 2VtzqwxyzAB",
			wantLines: []string{
				"Реклама. ООО «Ромашка». ИНН 1234567. Erid: 2VtzqwxyzAB",
			},
		},
		{
			name:      "no markers returns empty",
			in:        "Just a normal post about cats.",
			wantLines: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectAdMarkers(tc.in)
			if len(got) != len(tc.wantLines) {
				t.Fatalf("detectAdMarkers = %v, want %v", got, tc.wantLines)
			}
			for i, line := range tc.wantLines {
				if got[i] != line {
					t.Errorf("detectAdMarkers[%d] = %q, want %q", i, got[i], line)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Stub server — answers the resolve+publish pair (GET .../edit, POST /posts)
// plus the GET /posts snapshot walk when_type=3 slot recovery performs.
// Records every request count and the raw POST bodies (in arrival order).
// ---------------------------------------------------------------------------

type searchStub struct {
	mu       sync.Mutex
	requests int
	posts    [][]byte
}

func (s *searchStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *searchStub) postBodies() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.posts))
	copy(out, s.posts)
	return out
}

// newSearchStubServer serves the three endpoints the op's resolve+publish
// path can touch. editText is the resolved text every post returns (use
// editBodies for per-post text).
func newSearchStubServer(t *testing.T, editText string) (*httptest.Server, *searchStub) {
	t.Helper()
	return newSearchStubServerMap(t, nil, editText)
}

// newSearchStubServerMap is newSearchStubServer with per-id edit texts.
// A post id missing from the map returns 404 on the edit GET (a resolve
// failure — exercised by the partial-failure tests).
func newSearchStubServerMap(t *testing.T, editTexts map[int]string, fallbackText string) (*httptest.Server, *searchStub) {
	t.Helper()
	st := &searchStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.requests++
		st.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts-search/"), "/edit")
			id := 0
			for _, ch := range idStr {
				id = id*10 + int(ch-'0')
			}
			text := fallbackText
			if editTexts != nil {
				var ok bool
				text, ok = editTexts[id]
				if !ok {
					http.NotFound(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"%d","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":%q,"source_id":0}],"attachments":[]}`, id, text)
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			raw, _ := io.ReadAll(r.Body)
			st.mu.Lock()
			st.posts = append(st.posts, raw)
			st.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":7001}`))
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			// The before/after slot-recovery snapshots (when_type=3).
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[],"meta":{"current_page":1,"last_page":1,"per_page":20,"total":0}}`))
		default:
			t.Errorf("stub: unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

// requireValidation asserts err is a *ValidationError containing every
// substring, and that the stub saw zero requests — the refusal MUST fire
// before any HTTP call (a refused combination that still reaches the wire
// is the silent-drop defect family these guards close).
func requireValidation(t *testing.T, err error, requests int, subs ...string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want a *ops.ValidationError — a local refusal must be typed so the surfaces can map it (CLI exit 64, MCP tool error)", err, err)
	}
	for _, sub := range subs {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not name %q — every refusal must name the flag and the when-type", err.Error(), sub)
		}
	}
	if requests != 0 {
		t.Fatalf("stub saw %d request(s), want 0 — validation must refuse BEFORE any HTTP call", requests)
	}
}

// ---------------------------------------------------------------------------
// Validation tables — the union of the six deleted builders' guard tests,
// now driven against the ops directly. The stub server counts requests so a
// guard that stops firing shows up as a published post, not just a nil err.
// ---------------------------------------------------------------------------

func TestCopySearchPost_Validation(t *testing.T) {
	srv, st := newSearchStubServer(t, "x")
	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	cases := []struct {
		name string
		in   CopySearchPostInput
		subs []string
	}{
		{"no post-id", CopySearchPostInput{}, []string{"--post-id", "required"}},
		{"post-id zero", CopySearchPostInput{SearchPostID: pint(0)}, []string{"--post-id"}},
		{"post-id negative", CopySearchPostInput{SearchPostID: pint(-3)}, []string{"--post-id", "-3"}},
		{"when-type 0", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(0), SelectedPagesIDs: pids(123)}}, []string{"when-type", "0"}},
		{"when-type 4", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(4), SelectedPagesIDs: pids(123)}}, []string{"when-type", "4"}},
		{"when-type -1", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(-1), SelectedPagesIDs: pids(123)}}, []string{"when-type", "-1"}},
		{"F1: pages under when-type 3", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(10), SelectedPagesIDs: pids(99)}}, []string{"--to", "when-type 3", "#139"}},
		{"F2: date under when-type 1", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123), PublishDate: pstr("01.01.2027")}}, []string{"--date", "when-type 1", "#140"}},
		{"F2: date under when-type 3", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(10), PublishDate: pstr("01.01.2027")}}, []string{"--date", "when-type 3", "#140"}},
		{"schedules under when-type 1", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123), SchedulesIDs: pids(10, 11)}}, []string{"--schedules", "when-type 1", "#111"}},
		{"schedules under when-type 2", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(2), PublishDate: pstr("01.01.2027"), PublishHours: pstr("10"), PublishMinutes: pstr("00"), SchedulesIDs: pids(10)}}, []string{"--schedules", "when-type 2", "#111"}},
		{"when-type 3 without schedules", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3)}}, []string{"--schedules", "when-type 3", "required"}},
		{"when-type 2 without date", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(2), SelectedPagesIDs: pids(123)}}, []string{"--date", "--hours", "--minutes", "when-type 2"}},
		{"when-type 2 partial date (hours only)", CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(2), PublishHours: pstr("10")}}, []string{"--date", "required"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := st.count()
			_, err := CopySearchPost(ctx, c, tc.in)
			requireValidation(t, err, st.count()-before, tc.subs...)
		})
	}
}

func TestRewriteSearchPost_Validation(t *testing.T) {
	srv, st := newSearchStubServer(t, "x")
	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	cases := []struct {
		name string
		in   RewriteSearchPostInput
		subs []string
	}{
		{"post-id and post-ids mutually exclusive", RewriteSearchPostInput{SearchPostID: pint(1001), SearchPostIDs: pids(2001, 2002), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123)}}, []string{"--post-id", "--post-ids", "mutually exclusive"}},
		{"one id required", RewriteSearchPostInput{Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123)}}, []string{"--post-id", "--post-ids", "required"}},
		{"single-post text required", RewriteSearchPostInput{SearchPostID: pint(1001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123)}}, []string{"--text", "required", "--post-id"}},
		{"batch + text refused", RewriteSearchPostInput{SearchPostIDs: pids(2001, 2002), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123)}}, []string{"--text", "--post-ids"}},
		{"zero id in batch", RewriteSearchPostInput{SearchPostIDs: pids(2001, 0, 2003), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1)}}, []string{"positive", "position 2"}},
		{"negative id in batch", RewriteSearchPostInput{SearchPostIDs: pids(2001, -5, 2003), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1)}}, []string{"positive", "-5"}},
		{"F1: pages under when-type 3", RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(10), SelectedPagesIDs: pids(99)}}, []string{"--to", "when-type 3", "#139"}},
		{"F2: date under when-type 1", RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123), PublishMinutes: pstr("30")}}, []string{"--minutes", "when-type 1", "#140"}},
		{"F2: date under when-type 3", RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(10), PublishHours: pstr("10")}}, []string{"--hours", "when-type 3", "#140"}},
		{"schedules under when-type 1", RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123), SchedulesIDs: pids(10, 11)}}, []string{"--schedules", "when-type 1", "#111"}},
		{"when-type 3 without schedules", RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3)}}, []string{"--schedules", "required"}},
		{"when-type 0", RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(0), SelectedPagesIDs: pids(123)}}, []string{"when-type", "0"}},
		{"when-type 4", RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(4), SelectedPagesIDs: pids(123)}}, []string{"when-type", "4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := st.count()
			_, err := RewriteSearchPost(ctx, c, tc.in)
			requireValidation(t, err, st.count()-before, tc.subs...)
		})
	}
}

func TestImportSearchPost_Validation(t *testing.T) {
	srv, st := newSearchStubServer(t, "x")
	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	cases := []struct {
		name string
		in   ImportSearchPostInput
		subs []string
	}{
		{"post-id and post-ids mutually exclusive", ImportSearchPostInput{SearchPostID: pint(1001), SearchPostIDs: pids(2001, 2002), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(999)}}, []string{"--post-id", "--post-ids", "mutually exclusive"}},
		{"one id required", ImportSearchPostInput{PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(999)}}, []string{"--post-id", "--post-ids", "required"}},
		{"zero id in batch", ImportSearchPostInput{SearchPostIDs: pids(2001, 0, 2003), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(999)}}, []string{"positive", "position 2"}},
		{"F1: pages under when-type 3", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(10), SelectedPagesIDs: pids(99)}}, []string{"--to", "when-type 3", "#139"}},
		{"F1: pages under when-type 3 (batch)", ImportSearchPostInput{SearchPostIDs: pids(2001, 2002), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(10), SelectedPagesIDs: pids(99)}}, []string{"--to", "when-type 3", "#139"}},
		{"F2: date under when-type 1", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123), PublishDate: pstr("01.01.2027")}}, []string{"--date", "when-type 1", "#140"}},
		{"F2: date under when-type 3", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3), SchedulesIDs: pids(10), PublishDate: pstr("01.01.2027")}}, []string{"--date", "when-type 3", "#140"}},
		{"F2: hours only under when-type 1", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123), PublishHours: pstr("10")}}, []string{"--hours", "when-type 1"}},
		{"schedules under when-type 1", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(123), SchedulesIDs: pids(10, 11)}}, []string{"--schedules", "when-type 1", "#111"}},
		{"schedules under when-type 2", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(2), PublishDate: pstr("01.01.2027"), PublishHours: pstr("10"), PublishMinutes: pstr("00"), SchedulesIDs: pids(10)}}, []string{"--schedules", "when-type 2", "#111"}},
		{"when-type 3 without schedules", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(3)}}, []string{"--schedules", "required"}},
		{"when-type 0", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(0), SelectedPagesIDs: pids(123)}}, []string{"when-type", "0"}},
		{"when-type 4", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(4), SelectedPagesIDs: pids(123)}}, []string{"when-type", "4"}},
		{"when-type 2 missing date", ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(2), SelectedPagesIDs: pids(123)}}, []string{"--date", "required"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := st.count()
			_, err := ImportSearchPost(ctx, c, tc.in)
			requireValidation(t, err, st.count()-before, tc.subs...)
		})
	}
}

// ---------------------------------------------------------------------------
// Wire-shape tests — what the op actually puts on POST /posts.
// ---------------------------------------------------------------------------

// decodePostBody unmarshals a captured POST /posts body into a map.
func decodePostBody(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("captured POST body is not JSON: %v\n%s", err, raw)
	}
	return m
}

// TestImportSearchPost_BatchSendsSingleIDPerRequest pins the #141 decision:
// a batch import sends N requests, each carrying ONE scalar `ids` — never the
// comma-joined multi-id form that hits the server's unconditional
// schedules_ids read (measured: multi-id ids + when_type 2 → HTTP 500;
// single-id ids under the same mode → 200). Each post also keeps its own
// resolved text (the reason the client-side loop exists).
func TestImportSearchPost_BatchSendsSingleIDPerRequest(t *testing.T) {
	srv, st := newSearchStubServerMap(t, map[int]string{
		4001: "First post text",
		4002: "Second post text",
		4003: "Third post text",
	}, "")
	c := newTestClient(t, srv.URL)

	res, err := ImportSearchPost(context.Background(), c, ImportSearchPostInput{
		SearchPostIDs: pids(4001, 4002, 4003),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(1),
			SelectedPagesIDs:    pids(99),
		},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost: %v", err)
	}
	posts := st.postBodies()
	if len(posts) != 3 {
		t.Fatalf("POST count = %d, want 3 (one single-id request per post — the #141-safe shape)", len(posts))
	}
	wantTexts := []string{"First post text", "Second post text", "Third post text"}
	wantIDs := []string{"4001", "4002", "4003"}
	for i, raw := range posts {
		m := decodePostBody(t, raw)
		ids, _ := m["ids"].(string)
		if ids != wantIDs[i] || strings.Contains(ids, ",") {
			t.Errorf("post[%d] ids = %q, want scalar %q — a comma-joined ids field is the wire form the server 500s on (issue #141)", i, ids, wantIDs[i])
		}
		texts, _ := m["texts"].([]interface{})
		if len(texts) != 1 {
			t.Fatalf("post[%d] texts len = %d, want 1", i, len(texts))
		}
		if got := texts[0].(map[string]interface{})["text"]; got != wantTexts[i] {
			t.Errorf("post[%d] text = %v, want %q (each post keeps its own resolved text)", i, got, wantTexts[i])
		}
		if got := m["publication_when_type"]; got != float64(1) {
			t.Errorf("post[%d] publication_when_type = %v, want 1", i, got)
		}
		if got := m["as_copy"]; got != float64(1) {
			t.Errorf("post[%d] as_copy = %v, want 1", i, got)
		}
	}
	if len(res.PerPost) != 3 {
		t.Fatalf("res.PerPost len = %d, want 3 (every attempted post recorded)", len(res.PerPost))
	}
	for i, oc := range res.PerPost {
		if oc.Status != "created" {
			t.Errorf("PerPost[%d].Status = %q, want created", i, oc.Status)
		}
	}
	if len(res.IDs) != 3 || res.CreatedNoID != 0 {
		t.Errorf("res.IDs = %v CreatedNoID = %d, want 3 ids and 0", res.IDs, res.CreatedNoID)
	}
}

// TestRewriteSearchPost_BatchPreservesCallerOrder pins that a batch keeps the
// caller's id order on the wire — schedule slots bind positionally, so a
// sorted or deduplicated list would publish the wrong post into each slot.
func TestRewriteSearchPost_BatchPreservesCallerOrder(t *testing.T) {
	srv, st := newSearchStubServer(t, "x")
	c := newTestClient(t, srv.URL)

	_, err := RewriteSearchPost(context.Background(), c, RewriteSearchPostInput{
		SearchPostIDs: pids(2003, 2001, 2002),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(1),
			SelectedPagesIDs:    pids(123),
		},
	})
	if err != nil {
		t.Fatalf("RewriteSearchPost: %v", err)
	}
	posts := st.postBodies()
	if len(posts) != 3 {
		t.Fatalf("POST count = %d, want 3", len(posts))
	}
	want := []string{"2003", "2001", "2002"}
	for i, raw := range posts {
		if got := decodePostBody(t, raw)["ids"]; got != want[i] {
			t.Errorf("post[%d] ids = %v, want %s (caller order preserved)", i, got, want[i])
		}
	}
}

// TestCopySearchPost_WhenType3SendsSchedules is the _OK pair to the F1
// refusal: under when-type 3 the schedules DO reach the wire (the guard is
// not a refuse-everything guard — the mutation that refuses ALL schedules
// leaves the suite green without this).
func TestCopySearchPost_WhenType3SendsSchedules(t *testing.T) {
	srv, st := newSearchStubServer(t, "x")
	c := newTestClient(t, srv.URL)

	_, err := CopySearchPost(context.Background(), c, CopySearchPostInput{
		SearchPostID: pint(1001),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(3),
			SchedulesIDs:        pids(10, 11),
		},
	})
	if err != nil {
		t.Fatalf("CopySearchPost: %v", err)
	}
	posts := st.postBodies()
	if len(posts) != 1 {
		t.Fatalf("POST count = %d, want 1", len(posts))
	}
	m := decodePostBody(t, posts[0])
	if got := m["publication_when_type"]; got != float64(3) {
		t.Errorf("publication_when_type = %v, want 3", got)
	}
	sched, _ := m["schedules_ids"].([]interface{})
	if len(sched) != 2 || sched[0] != float64(10) || sched[1] != float64(11) {
		t.Errorf("schedules_ids = %v, want [10 11] — the schedules must reach the wire under when-type 3", sched)
	}
}

// F3 — a `search import --when-type 2 --date ... --to <page>` produces THE
// SAME request body on both surfaces. The op-level pin is the golden below;
// the two adapter tests (TestF3_CLIImportBodyMatchesMCPTool in cmd/hooppy,
// TestF3_MCPImportBodyMatchesCLI in cmd/hooppy-mcp) assert the identical
// literal from the recorded request each surface emitted. Byte-equality is
// then a property of the shared op — the mutation that drops
// publication_date from the body goes RED on both sides.
//
// goldenImportWhenType2Body is the exact POST /posts body for:
//
//	search import --post-id 1001 --when-type 2 --date 01.02.2027 \
//	    --hours 10 --minutes 30 --to 99
//
// (publication_how_type 2 is import's own default — the op applies it when
// the caller omits the field, so an agent never sees a 0 on the wire).
const goldenImportWhenType2Body = `{"as_copy":1,"publication_when_type":2,"publication_how_type":2,"publication_where_type":1,"selected_pages_ids":[99],"schedules_ids":[],"publication_date":{"date":"01.02.2027","hours":"10","minutes":"30"},"texts":[{"text":"x","source_id":0}],"attachments":[],"ids":"1001"}`

func TestF3_ImportWhenType2_DatePages_GoldenBody(t *testing.T) {
	srv, st := newSearchStubServer(t, "x")
	c := newTestClient(t, srv.URL)

	_, err := ImportSearchPost(context.Background(), c, ImportSearchPostInput{
		SearchPostID: pint(1001),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(2),
			SelectedPagesIDs:    pids(99),
			PublishDate:         pstr("01.02.2027"),
			PublishHours:        pstr("10"),
			PublishMinutes:      pstr("30"),
		},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost: %v", err)
	}
	posts := st.postBodies()
	if len(posts) != 1 {
		t.Fatalf("POST count = %d, want 1", len(posts))
	}
	// The client's request encoder appends one trailing "\n" to every body
	// (client.go json.NewEncoder().Encode) — identical framing on both
	// surfaces, so the document-level comparison trims exactly that one.
	if got := strings.TrimSuffix(string(posts[0]), "\n"); got != goldenImportWhenType2Body {
		t.Errorf("POST /posts body =\n%s\nwant byte-equal\n%s\n— CLI and MCP emit the identical body because the op builds it (F3)", got, goldenImportWhenType2Body)
	}
}

// TestImportSearchPost_HygieneInOutcomes pins that the op records the VK
// markup and ad-marker findings on the ORIGINAL resolved text (before the
// strip runs), while the WIRE carries the stripped text.
func TestImportSearchPost_HygieneInOutcomes(t *testing.T) {
	srv, st := newSearchStubServerMap(t, map[int]string{
		1001: "See [https://vk.com/x|Заголовок] here\nРеклама. ООО «Р». ИНН 1. Erid: 2Vtz",
	}, "")
	c := newTestClient(t, srv.URL)

	res, err := ImportSearchPost(context.Background(), c, ImportSearchPostInput{
		SearchPostID:  pint(1001),
		StripVKMarkup: pbool(true),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(1),
			SelectedPagesIDs:    pids(99),
		},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost: %v", err)
	}
	if !res.StripVKMarkup {
		t.Errorf("res.StripVKMarkup = false, want true (the result states which transformation ran)")
	}
	if len(res.PerPost) != 1 {
		t.Fatalf("PerPost len = %d, want 1", len(res.PerPost))
	}
	oc := res.PerPost[0]
	if len(oc.VKMarkup) != 1 || oc.VKMarkup[0] != "[https://vk.com/x|Заголовок]" {
		t.Errorf("VKMarkup = %v, want the raw marker (findings are on the ORIGINAL text, before strip)", oc.VKMarkup)
	}
	if len(oc.AdMarkers) != 1 || !strings.Contains(oc.AdMarkers[0], "Erid") {
		t.Errorf("AdMarkers = %v, want the disclosure line", oc.AdMarkers)
	}
	posts := st.postBodies()
	if len(posts) != 1 {
		t.Fatalf("POST count = %d, want 1", len(posts))
	}
	texts, _ := decodePostBody(t, posts[0])["texts"].([]interface{})
	got := texts[0].(map[string]interface{})["text"].(string)
	if strings.Contains(got, "[https://vk.com") || !strings.HasPrefix(got, "See Заголовок here") {
		t.Errorf("wire text = %q, want the VK markup stripped (ad markers kept — they are never auto-removed)", got)
	}
	if !strings.Contains(got, "Erid") {
		t.Errorf("wire text = %q dropped the advertising disclosure — ad markers warn but are NEVER auto-removed", got)
	}
}

// TestImportSearchPost_PartialBatchContract pins the three-outcome contract:
// a mid-batch publish failure leaves the already-published posts in res
// (IDs populated, per_post records all three) and returns *PartialPostError.
func TestImportSearchPost_PartialBatchContract(t *testing.T) {
	st := &searchStub{}
	var postN atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.requests++
		st.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"x","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"texts":[{"text":"t","source_id":0}],"attachments":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			n := postN.Add(1)
			st.mu.Lock()
			st.posts = append(st.posts, []byte{})
			st.mu.Unlock()
			if n == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message":"boom"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%d}`, 9000+n)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	res, err := ImportSearchPost(context.Background(), c, ImportSearchPostInput{
		SearchPostIDs: pids(11, 22, 33),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(1),
			SelectedPagesIDs:    pids(1),
		},
	})
	var ppe *hooppy.PartialPostError
	if !errors.As(err, &ppe) {
		t.Fatalf("err = %v (%T), want *hooppy.PartialPostError — partial must be type-discriminated from both success and total failure", err, err)
	}
	if res == nil {
		t.Fatal("res is nil on a partial batch — the already-published posts must not be discarded")
	}
	if len(res.IDs) != 2 {
		t.Errorf("res.IDs = %v, want the 2 ids that landed", res.IDs)
	}
	if len(res.PerPost) != 3 || res.PerPost[0].Status != "created" || res.PerPost[1].Status != "failed" || res.PerPost[2].Status != "created" {
		t.Errorf("PerPost = %+v, want [created, failed(22), created]", res.PerPost)
	}
}

// TestImportSearchPost_AllFailedBatch_PlainErrorNotPartial pins the other
// half of the contract: an EVERY-post-failed batch is a plain error (NOT
// *PartialPostError — the type a caller reads as "some succeeded"), while
// res still carries the per_post record of what was attempted.
func TestImportSearchPost_AllFailedBatch_PlainErrorNotPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			w.Write([]byte(`{"id":"x","texts":[{"text":"t","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"boom"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	res, err := ImportSearchPost(context.Background(), c, ImportSearchPostInput{
		SearchPostIDs: pids(11, 22),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(1),
			SelectedPagesIDs:    pids(1),
		},
	})
	if err == nil {
		t.Fatal("err = nil for an all-failed batch")
	}
	var ppe *hooppy.PartialPostError
	if errors.As(err, &ppe) {
		t.Fatalf("err is *PartialPostError for an ALL-failed batch — a caller type-asserting partial reads 'some succeeded' when nothing did (MAJOR 5)")
	}
	if !strings.Contains(err.Error(), "every post") {
		t.Errorf("error should name the total failure, got: %v", err)
	}
	if res == nil || len(res.PerPost) != 2 {
		t.Fatalf("res.PerPost = %+v, want the 2 attempted records even on total failure (stdout is the dedup record)", res)
	}
}

// TestImportSearchPost_SingleCreateNoID pins the third bucket on the
// single-post path: a create the server accepted while omitting the id is a
// SUCCESS with an unaddressable handle (created_no_id), never a failure —
// reporting it failed invites a duplicate-spawning re-run.
func TestImportSearchPost_SingleCreateNoID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts" {
			w.Write([]byte(`{}`)) // 2xx, no id
			return
		}
		w.Write([]byte(`{"id":"7001","texts":[{"text":"t"}],"attachments":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	res, err := ImportSearchPost(context.Background(), c, ImportSearchPostInput{
		SearchPostID: pint(7001),
		PublishSearchTarget: PublishSearchTarget{
			PublicationWhenType: pint(1),
			SelectedPagesIDs:    pids(1),
		},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil — a zero-id create is not a failure", err)
	}
	if res.CreatedNoID != 1 {
		t.Errorf("res.CreatedNoID = %d, want 1", res.CreatedNoID)
	}
	if len(res.PerPost) != 1 || res.PerPost[0].Status != "created_no_id" {
		t.Fatalf("PerPost = %+v, want one created_no_id record", res.PerPost)
	}
	if len(res.IDs) != 0 {
		t.Errorf("res.IDs = %v, want empty — a zero id must NOT be recorded as a real handle (issue #131)", res.IDs)
	}
}

// TestCopySearchPost_OmittedHowTypeDefaults pins the op-owned default: an
// omitted publication_how_type reaches the wire as 1 for copy/rewrite and 2
// for import (previously an omitted MCP field went to the wire as 0).
func TestPublishSearch_OmittedHowTypeDefaults(t *testing.T) {
	srv, st := newSearchStubServer(t, "x")
	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	if _, err := CopySearchPost(ctx, c, CopySearchPostInput{
		SearchPostID:        pint(1001),
		PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(1)},
	}); err != nil {
		t.Fatalf("CopySearchPost: %v", err)
	}
	if _, err := ImportSearchPost(ctx, c, ImportSearchPostInput{
		SearchPostID:        pint(1002),
		PublishSearchTarget: PublishSearchTarget{PublicationWhenType: pint(1), SelectedPagesIDs: pids(1)},
	}); err != nil {
		t.Fatalf("ImportSearchPost: %v", err)
	}
	posts := st.postBodies()
	if len(posts) != 2 {
		t.Fatalf("POST count = %d, want 2", len(posts))
	}
	if got := decodePostBody(t, posts[0])["publication_how_type"]; got != float64(1) {
		t.Errorf("copy publication_how_type = %v, want 1 (op default — never 0 on the wire)", got)
	}
	if got := decodePostBody(t, posts[1])["publication_how_type"]; got != float64(2) {
		t.Errorf("import publication_how_type = %v, want 2 (import's own default)", got)
	}
}
