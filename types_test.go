package hooppy

import (
	"encoding/json"
	"testing"
)

func TestSourceID_String_KnownIDs(t *testing.T) {
	cases := []struct {
		id   SourceID
		want string
	}{
		{SourceVK, "vkontakte"},
		{SourceOK, "odnoklassniki"},
		{SourceFacebook, "facebook"},
		{SourceTwitter, "twitter"},
		{SourcePinterest, "pinterest"},
		{SourceTelegramChan, "telegram_channel"},
		{SourceTelegramAcc, "telegram_account"},
		{SourceInstagram, "instagram"},
		{SourceYouTube, "youtube"},
		{SourceLinkedIn, "linkedin"},
		{SourceTikTok, "tiktok"},
		{SourceThreads, "threads"},
		{SourceMax, "max"},
	}
	for _, tc := range cases {
		if got := tc.id.String(); got != tc.want {
			t.Errorf("SourceID(%d).String() = %q, want %q", int(tc.id), got, tc.want)
		}
	}
}

func TestSourceID_String_UnknownID(t *testing.T) {
	if got := SourceID(999).String(); got != "unknown" {
		t.Errorf("SourceID(999).String() = %q, want %q", got, "unknown")
	}
}

func TestSourceID_AllConstantsHaveNames(t *testing.T) {
	all := []SourceID{
		SourceVK, SourceOK, SourceFacebook, SourceTwitter, SourceMyWorld,
		SourcePinterest, SourceTumblr, SourceTelegramChan, SourceInstagramFB,
		SourceTelegramAcc, SourceDzen, SourceTikTok, SourceYouTube, SourceLinkedIn,
		SourceWhatsApp, SourceRutube, SourceInstagram, SourceYappy, SourceMax,
		SourceThreads, SourceVKChats,
	}
	for _, id := range all {
		if id.String() == "unknown" {
			t.Errorf("SourceID(%d) has no name in sourceNames map", int(id))
		}
	}
}

func TestPostPublishNowPayload_RoundTrip(t *testing.T) {
	orig := PostPublishNowPayload{
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{100, 200, 300},
		Texts:               []PostText{{Text: "hello world", SourceID: 0}},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PostPublishNowPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.PublicationWhenType != orig.PublicationWhenType {
		t.Errorf("PublicationWhenType mismatch")
	}
	if len(decoded.SelectedPagesIDs) != len(orig.SelectedPagesIDs) {
		t.Errorf("SelectedPagesIDs length mismatch")
	}
	if len(decoded.Texts) != 1 || decoded.Texts[0].Text != "hello world" {
		t.Errorf("Texts mismatch")
	}
}

func TestPostPublishAtPayload_RoundTrip(t *testing.T) {
	orig := PostPublishAtPayload{
		PublicationWhenType: 2,
		PublicationHowType:  1,
		PublicationDate: PublicationDate{
			Date:    "15.06.2026",
			Hours:   "14",
			Minutes: "30",
		},
		SelectedPagesIDs: []int{1, 2},
		Texts:            []PostText{{Text: "scheduled", SourceID: 6}},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PostPublishAtPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.PublicationDate.Date != "15.06.2026" {
		t.Errorf("Date mismatch: %q", decoded.PublicationDate.Date)
	}
	if decoded.PublicationDate.Hours != "14" {
		t.Errorf("Hours mismatch: %q", decoded.PublicationDate.Hours)
	}
}

func TestBatchDeletePostsRequest_RoundTrip(t *testing.T) {
	orig := BatchDeletePostsRequest{IDs: "1,2,3,4,5"}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded BatchDeletePostsRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.IDs != "1,2,3,4,5" {
		t.Errorf("IDs = %q, want %q", decoded.IDs, "1,2,3,4,5")
	}
}

func TestAccountsResponse_RoundTrip(t *testing.T) {
	orig := AccountsResponse{
		TotalRows: 15,
		IsHasMore: true,
		List:      []Account{{ID: 1, SourceID: 6, SocialAccountID: "3251"}},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded AccountsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.TotalRows != 15 {
		t.Errorf("TotalRows = %d, want 15", decoded.TotalRows)
	}
	if len(decoded.List) != 1 || decoded.List[0].SocialAccountID != "3251" {
		t.Errorf("List mismatch")
	}
}

func TestPage_RoundTrip(t *testing.T) {
	orig := Page{
		ID:             123456,
		SourceID:       3,
		AccountID:      7890,
		SocialPageID:   "123456789012345",
		SocialPageName: "Test Page",
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Page
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SocialPageID != "123456789012345" {
		t.Errorf("SocialPageID = %q, want string", decoded.SocialPageID)
	}
	if decoded.SocialPageName != "Test Page" {
		t.Errorf("SocialPageName = %q", decoded.SocialPageName)
	}
}

// TestSearchPost_ParseMetrics covers issue #62: the server sends Likes,
// Reposts, Views, Comments and Involvement as display-formatted strings
// (e.g. "334,881", "0.520"). The fields stay string (the separator is not
// guaranteed to be a comma across locales, and a silent parse failure on an
// unexpected separator would be worse than the current honest string), but
// parse accessors let callers compute. The table includes a separated
// integer, a bare integer, a decimal ratio, an empty string and a malformed
// value — malformed input MUST return an error rather than a silent 0,
// because a helper that returns 0 on failure recreates the exact
// silent-wrongness this issue is about ("334,881" < "87,008" is true as a
// string comparison).
func TestSearchPost_ParseMetrics(t *testing.T) {
	intCases := []struct {
		name  string
		field string
		want  int
	}{
		{"separated integer", "334,881", 334881},
		{"bare integer", "864", 864},
		// 4+ digit ungrouped integers MUST be accepted: the prior regex
		// capped the ungrouped branch at [1-9]\d{0,2}, rejecting "1000"
		// and "334881" — legitimate plain integers the function's own doc
		// comment and error text promise to accept. Without these cases the
		// false-rejection bug is invisible (issue #65 item 1).
		{"four-digit ungrouped", "1000", 1000},
		{"six-digit ungrouped", "334881", 334881},
		{"zero", "0", 0},
		{"empty string", "", 0},
		{"malformed", "12abc", -1}, // -1 sentinel: expect an error, not 0
		// Signed values are parsed FAITHFULLY on the response side (issue
		// #65 item 3): the server is the source of truth, so a server-sent
		// "-5" likes parses to -5 rather than being clamped or rejected.
		// This is deliberately asymmetric with the request side, which
		// rejects negative IDs/pages. A caller wanting a domain check
		// applies it on the returned int.
		{"signed negative", "-5", -5},
		{"signed positive", "+864", 864},
		// Decimal-comma / non-thousands-grouped comma forms MUST error: the
		// vendor is a Russian-language service where comma is the decimal
		// separator, and stripping the comma before Atoi would silently turn
		// "1,2,3" into 123 with no error (issue #65 item 1).
		{"comma-grouped malformed", "1,2,3", -1},
	}
	parsers := []struct {
		name string
		fn   func(p SearchPost) (int, error)
		set  func(p *SearchPost, v string)
	}{
		{"ViewsInt", func(p SearchPost) (int, error) { return p.ViewsInt() }, func(p *SearchPost, v string) { p.Views = v }},
		{"LikesInt", func(p SearchPost) (int, error) { return p.LikesInt() }, func(p *SearchPost, v string) { p.Likes = v }},
		{"RepostsInt", func(p SearchPost) (int, error) { return p.RepostsInt() }, func(p *SearchPost, v string) { p.Reposts = v }},
		{"CommentsInt", func(p SearchPost) (int, error) { return p.CommentsInt() }, func(p *SearchPost, v string) { p.Comments = v }},
	}
	for _, pc := range parsers {
		for _, tc := range intCases {
			t.Run(pc.name+"/"+tc.name, func(t *testing.T) {
				var p SearchPost
				pc.set(&p, tc.field)
				got, err := pc.fn(p)
				if tc.want == -1 {
					// malformed: an error is REQUIRED, and it must NOT
					// silently return 0 (the false-confidence failure mode).
					if err == nil {
						t.Fatalf("%s(%q): expected an error, got nil (result=%d) — a silent 0 on malformed input is exactly the wrongness this accessor exists to prevent", pc.name, tc.field, got)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s(%q): unexpected error: %v", pc.name, tc.field, err)
				}
				if got != tc.want {
					t.Errorf("%s(%q) = %d, want %d", pc.name, tc.field, got, tc.want)
				}
			})
		}
	}

	floatCases := []struct {
		name  string
		field string
		want  float64
	}{
		{"decimal ratio", "0.520", 0.520},
		{"separated decimal ratio", "1,234.56", 1234.56},
		// A 4+-digit integer-part decimal MUST be accepted: the prior regex
		// capped the ungrouped branch at [1-9]\d{0,2}, so "1234.56" was
		// rejected — a legitimate value the function's doc comment promises
		// to accept. Without this case the false-rejection bug is invisible
		// (issue #65 item 1).
		{"ungrouped 4-digit-int decimal", "1234.56", 1234.56},
		{"bare integer ratio", "2", 2.0},
		{"zero", "0", 0},
		{"empty string", "", 0},
		{"malformed", "0.5abc", -1}, // -1 sentinel: expect an error
		// Signed involvement is parsed FAITHFULLY (issue #65 item 3): a
		// server-sent negative ratio is returned as-is, not clamped — see
		// the signed-value asymmetry note on the parse accessors.
		{"signed negative ratio", "-0.5", -0.5},
		// Decimal-comma forms MUST error, not silently parse to a 1000×-wrong
		// value: in the vendor's Russian locale "0,520" is the ratio 0.520,
		// but stripping the comma yields "0520" → 520.0 with err==nil — the
		// exact silent-wrongness class this accessor exists to prevent
		// (issue #65 item 1). A space-separated thousands form is also
		// rejected (the strip only handles commas).
		{"decimal comma", "0,520", -1},
		{"space-separated thousands", "1 234", -1},
	}
	for _, tc := range floatCases {
		t.Run("InvolvementFloat/"+tc.name, func(t *testing.T) {
			var p SearchPost
			p.Involvement = tc.field
			got, err := p.InvolvementFloat()
			if tc.want == -1 {
				if err == nil {
					t.Fatalf("InvolvementFloat(%q): expected an error, got nil (result=%v) — a silent 0 on malformed input is exactly the wrongness this accessor exists to prevent", tc.field, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("InvolvementFloat(%q): unexpected error: %v", tc.field, err)
			}
			if got != tc.want {
				t.Errorf("InvolvementFloat(%q) = %v, want %v", tc.field, got, tc.want)
			}
		})
	}
}
