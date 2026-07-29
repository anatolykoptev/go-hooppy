package hooppy

import (
	"encoding/json"
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

// TestPost_DecodeFullRow verifies a realistic GET /posts row decodes with
// every modelled field populated and the values round-trip — not just the
// absence of an unmarshal error. This is the RED-on-revert test: shrinking
// Post back to id-only makes every assertion below the first non-id field
// fail. It also covers the "posts list output contains the text and the
// publication slot, not just ids" requirement: the CLI marshals the
// decoded Post, so the struct carrying Text and PublicationDate is what
// puts them in the output.
func TestPost_DecodeFullRow(t *testing.T) {
	// Metrics are json.RawMessage: prove the decode accepts a STRING value
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
		"photo": "https://hooppy.ru/photo/abc.jpg",
		"photos_amount": 3,
		"pages": [{"id": 44567, "source_id": 1, "account_id": 33125, "social_page_id": "999", "social_page_name": "Группа", "social_page_photo": "https://pp.vk.me/p.jpg"}],
		"post_schedules": [{"id": 101820, "name": "Утро"}],
		"post_projects": [{"id": 92384, "name": "Рецепты"}],
		"created_by": 42,
		"errors_for_source_ids": {"1": "token expired", "6": "rate limited"}
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
	if string(p.Views) != `"1 234,881"` {
		t.Errorf("Views raw = %s, want the string metric verbatim", p.Views)
	}
	if string(p.Likes) != `"456"` {
		t.Errorf("Likes raw = %s, want %q", p.Likes, `"456"`)
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
	if p.Photo != "https://hooppy.ru/photo/abc.jpg" {
		t.Errorf("Photo = %q", p.Photo)
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
	if len(p.PostSchedules) == 0 {
		t.Error("PostSchedules raw is empty, want the nested schedule bytes")
	}
	if len(p.PostProjects) == 0 {
		t.Error("PostProjects raw is empty, want the nested project bytes")
	}
	if len(p.ErrorsForSourceIDs) == 0 {
		t.Error("ErrorsForSourceIDs raw is empty, want the error map bytes")
	}

	// Metrics as NUMBERS must also decode (json.RawMessage is type-agnostic;
	// a typed int/string field would fail one of the two shapes).
	numRow := `{"id": 7, "views": 1234, "likes": 56, "comments": 7, "reposts": 1}`
	var pn Post
	if err := json.Unmarshal([]byte(numRow), &pn); err != nil {
		t.Fatalf("unmarshal numeric-metric row: %v", err)
	}
	if string(pn.Views) != "1234" {
		t.Errorf("numeric Views raw = %s, want 1234", pn.Views)
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
	if p.Views != nil {
		t.Errorf("Views = %s, want nil when absent", p.Views)
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
