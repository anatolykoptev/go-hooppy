package hooppy

// SourceID identifies a social network inside the Hooppy system.
//
// PROVENANCE: the numeric ids and names below are merged from two vendor
// web bundles (https://hooppy.ru/_nuxt/, no auth) — a smart-tasks chunk
// and the main bundle — cross-checked against ids observed on a live
// account. The public page https://hooppy.ru/en lists network NAMES but
// contains no numeric ids; the earlier claim that ids were "published on"
// that page was false and is removed. Each entry below carries an evidence
// grade in its map comment:
//
//   - confirmed   — two independent vendor bundles agree on the id→name pair
//   - vendor      — one vendor bundle (smart-tasks chunk); also observed live
//   - observed    — id occurs on a live account; name from the vendor's slug
//     convention, not from a bundle table
//   - inferred    — id unique to an earlier inferred table, not verified
//     against any vendor bundle or live account
//
// Where the two sources conflicted, the bundle-confirmed id wins and the
// unverified alternative was dropped so no name maps to two ids. See
// TestSourceNames_Bijective for the invariant.
type SourceID int

const (
	SourceVK          SourceID = 1
	SourceOK          SourceID = 2
	SourceFacebook    SourceID = 3
	SourceTwitter     SourceID = 4
	SourceMyWorld     SourceID = 5
	SourcePinterest   SourceID = 6
	SourceInstagram   SourceID = 7
	SourceTumblr      SourceID = 8
	SourceTelegram    SourceID = 9
	SourceInstagramFB SourceID = 10
	SourceTelegramAcc SourceID = 11
	SourceDzen        SourceID = 13
	SourceTikTok      SourceID = 14
	SourceViber       SourceID = 16
	SourceYouTube     SourceID = 17
	SourceLinkedIn    SourceID = 18
	SourceWhatsApp    SourceID = 22
	SourceRutube      SourceID = 23
	SourceMax         SourceID = 28
	SourceYappy       SourceID = 32
	SourceThreads     SourceID = 34
	SourceVKChats     SourceID = 35
)

// sourceNames is the single id→name table for the library. Every
// SourceID constant MUST have an entry here (TestSourceID_AllConstantsHaveNames
// enforces that), and the mapping MUST be bijective — no name maps to two
// ids, no id maps to two names (TestSourceNames_Bijective enforces that).
var sourceNames = map[SourceID]string{
	SourceVK:          "vkontakte",        // vendor — smart-tasks chunk; observed live
	SourceOK:          "odnoklassniki",    // vendor — smart-tasks chunk; observed live
	SourceFacebook:    "facebook",         // confirmed — two bundles agree
	SourceTwitter:     "twitter",          // confirmed — two bundles agree
	SourceMyWorld:     "myworld",          // inferred — unverified
	SourcePinterest:   "pinterest",        // confirmed — two bundles agree; observed live
	SourceInstagram:   "instagram",        // confirmed — two bundles agree; observed live (id 7, NOT 29)
	SourceTumblr:      "tumblr",           // inferred — unverified
	SourceTelegram:    "telegram",         // confirmed — two bundles agree (id 9; was "telegram_channel", corrected to bundle name)
	SourceInstagramFB: "instagram_fb",     // observed — id 10 on live accounts, pages carry instagram.com links; second Instagram connection method
	SourceTelegramAcc: "telegram_account", // observed — id 11 on live accounts
	SourceDzen:        "dzen",             // vendor — smart-tasks chunk; observed live
	SourceTikTok:      "tiktok",           // confirmed — two bundles agree
	SourceViber:       "viber",            // confirmed — two bundles agree
	SourceYouTube:     "youtube",          // confirmed — two bundles agree; observed live
	SourceLinkedIn:    "linkedin",         // confirmed — two bundles agree; observed live
	SourceWhatsApp:    "whatsapp",         // inferred — unverified
	SourceRutube:      "rutube",           // inferred — unverified
	SourceMax:         "max",              // confirmed — two bundles agree (id 28, NOT 33)
	SourceYappy:       "yappy",            // inferred — unverified
	SourceThreads:     "threads",          // inferred — unverified
	SourceVKChats:     "vkontakte_chats",  // inferred — unverified
}

// String returns the human-readable name of the social network, or
// "unknown" for an id absent from sourceNames.
func (s SourceID) String() string {
	if name, ok := sourceNames[s]; ok {
		return name
	}
	return "unknown"
}
