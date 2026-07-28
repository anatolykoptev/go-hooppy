package hooppy

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
	Name                        string `json:"name"`
	State                       int    `json:"state"`                            // 1=active, 0=paused
	PublicationHowType          int    `json:"publication_how_type"`             // 1=manual, 2=by project
	PublicationWhereType        int    `json:"publication_where_type"`           // 1=pages
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
