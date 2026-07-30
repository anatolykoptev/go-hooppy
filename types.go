package hooppy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Types derived from the Hooppy OpenAPI 3.0 specification (openapi.yaml).
// The live API may return additional undocumented fields; Go's json.Unmarshal
// silently ignores them, so only documented fields are modelled here.

// Account represents a connected social network account.
// Note: social_account_id is a string in the live API (despite the OpenAPI
// spec declaring int32) because some social networks use non-numeric IDs.
type Account struct {
	ID                 int    `json:"id"`
	SourceID           int    `json:"source_id"`
	SocialAccountID    string `json:"social_account_id"`
	SocialAccountName  string `json:"social_account_name"`
	SocialAccountPhoto string `json:"social_account_photo"`
}

// Page represents a group/page within a social network account.
// Note: social_page_id and social_account_id are strings in the live API.
//
// PageID is the page identifier used inside GET /posts rows (pages[] items
// carry {source_id, page_id}, not the {id, ...} shape the accounts surface
// uses). It is 0 in the accounts-surface response (where the key is "id",
// captured by ID above). Narrow: no token fields — see
// TestPost_DecodeCredentialHygiene.
type Page struct {
	ID              int    `json:"id"`
	SourceID        int    `json:"source_id"`
	AccountID       int    `json:"account_id"`
	SocialPageID    string `json:"social_page_id"`
	SocialPageName  string `json:"social_page_name"`
	SocialPagePhoto string `json:"social_page_photo"`
	PageID          int    `json:"page_id,omitempty"`
}

// Project groups posts for multi-platform publishing.
type Project struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Schedule defines a recurring publication plan.
// Schedule is a publishing schedule. The Hooppy API returns many fields
// beyond id/name; the ones below were discovered via live API probing
// (DELETE /posts/schedules/{id} response). Fields not yet modelled are
// silently ignored by encoding/json.
type Schedule struct {
	ID                   int    `json:"id"`
	UserID               int    `json:"user_id,omitempty"`
	Position             int    `json:"position,omitempty"`
	Name                 string `json:"name"`
	State                int    `json:"state,omitempty"`
	StopDate             int    `json:"stop_date,omitempty"`
	StartDate            int    `json:"start_date,omitempty"`
	IsDeleted            int    `json:"is_deleted,omitempty"`
	PublicationHowType   int    `json:"publication_how_type,omitempty"`
	PublicationWhereType int    `json:"publication_where_type,omitempty"`
}

// SchedulePayload is the request body for POST /posts/schedules (create)
// and PUT /posts/schedules/{id} (update). The Hooppy API requires ALL
// fields to be present (discovered via iterative 500-error probing —
// each missing field returns "Undefined index: <field>").
//
// Use NewSchedulePayload(name) to get a payload with sensible defaults
// (all flags off/0, state=1, publication_how_type=1, publication_where_type=1);
// then override only the fields you need.
//
// MODE INVARIANT (issue #66): the server enforces a mode-dependent
// requirement that SchedulePayload MUST satisfy before any request:
//   - publication_how_type=1 (manual): selected_pages_by_source_ids MUST be
//     non-empty (at least one source→pages entry).
//   - publication_how_type=2 (by project): project_id MUST be non-zero.
//
// NewSchedulePayload defaults to how_type=1 with NO pages — its output fails
// Validate() until the caller sets SelectedPagesBySourceIDs. This is
// intentional: a constructor whose output is silently accepted by the server
// was never possible (every CreateSchedule call 500'd), so the honest default
// is one that fails locally with a clear error rather than remotely with
// "Undefined index". Callers who want how_type=2 must set PublicationHowType=2
// and ProjectID.
//
// KNOWN-HOSTILE FIELD SHAPES (issue #66, out of scope for this PR):
// publish_as_story_source_ids and share_stories_to_feed_source_ids are typed
// int here, but the live API carries comma-separated strings like "1,2,7,9".
// The server coerces, so the int type is not fatal for the create/update path,
// but a byte-identity round trip through SchedulePayload would mangle them
// (int 0 vs. the original string). Use UpdateScheduleFromEdit for round-trip
// preservation; do not "fix" these types without a separate measurement PR.
//
// UNDOCUMENTED: these fields are not in the public OpenAPI spec (v0.1.0).
// The API may change without notice.
type SchedulePayload struct {
	Name                 string `json:"name"`
	State                int    `json:"state"`                  // measured: 1=active (Активно), 2=deferred launch (Отложенный запуск), 3=stopped (Остановлено); 0 not observed on the live account. Labels from the Hooppy Nuxt web bundle.
	PublicationHowType   int    `json:"publication_how_type"`   // 1=manual, 2=by project
	PublicationWhereType int    `json:"publication_where_type"` // 1=Страницы/Pages, 2=Фотоальбомы/Photo albums (labels from the Hooppy Nuxt web bundle).
	// For a schedule-driven post (publication_when_type=3, by schedule) the
	// schedule carries the page list, so selected_pages_by_source_ids is empty;
	// a published post keeps its frozen snapshot. where_type=1 appears on both
	// schedule-driven and non-schedule-driven posts alike — the field that
	// separates them is when_type, NOT where_type (measured on a live account).
	WatermarkID                 int    `json:"watermark_id"`                     // 0=none
	UTMTags                     string `json:"utm_tags"`                         // ""=none
	IsUniqueContent             int    `json:"is_unique_content"`                // 0/1
	IsPostsRepeated             int    `json:"is_posts_repeated"`                // 0/1
	IsRandomContent             int    `json:"is_random_content"`                // 0/1
	IsCommentsDisabled          int    `json:"is_comments_disabled"`             // 0/1
	PublishAsStory              int    `json:"publish_as_story"`                 // 0/1
	PublishAsStorySourceIDs     int    `json:"publish_as_story_source_ids"`      // 0=none — KNOWN-HOSTILE: server carries "1,2,7,9" (string), coerces; see struct doc.
	PublishAsReels              int    `json:"publish_as_reels"`                 // 0/1
	PublishAsClips              int    `json:"publish_as_clips"`                 // 0/1
	PublishAsShorts             int    `json:"publish_as_shorts"`                // 0/1
	PublishAsArticle            int    `json:"publish_as_article"`               // 0/1
	PublishAsArticleByLink      int    `json:"publish_as_article_by_link"`       // 0/1
	PublishInChannel            int    `json:"publish_in_channel"`               // 0/1
	ShareStoriesToFeed          int    `json:"share_stories_to_feed"`            // 0/1
	ShareStoriesToFeedSourceIDs int    `json:"share_stories_to_feed_source_ids"` // 0=none — KNOWN-HOSTILE: server carries "1,2,7,9" (string), coerces; see struct doc.
	ShareReelsToFeed            int    `json:"share_reels_to_feed"`              // 0/1
	ShareClipsToFeed            int    `json:"share_clips_to_feed"`              // 0/1
	ShareClipsToFeedWithText    int    `json:"share_clips_to_feed_with_text"`    // 0/1
	ShareClipsToFeedIfNoVideo   int    `json:"share_clips_to_feed_if_no_video"`  // 0/1
	ShareChannelToFeed          int    `json:"share_channel_to_feed"`            // 0/1
	ExpandClipsTitle            int    `json:"expand_clips_title"`               // 0/1
	PublishAsUser               int    `json:"publish_as_user"`                  // 0/1
	AddLinkToUser               int    `json:"add_link_to_user"`                 // 0/1
	MessageToCommunity          int    `json:"message_to_community"`             // 0/1
	MessageToChannel            int    `json:"message_to_channel"`               // 0/1
	DownloadVKVideos            int    `json:"download_vk_videos"`               // 0/1
	SaveVKVideosNames           int    `json:"save_vk_videos_names"`             // 0/1
	PlanByNetwork               int    `json:"plan_by_network"`                  // 0/1
	PublishAsCarousel           int    `json:"publish_as_carousel"`              // 0/1
	// ProjectID is required when PublicationHowType=2 (by project). The server
	// 500s with "Undefined index: project_id" if it is absent. Added in #66 so
	// the mode invariant is satisfiable for how_type=2.
	ProjectID int `json:"project_id"`
	// SelectedPagesBySourceIDs is required when PublicationHowType=1 (manual).
	// Same shape as ScheduleEditResponse.SelectedPagesBySourceIDs:
	// source_id → list of page ids. The server 500s if it is absent or empty
	// under how_type=1. Added in #66 so the mode invariant is satisfiable.
	SelectedPagesBySourceIDs map[int][]int `json:"selected_pages_by_source_ids"`
}

// NewSchedulePayload returns a SchedulePayload with sensible defaults:
// all flags off (0), state=active, publication_how_type=manual,
// publication_where_type=pages. Override fields as needed before
// calling CreateSchedule or UpdateSchedule.
//
// The default how_type=1 requires a non-empty SelectedPagesBySourceIDs to
// pass Validate() — set it before calling CreateSchedule, or switch to
// how_type=2 with a ProjectID. See SchedulePayload doc for the mode invariant.
func NewSchedulePayload(name string) SchedulePayload {
	return SchedulePayload{
		Name:                 name,
		State:                1,
		PublicationHowType:   1,
		PublicationWhereType: 1,
	}
}

// Validate checks the mode invariant the Hooppy server enforces on
// POST /posts/schedules and PUT /posts/schedules/{id}:
//   - publication_how_type=1 (manual): SelectedPagesBySourceIDs must be
//     non-empty (at least one source→pages entry).
//   - publication_how_type=2 (by project): ProjectID must be non-zero.
//
// Returns an error naming which invariant failed and what would satisfy it,
// so the caller learns the requirement without a server 500 that says only
// "Undefined index: <key>". CreateSchedule calls this before any request;
// callers of UpdateSchedule should call it explicitly (UpdateSchedule itself
// does not guard — see its doc for why).
func (p SchedulePayload) Validate() error {
	switch p.PublicationHowType {
	case 1:
		if len(p.SelectedPagesBySourceIDs) == 0 {
			return fmt.Errorf("hooppy: schedule payload invalid: publication_how_type=1 (manual) requires a non-empty selected_pages_by_source_ids (at least one source→pages entry); set SelectedPagesBySourceIDs or switch to publication_how_type=2 with a ProjectID")
		}
	case 2:
		if p.ProjectID == 0 {
			return fmt.Errorf("hooppy: schedule payload invalid: publication_how_type=2 (by project) requires a non-zero project_id; set ProjectID or switch to publication_how_type=1 with SelectedPagesBySourceIDs")
		}
	default:
		return fmt.Errorf("hooppy: schedule payload invalid: publication_how_type=%d is not a recognised mode (1=manual, 2=by project)", p.PublicationHowType)
	}
	return nil
}

// ScheduleResponse is returned by POST/PUT/DELETE /posts/schedules.
// The API returns the full schedule list on success.
type ScheduleResponse struct {
	Success   bool       `json:"success"`
	Schedules []Schedule `json:"schedules"`
}

// ScheduleTimeSlot is one time slot in a schedule's times array. Both Hours
// and Minutes are FlexInt because the API encodes them polymorphically:
// measured across several schedules, minutes arrived as a JSON number in 21
// values and as a JSON string ("00") in 7; hours was a number in every sample
// but is the same field family, so it is treated the same way. A bare int
// here aborts the whole decode — the bug this repo has shipped five times.
// FlexInt already handles number-or-numeric-string; reusing it rather than
// inventing a second one.
type ScheduleTimeSlot struct {
	Hours   FlexInt `json:"hours"`
	Minutes FlexInt `json:"minutes"`
}

// ScheduleEditResponse is returned by GET /posts/schedules/{id}/edit. It
// carries 72 keys — three more than the list response — including ten fields
// the list never returns: times, posts_hashtags, posts_links, project_id,
// projects, selected_pages_by_source_ids, selected_albums_by_source_ids,
// social_pages_by_accounts, social_albums_by_pages, watermarks.
//
// times is the posting schedule itself: an array of 7 arrays, one per
// weekday, each holding that day's slots (ScheduleTimeSlot). A weekday with
// no slots is an empty array. That is how a "ПН/СР/ЧТ" schedule is expressed
// — days with slots and days without.
//
// Field types are chosen from the evidence in the issue (measured, not
// inferred from the name):
//   - project_id: int (measured).
//   - selected_pages_by_source_ids / selected_albums_by_source_ids:
//     map[int][]int — same shape as PostEditResponse.SelectedPagesBySourceIDs
//     (source_id → list of page/album ids).
//   - social_pages_by_accounts: []SocialPagesByAccount — an ARRAY, despite
//     the "by_accounts" suffix reading like a map keyed by account id. This
//     is the sixth instance on this API of a name implying the wrong shape
//     (errors_for_source_ids was an array of ints, not a map; photo was an
//     object, not a string). Each element is {account, pages}; the account
//     and page sub-objects use a DIFFERENT field set from the accounts-surface
//     Account/Page types (social_id/name/photo/link, not social_account_id/
//     social_account_name/...), so they get their own narrow types —
//     SocialPagesAccount and SocialPagesPage — which list ONLY the safe
//     fields measured on the wire. The OAuth tokens page objects carry
//     elsewhere in this API (access_token, bot_token, refresh_token,
//     password, wp_app_password, access_token_secret) are intentionally NOT
//     modelled and therefore cannot reach the marshalled output. See
//     TestScheduleEdit_DecodeCredentialHygiene.
//   - projects: []Project — array of full project objects (id, user_id,
//     position, name, is_deleted, publication_where_type, posts_count,
//     watermark_id, utm_tags, ...). The existing narrow Project type (id/
//     name) FITS: Go's encoding/json silently ignores the extra fields, so
//     the decode succeeds and captures the two fields callers need. Reused
//     rather than widened — a wider struct would model fields nobody reads
//     and risk a wrong-guess abort on an unmeasured nested type.
//   - watermarks: []Watermark — array of narrow watermark objects (no
//     credential fields).
//   - posts_hashtags, posts_links: json.RawMessage — measured as objects,
//     but the key/value shape is not evidenced; RawMessage is the one choice
//     that cannot abort the decode on a wrong guess.
//   - social_albums_by_pages: json.RawMessage — an EMPTY ARRAY in every
//     sample; the element shape is therefore UNOBSERVED. RawMessage avoids
//     guessing a struct that would abort the decode once a non-empty
//     response arrives.
//
// UNDOCUMENTED: GET /posts/schedules/{id}/edit is not in the public OpenAPI
// spec (v0.1.0). Discovered via API probing — may change without notice.
type ScheduleEditResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Times is the posting schedule: 7 arrays (one per weekday), each
	// holding that day's slots. A weekday with no slots is an empty array.
	Times [][]ScheduleTimeSlot `json:"times"`
	// The ten list-absent fields (issue #69).
	PostsHashtags             json.RawMessage        `json:"posts_hashtags"` // object — key/value shape not measured
	PostsLinks                json.RawMessage        `json:"posts_links"`    // object — key/value shape not measured
	ProjectID                 int                    `json:"project_id"`
	Projects                  []Project              `json:"projects"`
	SelectedPagesBySourceIDs  map[int][]int          `json:"selected_pages_by_source_ids"`
	SelectedAlbumsBySourceIDs map[int][]int          `json:"selected_albums_by_source_ids"`
	SocialPagesByAccounts     []SocialPagesByAccount `json:"social_pages_by_accounts"`
	SocialAlbumsByPages       json.RawMessage        `json:"social_albums_by_pages"` // empty array in every sample; element shape unobserved
	Watermarks                []Watermark            `json:"watermarks"`
}

// SocialPagesByAccount is one element of the social_pages_by_accounts array
// on GET /posts/schedules/{id}/edit: a connected account and the pages
// available to it for this schedule. The field name reads like a map keyed
// by account id, but the wire shape is an ARRAY of these objects — the
// sixth name-implies-wrong-shape instance on this API.
//
// Narrow: only the account and pages sub-objects are modelled, and each of
// those lists only the safe fields measured on the wire (see SocialPagesAccount
// and SocialPagesPage). The OAuth tokens page objects carry elsewhere in this
// API are NOT modelled and cannot reach the marshalled output — see
// TestScheduleEdit_DecodeCredentialHygiene.
type SocialPagesByAccount struct {
	Account SocialPagesAccount `json:"account"`
	Pages   []SocialPagesPage  `json:"pages"`
}

// SocialPagesAccount is the account sub-object inside a
// social_pages_by_accounts element. Its field set DIFFERS from the
// accounts-surface Account type (social_id/name/photo/link here, not
// social_account_id/social_account_name/social_account_photo), so it gets
// its own narrow type rather than reusing Account.
//
// Field types from the measured capture (15 elements):
//   - id: number (int) — the account's internal id.
//   - social_id: STRING — the same number-beside-stringified-id pattern that
//     bit photo.id; typed string, not int, because the wire sends a string.
//   - source_id: number (int).
//   - name, photo, link: strings (photo/link are URLs).
//
// No token fields are modelled. Page-shaped objects elsewhere on this API
// carry access_token/bot_token/refresh_token/password/wp_app_password/
// access_token_secret; this account sub-object was not observed carrying
// them, but the narrow modelling guarantees they cannot leak even if a
// future response includes them.
type SocialPagesAccount struct {
	ID       int    `json:"id"`
	SocialID string `json:"social_id"`
	SourceID int    `json:"source_id"`
	Name     string `json:"name"`
	Photo    string `json:"photo"`
	Link     string `json:"link"`
}

// SocialPagesPage is one page inside a social_pages_by_accounts element's
// pages array. Its field set DIFFERS from the accounts-surface Page type
// (social_id/type/name/alias/photo/link here, not social_page_id/
// social_page_name/social_page_photo/page_id), so it gets its own narrow
// type rather than reusing Page.
//
// Field types from the measured capture:
//   - id: number (int).
//   - social_id: STRING — number-beside-stringified-id pattern; typed string.
//   - type: string (e.g. "board").
//   - name, alias, photo, link: strings.
//
// No token fields are modelled. This is the credential-hygiene-critical
// surface: page-shaped objects on this API carry live OAuth tokens
// (access_token, bot_token, refresh_token, password, wp_app_password,
// access_token_secret). The sample showed only safe fields, but the sample
// is one account's worth — the narrow struct guarantees the token values
// are dropped at decode and absent from any re-marshal, so a credential
// cannot reach stdout via printJSON. See TestScheduleEdit_DecodeCredentialHygiene.
type SocialPagesPage struct {
	ID       int    `json:"id"`
	SocialID string `json:"social_id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Alias    string `json:"alias"`
	Photo    string `json:"photo"`
	Link     string `json:"link"`
}

// DeleteResponse is returned by DELETE endpoints (schedules, projects).
type DeleteResponse struct {
	Success bool `json:"success"`
}

// User is the current authenticated user. The API returns many fields;
// only the most useful ones are modelled. Sensitive fields (api_token,
// ord, passwords) are intentionally excluded.
type User struct {
	ID                int    `json:"id"`
	Email             string `json:"email"`
	EmailVerifiedDate string `json:"email_verified_date,omitempty"`
	RegistrationDate  string `json:"registration_date,omitempty"`
	RegistrationLang  string `json:"registration_lang,omitempty"`
	PlanType          int    `json:"plan_type,omitempty"`
	TimezoneID        int    `json:"timezone_id,omitempty"`
	IsDeleted         int    `json:"is_deleted,omitempty"`
}

// UserResponse wraps GET /users/me.
type UserResponse struct {
	User User `json:"user"`
}

// SettingsResponse is returned by GET /users/settings. Narrowly modelled:
// only timezone_id, timezone_offset, and the timezones array ({id, name})
// are kept. The response carries api_token, gpt_key, and ru_captcha_key —
// NONE of them are modelled, so they are dropped at decode and absent from
// any re-marshal. A credential cannot reach stdout via printJSON. See
// TestSettings_DecodeCredentialHygiene.
//
// TimezoneOffset is an integer count of HOURS from UTC (e.g. 3 for UTC+3,
// -5 for UTC-5). Fractional offsets (UTC+5:30, UTC+5:45) are NOT supported
// by the server — confirmed by measurement; the field is a plain int, not
// a float. The batch slot path (fillScheduleSlots) uses this to format the
// publication date as dd.mm.yyyy at the account's offset; a fractional
// offset would require minutes, but the server does not return one.
type SettingsResponse struct {
	TimezoneID     int        `json:"timezone_id,omitempty"`
	TimezoneOffset int        `json:"timezone_offset,omitempty"`
	Timezones      []Timezone `json:"timezones,omitempty"`
}

// Timezone is one entry in the timezones array on GET /users/settings.
type Timezone struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Watermark is an image watermark configuration.
type Watermark struct {
	ID       int    `json:"id"`
	UserID   int    `json:"user_id,omitempty"`
	Name     string `json:"name"`
	File     string `json:"file"`
	Space    int    `json:"space,omitempty"`
	Position int    `json:"position,omitempty"`
	Opacity  int    `json:"opacity,omitempty"`
	Size     int    `json:"size,omitempty"`
}

// WatermarkPayload is the request body for POST/PUT /watermarks.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
type WatermarkPayload struct {
	Name     string `json:"name"`
	File     string `json:"file"` // file path or ""
	Space    int    `json:"space"`
	Position int    `json:"position"`
	Opacity  int    `json:"opacity"`
	Size     int    `json:"size"`
}

// WatermarksResponse wraps GET /watermarks and POST/PUT/DELETE /watermarks[/{id}].
type WatermarksResponse struct {
	List      []Watermark `json:"list"`
	TotalRows int         `json:"total_rows"`
	IsHasMore bool        `json:"is_has_more"`
	RowsLimit int         `json:"rows_limit"`
}

// WatermarkResponse wraps POST/PUT/DELETE /watermarks[/{id}].
type WatermarkResponse struct {
	ID         int         `json:"id,omitempty"`
	Success    bool        `json:"success"`
	Watermarks []Watermark `json:"watermarks"`
}

// Proxy is a proxy server configuration.
type Proxy struct {
	ID       int    `json:"id"`
	UserID   int    `json:"user_id,omitempty"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Port     string `json:"port"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

// ProxyPayload is the request body for POST/PUT /proxies[/{id}].
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
type ProxyPayload struct {
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Port     string `json:"port"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

// ProxiesResponse wraps GET /proxies.
type ProxiesResponse struct {
	List      []Proxy `json:"list"`
	TotalRows int     `json:"total_rows"`
	IsHasMore bool    `json:"is_has_more"`
	RowsLimit int     `json:"rows_limit"`
}

// ProxyResponse wraps POST/PUT/DELETE /proxies[/{id}].
type ProxyResponse struct {
	ID      int     `json:"id,omitempty"`
	Success bool    `json:"success"`
	Proxies []Proxy `json:"proxies"`
}

// Notification is a publication status notification.
type Notification struct {
	ID            int    `json:"id"`
	UserID        int    `json:"user_id,omitempty"`
	ObjectID      int    `json:"object_id,omitempty"`
	ObjectType    int    `json:"object_type,omitempty"`
	SourceID      int    `json:"source_id,omitempty"`
	PageID        int    `json:"page_id,omitempty"`
	IsError       int    `json:"is_error,omitempty"`
	OperationDate string `json:"operation_date,omitempty"`
	TimeInWork    string `json:"time_in_work,omitempty"`
	Data          string `json:"data,omitempty"`
	IsViewed      int    `json:"is_viewed,omitempty"`
}

// NotificationsResponse wraps GET /notifications.
type NotificationsResponse struct {
	List      []Notification `json:"list"`
	TotalRows int            `json:"total_rows"`
	IsHasMore bool           `json:"is_has_more"`
	RowsLimit int            `json:"rows_limit"`
}

// ProjectPayload is the request body for POST /posts/projects (create).
// The Hooppy API requires ALL fields to be present (discovered via
// iterative 500-error probing). Use NewProjectPayload(name, pageID) to
// get a payload with sensible defaults.
//
// UNDOCUMENTED: not in the public OpenAPI spec (v0.1.0).
type ProjectPayload struct {
	Name                        string `json:"name"`
	PublicationWhereType        int    `json:"publication_where_type"`
	SelectedPagesIDs            []int  `json:"selected_pages_ids"`
	WatermarkID                 int    `json:"watermark_id"`
	UTMTags                     string `json:"utm_tags"`
	IsUniqueContent             int    `json:"is_unique_content"`
	IsCommentsDisabled          int    `json:"is_comments_disabled"`
	PublishAsStory              int    `json:"publish_as_story"`
	PublishAsStorySourceIDs     int    `json:"publish_as_story_source_ids"`
	PublishAsReels              int    `json:"publish_as_reels"`
	PublishAsClips              int    `json:"publish_as_clips"`
	PublishAsShorts             int    `json:"publish_as_shorts"`
	PublishAsArticle            int    `json:"publish_as_article"`
	PublishAsArticleByLink      int    `json:"publish_as_article_by_link"`
	PublishInChannel            int    `json:"publish_in_channel"`
	ShareStoriesToFeed          int    `json:"share_stories_to_feed"`
	ShareStoriesToFeedSourceIDs int    `json:"share_stories_to_feed_source_ids"`
	ShareReelsToFeed            int    `json:"share_reels_to_feed"`
	ShareClipsToFeed            int    `json:"share_clips_to_feed"`
	ShareClipsToFeedWithText    int    `json:"share_clips_to_feed_with_text"`
	ShareClipsToFeedIfNoVideo   int    `json:"share_clips_to_feed_if_no_video"`
	ShareChannelToFeed          int    `json:"share_channel_to_feed"`
	ExpandClipsTitle            int    `json:"expand_clips_title"`
	PublishAsUser               int    `json:"publish_as_user"`
	AddLinkToUser               int    `json:"add_link_to_user"`
	MessageToCommunity          int    `json:"message_to_community"`
	MessageToChannel            int    `json:"message_to_channel"`
	DownloadVKVideos            int    `json:"download_vk_videos"`
	SaveVKVideosNames           int    `json:"save_vk_videos_names"`
	PlanByNetwork               int    `json:"plan_by_network"`
	PublishAsCarousel           int    `json:"publish_as_carousel"`
	PublishOnlyInVideos         int    `json:"publish_only_in_videos"`
	NotPublishInVideos          int    `json:"not_publish_in_videos"`
	RepeatVideo                 int    `json:"repeat_video"`
	ParseLinks                  int    `json:"parse_links"`
	PublishByAccount            int    `json:"publish_by_account"`
	PublishByAccountSourceIDs   int    `json:"publish_by_account_source_ids"`
	PrivacyLevel                int    `json:"privacy_level"`
	YouTubeCategory             int    `json:"youtube_category"`
	DonutPaidDuration           int    `json:"donut_paid_duration"`
	DeletePostsDay              int    `json:"delete_posts_day"`
	DeletePostsHour             int    `json:"delete_posts_hour"`
	PostsCaption                int    `json:"posts_caption"`
	PostsCaptionPositionType    int    `json:"posts_caption_position_type"`
	PostsCaptionSpaceType       int    `json:"posts_caption_space_type"`
	PhotosCaption               int    `json:"photos_caption"`
	TGButtons                   int    `json:"tg_buttons"`
	VideosTitle                 int    `json:"videos_title"`
	PostsComment                int    `json:"posts_comment"`
	PublishCommentByAccount     int    `json:"publish_comment_by_account"`
	PostsHashtags               int    `json:"posts_hashtags"`
	PostsLinks                  int    `json:"posts_links"`
	PostsRewrite                int    `json:"posts_rewrite"`
	PostsLocation               int    `json:"posts_location"`
	PostsLocationVK             int    `json:"posts_location_vk"`
	PostsPhoto                  int    `json:"posts_photo"`
	PostsPhotoAlways            int    `json:"posts_photo_always"`
}

// NewProjectPayload returns a ProjectPayload with sensible defaults:
// all flags off (0), publication_where_type=pages. Override fields
// as needed before calling CreateProject.
func NewProjectPayload(name string, pageID int) ProjectPayload {
	return ProjectPayload{
		Name:                 name,
		PublicationWhereType: 1,
		SelectedPagesIDs:     []int{pageID},
	}
}

// ProjectResponse wraps POST /posts/projects.
type ProjectResponse struct {
	ID       int       `json:"id"`
	Projects []Project `json:"projects"`
}

// CrossPostMode identifies a cross-posting endpoint (PUT /posts/{mode}).
// All modes accept the same payload as POST /posts and return {"id":...}.
//
// UNDOCUMENTED: these endpoints are not in the public OpenAPI spec (v0.1.0).
type CrossPostMode string

const (
	CrossPostModeSearch     CrossPostMode = "search"
	CrossPostModeCopy       CrossPostMode = "copy"
	CrossPostModeSources    CrossPostMode = "sources"
	CrossPostModeImport     CrossPostMode = "import"
	CrossPostModeCrossPost  CrossPostMode = "crosspost"
	CrossPostModeRewrite    CrossPostMode = "rewrite"
	CrossPostModeTranslate  CrossPostMode = "translate"
	CrossPostModeQueue      CrossPostMode = "queue"
	CrossPostModeDrafts     CrossPostMode = "drafts"
	CrossPostModeTemplates  CrossPostMode = "templates"
	CrossPostModeRSS        CrossPostMode = "rss"
	CrossPostModeFeeds      CrossPostMode = "feeds"
	CrossPostModeTags       CrossPostMode = "tags"
	CrossPostModeWatermarks CrossPostMode = "watermarks"
	CrossPostModeBatch      CrossPostMode = "batch"
)

// PostIDResponse is returned by PUT /posts/{mode} cross-posting endpoints.
//
// Slot reporting (issue #69): when a post is created into a schedule
// (publication_when_type=3), the server assigns the publication slot
// server-side and returns only the new id. The library now reads the slot
// back and populates the fields below so the caller does not need a second
// call they have to know to make. The read-back is best-effort: a lookup
// failure populates SlotLookupError and the method still returns the id
// (exit zero) — losing the id because a follow-up read failed is strictly
// worse than today's behaviour.
//
//   - PublicationDate: the assigned slot ({date, hours, minutes} shape from
//     GET /posts/{id}/edit). Populated for the primary id (ID). For a batch,
//     per-id slots are in Slots.
//   - ScheduleID: the schedule the post was created into.
//   - SlotLookupError: set when the read-back failed (id still returned).
//   - IDs: all created post ids for a batch. The server does NOT send "ids"
//     in the response — it returns {"success": true} for a batch with no id
//     or ids. IDs is populated by the client from a schedule snapshot diff
//     (before vs after the create). The json tag "ids,omitempty" is kept so
//     the recovered ids marshal to output (the CLI prints this struct as
//     JSON); the tag WOULD decode a server-sent "ids" field, but the server
//     does not send one (measured on both single and batch paths), so decode
//     leaves the field empty in practice. Empty for a single-post create
//     (which returns {"id": ...}).
//   - ID: for a single-post create, the wire id from the server (never
//     overwritten by the diff — the diff supplies the slot, not the
//     identity). For a batch, set to the first recovered id (ordered by
//     publication timestamp) so callers reading only ID get a valid id
//     instead of 0.
//   - Slots: per-id slots for a batch; empty for a single-post create.
type PostIDResponse struct {
	ID              int              `json:"id"`
	IDs             []int            `json:"ids,omitempty"`
	PublicationDate *PublicationDate `json:"publication_date,omitempty"`
	ScheduleID      int              `json:"schedule_id,omitempty"`
	SlotLookupError string           `json:"slot_lookup_error,omitempty"`
	Slots           []ScheduleSlot   `json:"slots,omitempty"`
}

// ScheduleSlot is one created post's assigned publication slot, used in the
// per-id Slots array of a batch PostIDResponse. The shape mirrors the flat
// PublicationDate/ScheduleID fields that a single-post create populates.
type ScheduleSlot struct {
	ID              int              `json:"id"`
	PublicationDate *PublicationDate `json:"publication_date,omitempty"`
}

// Post is a post returned by GET /posts (the user's own posts). The live
// API returns twenty-four fields per row; they are modelled here so the
// decode boundary keeps them instead of discarding twenty-three.
//
// Field types are derived from the evidence available without calling the
// live API: the OpenAPI spec (which documents only id), the sibling
// SearchPost struct (scraped posts — a DIFFERENT API surface), and
// PostEditResponse (the own-post edit endpoint). Where the evidence for a
// field's type was genuinely absent, the field is json.RawMessage — the
// one choice that cannot abort the entire unmarshal on a wrong guess
// (a JSON number fails to decode into a Go string, and vice versa). Each
// such field says so in its doc comment.
//
// Credential hygiene: Pages reuses the narrow Page struct, which models
// only id/source/social-ids/name/photo — never the access_token,
// bot_token, refresh_token, password, wp_app_password, or
// access_token_secret that page objects carry elsewhere in this API.
// See TestPost_DecodeCredentialHygiene for the guard.
type Post struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	// PublicationDate is the slot a schedule assigned. It is an OBJECT,
	// not a string — a different shape from the PublicationDate used by
	// the publish/edit payloads (date/hours/minutes). See
	// PostPublicationDate for the measured {date, time, timestamp,
	// source_timestamp} shape.
	PublicationDate *PostPublicationDate `json:"publication_date"`
	// is_published: 0/1 flag (API boolean convention — Schedule.IsDeleted,
	// SearchPost.IsUsed, ListPostsFilter.is_published all use int 0/1).
	IsPublished int `json:"is_published"`
	// is_ad: the vendor's own advertising flag (0/1, API boolean convention).
	IsAd int `json:"is_ad"`
	// is_repeated: 0/1 flag (API boolean convention; SchedulePayload.IsPostsRepeated).
	IsRepeated int `json:"is_repeated"`
	// is_attachments_in_process: 0/1 flag — direct sibling evidence from
	// SearchPost.IsAttachmentsInProcess (int).
	IsAttachmentsInProcess int `json:"is_attachments_in_process"`
	// is_planned_by_networks: 0/1 flag (API boolean convention; SchedulePayload.PlanByNetwork).
	IsPlannedByNetworks int `json:"is_planned_by_networks"`
	// is_planning_by_networks_needed: 0/1 flag (API boolean convention).
	IsPlanningByNetworksNeeded int `json:"is_planning_by_networks_needed"`
	// views, likes, comments, reposts: engagement metrics. Measured: null
	// on unpublished posts (present and null, not absent). On published
	// posts the value shape is inferred from SearchPost (a different,
	// scraped surface) which receives thousands-separated STRINGS
	// ("334,881"); the own-post list surface's metric type is unverified,
	// so a number is plausible. Metric tolerates null, string, AND number
	// via a custom UnmarshalJSON so a wrong guess cannot abort the decode —
	// the same bug class as Post.Photo (string vs object) this fix
	// addresses. See the Metric type for the accessors.
	Views    Metric `json:"views"`
	Likes    Metric `json:"likes"`
	Comments Metric `json:"comments"`
	Reposts  Metric `json:"reposts"`
	// link: URL of the published post (SearchPost.Link is string).
	Link string `json:"link"`
	// source_link: URL of the original source (URL convention, same as link/repost_link).
	SourceLink string `json:"source_link"`
	// repost_link / repost_title: the reposted source (Repost.Link / Repost.Title are strings).
	RepostLink  string `json:"repost_link"`
	RepostTitle string `json:"repost_title"`
	// photo: a media descriptor OBJECT, not a URL string — measured from a
	// live GET /posts response. The name is misleading: type was "video"
	// in the capture, so this field is not photo-specific. Inside the
	// object, id is an OPAQUE identifier (number ×1, non-numeric string
	// ×52 — modelled as PhotoID) and updated_date is a NULLABLE timestamp
	// (null ×2, number ×39, numeric string ×12 — modelled as FlexInt);
	// every other field is stable. See PostPhoto for the censused shape
	// and the two opposite representations. A pointer so a text-only post
	// (photo null/absent) decodes to nil rather than a zero-value struct.
	Photo *PostPhoto `json:"photo"`
	// photos_amount: photo count (SearchPostsFilter.PhotosAmount is int).
	PhotosAmount int `json:"photos_amount"`
	// pages: the page targets this post publishes to. Reuses the narrow
	// Page struct (id/source/social-ids/name/photo/page_id only) so the
	// OAuth tokens page objects carry elsewhere CANNOT reach the
	// marshalled output. See TestPost_DecodeCredentialHygiene.
	Pages []Page `json:"pages"`
	// post_schedules: nested schedule references. Measured shape:
	// [{"id":…,"name":…}] — modeled as a struct slice, not RawMessage, so
	// the values are typed and reachable. Not page-shaped, so no
	// credential leak risk.
	PostSchedules []PostSchedule `json:"post_schedules"`
	// post_projects: nested project references. Measured shape:
	// [{"id":…,"name":…}] — reuses the narrow Project struct (id/name
	// only). Not page-shaped, so no credential leak risk.
	PostProjects []Project `json:"post_projects"`
	// created_by: user id of the post's author (PostEditResponse.CreatedBy is int).
	CreatedBy int `json:"created_by"`
	// errors_for_source_ids: the source_ids of the networks a publication
	// failed on (the same source_id space as sources.go — SourceVK=1,
	// SourceOK=2, …). Measured on 12 published rows where the field is
	// populated: an ARRAY in 12 of 12, and its 14 elements are plain
	// INTEGERS (e.g. [2, 4] — the networks the post failed on). There is
	// no map form and no abort risk, so []int is the honest model — and
	// integers cannot carry an opaque token, which closes the credential
	// concern a []json.RawMessage raised. An empty array (no failures)
	// decodes to a non-nil zero-length slice.
	ErrorsForSourceIDs []int `json:"errors_for_source_ids"`
}

// PostPublicationDate is the publication_date object returned in a GET
// /posts row. It is a DIFFERENT shape from the PublicationDate used by the
// publish/edit payloads (which is {date, hours, minutes}): the list row
// carries {date, time, timestamp, source_timestamp}, where date is a
// "29 Июля"-style display string, time is a "12:25"-style display string,
// and the two timestamps are integers that differ from each other (one
// appears to carry a timezone offset). Both timestamps are kept; they are
// not collapsed.
//
// The time field is ZERO-PADDED on the list surface (e.g. "09:20", not
// "9:20"). postPubDateToPublicationDate parses it by splitting on ":" — a
// malformed time string (missing colon, wrong number of parts) leaves
// Hours/Minutes empty and the batch path populates slot_lookup_error
// naming the malformed value, rather than silently producing a slot with
// no time.
//
// Timestamp and SourceTimestamp are modelled as FlexInt, not bare int64:
// measured as a JSON number in all 60 census rows, so this is not a live
// break today — but on this API the string form of a numeric field has
// already appeared twice (PostPhoto.UpdatedDate: numeric string ×12 of 53),
// and a bare int64 aborts the whole list decode when it does. FlexInt's
// Int64() accessor keeps callers unchanged. See issue #74 (the sweep).
type PostPublicationDate struct {
	Date            string  `json:"date"`             // "29 Июля"-style display date
	Time            string  `json:"time"`             // "09:20"-style display time (zero-padded HH:MM)
	Timestamp       FlexInt `json:"timestamp"`        // unix timestamp (number in all 60 rows; FlexInt — a stringified numeric has appeared on this API)
	SourceTimestamp FlexInt `json:"source_timestamp"` // unix timestamp carrying a tz offset (same polymorphism note as Timestamp)
}

// PostPhoto is the media descriptor carried in Post.Photo. Despite the
// field name, it is NOT photo-specific: the measured capture had
// type:"video". Types below are censused across 60 rows on three pages —
// a field's type is a property of the collection, not of one row, AND the
// type alone is not the specification: "string" can mean a numeric string
// or an opaque token, and those demand opposite models. Sample the VALUES
// before choosing a representation.
//
// Two fields are POLYMORPHIC across the collection, and they need OPPOSITE
// representations:
//
//   - id arrives as a JSON number on one row and a JSON string on 52, but
//     52 of 53 values are NON-NUMERIC tokens ("fakeTokExample01" shape).
//     It is an opaque identifier that happens to be numeric sometimes, so
//     it is modelled as PhotoID (a string): a number on the wire is stored
//     as its decimal text, an opaque token is stored untouched. Never
//     parsed as an integer — there is nothing numeric about
//     "fakeTokExample01".
//   - updated_date arrives as null (×2), a JSON number (×39), or a JSON
//     numeric string (×12). Every non-null value parses as an integer. It
//     is a nullable unix timestamp that is sometimes stringified, so it is
//     modelled as FlexInt (a number-or-numeric-string, nil when null) —
//     the FlexInt idea applied to the field that actually is one.
//
// Every other field is stable across all 60 rows; their types are exactly
// as censused:
//
//	access_key string, description string, duration number, file_path string,
//	folder string, is_used number, name string, owner_id number,
//	post_id string, preview string, source_id number, sticker string,
//	text string, title string, type string
//
// Anonymised example (number-form id):
//
//	{"id":3,"owner_id":4,"post_id":"5","access_key":"A","source_id":1,
//	 "type":"video","title":"A","description":"","duration":383,
//	 "preview":"https://example.invalid/x","is_used":0,"name":"A",
//	 "sticker":"","file_path":"A","text":"","folder":"A",
//	 "updated_date":1700000000}
type PostPhoto struct {
	ID          PhotoID `json:"id"`           // OPAQUE: number ×1, non-numeric string ×52 — model as string, never parse
	OwnerID     int     `json:"owner_id"`     // number (stable)
	PostID      string  `json:"post_id"`      // string (stable, while id is opaque)
	AccessKey   string  `json:"access_key"`   // string (stable)
	SourceID    int     `json:"source_id"`    // number (stable)
	Type        string  `json:"type"`         // "video" observed — not photo-specific
	Title       string  `json:"title"`        // string (stable)
	Description string  `json:"description"`  // string (stable)
	Duration    int     `json:"duration"`     // number (stable)
	Preview     string  `json:"preview"`      // string (stable)
	IsUsed      int     `json:"is_used"`      // number (stable)
	Name        string  `json:"name"`         // string (stable)
	Sticker     string  `json:"sticker"`      // string (stable)
	FilePath    string  `json:"file_path"`    // string (stable)
	Text        string  `json:"text"`         // string (stable)
	Folder      string  `json:"folder"`       // string (stable)
	UpdatedDate FlexInt `json:"updated_date"` // NULLABLE TIMESTAMP: null ×2, number ×39, numeric string ×12
}

// PostSchedule is a nested schedule reference inside a GET /posts row's
// post_schedules array. Measured shape: {"id":…,"name":…}. Narrow — no
// token fields (unlike the full Schedule type which carries state/position/
// dates); this is the list-surface projection, not the editable schedule.
type PostSchedule struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Metric is an engagement metric (views/likes/comments/reposts) on a Post.
// Measured: null on unpublished posts (present and null, not absent), AND
// null on published posts too — 12 of 12 published rows on the measured
// account have views: null. The populated metric shape is therefore
// genuinely UNOBSERVED on this account: no recorded fixture (testdata/)
// carries a populated Metric, and none can be produced from here without a
// different account. The string/number shapes are covered only by the
// hand-written TestPost_DecodeFullRow, INFERRED from SearchPost (a different,
// scraped surface) which receives thousands-separated strings ("334,881");
// the own-post list surface's published metric type is unverified, so a
// number is plausible. Metric tolerates null, string, AND number via a
// custom UnmarshalJSON — a typed string/int field would abort the whole
// decode on the wrong shape, the same bug class as Post.Photo (string vs
// object) that this fix addresses. The raw JSON bytes are preserved so
// MarshalJSON round-trips the exact wire value (string stays quoted, number
// stays bare) and printJSON output stays clean.
//
// Accessors: Set reports whether a non-null value was present; String
// returns the value as a string (the quoted content for a string, the raw
// digits for a number, "" when unset); Int parses the value with the same
// thousands-separator-stripping rule as SearchPost.ViewsInt (so callers can
// compare Post metrics without reimplementing the "334,881" parse).
type Metric struct {
	raw json.RawMessage
	set bool
}

// UnmarshalJSON accepts null (→ unset), a JSON string, or a JSON number.
// Any other shape (object/array) returns an error rather than silently
// storing it — same doctrine as FlexInt/PhotoID: a shape change is loud,
// not silent. (Without this guard {"a":1} would silently round-trip as a
// string via String().)
func (m *Metric) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) == 0 || s == "null" {
		m.raw, m.set = nil, false
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		m.raw = append(json.RawMessage(nil), b...)
		m.set = true
		return nil
	}
	// Bare number — validate via json.Number so an object/array is rejected.
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("Metric: expected string or number, got %s: %w", s, err)
	}
	m.raw = append(json.RawMessage(nil), b...)
	m.set = true
	return nil
}

// MarshalJSON round-trips the captured wire value, or null when unset.
func (m Metric) MarshalJSON() ([]byte, error) {
	if !m.set {
		return []byte("null"), nil
	}
	return m.raw, nil
}

// Set reports whether a non-null value was present.
func (m Metric) IsSet() bool { return m.set }

// String returns the metric value as a string: the quoted content for a
// JSON string, the raw digits for a JSON number, "" when unset/null.
func (m Metric) String() string {
	if !m.set {
		return ""
	}
	var s string
	if json.Unmarshal(m.raw, &s) == nil {
		return s
	}
	return string(m.raw)
}

// Int parses the metric value as an integer using the same
// thousands-separator-stripping rule as SearchPost.ViewsInt (so a Post
// metric of "334,881" yields 334881, and a bare number "1234" yields 1234).
// Returns 0 with a nil error when unset/null. A malformed value (an
// unexpected separator, a decimal-comma form) returns an error rather than
// a silent 0 — the same discipline as parseMetricInt. Callers can compare
// Post metrics without reimplementing the parse.
func (m Metric) Int() (int, error) {
	if !m.set {
		return 0, nil
	}
	return parseMetricInt("Metric", m.String())
}

// FlexInt is an integer field the API encodes as EITHER a JSON number or a
// JSON numeric string ("123"), and may be null. It is the right model for a
// field whose values are ALL integers but whose wire form varies —
// PostPhoto.UpdatedDate (null ×2, number ×39, numeric string ×12 across 60
// rows). It is the WRONG model for PostPhoto.ID, whose string form is
// usually a NON-NUMERIC opaque token (see PhotoID): a field's type is a
// property of the collection, not of the row you happened to look at, AND
// the type alone is not the specification — sample the VALUES before
// choosing a representation.
//
// FlexInt accepts a JSON number, a JSON string holding an integer, or null
// (→ unset) via a custom UnmarshalJSON. The raw wire bytes are preserved so
// MarshalJSON round-trips the exact form WHEN SET (string stays quoted,
// number stays bare) and printJSON output stays clean. NOTE: a null/unset
// FlexInt marshals as 0 (the int zero-value convention), NOT null — so a
// null updated_date does NOT round-trip as null. Int64() returns the typed
// value regardless of the wire form; IsSet() reports whether a non-null
// value was present.
//
// SWEEP: FlexInt is applied here to PostPhoto.UpdatedDate and
// PostPublicationDate.{Timestamp,SourceTimestamp}. Three sibling sites
// (MediaItem.UpdatedDate, Photo.{ID,UpdatedDate}, SearchPostPhoto.ID) have
// the same polymorphism and are confirmed broken today — tracked in
// issue #74; each needs its own measurement.
type FlexInt struct {
	raw json.RawMessage
	set bool
}

// UnmarshalJSON accepts null (→ unset), a JSON number, or a JSON string
// holding an integer. Any other shape returns an error rather than silently
// coercing — a silent coerce would recreate the wrong-type-hides-bug class
// this type exists to prevent.
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) == 0 || s == "null" {
		f.raw, f.set = nil, false
		return nil
	}
	// Validate: must be a number or a quoted string. Reject objects/arrays
	// outright so a shape change is loud, not silent.
	//
	// A container is reported as a *json.UnmarshalTypeError specifically. The
	// error TYPE is load-bearing, not decoration: the fixture gate classifies
	// a decode failure by errors.As, and a fmt.Errorf around a strconv error —
	// which this returned until 2026-07-30 — reads as a VALUE problem. A shape
	// regression landing on any FlexInt field was then invisible to both
	// oracles at once. "Loud" has to mean loud in the channel someone listens
	// on.
	if s[0] == '[' || s[0] == '{' {
		kind := "array"
		if s[0] == '{' {
			kind = "object"
		}
		return &json.UnmarshalTypeError{Value: kind, Type: reflect.TypeOf(FlexInt{})}
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		if _, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64); err != nil {
			return fmt.Errorf("FlexInt: string %q is not an integer: %w", str, err)
		}
	} else if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		return fmt.Errorf("FlexInt: expected number or string, got %s: %w", s, err)
	}
	f.raw = append(json.RawMessage(nil), b...)
	f.set = true
	return nil
}

// MarshalJSON round-trips the captured wire value, or 0 when unset (a missing
// FlexInt field marshals as 0, matching the int zero-value convention).
func (f FlexInt) MarshalJSON() ([]byte, error) {
	if !f.set {
		return []byte("0"), nil
	}
	return f.raw, nil
}

// IsSet reports whether a non-null value was present on the wire.
func (f FlexInt) IsSet() bool { return f.set }

// Int64 returns the integer value whether the wire form was a bare number or
// a quoted string ("123" → 123). Returns 0 when unset/null. The accessor is
// the point: callers compare a typed int, never the raw wire form, so a
// string-vs-number split in the collection cannot reach them.
func (f FlexInt) Int64() int64 {
	if !f.set {
		return 0
	}
	// Bare number first (the common case).
	var n json.Number
	if json.Unmarshal(f.raw, &n) == nil {
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	// Quoted string form.
	var str string
	if json.Unmarshal(f.raw, &str) == nil {
		if i, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64); err == nil {
			return i
		}
	}
	return 0
}

// PhotoID is an opaque media identifier that the API encodes as EITHER a
// JSON number or a JSON string — and, crucially, the string form is usually
// a NON-NUMERIC token ("fakeTokExample01", "synth_token_ab01cd" shape).
// Of 53 censused PostPhoto.ID values only one was numeric; the other 52 are
// opaque tokens. PhotoID is therefore a STRING, not an integer: a number on
// the wire is stored as its decimal text, and an opaque token is stored
// untouched. There is nothing numeric to parse — modelling it as an integer
// (FlexInt) is the bug that shipped four times, because the first real call
// hit a non-numeric token and FlexInt rejected it with
// `FlexInt: string "synth_token_ef02gh" is not an integer`.
//
// PhotoID is a defined string type, so the accessor is the value itself:
// number-form 123456789 and string-form "123456789" both yield "123456789",
// and "fakeTokExample01" survives verbatim. That equivalence is the point.
//
// SWEEP: PhotoID is applied here to PostPhoto.ID. Two sibling id sites
// (MediaItem.ID, Photo.ID) and SearchPostPhoto.ID have the same
// opaque-token-vs-number split and are confirmed broken today — tracked in
// issue #74; each needs its own measurement.
type PhotoID string

// UnmarshalJSON accepts a JSON string (the common, opaque-token form) or a
// JSON number (stored as its decimal text). null decodes to "". Any other
// shape returns an error rather than silently coercing — a silent coerce
// would recreate the wrong-type-hides-bug class this type exists to prevent.
func (p *PhotoID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) == 0 || s == "null" {
		*p = ""
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*p = PhotoID(str)
		return nil
	}
	// Bare number → store its decimal text (e.g. 123456789 → "123456789").
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("PhotoID: expected string or number, got %s: %w", s, err)
	}
	*p = PhotoID(string(n))
	return nil
}

// Photo is an uploaded photo attachment.
type Photo struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Text        string `json:"text,omitempty"`
	Folder      string `json:"folder"`
	FilePath    string `json:"file_path"`
	UpdatedDate string `json:"updated_date,omitempty"`
}

// Video is an uploaded video attachment.
type Video struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	Name              string `json:"name"`
	Title             string `json:"title,omitempty"`
	Description       string `json:"description,omitempty"`
	Folder            string `json:"folder"`
	FilePath          string `json:"file_path"`
	FileThumbnailPath string `json:"file_thumbnail_path,omitempty"`
	Seconds           int    `json:"seconds,omitempty"`
}

// Document is an uploaded document attachment.
type Document struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Folder   string `json:"folder"`
	FilePath string `json:"file_path"`
	Size     int64  `json:"size"`
}

// Settings controls per-post publishing options (VK-specific flags, comments,
// pin, repeat, story/clip/shorts toggles, etc.).
type Settings struct {
	DeletePostDay             *int  `json:"delete_post_day,omitempty"`
	DeletePostHour            *int  `json:"delete_post_hour,omitempty"`
	DisableComments           *bool `json:"disable_comments,omitempty"`
	DownloadVKVideo           *bool `json:"download_vk_video,omitempty"`
	MessageToChannel          *bool `json:"message_to_channel,omitempty"`
	MessageToCommunity        *bool `json:"message_to_community,omitempty"`
	NotPublishInVideos        *bool `json:"not_publish_in_videos,omitempty"`
	PublishOnlyInVideos       *bool `json:"publish_only_in_videos,omitempty"`
	PinPost                   *bool `json:"pin_post,omitempty"`
	PlanByNetwork             *bool `json:"plan_by_network,omitempty"`
	PublishAsCarousel         *bool `json:"publish_as_carousel,omitempty"`
	PublishAsClips            *bool `json:"publish_as_clips,omitempty"`
	ShareClipsToFeed          *bool `json:"share_clips_to_feed,omitempty"`
	ShareClipsToFeedWithText  *bool `json:"share_clips_to_feed_with_text,omitempty"`
	ShareClipsToFeedIfNoVideo *bool `json:"share_clips_to_feed_if_no_video,omitempty"`
	PublishAsShorts           *bool `json:"publish_as_shorts,omitempty"`
	PublishAsStory            *bool `json:"publish_as_story,omitempty"`
	ShareStoriesToFeed        *bool `json:"share_stories_to_feed,omitempty"`
	PublishInChannel          *bool `json:"publish_in_channel,omitempty"`
	ShareChannelToFeed        *bool `json:"share_channel_to_feed,omitempty"`
	RepeatType                *int  `json:"repeat_type,omitempty"`
	RandomContent             *bool `json:"random_content,omitempty"`
	PublishByAccount          *bool `json:"publish_by_account,omitempty"`
}

// PostText is a text block targeted at a specific social network.
// SourceID 0 means the text is shared across all selected networks.
type PostText struct {
	Text     string `json:"text"`
	SourceID int    `json:"source_id"`
}

// MediaAttachment wraps a list of photos or videos.
type MediaAttachment struct {
	Type string      `json:"type"` // always "photos"
	Data []MediaItem `json:"data"`
}

// MediaItem is a Photo or Video inside a MediaAttachment. The API uses
// a oneOf; we model the union with a single struct for simplicity.
type MediaItem struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	Name              string `json:"name"`
	Text              string `json:"text,omitempty"`
	Title             string `json:"title,omitempty"`
	Description       string `json:"description,omitempty"`
	Folder            string `json:"folder"`
	FilePath          string `json:"file_path"`
	FileThumbnailPath string `json:"file_thumbnail_path,omitempty"`
	Seconds           int    `json:"seconds,omitempty"`
	UpdatedDate       string `json:"updated_date,omitempty"`
}

// DocumentsAttachment wraps a list of documents.
type DocumentsAttachment struct {
	Type string     `json:"type"` // always "documents"
	Data []Document `json:"data"`
}

// SettingsAttachment wraps post-level Settings.
type SettingsAttachment struct {
	Type string   `json:"type"` // always "settings"
	Data Settings `json:"data"`
}

// Poll represents a poll attachment.
type Poll struct {
	Question          string       `json:"question"`
	Answers           []PollAnswer `json:"answers"`
	IsAnonymous       bool         `json:"is_anonymous"`
	IsMultipleAnswers bool         `json:"is_multiple_answers"`
	UntilTime         int          `json:"until_time"`
	BGImage           *MediaItem   `json:"bg_image,omitempty"`
}

// PollAnswer represents a single poll answer option.
type PollAnswer struct {
	Text string `json:"text"`
}

// Repost represents a repost attachment (VK/OK).
type Repost struct {
	Link  string `json:"link"`
	Title string `json:"title"`
}

// Comment represents a comment attachment.
type Comment struct {
	Text             string     `json:"text"`
	PublishByAccount bool       `json:"publish_by_account"`
	Photo            *MediaItem `json:"photo,omitempty"`
}

// TelegramButton represents a single Telegram inline button.
type TelegramButton struct {
	Name string `json:"name"`
	Link string `json:"link"`
}

// TelegramButtons wraps a list of Telegram buttons.
type TelegramButtons struct {
	List []TelegramButton `json:"list"`
}

// Attachment is a discriminated union of MediaAttachment,
// DocumentsAttachment, or SettingsAttachment. Use the concrete types
// directly when building a post payload.
type Attachment struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// PublicationDate specifies a scheduled publication time.
type PublicationDate struct {
	Date    string `json:"date"`    // dd.mm.yyyy
	Hours   string `json:"hours"`   // HH
	Minutes string `json:"minutes"` // MM
}

// PostPublishNowPayload publishes immediately to manually selected pages.
type PostPublishNowPayload struct {
	PublicationWhenType int          `json:"publication_when_type"` // 1
	PublicationHowType  int          `json:"publication_how_type"`  // 1
	SelectedPagesIDs    []int        `json:"selected_pages_ids"`
	Texts               []PostText   `json:"texts"`
	Attachments         []Attachment `json:"attachments"`
}

// PostPublishAtPayload publishes at a specific date/time to manually selected pages.
type PostPublishAtPayload struct {
	PublicationWhenType int             `json:"publication_when_type"` // 2
	PublicationHowType  int             `json:"publication_how_type"`  // 1
	PublicationDate     PublicationDate `json:"publication_date"`
	SelectedPagesIDs    []int           `json:"selected_pages_ids"`
	Texts               []PostText      `json:"texts"`
	Attachments         []Attachment    `json:"attachments"`
}

// PostPublishBySchedulePayload publishes via one or more schedules.
type PostPublishBySchedulePayload struct {
	PublicationWhenType int          `json:"publication_when_type"` // 3
	PublicationHowType  int          `json:"publication_how_type"`  // 1 (ignored)
	SchedulesIDs        []int        `json:"schedules_ids"`
	Texts               []PostText   `json:"texts"`
	Attachments         []Attachment `json:"attachments"`
}

// PostPublishByProjectPayload publishes via a project.
// The Hooppy API requires schedules_ids even when project_id is set
// (when_type=3 always uses schedules). project_id is an optional filter
// that scopes the post to a specific project.
type PostPublishByProjectPayload struct {
	PublicationWhenType int          `json:"publication_when_type"` // 3
	PublicationHowType  int          `json:"publication_how_type"`  // 1 (ignored)
	ProjectID           int          `json:"project_id"`
	SchedulesIDs        []int        `json:"schedules_ids"` // required by API even for project
	Texts               []PostText   `json:"texts"`
	Attachments         []Attachment `json:"attachments"`
}

// CreatePostResponse is returned by POST /posts.
type CreatePostResponse struct {
	ID int `json:"id"`
}

// DeletePostResponse is returned by DELETE /posts/{id}.
type DeletePostResponse struct {
	Success bool `json:"success"`
}

// BatchDeletePostsRequest is the body for POST /posts/batch/delete.
type BatchDeletePostsRequest struct {
	IDs string `json:"ids"` // comma-separated, no spaces
}

// BatchMovePostsRequest is the body for POST /posts/batch/move (issue #105).
// PostsIDs is a comma-joined STRING — a JSON array makes the server throw
// ErrorException: explode(...) and return 500 (measured live 2026-07-30).
// Same convention as BatchDeletePostsRequest.IDs.
type BatchMovePostsRequest struct {
	ScheduleID int    `json:"schedule_id"`
	PostsIDs   string `json:"posts_ids"` // comma-separated post IDs, no spaces
}

// PostMoveResult is the outcome of a single-post MovePost (issue #105). The
// POST /posts/batch/move response is just {"success":true}; the new
// publication_date is recovered from a post-move GET /posts/{id}/edit, because
// a move re-slots the post to the TAIL of the target queue and the server
// assigns the date — moving into a booked schedule is a silent months-long
// delay otherwise (measured: into a booked schedule → a date months out; into
// a stopped schedule → 01.01.1970). ScheduleID is the target schedule the
// post was moved to. Warning is non-empty when the recovered date is the
// epoch or any past date — the signature of a move into a stopped schedule.
type PostMoveResult struct {
	Success         bool             `json:"success"`
	ScheduleID      int              `json:"schedule_id"`
	PublicationDate *PublicationDate `json:"publication_date,omitempty"`
	SlotLookupError string           `json:"slot_lookup_error,omitempty"`
	Warning         string           `json:"warning,omitempty"`
}

// MovedPost is one entry in a BatchMovePosts result. The batch endpoint
// returns {"success":true} with no per-post dates, so each post's new
// publication_date is recovered from a post-move GET /posts/{id}/edit (one
// read per id). A read failure populates SlotLookupError and leaves
// PublicationDate nil — the move succeeded (the post exists in the target
// schedule); the date is reporting. Warning is non-empty when the recovered
// date is the epoch or any past date — the signature of a move into a stopped
// schedule.
type MovedPost struct {
	ID              int              `json:"id"`
	ScheduleID      int              `json:"schedule_id"`
	PublicationDate *PublicationDate `json:"publication_date,omitempty"`
	SlotLookupError string           `json:"slot_lookup_error,omitempty"`
	Warning         string           `json:"warning,omitempty"`
}

// BatchMovePostsResult is the outcome of BatchMovePosts (issue #105).
type BatchMovePostsResult struct {
	Success bool        `json:"success"`
	Moved   []MovedPost `json:"moved"`
}

// ScheduleDay is one day's cell in the calendar returned by
// GET /posts/schedules/{id}/posts. The day's dd.mm.yyyy date is the MAP KEY;
// DayName and DayDate are the server's DISPLAY strings for that same date,
// measured live 2026-07-30 as "Пт" and "1 Января" on a ru account.
//
// Do not derive them from the key: they are localised by the server, and only
// the ru rendering has been observed. Read them, do not reconstruct them.
//
// Measured live 2026-07-30: the day value is an OBJECT, not a bare post array.
// It was first declared as []Post, which made every decode of this endpoint
// fail — see TestLiveFixtureDecodes.
type ScheduleDay struct {
	DayName string `json:"day_name"` // short weekday, e.g. "Пт"
	DayDate string `json:"day_date"` // display date, e.g. "1 Января"
	Posts   []Post `json:"posts"`
}

// ScheduleCalendar is the posts_by_days calendar: dd.mm.yyyy → that day's
// cell. It exists as a named type solely to carry UnmarshalJSON.
//
// An EMPTY posts_by_days arrives as a JSON list, `[]`, while a populated one
// arrives as an object. A plain map[string]ScheduleDay field aborts the ENTIRE
// decode on the empty case — taking total_rows with it, which is the one
// number the caller still needs there.
//
// Measured live 2026-07-30, three independent ways, always `[]` and never `{}`:
// a page past the end (total_rows 96), a date window with no days in it
// (total_rows 0), and a schedule with an empty queue.
//
// The cause is PHP's json_encode, which cannot distinguish an empty
// associative array from an empty list — but do NOT generalise that to the
// whole API: testdata/live/schedule_edit.json records
// selected_albums_by_source_ids and selected_pages_by_source_ids as `{}`, two
// empty PHP associative arrays that arrive as objects. The `[]` form is
// measured for THIS field; adding UnmarshalJSON to other map fields on the
// strength of the general claim would be cargo-culting it.
type ScheduleCalendar map[string]ScheduleDay

// MarshalJSON emits `{}` for a nil calendar rather than `null`. Both
// front-ends re-emit this envelope verbatim, and the repo's own rule for
// day_counts is that the output shape must not change between branches.
func (c ScheduleCalendar) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]ScheduleDay(c))
}

// UnmarshalJSON accepts the object form and PHP's empty-list form. A NON-empty
// list is still an error: that would be a real shape change, not this quirk.
func (c *ScheduleCalendar) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var list []json.RawMessage
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return err
		}
		if len(list) > 0 {
			return fmt.Errorf("posts_by_days: got a JSON array of %d elements; only the EMPTY array is accepted (PHP's encoding of an empty calendar), a populated calendar must be an object", len(list))
		}
		*c = ScheduleCalendar{}
		return nil
	}
	var m map[string]ScheduleDay
	if err := json.Unmarshal(trimmed, &m); err != nil {
		return err
	}
	if m == nil {
		// `null`, or the field absent from an object we still decode: make the
		// non-nil guarantee unconditional rather than true-for-two-encodings.
		m = map[string]ScheduleDay{}
	}
	*c = m
	return nil
}

// SchedulePostsResponse is the envelope for GET /posts/schedules/{id}/posts
// (issue #106). PostsByDays is keyed dd.mm.yyyy → that day's cell. TotalRows is
// the queue depth. One call returns the whole calendar — no paged walk.
//
// The LAST key in PostsByDays is the booked-until date ONLY when IsHasMore is
// false AND the query was not narrowed by date or page: a narrowed query's last
// key is the WINDOW's last day. The caveat is on ListSchedulePosts and on both
// front-ends, and it belongs here too — this is the value a caller actually
// holds.
type SchedulePostsResponse struct {
	PostsByDays ScheduleCalendar `json:"posts_by_days"`
	TotalRows   int              `json:"total_rows"`
	RowsLimit   int              `json:"rows_limit"`
	IsHasMore   bool             `json:"is_has_more"`
}

// UploadMediaResponse is returned by POST /files/media/upload.
type UploadMediaResponse struct {
	Photo MediaItem `json:"photo"` // photo or video
}

// UploadDocumentResponse is returned by POST /files/documents/upload.
type UploadDocumentResponse struct {
	Document Document `json:"document"`
}

// AccountsResponse wraps GET /accounts.
type AccountsResponse struct {
	List      []Account `json:"list"`
	TotalRows int       `json:"total_rows"`
	IsHasMore bool      `json:"is_has_more"`
	RowsLimit int       `json:"rows_limit"`
}

// PagesResponse wraps GET /accounts/pages.
type PagesResponse struct {
	List      []Page `json:"list"`
	TotalRows int    `json:"total_rows"`
	IsHasMore bool   `json:"is_has_more"`
	RowsLimit int    `json:"rows_limit"`
}

// ProjectsResponse wraps GET /posts/projects.
type ProjectsResponse struct {
	List      []Project `json:"list"`
	TotalRows int       `json:"total_rows"`
	IsHasMore bool      `json:"is_has_more"`
	RowsLimit int       `json:"rows_limit"`
}

// SchedulesResponse wraps GET /posts/schedules.
type SchedulesResponse struct {
	List      []Schedule `json:"list"`
	TotalRows int        `json:"total_rows"`
	IsHasMore bool       `json:"is_has_more"`
	RowsLimit int        `json:"rows_limit"`
}

// PostsResponse wraps GET /posts.
type PostsResponse struct {
	List      []Post `json:"list"`
	TotalRows int    `json:"total_rows"`
	IsHasMore bool   `json:"is_has_more"`
	RowsLimit int    `json:"rows_limit"`
}

// --- Posts search (scraping external pages) — UNDOCUMENTED ---

// SearchPostPhoto is a photo attachment in a scraped post.
type SearchPostPhoto struct {
	ID      int    `json:"id"`
	OwnerID int    `json:"owner_id"`
	URL     string `json:"url"`
	Info    string `json:"info"`
}

// SearchPostOwner is the source page/group of a scraped post.
type SearchPostOwner struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Alias string `json:"alias"`
	Photo string `json:"photo"`
	Link  string `json:"link"`
}

// SearchPost is a post scraped from an external social media page.
type SearchPost struct {
	ID                     int               `json:"id"`
	IsAttachmentsInProcess int               `json:"is_attachments_in_process"`
	SourceID               int               `json:"source_id"`
	PublicationDate        string            `json:"publication_date"`
	Text                   string            `json:"text"`
	Photos                 []SearchPostPhoto `json:"photos"`
	Videos                 []json.RawMessage `json:"videos"`
	Audios                 []json.RawMessage `json:"audios"`
	Documents              []json.RawMessage `json:"documents"`
	Owner                  SearchPostOwner   `json:"owner"`
	Link                   string            `json:"link"`
	Likes                  string            `json:"likes"`
	Reposts                string            `json:"reposts"`
	Views                  string            `json:"views"`
	Comments               string            `json:"comments"`
	Involvement            string            `json:"involvement"`
	VideoDuration          int               `json:"video_duration"`
	IsUsed                 int               `json:"is_used"`
}

// SearchPostsResponse wraps GET /posts-search.
type SearchPostsResponse struct {
	List        []SearchPost      `json:"list"`
	TotalRows   int               `json:"total_rows"`
	IsHasMore   bool              `json:"is_has_more"`
	RowsLimit   int               `json:"rows_limit"`
	FiltersPlug []json.RawMessage `json:"filters_plug"`
}

// SearchPostsFilter is the query filter for GET /posts-search.
type SearchPostsFilter struct {
	Text       string
	DateFrom   string // dd.mm.yyyy
	DateTo     string // dd.mm.yyyy
	SourceType int    // 1=social, 2=RSS; must be non-negative (0 = unset); negatives are rejected before any request (see ListSearchPosts)
	// Deprecated: not a server-side filter on /posts-search; a non-zero value
	// errors before the request (#67, #73). Use SourceType, ContentTypes,
	// PhotosAmount, VideoDuration, or Text to narrow.
	SourceID int
	// Deprecated: not a server-side filter on /posts-search; a non-zero value
	// errors before the request (#67, #73). Use SourceType, ContentTypes,
	// PhotosAmount, VideoDuration, or Text to narrow.
	SourceResourceID int
	// Deprecated: not a server-side filter on /posts-search; a non-zero value
	// errors before the request (#67, #73). Use SourceType, ContentTypes,
	// PhotosAmount, VideoDuration, or Text to narrow.
	OwnerID int
	Page    int // 1-indexed; must be non-negative (0 = unset = first page); negatives are rejected before any request — a negative drops the param and the server silently returns page 1
	// Sorting — reaches the wire but NOT differentially measured (see the
	// "assumed" group in TestPhantomFilterSweep). Classified `works` on
	// wire-reach only; a differential run would promote it to measured.
	SortBy        string // publication_date, likes, reposts, comments, views, involvement
	SortDirection string // desc (default) or asc
	// Metric thresholds — NOT server-side filters (#63); a non-zero value
	// errors before the request. Use SortBy (likes|views|reposts|comments|
	// involvement) instead, which does work server-side.
	MinLikes       int
	MinViews       int
	MinComments    int
	MinReposts     int
	MinInvolvement float64
	// Content filters (empirically verified).
	PhotosAmount        int    // photo-count bucket key (video content only is VideoDuration); must be non-negative (0 = unset); negatives are rejected before any request. Pass-through: any positive key is sent verbatim — the valid key space is NOT enumerable client-side (filters_plug values:[] is empty). Saturates: keys 10 and 99 return identical counts, so the semantics are "N or more photos", not "exactly N". See the measured table in ListSearchPosts.
	VideoDuration       int    // video-duration bucket key (video content only); must be non-negative (0 = unset); negatives are rejected before any request. Pass-through: any positive key is sent verbatim — the valid key space is NOT enumerable client-side (filters_plug values:[] is empty). A prior guard hardcoded a 1..4 enum from a narrow measurement; a wider measurement found keys 5-8 are real and each returns a distinct result set, so the enum was removed. Do NOT re-introduce a hardcoded upper bound. See the measured table in ListSearchPosts.
	ContentTypes        string // comma-separated: photos, videos, audios, documents, links (AND filter)
	ContentTypesExclude string // comma-separated — exclude posts with these types
}

// SourceResource is a configured source of posts to scrape (a group of social media pages).
type SourceResource struct {
	ID                    int    `json:"id"`
	UserID                int    `json:"user_id"`
	Name                  string `json:"name"`
	SourceType            int    `json:"source_type"` // 1=social, 2=RSS
	SearchType            int    `json:"search_type"` // 1=pages, 2=hashtag
	SourceID              int    `json:"source_id"`   // social network ID
	Data                  string `json:"data"`        // URLs separated by \n
	Hashtag               string `json:"hashtag"`
	Link                  string `json:"link"`
	PostsFilter           *bool  `json:"posts_filter"`
	PostsText             *bool  `json:"posts_text"`
	PostsUpgrade          *bool  `json:"posts_upgrade"`
	PostsTextModification *bool  `json:"posts_text_modification"`
}

// SourceResourcesResponse wraps GET /posts-search/source-resources.
type SourceResourcesResponse struct {
	List []SourceResource `json:"list"`
}

// SocialAccount is an authenticated account that can be used as a parser.
type SocialAccount struct {
	ID       int    `json:"id"`
	SourceID int    `json:"source_id"`
	Name     string `json:"name"`
}

// ParsingFormResponse wraps GET /posts-search/parsing/form.
type ParsingFormResponse struct {
	SourceResources     []SourceResource `json:"source_resources"`
	SocialAccounts      []SocialAccount  `json:"social_accounts"`
	IsParsingInProgress bool             `json:"is_parsing_in_progress"`
}

// ParsingStartPayload is the request body for POST /posts-search/parsing/start.
//
// Wire format for date_from/date_to is dd.mm.yyyy strings ("" = any), NOT
// unix timestamps — the server's createDateFromString (DatesTrait.php) parses
// date strings and rejects every non-zero integer (issue #61). MarshalJSON
// emits the strings; the int fields below are a deprecated convenience that
// convert to dd.mm.yyyy in UTC.
type ParsingStartPayload struct {
	SourceType                int `json:"source_type"`                   // 1=social, 2=RSS
	SearchType                int `json:"search_type"`                   // 1=pages, 2=hashtag
	SourceID                  int `json:"source_id"`                     // social network ID
	SourceResourceID          int `json:"source_resource_id"`            // source resource ID
	SocialAccountForParsingID int `json:"social_account_for_parsing_id"` // account to parse with
	// DateFromDay is the start of the date window as dd.mm.yyyy ("" = any).
	// Takes precedence over the deprecated DateFrom int when non-empty.
	DateFromDay string `json:"-"`
	// DateToDay is the end of the date window as dd.mm.yyyy ("" = any).
	// Takes precedence over the deprecated DateTo int when non-empty.
	DateToDay string `json:"-"`
	// Deprecated: DateFrom is a unix-second timestamp converted to dd.mm.yyyy
	// in UTC for the wire. A day-granularity wire format cannot faithfully
	// carry an instant, and the conversion zone is UTC (not the account's
	// timezone — see issue #62 for the sibling one-day-offset defect). Use
	// DateFromDay for an exact dd.mm.yyyy value. 0 = any.
	DateFrom int `json:"date_from"`
	// Deprecated: DateTo is a unix-second timestamp converted to dd.mm.yyyy
	// in UTC for the wire. See DateFrom for the zone caveat and issue #62.
	// Use DateToDay for an exact dd.mm.yyyy value. 0 = any.
	DateTo int `json:"date_to"`
}

// dayDateFormat is the vendor's date-only wire format for parsing dates.
const dayDateFormat = "02.01.2006"

// validateDDMMYYYY rejects a non-empty day string that is not dd.mm.yyyy
// before any HTTP request is issued. The server's createDateFromString
// returns a three-word 500 on a malformed date — the client validates first
// so the error names the expected format (issue #61). Shared by StartParsing
// and ListSchedulePosts; the caller wraps with its op name so a refusal
// identifies which call rejected (issue #116). Lives beside dayDateFormat
// (the format it parses) so a reader in projects.go or posts_search.go
// finds the validator and the format constant in one place.
func validateDDMMYYYY(field, day string) error {
	if day == "" {
		return nil
	}
	if _, err := time.Parse(dayDateFormat, day); err != nil {
		return fmt.Errorf("%s %q is not a valid dd.mm.yyyy date", field, day)
	}
	return nil
}

// MarshalJSON emits date_from/date_to as dd.mm.yyyy strings ("" = any),
// the wire format POST /posts-search/parsing/start expects (issue #61).
// The Day string fields take precedence over the deprecated int fields.
func (p ParsingStartPayload) MarshalJSON() ([]byte, error) {
	type wire struct {
		SourceType                int    `json:"source_type"`
		SearchType                int    `json:"search_type"`
		SourceID                  int    `json:"source_id"`
		SourceResourceID          int    `json:"source_resource_id"`
		SocialAccountForParsingID int    `json:"social_account_for_parsing_id"`
		DateFrom                  string `json:"date_from"`
		DateTo                    string `json:"date_to"`
	}
	return json.Marshal(wire{
		SourceType:                p.SourceType,
		SearchType:                p.SearchType,
		SourceID:                  p.SourceID,
		SourceResourceID:          p.SourceResourceID,
		SocialAccountForParsingID: p.SocialAccountForParsingID,
		DateFrom:                  parsingWireDate(p.DateFromDay, p.DateFrom),
		DateTo:                    parsingWireDate(p.DateToDay, p.DateTo),
	})
}

// parsingWireDate resolves a date field to its dd.mm.yyyy wire string: the
// Day string wins when non-empty; otherwise a non-zero unix-second int is
// formatted in UTC; 0/empty yields "" (any).
func parsingWireDate(day string, unix int) string {
	if day != "" {
		return day
	}
	if unix == 0 {
		return ""
	}
	return time.Unix(int64(unix), 0).UTC().Format(dayDateFormat)
}

// ParsingStartResponse wraps POST /posts-search/parsing/start.
type ParsingStartResponse struct {
	Success bool `json:"success"`
}

// CopySearchPostPayload copies or rewrites a scraped post (from GET /posts-search)
// to the user's own pages. Used by CopySearchPost (PUT /posts/copy),
// RewriteSearchPost (POST /posts with as_copy=1), and ImportSearchPost
// (PUT /posts/import).
//
// Photo handling: to include photos when rewriting, call GetSearchPostEdit
// to get the scraped post's attachments, extract photo data, and pass them
// in Attachments as [{type: "photos", data: [photo objects]}].
//
// ID precedence — SearchPostIDs (batch) wins over SearchPostID (single):
//   - SearchPostIDs non-empty: the slice is comma-joined in CALLER ORDER and
//     sent as the ids wire field. The server assigns schedule slots in the
//     order it receives ids, so the caller controls publication order.
//   - SearchPostIDs empty + SearchPostID non-zero: the scalar is sent as the
//     sole id (the legacy single-post path).
//   - both set: RewriteSearchPost/ImportSearchPost error before any request
//     (ambiguous intent — pass only one).
//   - both empty: RewriteSearchPost/ImportSearchPost error before any request
//     (nothing to copy).
//
// CopySearchPost (PUT /posts/copy) uses a different wire shape — it serializes
// SearchPostID as the singular search_post_id int directly and does NOT send
// the ids string; the batch slice does not apply to that endpoint.
//
// UNDOCUMENTED: PUT /posts/copy, POST /posts with as_copy=1, and
// PUT /posts/import are not in the public OpenAPI spec.
type CopySearchPostPayload struct {
	SearchPostID        int              `json:"search_post_id"`             // single scraped post ID (legacy; used by CopySearchPost, and by Rewrite/Import when SearchPostIDs is empty)
	SearchPostIDs       []int            `json:"search_post_ids,omitempty"`  // batch of scraped post IDs; when non-empty, wins over SearchPostID on Rewrite/Import (comma-joined in caller order). CopySearchPost REFUSES a non-empty slice before any request — PUT /posts/copy takes a singular search_post_id int and silently ignores search_post_ids, so a batch slice on that endpoint is a phantom (it would marshal onto the wire with err == nil); the slice is honoured only by RewriteSearchPost/ImportSearchPost, which join it into the ids wire field.
	PublicationWhenType int              `json:"publication_when_type"`      // 1=now, 2=at specific time, 3=by schedule
	PublicationHowType  int              `json:"publication_how_type"`       // 1
	SelectedPagesIDs    []int            `json:"selected_pages_ids"`         // for when_type=1 or 2
	SchedulesIDs        []int            `json:"schedules_ids"`              // for when_type=3
	PublicationDate     *PublicationDate `json:"publication_date,omitempty"` // for when_type=2
	Texts               []PostText       `json:"texts"`                      // custom text to override original
	Attachments         []Attachment     `json:"attachments"`                // photos from GetSearchPostEdit
}

// SearchPostEditResponse is returned by GET /posts-search/{id}/edit?as_copy=1.
// It contains the scraped post's texts and attachments in a format suitable
// for re-publishing via POST /posts with as_copy=1.
//
// The Attachments field contains objects with Type ("photo", "video", etc.)
// and Data (the photo/video metadata: id, url, source_id).
//
// UNDOCUMENTED: GET /posts-search/{id}/edit is not in the public OpenAPI spec.
type SearchPostEditResponse struct {
	ID                   string           `json:"id"`
	PublicationWhenType  int              `json:"publication_when_type"`
	PublicationHowType   int              `json:"publication_how_type"`
	PublicationWhereType int              `json:"publication_where_type"`
	PublicationDate      *PublicationDate `json:"publication_date"`
	CreatedBy            int              `json:"created_by"`
	Texts                []PostText       `json:"texts"`
	Attachments          []Attachment     `json:"attachments"`
}

// SearchPost metric parse accessors (issue #62).
//
// Wire format: the server sends Likes, Reposts, Views, Comments and
// Involvement as display-formatted STRINGS, not numbers — e.g.
// {"views": "334,881", "likes": "1", "involvement": "0.520"}. Integer
// metrics use a thousands separator (comma in the observed locale); the
// separator is NOT guaranteed to be a comma across the vendor's locales, so
// the struct fields stay string and these accessors strip the separator and
// parse. Involvement is a decimal ratio (0.0–100.0-ish), not a percentage
// string. Malformed input returns an error rather than a silent 0 — a
// helper that returned 0 on failure would recreate the exact
// silent-wrongness this accessor exists to prevent (a naive string
// comparison ranks "334,881" below "87,008").
//
// Do NOT retype the fields to numbers with a custom unmarshaller: a silent
// parse failure on an unexpected separator would be worse than the current
// honest string.
//
// Signed-value asymmetry (issue #65 item 3): metricShapeRe accepts an
// optional leading sign, so a server-sent "-5" likes parses to -5. This is
// FAITHFUL parsing on the response side, not a silent wrong value — the
// server is the source of truth for what it emitted, and clamping or
// rejecting its data here would mask a vendor bug rather than surface it.
// This is deliberately asymmetric with the request side (ListSearchPosts /
// ListPosts / ListAccounts / ListPages reject negative IDs and page
// numbers before any request): on the request side the CALLER is the
// source of input and we can validate intent; on the response side the
// SERVER is the source and we must reflect what it sent. A caller that
// wants a domain check (e.g. likes must be >= 0) can apply it on the
// returned int — the accessor does not silently drop the signal.
func (p SearchPost) ViewsInt() (int, error)    { return parseMetricInt("views", p.Views) }
func (p SearchPost) LikesInt() (int, error)    { return parseMetricInt("likes", p.Likes) }
func (p SearchPost) RepostsInt() (int, error)  { return parseMetricInt("reposts", p.Reposts) }
func (p SearchPost) CommentsInt() (int, error) { return parseMetricInt("comments", p.Comments) }
func (p SearchPost) InvolvementFloat() (float64, error) {
	return parseMetricFloat("involvement", p.Involvement)
}

// metricShapeRe pins the wire format the parse accessors accept BEFORE any
// comma-stripping. The shape is: an optional sign, then either a plain
// decimal (0, 864, 1000, 334881, 0.520, 1234.56, 2.0) or a
// comma-thousands-grouped integer part (1,520 / 334,881 / 1,234.56). A
// leading-zero head followed by a comma (e.g. "0,520") is REJECTED: the
// vendor is a Russian-language service where the comma is the decimal
// separator, so "0,520" is the ratio 0.520, not 520 — and stripping the
// comma before ParseFloat would yield 520.0 with err==nil, the exact
// 1000×-wrong silent value this accessor exists to prevent. Thousands
// grouping never writes a leading zero before the comma, so rejecting this
// shape costs nothing. Non-thousands-grouped comma forms ("1,2,3", "3,14")
// and other-locale separators ("1 234", "1.234,56") are rejected too. The
// ungrouped branch allows any number of digits ([1-9]\d*) so a plain
// integer of 1000+ or a decimal with a 4+-digit integer part is accepted —
// the prior [1-9]\d{0,2} cap rejected "1000", "334881", and "1234.56",
// contradicting the function's own doc comment and error text. See issue
// #65 item 1.
var metricShapeRe = regexp.MustCompile(`^[+-]?(0(\.\d+)?|[1-9]\d*(\.\d+)?|[1-9]\d{0,2}(,\d{3})+(\.\d+)?)$`)

// validateAndStripMetric validates that v is a well-formed metric string
// (per metricShapeRe) and returns it with thousands-separator commas
// removed, ready for strconv.Atoi/ParseFloat. The bool is true iff v was
// non-empty and matched the shape — callers use it (not an empty-string
// re-test) to decide "no value → 0", so a future regex edit that admits
// the empty string cannot silently turn a present value into a 0. A shape
// failure returns (false, error) naming the field — the shared gate for
// parseMetricInt and parseMetricFloat so the regex fix lives in one
// place, not two.
func validateAndStripMetric(name, v string) (string, bool, error) {
	if v == "" {
		return "", false, nil
	}
	if !metricShapeRe.MatchString(v) {
		return "", false, fmt.Errorf("hooppy: SearchPost.%s: parse %q: not a well-formed metric (expected a plain or comma-thousands-grouped number; a decimal comma is not accepted — the vendor's locale uses comma as the decimal separator)", name, v)
	}
	return strings.ReplaceAll(v, ",", ""), true, nil
}

// parseMetricInt strips the observed thousands separator (comma) and parses
// an integer metric string as sent by the server (e.g. "334,881" → 334881).
// An empty string is treated as 0 (the server sends "" for absent metrics).
// The shape is validated BEFORE stripping so a decimal-comma form (which the
// Russian-language vendor uses as its decimal separator) is rejected rather
// than silently parsed to a wrong value. Any other unparseable form —
// including an unexpected separator from a different locale — returns an
// error rather than a silent 0, so a caller cannot accidentally rank on a
// wrong value.
func parseMetricInt(name, v string) (int, error) {
	s, present, err := validateAndStripMetric(name, v)
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("hooppy: SearchPost.%s: parse %q: %w", name, v, err)
	}
	return n, nil
}

// parseMetricFloat strips the observed thousands separator (comma) and parses
// a decimal metric string (involvement is a ratio, e.g. "0.520" or "1,234.56").
// The shape is validated BEFORE stripping so a decimal-comma form (e.g.
// "0,520", which the Russian-language vendor emits as the ratio 0.520) is
// rejected rather than silently parsed to 520.0 — a 1000×-wrong value with
// err==nil. Same error discipline as parseMetricInt.
func parseMetricFloat(name, v string) (float64, error) {
	s, present, err := validateAndStripMetric(name, v)
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("hooppy: SearchPost.%s: parse %q: %w", name, v, err)
	}
	return f, nil
}
