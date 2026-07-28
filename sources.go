package hooppy

// SourceID identifies a social network inside the Hooppy system.
// Values are stable identifiers published on https://hooppy.ru/en.
type SourceID int

const (
	SourceVK           SourceID = 1
	SourceOK           SourceID = 2
	SourceFacebook     SourceID = 3
	SourceTwitter      SourceID = 4
	SourceMyWorld      SourceID = 5
	SourcePinterest    SourceID = 6
	SourceTumblr       SourceID = 8
	SourceTelegramChan SourceID = 9
	SourceInstagramFB  SourceID = 10
	SourceTelegramAcc  SourceID = 11
	SourceDzen         SourceID = 13
	SourceTikTok       SourceID = 14
	SourceYouTube      SourceID = 17
	SourceLinkedIn     SourceID = 18
	SourceWhatsApp     SourceID = 22
	SourceRutube       SourceID = 23
	SourceInstagram    SourceID = 29
	SourceYappy        SourceID = 32
	SourceMax          SourceID = 33
	SourceThreads      SourceID = 34
	SourceVKChats      SourceID = 35
)

var sourceNames = map[SourceID]string{
	SourceVK:           "vkontakte",
	SourceOK:           "odnoklassniki",
	SourceFacebook:     "facebook",
	SourceTwitter:      "twitter",
	SourceMyWorld:      "myworld",
	SourcePinterest:    "pinterest",
	SourceTumblr:       "tumblr",
	SourceTelegramChan: "telegram_channel",
	SourceInstagramFB:  "instagram_fb",
	SourceTelegramAcc:  "telegram_account",
	SourceDzen:         "dzen",
	SourceTikTok:       "tiktok",
	SourceYouTube:      "youtube",
	SourceLinkedIn:     "linkedin",
	SourceWhatsApp:     "whatsapp",
	SourceRutube:       "rutube",
	SourceInstagram:    "instagram",
	SourceYappy:        "yappy",
	SourceMax:          "max",
	SourceThreads:      "threads",
	SourceVKChats:      "vkontakte_chats",
}

// String returns the human-readable name of the social network.
func (s SourceID) String() string {
	if name, ok := sourceNames[s]; ok {
		return name
	}
	return "unknown"
}
