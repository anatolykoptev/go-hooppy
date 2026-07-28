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
		ID:             2355344,
		SourceID:       3,
		AccountID:      32323,
		SocialPageID:   "333711379818750",
		SocialPageName: "GRAND BAZAR",
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Page
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SocialPageID != "333711379818750" {
		t.Errorf("SocialPageID = %q, want string", decoded.SocialPageID)
	}
	if decoded.SocialPageName != "GRAND BAZAR" {
		t.Errorf("SocialPageName = %q", decoded.SocialPageName)
	}
}
