package hooppy

import (
	"encoding/json"
	"strings"
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
	PublishAsStorySourceIDs     int    `json:"publish_as_story_source_ids"`      // 0=none
	PublishAsReels              int    `json:"publish_as_reels"`                 // 0/1
	PublishAsClips              int    `json:"publish_as_clips"`                 // 0/1
	PublishAsShorts             int    `json:"publish_as_shorts"`                // 0/1
	PublishAsArticle            int    `json:"publish_as_article"`               // 0/1
	PublishAsArticleByLink      int    `json:"publish_as_article_by_link"`       // 0/1
	PublishInChannel            int    `json:"publish_in_channel"`               // 0/1
	ShareStoriesToFeed          int    `json:"share_stories_to_feed"`            // 0/1
	ShareStoriesToFeedSourceIDs int    `json:"share_stories_to_feed_source_ids"` // 0=none
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
}

// NewSchedulePayload returns a SchedulePayload with sensible defaults:
// all flags off (0), state=active, publication_how_type=manual,
// publication_where_type=pages. Override fields as needed before
// calling CreateSchedule or UpdateSchedule.
func NewSchedulePayload(name string) SchedulePayload {
	return SchedulePayload{
		Name:                 name,
		State:                1,
		PublicationHowType:   1,
		PublicationWhereType: 1,
	}
}

// ScheduleResponse is returned by POST/PUT/DELETE /posts/schedules.
// The API returns the full schedule list on success.
type ScheduleResponse struct {
	Success   bool       `json:"success"`
	Schedules []Schedule `json:"schedules"`
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
type PostIDResponse struct {
	ID int `json:"id"`
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
	// in the capture, so this field is not photo-specific. post_id is a
	// STRING while id and owner_id are numbers. See PostPhoto for the
	// measured shape. A pointer so a text-only post (photo null/absent)
	// decodes to nil rather than a zero-value struct.
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
	// errors_for_source_ids: per-post publication failures. Measured: an
	// ARRAY (the server sends [], not the object a prior fixture guessed).
	// The item shape is NOT measured — modeled as []json.RawMessage so any
	// array content decodes without aborting, and no struct fields are
	// inferred from the field name (the rule this whole fix enforces).
	// OPEN CONCERN: capture a post with non-empty errors to type the items.
	ErrorsForSourceIDs []json.RawMessage `json:"errors_for_source_ids"`
}

// PostPublicationDate is the publication_date object returned in a GET
// /posts row. It is a DIFFERENT shape from the PublicationDate used by the
// publish/edit payloads (which is {date, hours, minutes}): the list row
// carries {date, time, timestamp, source_timestamp}, where date is a
// "29 Июля"-style display string, time is a "12:25"-style display string,
// and the two timestamps are integers that differ from each other (one
// appears to carry a timezone offset). Both timestamps are kept; they are
// not collapsed.
type PostPublicationDate struct {
	Date            string `json:"date"`             // "29 Июля"-style display date
	Time            string `json:"time"`             // "12:25"-style display time
	Timestamp       int64  `json:"timestamp"`        // unix timestamp
	SourceTimestamp int64  `json:"source_timestamp"` // unix timestamp (carries a timezone offset)
}

// PostPhoto is the media descriptor carried in Post.Photo. Despite the
// field name, it is NOT photo-specific: the measured capture had
// type:"video". Note the mixed types: id and owner_id are NUMBERS while
// post_id is a STRING. Measured from a live GET /posts response:
//
//	{"id":3,"owner_id":4,"post_id":"5","access_key":"A","source_id":1,
//	 "type":"video","title":"A","description":"","duration":383,
//	 "preview":"https://example.invalid/x"}
type PostPhoto struct {
	ID          int    `json:"id"`
	OwnerID     int    `json:"owner_id"`
	PostID      string `json:"post_id"` // STRING while id/owner_id are numbers
	AccessKey   string `json:"access_key"`
	SourceID    int    `json:"source_id"`
	Type        string `json:"type"` // "video" observed — not photo-specific
	Title       string `json:"title"`
	Description string `json:"description"`
	Duration    int    `json:"duration"`
	Preview     string `json:"preview"`
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
// Measured: null on unpublished posts (present and null, not absent). On
// published posts the value shape is inferred from SearchPost (a different,
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
// digits for a number, "" when unset).
type Metric struct {
	raw json.RawMessage
	set bool
}

// UnmarshalJSON accepts null (→ unset), a JSON string, or a JSON number.
func (m *Metric) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) == 0 || s == "null" {
		m.raw, m.set = nil, false
		return nil
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
	Text             string
	DateFrom         string // dd.mm.yyyy
	DateTo           string // dd.mm.yyyy
	SourceType       int    // 1=social, 2=RSS
	SourceID         int    // social network ID (1=VK, 7=Instagram, etc.)
	SourceResourceID int    // source resource ID (from ListSourceResources)
	OwnerID          int    // page ID within source
	Page             int
	// Sorting (empirically verified).
	SortBy        string // publication_date, likes, reposts, comments, views, involvement
	SortDirection string // desc (default) or asc
	// Metric filters (empirically verified).
	MinLikes       int
	MinViews       int
	MinComments    int
	MinReposts     int
	MinInvolvement float64
	// Content filters (empirically verified).
	PhotosAmount        int    // exact photo count
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
type ParsingStartPayload struct {
	SourceType                int `json:"source_type"`                   // 1=social, 2=RSS
	SearchType                int `json:"search_type"`                   // 1=pages, 2=hashtag
	SourceID                  int `json:"source_id"`                     // social network ID
	SourceResourceID          int `json:"source_resource_id"`            // source resource ID
	SocialAccountForParsingID int `json:"social_account_for_parsing_id"` // account to parse with
	DateFrom                  int `json:"date_from"`                     // unix timestamp, 0=any
	DateTo                    int `json:"date_to"`                       // unix timestamp, 0=any
}

// ParsingStartResponse wraps POST /posts-search/parsing/start.
type ParsingStartResponse struct {
	Success bool `json:"success"`
}

// CopySearchPostPayload copies or rewrites a scraped post (from GET /posts-search)
// to the user's own pages. Used by CopySearchPost (PUT /posts/copy) and
// RewriteSearchPost (POST /posts with as_copy=1).
//
// Photo handling: to include photos when rewriting, call GetSearchPostEdit
// to get the scraped post's attachments, extract photo data, and pass them
// in Attachments as [{type: "photos", data: [photo objects]}].
//
// UNDOCUMENTED: PUT /posts/copy and POST /posts with as_copy=1 + search_post_id
// are not in the public OpenAPI spec.
type CopySearchPostPayload struct {
	SearchPostID        int              `json:"search_post_id"`             // ID from GET /posts-search (REQUIRED)
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
