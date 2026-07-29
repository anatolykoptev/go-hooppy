package hooppy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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
type Page struct {
	ID              int    `json:"id"`
	SourceID        int    `json:"source_id"`
	AccountID       int    `json:"account_id"`
	SocialPageID    string `json:"social_page_id"`
	SocialPageName  string `json:"social_page_name"`
	SocialPagePhoto string `json:"social_page_photo"`
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

// Post is a minimal post representation. The live API returns many more
// fields depending on context; callers needing them should decode the raw
// response body directly.
type Post struct {
	ID int `json:"id"`
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
	SourceType       int    // 1=social, 2=RSS; must be non-negative (0 = unset); negatives are rejected before any request (see ListSearchPosts)
	SourceID         int    // social network ID (1=VK, 7=Instagram, etc.); must be non-negative (0 = unset); negatives are rejected before any request
	SourceResourceID int    // source resource ID (from ListSourceResources); must be non-negative (0 = unset); negatives are rejected before any request
	OwnerID          int    // page ID within source; must be non-negative (0 = unset); negatives are rejected before any request
	Page             int    // 1-indexed; must be non-negative (0 = unset = first page); negatives are rejected before any request — a negative drops the param and the server silently returns page 1
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
