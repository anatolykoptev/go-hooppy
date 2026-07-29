package hooppy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		{SourceInstagram, "instagram"},
		{SourceTelegram, "telegram"},
		{SourceTelegramAcc, "telegram_account"},
		{SourceInstagramFB, "instagram_fb"},
		{SourceYouTube, "youtube"},
		{SourceLinkedIn, "linkedin"},
		{SourceTikTok, "tiktok"},
		{SourceViber, "viber"},
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
		SourcePinterest, SourceInstagram, SourceTumblr, SourceTelegram,
		SourceInstagramFB, SourceTelegramAcc, SourceDzen, SourceTikTok,
		SourceViber, SourceYouTube, SourceLinkedIn, SourceWhatsApp, SourceRutube,
		SourceMax, SourceYappy, SourceThreads, SourceVKChats,
		SourceTelegramChan, // deprecated alias — must still resolve
	}
	for _, id := range all {
		if id.String() == "unknown" {
			t.Errorf("SourceID(%d) has no name in sourceNames map", int(id))
		}
	}
}

// TestSourceNames_Bijective enforces that sourceNames is a bijection: no
// name maps to two different ids, and no id maps to two names. This catches
// the class of bug where two vendor tables are merged carelessly and a
// single network name (e.g. "instagram") ends up pointing at two ids
// (e.g. 7 and 29) — the report would render contradictory network names
// for the same network depending on which connection method the row used.
func TestSourceNames_Bijective(t *testing.T) {
	// id → name: Go maps already enforce that no key appears twice, so the
	// "no id to two names" direction is structurally guaranteed. We still
	// check it explicitly for documentation.
	seenID := make(map[SourceID]string, len(sourceNames))
	for id, name := range sourceNames {
		if prev, ok := seenID[id]; ok {
			t.Errorf("id %d maps to two names: %q and %q", int(id), prev, name)
		}
		seenID[id] = name
	}
	// name → id: this is the direction that can silently break when two
	// tables are merged. A name pointing at two ids means the doctor report
	// would render the same network name for two different source_ids,
	// hiding that they are distinct connection methods.
	seenName := make(map[string]SourceID, len(sourceNames))
	for id, name := range sourceNames {
		if prev, ok := seenName[name]; ok {
			t.Errorf("name %q maps to two ids: %d and %d — no name may map to two ids", name, int(prev), int(id))
		}
		seenName[name] = id
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

// TestPost_DecodeFullRow verifies a realistic GET /posts row decodes with
// every modelled field populated and the values round-trip — not just the
// absence of an unmarshal error. This is the RED-on-revert test: shrinking
// Post back to id-only makes every assertion below the first non-id field
// fail. It also covers the "posts list output contains the text and the
// publication slot, not just ids" requirement: the CLI marshals the
// decoded Post, so the struct carrying Text and PublicationDate is what
// puts them in the output.
func TestPost_DecodeFullRow(t *testing.T) {
	// Metrics are Metric: prove the decode accepts a STRING value
	// (the SearchPost "334,881" thousands-separated shape) AND a NUMBER
	// value, since the own-post surface's metric type is unverified.
	row := `{
		"id": 123456,
		"text": "Весенний салат с редисом",
		"publication_date": {"date": "29 Июля", "time": "12:25", "timestamp": 1753770300, "source_timestamp": 1753773900},
		"is_published": 1,
		"is_ad": 0,
		"is_repeated": 0,
		"is_attachments_in_process": 0,
		"is_planned_by_networks": 1,
		"is_planning_by_networks_needed": 0,
		"views": "1 234,881",
		"likes": "456",
		"comments": "78",
		"reposts": "12",
		"link": "https://vk.com/wall-1_2",
		"source_link": "https://example.com/source",
		"repost_link": "https://vk.com/wall-3_4",
		"repost_title": "Оригинал",
		"photo": {"id": 10, "owner_id": 20, "post_id": "30", "access_key": "k", "source_id": 1, "type": "video", "title": "T", "description": "", "duration": 383, "preview": "https://example.invalid/x"},
		"photos_amount": 3,
		"pages": [{"id": 44567, "source_id": 1, "account_id": 33125, "social_page_id": "999", "social_page_name": "Группа", "social_page_photo": "https://pp.vk.me/p.jpg"}],
		"post_schedules": [{"id": 101820, "name": "Утро"}],
		"post_projects": [{"id": 92384, "name": "Рецепты"}],
		"created_by": 42,
		"errors_for_source_ids": [{"source_id": 1}]
	}`
	var p Post
	if err := json.Unmarshal([]byte(row), &p); err != nil {
		t.Fatalf("unmarshal full row: %v", err)
	}
	if p.ID != 123456 {
		t.Errorf("ID = %d, want 123456", p.ID)
	}
	if p.Text != "Весенний салат с редисом" {
		t.Errorf("Text = %q, want the post body", p.Text)
	}
	if p.PublicationDate == nil {
		t.Fatal("PublicationDate = nil, want the slot object")
	}
	if p.PublicationDate.Date != "29 Июля" {
		t.Errorf("PublicationDate.Date = %q, want %q", p.PublicationDate.Date, "29 Июля")
	}
	if p.PublicationDate.Time != "12:25" {
		t.Errorf("PublicationDate.Time = %q, want %q", p.PublicationDate.Time, "12:25")
	}
	if p.PublicationDate.Timestamp != 1753770300 {
		t.Errorf("PublicationDate.Timestamp = %d, want 1753770300", p.PublicationDate.Timestamp)
	}
	// Both timestamps are kept and differ — do not collapse them.
	if p.PublicationDate.SourceTimestamp != 1753773900 {
		t.Errorf("PublicationDate.SourceTimestamp = %d, want 1753773900", p.PublicationDate.SourceTimestamp)
	}
	if p.PublicationDate.Timestamp == p.PublicationDate.SourceTimestamp {
		t.Error("Timestamp and SourceTimestamp are equal — they should differ (one carries a tz offset)")
	}
	if p.IsPublished != 1 {
		t.Errorf("IsPublished = %d, want 1", p.IsPublished)
	}
	if p.IsAd != 0 {
		t.Errorf("IsAd = %d, want 0", p.IsAd)
	}
	if p.IsAttachmentsInProcess != 0 {
		t.Errorf("IsAttachmentsInProcess = %d, want 0", p.IsAttachmentsInProcess)
	}
	if p.IsPlannedByNetworks != 1 {
		t.Errorf("IsPlannedByNetworks = %d, want 1", p.IsPlannedByNetworks)
	}
	if p.IsPlanningByNetworksNeeded != 0 {
		t.Errorf("IsPlanningByNetworksNeeded = %d, want 0", p.IsPlanningByNetworksNeeded)
	}
	if p.IsRepeated != 0 {
		t.Errorf("IsRepeated = %d, want 0", p.IsRepeated)
	}
	if !p.Views.IsSet() || p.Views.String() != "1 234,881" {
		t.Errorf("Views = %+v, want the string metric verbatim", p.Views)
	}
	if !p.Likes.IsSet() || p.Likes.String() != "456" {
		t.Errorf("Likes = %+v, want %q", p.Likes, "456")
	}
	if p.Link != "https://vk.com/wall-1_2" {
		t.Errorf("Link = %q", p.Link)
	}
	if p.SourceLink != "https://example.com/source" {
		t.Errorf("SourceLink = %q", p.SourceLink)
	}
	if p.RepostLink != "https://vk.com/wall-3_4" {
		t.Errorf("RepostLink = %q", p.RepostLink)
	}
	if p.RepostTitle != "Оригинал" {
		t.Errorf("RepostTitle = %q", p.RepostTitle)
	}
	if p.Photo == nil {
		t.Fatal("Photo = nil, want the media-descriptor object")
	}
	if p.Photo.PostID != "30" || p.Photo.ID != 10 || p.Photo.OwnerID != 20 {
		t.Errorf("Photo = %+v, want id 10 / owner_id 20 / post_id \"30\"", p.Photo)
	}
	if p.Photo.Type != "video" {
		t.Errorf("Photo.Type = %q, want %q (not photo-specific)", p.Photo.Type, "video")
	}
	if p.PhotosAmount != 3 {
		t.Errorf("PhotosAmount = %d, want 3", p.PhotosAmount)
	}
	if len(p.Pages) != 1 || p.Pages[0].ID != 44567 || p.Pages[0].SocialPageName != "Группа" {
		t.Errorf("Pages = %+v, want one page with id 44567", p.Pages)
	}
	if p.CreatedBy != 42 {
		t.Errorf("CreatedBy = %d, want 42", p.CreatedBy)
	}
	if len(p.PostSchedules) != 1 || p.PostSchedules[0].ID != 101820 || p.PostSchedules[0].Name != "Утро" {
		t.Errorf("PostSchedules = %+v, want one {id:101820,name:Утро}", p.PostSchedules)
	}
	if len(p.PostProjects) != 1 || p.PostProjects[0].ID != 92384 || p.PostProjects[0].Name != "Рецепты" {
		t.Errorf("PostProjects = %+v, want one {id:92384,name:Рецепты}", p.PostProjects)
	}
	if len(p.ErrorsForSourceIDs) != 1 {
		t.Errorf("ErrorsForSourceIDs = %d items, want 1 (array with one item)", len(p.ErrorsForSourceIDs))
	}

	// Metrics as NUMBERS must also decode (Metric tolerates null, string,
	// AND number — a typed string/int field would fail one of the shapes,
	// the same bug class as the photo field).
	numRow := `{"id": 7, "views": 1234, "likes": 56, "comments": 7, "reposts": 1}`
	var pn Post
	if err := json.Unmarshal([]byte(numRow), &pn); err != nil {
		t.Fatalf("unmarshal numeric-metric row: %v", err)
	}
	if !pn.Views.IsSet() || pn.Views.String() != "1234" {
		t.Errorf("numeric Views = %+v, want 1234", pn.Views)
	}

	// Metrics as NULL must decode to an unset Metric (not abort the decode).
	nullRow := `{"id": 8, "views": null, "likes": null, "comments": null, "reposts": null}`
	var pnul Post
	if err := json.Unmarshal([]byte(nullRow), &pnul); err != nil {
		t.Fatalf("unmarshal null-metric row: %v", err)
	}
	if pnul.Views.IsSet() {
		t.Errorf("null Views = %+v, want unset (null → IsSet false)", pnul.Views)
	}
}

// TestPost_DecodeMissingOptionalFields verifies a row that omits optional
// fields (an unpublished post with no slot, no pages, no metrics) decodes
// without error — the API omits keys depending on post state.
func TestPost_DecodeMissingOptionalFields(t *testing.T) {
	row := `{"id": 99, "text": "черновик", "is_published": 0}`
	var p Post
	if err := json.Unmarshal([]byte(row), &p); err != nil {
		t.Fatalf("unmarshal sparse row: %v", err)
	}
	if p.ID != 99 {
		t.Errorf("ID = %d, want 99", p.ID)
	}
	if p.Text != "черновик" {
		t.Errorf("Text = %q, want черновик", p.Text)
	}
	if p.PublicationDate != nil {
		t.Errorf("PublicationDate = %+v, want nil for an unpublished post", p.PublicationDate)
	}
	if len(p.Pages) != 0 {
		t.Errorf("Pages = %d, want empty", len(p.Pages))
	}
	if p.Views.IsSet() {
		t.Errorf("Views = %+v, want unset when absent", p.Views)
	}
}

// TestPost_DecodeCredentialHygiene verifies that nested page objects
// carrying OAuth tokens (access_token, bot_token, refresh_token, password,
// wp_app_password, access_token_secret) cannot reach the marshalled Post
// output. Post.Pages reuses the narrow Page struct, which models only
// id/source/social-ids/name/photo — the token fields are silently dropped
// at decode and therefore absent from any re-marshal. This is the
// RED-on-revert test for the credential-hygiene invariant: if Post.Pages
// were changed to map[string]interface{} or json.RawMessage, the token
// values would leak through to stdout via printJSON.
func TestPost_DecodeCredentialHygiene(t *testing.T) {
	row := `{
		"id": 1,
		"text": "x",
		"pages": [
			{
				"id": 44567, "source_id": 1, "account_id": 33125,
				"social_page_id": "999", "social_page_name": "Группа",
				"social_page_photo": "https://pp.vk.me/p.jpg",
				"access_token": "SECRET_ACCESS_TOKEN_VALUE",
				"bot_token": "SECRET_BOT_TOKEN_VALUE",
				"refresh_token": "SECRET_REFRESH_TOKEN_VALUE",
				"password": "SECRET_PASSWORD_VALUE",
				"wp_app_password": "SECRET_WP_APP_PASSWORD_VALUE",
				"access_token_secret": "SECRET_ACCESS_TOKEN_SECRET_VALUE"
			}
		]
	}`
	var p Post
	if err := json.Unmarshal([]byte(row), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	// The token VALUES must be absent from the marshalled output — not just
	// the field names (which Page doesn't model), but the secrets themselves,
	// so a credential cannot reach stdout via printJSON.
	secretValues := []string{
		"SECRET_ACCESS_TOKEN_VALUE",
		"SECRET_BOT_TOKEN_VALUE",
		"SECRET_REFRESH_TOKEN_VALUE",
		"SECRET_PASSWORD_VALUE",
		"SECRET_WP_APP_PASSWORD_VALUE",
		"SECRET_ACCESS_TOKEN_SECRET_VALUE",
	}
	for _, secret := range secretValues {
		if strings.Contains(got, secret) {
			t.Errorf("marshalled Post leaked credential value %q:\n%s", secret, got)
		}
	}
	// The modelled page fields must still be present (the narrow struct
	// kept the safe fields while dropping the tokens).
	if !strings.Contains(got, `"social_page_name":"Группа"`) {
		t.Errorf("marshalled Post lost the safe page field social_page_name:\n%s", got)
	}
	if len(p.Pages) != 1 || p.Pages[0].ID != 44567 {
		t.Errorf("Pages = %+v, want one page with id 44567", p.Pages)
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

// TestPost_DecodeRealCapture is the regression guard built from a REAL
// captured GET /posts response shape (testdata/post_list_row.json), not a
// hand-written fixture. IDs → small integers, names → "A", URLs →
// example.invalid, but every KEY and every VALUE TYPE is exactly as the
// server sends them — including the null metrics on an unpublished post and
// the string post_id inside the numeric-id photo object.
//
// This is the RED-on-revert test for the photo-type bug: Post.Photo was
// typed string but the API sends an object, which aborted the entire
// unmarshal. A hand-typed fixture encoded the same wrong guess, so the suite
// was green over a command that could not run. This capture cannot lie about
// the shape because it was recorded from the wire.
func TestPost_DecodeRealCapture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "post_list_row.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var resp PostsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal real capture: %v\n— a wrong field type aborts the whole decode (the photo=string bug class)", err)
	}
	if len(resp.List) != 1 {
		t.Fatalf("len(List) = %d, want 1", len(resp.List))
	}
	p := resp.List[0]

	// The command must return rows with text and the publication slot —
	// these two are the user-visible requirement.
	if p.Text != "A" {
		t.Errorf("Text = %q, want %q", p.Text, "A")
	}
	if p.PublicationDate == nil {
		t.Fatal("PublicationDate = nil, want the slot object")
	}
	if p.PublicationDate.Date != "29 Июля" || p.PublicationDate.Time != "12:25" {
		t.Errorf("PublicationDate = %+v, want date 29 Июля / time 12:25", p.PublicationDate)
	}

	// photo is an OBJECT — the crash field. Must decode into a struct, not a string.
	if p.Photo == nil {
		t.Fatal("Photo = nil, want the media-descriptor object")
	}
	if p.Photo.PostID != "5" {
		t.Errorf("Photo.PostID = %q, want %q (string while id/owner_id are numbers)", p.Photo.PostID, "5")
	}
	if p.Photo.ID != 3 || p.Photo.OwnerID != 4 {
		t.Errorf("Photo.ID/OwnerID = %d/%d, want 3/4 (numbers)", p.Photo.ID, p.Photo.OwnerID)
	}
	if p.Photo.Type != "video" {
		t.Errorf("Photo.Type = %q, want %q (not photo-specific despite the field name)", p.Photo.Type, "video")
	}

	// Metrics are null on an unpublished post — must not abort the decode.
	for name, m := range map[string]Metric{
		"Views": p.Views, "Likes": p.Likes, "Comments": p.Comments, "Reposts": p.Reposts,
	} {
		if m.IsSet() {
			t.Errorf("%s = %+v, want unset (null on unpublished post)", name, m)
		}
	}

	// Nested arrays modeled as structs.
	if len(p.PostSchedules) != 1 || p.PostSchedules[0].ID != 7 || p.PostSchedules[0].Name != "A" {
		t.Errorf("PostSchedules = %+v, want one {id:7,name:A}", p.PostSchedules)
	}
	if len(p.PostProjects) != 1 || p.PostProjects[0].ID != 8 || p.PostProjects[0].Name != "A" {
		t.Errorf("PostProjects = %+v, want one {id:8,name:A}", p.PostProjects)
	}
	// errors_for_source_ids is an array (empty here — a post with no errors).
	if len(p.ErrorsForSourceIDs) != 0 {
		t.Errorf("ErrorsForSourceIDs = %d items, want 0 (empty array)", len(p.ErrorsForSourceIDs))
	}

	// pages[] items carry source_id + page_id. The narrow Page type captures
	// source_id; page_id is captured via Page.PageID.
	if len(p.Pages) != 1 || p.Pages[0].SourceID != 1 {
		t.Errorf("Pages = %+v, want one with source_id 1", p.Pages)
	}
	if p.Pages[0].PageID != 6 {
		t.Errorf("Pages[0].PageID = %d, want 6", p.Pages[0].PageID)
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
