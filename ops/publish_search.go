// publish_search.go — the shared scraped-post publishing operations:
// CopySearchPost (deprecated single-post), RewriteSearchPost and
// ImportSearchPost. Both surfaces (CLI `search copy|rewrite|import`, the
// three hooppy_*_search_post MCP tools) are thin adapters over these
// functions.
//
// The six hand-kept builders this replaces had drifted into real defects:
//   - `--to`/selected_pages_ids was silently dropped under when-type 3
//     (issue #139),
//   - `--date/--hours/--minutes` were silently dropped under when-type 1
//     and 3 (issue #140),
//   - MCP import accepted fixed-date and direct-page publishing that the
//     CLI lacked entirely (audit F-7),
//   - a multi-id `ids` wire field with when-type 1|2 hit the server's
//     unconditional `schedules_ids` read and returned HTTP 500 (issue #141).
//
// Issue #141 is decided ALLOW, not refuse: the server's batch defect fires
// only when ONE request carries a comma-joined multi-id `ids` field —
// measured: `ids:"7077704,7077730"` + when_type 2 → 500 "Undefined index:
// schedules_ids", while the SAME posts as per-request single ids (`ids:
// "7077704"`) → 200. These ops always publish per-post single-id
// (client-side resolve+publish loop — required anyway for per-post text,
// attachments and strip-vk-markup), so the multi-id wire form the server
// cannot serve is never emitted. Refusing the combination would forbid a
// journey that works end to end today.
//
// Presence semantics: every optional field is a pointer. nil means "not
// given" — so `--to` omitted and `--to ""` are distinguishable from a real
// list, and an omitted publication_when_type defaults to 3 (by schedule —
// the safe default: a published post cannot be un-published) while a given
// 0, 4 or -1 is refused.
package ops

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/anatolykoptev/go-hooppy"
)

// PublishSearchTarget carries the where/when fields shared by the three
// scraped-post publishing inputs. It is embedded, so its fields promote
// into each input's flag set and MCP input schema.
type PublishSearchTarget struct {
	PublicationWhenType *int    `json:"publication_when_type,omitempty" jsonschema:"Publication timing: 1=publish now, 2=at a specific time, 3=by schedule. Omit = 3 (the safe default — a published post cannot be un-published)." flag:"when-type" flagusage:"1=publish now, 2=at specific time, 3=by schedule (default 3 — a published post cannot be un-published; pass --when-type 1 explicitly to publish now)"`
	PublicationHowType  *int    `json:"publication_how_type,omitempty" jsonschema:"Publication how type. Omit = the operation default (1 for copy/rewrite, 2 for import)." flag:"how-type" flagusage:"publication how type (omit = the command default: 1 for copy/rewrite, 2 for import)"`
	SelectedPagesIDs    *[]int  `json:"selected_pages_ids,omitempty" jsonschema:"Page IDs to publish to — only meaningful with publication_when_type 1 or 2; refused under 3 (a schedule already carries its own pages). Use list_pages to get IDs." flag:"to" flagusage:"comma-separated page IDs to publish to (only with --when-type 1 or 2; refused under --when-type 3)"`
	SchedulesIDs        *[]int  `json:"schedules_ids,omitempty" jsonschema:"Schedule IDs — required for publication_when_type 3, refused under 1 or 2. Use list_schedules to get IDs." flag:"schedules" flagusage:"comma-separated schedule IDs (required for --when-type 3; refused under --when-type 1 or 2)"`
	PublishDate         *string `json:"publish_date,omitempty" jsonschema:"Publication date dd.mm.yyyy — required for publication_when_type 2, refused under 1 or 3." flag:"date" flagusage:"publication date dd.mm.yyyy (required for --when-type 2; refused under --when-type 1 or 3)"`
	PublishHours        *string `json:"publish_hours,omitempty" jsonschema:"Publication hours HH (with publish_date, for publication_when_type 2)." flag:"hours" flagusage:"publication hours HH (with --date, for --when-type 2)"`
	PublishMinutes      *string `json:"publish_minutes,omitempty" jsonschema:"Publication minutes MM (with publish_date, for publication_when_type 2)." flag:"minutes" flagusage:"publication minutes MM (with --date, for --when-type 2)"`
}

// CopySearchPostInput is the parameter set of ops.CopySearchPost — the
// deprecated single-post copy kept for backwards compatibility (new work
// uses ImportSearchPost). Single-post only by construction: there is no
// batch field to drift.
type CopySearchPostInput struct {
	SearchPostID *int `json:"search_post_id,omitempty" jsonschema:"ID of the scraped post (from list_search_posts). REQUIRED. Deprecated: use import_search_post instead." flag:"post-id" flagusage:"scraped post ID from 'search posts' (REQUIRED). copy is single-post only — for a batch use 'search import --post-ids'"`
	PublishSearchTarget
}

// RewriteSearchPostInput is the parameter set of ops.RewriteSearchPost.
type RewriteSearchPostInput struct {
	SearchPostID  *int    `json:"search_post_id,omitempty" jsonschema:"ID of a single scraped post (from list_search_posts). Mutually exclusive with search_post_ids; pass exactly one." flag:"post-id" flagusage:"scraped post ID from 'search posts' (single; mutually exclusive with --post-ids)"`
	SearchPostIDs *[]int  `json:"search_post_ids,omitempty" jsonschema:"IDs of scraped posts (from list_search_posts) for a batch rewrite — each post is resolved and published independently (client-side loop). Mutually exclusive with search_post_id; pass exactly one. Batch keeps each post's original text (text is refused with search_post_ids)." flag:"post-ids" flagusage:"comma-separated scraped post IDs from 'search posts' (batch; mutually exclusive with --post-id). Each post is resolved and published independently — attachments preserved per post. Batch keeps each post's original text (--text is rejected with --post-ids)."`
	Text          *string `json:"text,omitempty" jsonschema:"New text for the post — REQUIRED for search_post_id, refused with search_post_ids (a batch cannot express per-post text)." flag:"text" flagusage:"new text for the post (required for --post-id; NOT allowed with --post-ids — batch keeps each post's original text)"`
	NoAttachments *bool   `json:"no_attachments,omitempty" jsonschema:"Strip all attachments (photos, videos, links, etc.) — publish text only." flag:"no-attachments" flagusage:"strip all attachments (photos, videos, links, etc.) — publish text only"`
	PublishSearchTarget
}

// ImportSearchPostInput is the parameter set of ops.ImportSearchPost.
type ImportSearchPostInput struct {
	SearchPostID  *int   `json:"search_post_id,omitempty" jsonschema:"ID of a single scraped post (from list_search_posts). Mutually exclusive with search_post_ids; pass exactly one." flag:"post-id" flagusage:"scraped post ID from 'search posts' (single; mutually exclusive with --post-ids)"`
	SearchPostIDs *[]int `json:"search_post_ids,omitempty" jsonschema:"IDs of scraped posts (from list_search_posts) for a batch import — each post is resolved and published independently (client-side loop), so text and attachments are preserved per post. Mutually exclusive with search_post_id; pass exactly one." flag:"post-ids" flagusage:"comma-separated scraped post IDs from 'search posts' (batch; mutually exclusive with --post-id). Each post is resolved and published independently (client-side loop) — text and attachments preserved per post. Schedule slots are reported in publication order (the queue's own order), not the order given on the command line."`
	NoAttachments *bool  `json:"no_attachments,omitempty" jsonschema:"Strip all attachments (photos, videos, links, etc.) — publish text only." flag:"no-attachments" flagusage:"strip all attachments (photos, videos, links, etc.)"`
	StripVKMarkup *bool  `json:"strip_vk_markup,omitempty" jsonschema:"Convert VK wiki-link markup [url|text] to text (opt-in). Advertising disclosures (Erid/Реклама./ИНН) are NEVER auto-removed: a disclosure that genuinely is our advertising must stay, and the tool cannot tell the two apart." flag:"strip-vk-markup" flagusage:"convert VK wiki-link markup [url|text] to text (opt-in). Advertising disclosures (Erid/Реклама./ИНН) are NEVER auto-removed: a disclosure that genuinely is our advertising must stay, and the tool cannot tell the two apart."`
	PublishSearchTarget
}

// how-type defaults when the caller omits the field. The CLI defaults these
// per command; the op owns them so MCP gets the same wire values (an omitted
// publication_how_type previously reached the wire as 0 there).
const (
	howTypeDefault   = 1 // copy / rewrite
	howTypeSchedPage = 2 // import (by schedule pages)
)

// fieldRef renders a field's name on BOTH surfaces: "--to
// (selected_pages_ids)". A refusal handed to an operator OR an agent is a
// corrective instruction — it must name the knob that surface's caller can
// actually set. The flag name is the CLI knob; the json name is the MCP
// property.
func fieldRef(in any, goName string) string {
	for _, f := range InputFields(reflect.TypeOf(in)) {
		if f.GoName != goName {
			continue
		}
		if f.FlagName != "" && f.FlagName != "-" && f.JSONName != "" && f.JSONName != f.FlagName {
			return "--" + f.FlagName + " (" + f.JSONName + ")"
		}
		return f.displayName()
	}
	return goName
}

// derefInt / derefIntSlice / derefBool / derefStr flatten pointer-presence
// fields to their effective values.
func derefInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func derefIntSlice(p *[]int) []int {
	if p == nil {
		return nil
	}
	return *p
}

func derefBool(p *bool) bool {
	return p != nil && *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// resolveSearchTarget validates the embedded target fields and builds the
// hooppy.PublishTarget. Every refusal is a *ValidationError raised before
// any request is built, and names BOTH surface spellings of the flag and
// the effective when-type — the rule #138 established for --schedules,
// extended symmetrically (#139 pages-under-3, #140 date-under-non-2):
// a flag combination that cannot do what the caller asked fails before the
// request, naming what would have happened.
//
// defaultHowType is the operation's own how-type (copy/rewrite 1, import 2)
// applied when the caller omits the field.
func resolveSearchTarget(in any, t PublishSearchTarget, defaultHowType int) (hooppy.PublishTarget, error) {
	whenType := derefInt(t.PublicationWhenType, 3)
	if whenType < 1 || whenType > 3 {
		return hooppy.PublishTarget{}, &ValidationError{msg: fmt.Sprintf(
			"%s must be 1 (publish now), 2 (at a specific time) or 3 (by schedule) — got %d; an out-of-range when-type must not fall through to the publish-now branch",
			fieldRef(in, "PublicationWhenType"), whenType)}
	}
	pages := derefIntSlice(t.SelectedPagesIDs)
	schedules := derefIntSlice(t.SchedulesIDs)

	// Meaningless combinations first — these are the silent-drop defects:
	// the flag is accepted and then discarded by the when-type switch, so
	// the publish happens against a DIFFERENT target than the caller set.
	// Each message names the flag (on both surfaces), the when-type, and
	// what the flag's owner believed would happen.
	if whenType == 3 && len(pages) > 0 {
		return hooppy.PublishTarget{}, &ValidationError{msg: fmt.Sprintf(
			"%s is only meaningful with when-type 1 or 2 — under when-type 3 the schedule owns the page set and this flag is silently dropped (issue #139). Drop %s to publish on the schedule's pages, or set %s to 1 or 2 to target pages directly",
			fieldRef(in, "SelectedPagesIDs"), fieldRef(in, "SelectedPagesIDs"), fieldRef(in, "PublicationWhenType"))}
	}
	dateFlags := dateFlagsGiven(in, t)
	if whenType != 2 && len(dateFlags) > 0 {
		how := "publishes NOW"
		if whenType == 3 {
			how = "lands on the schedule's own slots"
		}
		return hooppy.PublishTarget{}, &ValidationError{msg: fmt.Sprintf(
			"the date flags (%s) are only meaningful with when-type 2 (at a specific time) — under when-type %d they are silently dropped and the post %s (issue #140). Set %s to 2 to publish at a set time, or drop the date flags",
			strings.Join(dateFlags, ", "), whenType, how, fieldRef(in, "PublicationWhenType"))}
	}
	if whenType != 3 && len(schedules) > 0 {
		return hooppy.PublishTarget{}, &ValidationError{msg: fmt.Sprintf(
			"%s is only meaningful with when-type 3 (by schedule) — under when-type %d the schedules are silently dropped and the post is published by page/time instead (issue #111). Set %s to 3 to queue by schedule, or drop %s to publish as when-type %d intends",
			fieldRef(in, "SchedulesIDs"), whenType, fieldRef(in, "PublicationWhenType"),
			fieldRef(in, "SchedulesIDs"), whenType)}
	}
	// Then the required-field checks for the chosen when-type.
	if whenType == 3 && len(schedules) == 0 {
		return hooppy.PublishTarget{}, &ValidationError{msg: fmt.Sprintf(
			"%s is required with when-type 3 (by schedule) — a schedule-driven publish targeted at no schedule publishes to nothing",
			fieldRef(in, "SchedulesIDs"))}
	}
	if whenType == 2 && (derefStr(t.PublishDate) == "" || derefStr(t.PublishHours) == "" || derefStr(t.PublishMinutes) == "") {
		return hooppy.PublishTarget{}, &ValidationError{msg: fmt.Sprintf(
			"%s, %s and %s are all required with when-type 2 (at a specific time)",
			fieldRef(in, "PublishDate"), fieldRef(in, "PublishHours"), fieldRef(in, "PublishMinutes"))}
	}
	target := hooppy.PublishTarget{
		PublicationWhenType: whenType,
		PublicationHowType:  derefInt(t.PublicationHowType, defaultHowType),
	}
	switch whenType {
	case 3:
		target.SchedulesIDs = schedules
	case 2:
		target.SelectedPagesIDs = pages
		target.PublicationDate = &hooppy.PublicationDate{
			Date:    derefStr(t.PublishDate),
			Hours:   derefStr(t.PublishHours),
			Minutes: derefStr(t.PublishMinutes),
		}
	default:
		target.SelectedPagesIDs = pages
	}
	return target, nil
}

// dateFlagsGiven lists the display names of the date fields the caller
// actually supplied (pointer non-nil), so the #140 refusal names exactly
// the flags that would have been dropped.
func dateFlagsGiven(in any, t PublishSearchTarget) []string {
	var names []string
	if t.PublishDate != nil {
		names = append(names, fieldRef(in, "PublishDate"))
	}
	if t.PublishHours != nil {
		names = append(names, fieldRef(in, "PublishHours"))
	}
	if t.PublishMinutes != nil {
		names = append(names, fieldRef(in, "PublishMinutes"))
	}
	return names
}

// searchPostIDs resolves the exactly-one-of pair into the ordered id list.
// A batch (--post-ids / search_post_ids) keeps caller order — schedule
// slots bind positionally to each id's own publish. Every id must be
// positive (the server treats 0 as unset and a negative is never valid).
func searchPostIDs(in any, single *int, batch *[]int) (ids []int, err error) {
	if single != nil && len(derefIntSlice(batch)) > 0 {
		return nil, &ValidationError{msg: fieldRef(in, "SearchPostID") + " and " + fieldRef(in, "SearchPostIDs") +
			" are mutually exclusive — pass only one (the scalar for a single post, the list for a batch)"}
	}
	if single == nil && len(derefIntSlice(batch)) == 0 {
		return nil, &ValidationError{msg: fieldRef(in, "SearchPostID") + " or " + fieldRef(in, "SearchPostIDs") +
			" is required — pass the scraped post ID(s) to publish"}
	}
	if single != nil {
		ids = []int{*single}
	} else {
		ids = *batch
	}
	for i, id := range ids {
		if id <= 0 {
			return nil, &ValidationError{msg: fmt.Sprintf("%s must be positive scraped post IDs — got %d at position %d",
				fieldRef(in, "SearchPostIDs"), id, i+1)}
		}
	}
	return ids, nil
}

// CopySearchPost is `search copy` / hooppy_copy_search_post — the DEPRECATED
// single-post copy. It validates the input and delegates to the library's
// CopySearchPost (which routes through the same resolve+publish primitives
// as import). Kept for backwards compatibility; new work should call
// ImportSearchPost.
func CopySearchPost(ctx context.Context, c *hooppy.Client, in CopySearchPostInput) (*hooppy.PostIDResponse, error) {
	if in.SearchPostID == nil {
		return nil, &ValidationError{msg: fieldRef(in, "SearchPostID") + " is required — the scraped post ID to copy (from 'search posts' / list_search_posts)"}
	}
	if *in.SearchPostID <= 0 {
		return nil, &ValidationError{msg: fmt.Sprintf("%s must be a positive scraped post ID — got %d", fieldRef(in, "SearchPostID"), *in.SearchPostID)}
	}
	target, err := resolveSearchTarget(in, in.PublishSearchTarget, howTypeDefault)
	if err != nil {
		return nil, err
	}
	return c.CopySearchPost(ctx, hooppy.CopySearchPostPayload{
		SearchPostID:        *in.SearchPostID,
		PublicationWhenType: target.PublicationWhenType,
		PublicationHowType:  target.PublicationHowType,
		SelectedPagesIDs:    target.SelectedPagesIDs,
		SchedulesIDs:        target.SchedulesIDs,
		PublicationDate:     target.PublicationDate,
	})
}

// RewriteSearchPost is `search rewrite` / hooppy_rewrite_search_post. The
// op owns every combination check the six hand-kept builders enforced
// (mutually exclusive ids, single-post text required, batch text refusal,
// positive ids) plus the when-type target checks, then delegates to the
// library for the resolve+publish loop (which carries the batch+Texts
// broadcast guard and the partial-result contract).
func RewriteSearchPost(ctx context.Context, c *hooppy.Client, in RewriteSearchPostInput) (*hooppy.PostIDResponse, error) {
	ids, err := searchPostIDs(in, in.SearchPostID, in.SearchPostIDs)
	if err != nil {
		return nil, err
	}
	batch := len(derefIntSlice(in.SearchPostIDs)) > 0
	text := derefStr(in.Text)
	if batch && text != "" {
		return nil, &ValidationError{msg: fieldRef(in, "Text") + " is not allowed with " + fieldRef(in, "SearchPostIDs") +
			" — a batch cannot express per-post text (each post keeps its original, resolved per post). For a text override, run one --post-id per post"}
	}
	if !batch && text == "" {
		return nil, &ValidationError{msg: fieldRef(in, "Text") + " is required for " + fieldRef(in, "SearchPostID") +
			" — a single-post rewrite replaces the text; pass --text, or use 'search import' to keep the original"}
	}
	target, err := resolveSearchTarget(in, in.PublishSearchTarget, howTypeDefault)
	if err != nil {
		return nil, err
	}
	payload := hooppy.CopySearchPostPayload{
		PublicationWhenType: target.PublicationWhenType,
		PublicationHowType:  target.PublicationHowType,
		SelectedPagesIDs:    target.SelectedPagesIDs,
		SchedulesIDs:        target.SchedulesIDs,
		PublicationDate:     target.PublicationDate,
		NoAttachments:       derefBool(in.NoAttachments),
	}
	if batch {
		payload.SearchPostIDs = ids
	} else {
		payload.SearchPostID = ids[0]
		payload.Texts = []hooppy.PostText{{Text: text, SourceID: 0}}
	}
	return c.RewriteSearchPost(ctx, payload)
}

// ImportPostOutcome is one attempted post's record — the dedup-via-stdout
// contract runImport established: a "created" entry carries the published
// id, a "created_no_id" entry records a create the server accepted while
// omitting the id (post_id still present, value 0 — the key must not be
// omitempty'd or a re-run cannot tell it from a missing field), a "failed"
// entry carries the reason. VKMarkup/AdMarkers are the raw hygiene findings
// on the ORIGINAL resolved text (before strip), so a surface can warn on
// what the source post actually contained.
type ImportPostOutcome struct {
	SearchPostID int      `json:"search_post_id"`
	Status       string   `json:"status"` // "created", "created_no_id" or "failed"
	PostID       int      `json:"post_id"`
	Error        string   `json:"error,omitempty"`
	VKMarkup     []string `json:"vk_markup,omitempty"`
	AdMarkers    []string `json:"ad_markers,omitempty"`
}

// ImportSearchPostResult is the op's aggregate: the PostIDResponse fields
// (id/ids/slots/created_no_id — the library's own batch aggregation shape,
// embedded so the wire names promote verbatim) plus the per-post record
// both surfaces emit. StripVKMarkup echoes the flag so the result states
// which transformation ran.
type ImportSearchPostResult struct {
	hooppy.PostIDResponse
	StripVKMarkup bool                `json:"strip_vk_markup"`
	PerPost       []ImportPostOutcome `json:"per_post"`
}

// ImportSearchPost is `search import` / hooppy_import_search_post. The op
// owns validation, target building, the per-post resolve+publish loop (the
// same client-side shape the library's ImportSearchPost uses — each post a
// single-id POST, never the multi-id `ids` wire form that trips the #141
// server defect), the optional VK-markup strip, the attachment strip, and
// the hygiene scan.
//
// Result contract (mirrors resolvePublishBatch's three outcomes, with
// PerPost populated on EVERY path — the record of what was attempted):
//   - err == nil                        → every post succeeded (created or
//     created_no_id); a CreateNoIDError is a success bucket, not a failure
//   - err is *hooppy.PartialPostError    → some succeeded, some failed;
//     res.PostIDResponse carries what landed
//   - err non-nil, not *PartialPostError → every post failed (single post
//     returns that post's error verbatim); res is still non-nil so the
//     caller can emit the per_post record
//
// A *ValidationError means the input was refused before ANY request —
// no resolve, no publish.
func ImportSearchPost(ctx context.Context, c *hooppy.Client, in ImportSearchPostInput) (*ImportSearchPostResult, error) {
	ids, err := searchPostIDs(in, in.SearchPostID, in.SearchPostIDs)
	if err != nil {
		return nil, err
	}
	target, err := resolveSearchTarget(in, in.PublishSearchTarget, howTypeSchedPage)
	if err != nil {
		return nil, err
	}
	strip := derefBool(in.StripVKMarkup)
	res := &ImportSearchPostResult{StripVKMarkup: strip, PerPost: make([]ImportPostOutcome, 0, len(ids))}
	var failed []hooppy.PostFailure
	for _, id := range ids {
		content, err := c.ResolveSearchPost(ctx, id)
		if err != nil {
			res.PerPost = append(res.PerPost, ImportPostOutcome{
				SearchPostID: id, Status: "failed",
				Error: fmt.Sprintf("ResolveSearchPost(%d): %v", id, err)})
			failed = append(failed, hooppy.PostFailure{SearchPostID: id, Err: fmt.Errorf("ResolveSearchPost(%d): %w", id, err)})
			continue
		}
		text := ""
		if len(content.Texts) > 0 {
			text = content.Texts[0].Text
		}
		outcome := ImportPostOutcome{
			SearchPostID: id,
			VKMarkup:     detectVKMarkup(text),
			AdMarkers:    detectAdMarkers(text),
		}
		if strip {
			content.Texts = []hooppy.PostText{{Text: stripVKMarkup(text), SourceID: 0}}
		}
		if derefBool(in.NoAttachments) {
			content.Attachments = nil
		}
		r, err := c.PublishPost(ctx, content, target)
		if err != nil {
			var cnid *hooppy.CreateNoIDError
			if errors.As(err, &cnid) {
				// The create SUCCEEDED and the server omitted the id — a
				// third bucket, never a failure (the post may exist on the
				// server; reporting it as failed invites a duplicate
				// re-run). Count it in CreatedNoID, keep the zero id out of
				// IDs (a zero flows into move/update/delete as a real
				// handle, issue #131).
				outcome.Status = "created_no_id"
				res.CreatedNoID++
				res.PerPost = append(res.PerPost, outcome)
				continue
			}
			outcome.Status = "failed"
			outcome.Error = fmt.Sprintf("PublishPost(%d): %v", id, err)
			res.PerPost = append(res.PerPost, outcome)
			failed = append(failed, hooppy.PostFailure{SearchPostID: id, Err: fmt.Errorf("PublishPost(%d): %w", id, err)})
			continue
		}
		outcome.Status = "created"
		outcome.PostID = r.ID
		res.PerPost = append(res.PerPost, outcome)
		res.IDs = append(res.IDs, r.ID)
		res.Slots = append(res.Slots, r.Slots...)
		if res.ID == 0 {
			res.ID = r.ID
			res.PublicationDate = r.PublicationDate
			res.ScheduleID = r.ScheduleID
		}
		if r.SlotLookupError != "" {
			if res.SlotLookupError != "" {
				res.SlotLookupError += "; "
			}
			res.SlotLookupError += r.SlotLookupError
		}
	}
	if len(failed) == 0 {
		return res, nil
	}
	// Single post: return that post's own error verbatim (the pre-batch
	// contract — one post, one error). The result stays populated so the
	// caller can still emit the per_post record.
	if len(ids) == 1 {
		return res, failed[0].Err
	}
	// All-failed batch (no ids landed AND no created-no-id successes): a
	// plain error, NOT *PartialPostError — the type-asserted signal a
	// caller reads as "some succeeded". Wording stays honest about the
	// zero-id case: posts may exist without usable handles.
	if len(res.IDs) == 0 && res.CreatedNoID == 0 {
		return res, fmt.Errorf("hooppy: ImportSearchPost: every post in the batch failed (%d/%d); no post ids were returned — first failure: %w",
			len(failed), len(ids), failed[0].Err)
	}
	return res, &hooppy.PartialPostError{Result: &res.PostIDResponse, Failed: failed}
}

// --- VK wiki-markup + advertising-disclosure hygiene ---------------------
//
// Moved from cmd/hooppy/import_text.go: the transformation (strip) is part
// of request building, and the findings feed both surfaces' output — so the
// op owns all three. The tables and parsing rules are unchanged; their tests
// moved to ops/publish_search_test.go with the functions.

// adMarkers are the Russian advertising-disclosure markers the tool scans
// for. They are NEVER auto-removed: a disclosure that genuinely is our
// advertising must stay, and the tool cannot tell the two cases apart. This
// table exists only to drive the warning, never a transformation.
//
// Case sensitivity is per-marker, decided by false-positive risk:
//   - "Erid" is matched case-INSENSITIVELY. It is a Latin token and the
//     surrounding disclosure text is Cyrillic, so the lower-case "erid:"
//     form (written at least as often as "Erid:") cannot appear inside a
//     Russian word — no false-positive cost. Matching it case-sensitively
//     missed the common form of the single most important marker. Do NOT
//     "fix" this back to case-sensitive: the Latin-token-in-Cyrillic-body
//     invariant is what makes the fold safe.
//   - "ИНН" is matched case-INSENSITIVELY. It is a Cyrillic ABBREVIATION,
//     not a word — the lower-case "инн" is not an ordinary Russian word, so
//     folding has no false-positive cost (a hit is one extra warning, never
//     corruption), and the miss is real: "инн 1234567890" in a sloppy source
//     escapes detection when the marker is case-sensitive.
//   - "Реклама." stays case-SENSITIVE. It is a Cyrillic WORD as well as a
//     disclosure marker; the lower-case "реклама." is an ordinary Russian
//     word ("это реклама. хорошая.") and would fire on non-disclosure text,
//     so folding would trade a real false-positive cost for no gain. The
//     word/abbreviation distinction is why ИНН folds and Реклама. does not.
type adMarker struct {
	needle   string
	foldCase bool
}

var adMarkers = []adMarker{
	{needle: "Erid", foldCase: true},
	{needle: "Реклама.", foldCase: false},
	{needle: "ИНН", foldCase: true},
}

// stripVKMarkup converts VK wiki-link markup [url|text] to text, and the
// internal-page form [[page|display]] to display. It is deliberately
// conservative: a malformed marker (no closing bracket, no pipe, empty
// brackets, an unterminated [[, or a "[" inside the inner text) is left
// byte-untouched. Mangling text on the way into a live publishing queue is
// worse than leaving markup in.
//
// Parsing rules (decided from the markup grammar, not a regex, so a `|`
// inside the display text is handled correctly — the FIRST `|` is the
// separator):
//   - [url|text]        → text
//   - [[page|display]]  → display
//   - [[page]]          → left untouched (a page reference, not display text)
//   - [url]             → left untouched (no pipe = not a wiki-link)
//   - []                → left untouched
//   - unterminated [    → left untouched
//   - unterminated [[   → left untouched (BOTH "[" copied, scan continues
//     after them — advancing one would re-enter the
//     single-bracket parser on the second "[" and
//     mangle "[[a|b]|c]" into "[b|c]")
//   - inner containing "[" → left untouched DELIBERATELY, not because such a
//     marker is invalid. Display text CAN legitimately contain "["
//     ("[https://vk.com/x|see [note]]" is a real link whose display is
//     "see [note]"). The conversion is left untouched to keep stripVKMarkup
//     idempotent and to avoid the nested-bracket corruption class (the old
//     first-close rule turned "[url|[x|y]]" into "[x|y]" on pass 1 and "y"
//     on pass 2). The COST of this trade-off is that a rare valid form whose
//     display text contains "[" is NOT converted — the markup reaches the
//     wire as-is. Handling that form correctly would require bracket-aware
//     display parsing; until then, leaving it untouched is the safe choice.
//
// The function operates on a byte-by-byte scan with strings.Index for the
// closing bracket, so it never half-rewrites a malformed marker: if the
// close is missing, the opening bracket(s) are copied verbatim and scanning
// continues.
func stripVKMarkup(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	i := 0
	for i < len(text) {
		if text[i] != '[' {
			b.WriteByte(text[i])
			i++
			continue
		}
		// '[[' — internal-page form.
		if i+1 < len(text) && text[i+1] == '[' {
			rest := text[i+2:]
			end := strings.Index(rest, "]]")
			if end < 0 {
				// No closing ]] — leave BOTH "[" untouched and advance past
				// them. Advancing by one would re-enter the single-bracket
				// parser on the second "[", which can find a later "]" and
				// "|" and transform text that was never a valid marker
				// ("[[a|b]|c]" was mangled into "[b|c]"). A malformed marker
				// must pass through byte-identical.
				b.WriteString(text[i : i+2])
				i += 2
				continue
			}
			inner := rest[:end]
			if strings.Contains(inner, "[") {
				// A "[" inside the inner is left byte-untouched. It MAY be
				// nested/broken markup ("[[a|[c|d]]]") OR a legitimate display
				// text containing "[" — see the stripVKMarkup doc comment for
				// the trade-off. Leaving it untouched keeps the conversion
				// idempotent and avoids the nested-bracket corruption class.
				b.WriteString(text[i : i+2+end+2])
				i += 2 + end + 2
				continue
			}
			pipe := strings.Index(inner, "|")
			if pipe < 0 {
				// [[page]] — no display text; leave the whole marker untouched.
				b.WriteString(text[i : i+2+end+2])
				i += 2 + end + 2
				continue
			}
			// [[page|display]] → display
			b.WriteString(inner[pipe+1:])
			i += 2 + end + 2
			continue
		}
		// Single '[' — [url|text] form.
		rest := text[i+1:]
		end := strings.Index(rest, "]")
		if end < 0 {
			// No closing ] — leave the '[' untouched, continue after it.
			b.WriteByte(text[i])
			i++
			continue
		}
		inner := rest[:end]
		if strings.Contains(inner, "[") {
			// A "[" inside the inner is left byte-untouched. It MAY be
			// nested/broken markup ("[url|[x|y]]" would lose the outer
			// bracket and become non-idempotent: pass1="[x|y]", pass2="y")
			// OR a legitimate display text containing "[" such as
			// "[https://vk.com/x|see [note]]" — see the stripVKMarkup doc
			// comment for the trade-off. Leaving it untouched keeps the
			// conversion idempotent; the cost is the rare valid form is not
			// converted.
			b.WriteString(text[i : i+1+end+1])
			i += 1 + end + 1
			continue
		}
		pipe := strings.Index(inner, "|")
		if pipe < 0 {
			// [url] or [] — no pipe, not a wiki-link; leave untouched.
			b.WriteString(text[i : i+1+end+1])
			i += 1 + end + 1
			continue
		}
		// [url|text] → text
		b.WriteString(inner[pipe+1:])
		i += 1 + end + 1
	}
	return b.String()
}

// detectVKMarkup returns the raw [url|text] and [[page|display]] markers
// found in the text, or nil if none. Unterminated brackets are NOT reported
// (they are not markers — they are literal text). A "[" inside a marker's
// inner is also NOT reported: stripVKMarkup leaves such a marker
// byte-untouched (the inner "[" may be nested/broken markup such as
// "[[a|[c|d]]]" OR a legitimate display text containing "[" — see
// stripVKMarkup's doc comment for the trade-off), so reporting it would
// warn "VK markup found" and suggest --strip-vk-markup — a flag that
// changes nothing for that marker. Detection and strip must agree on what
// is a marker. [[page]] without a pipe IS reported: it is valid VK wiki
// markup (an internal page reference), just without display text, and the
// operator should know it is there even though strip leaves it untouched.
func detectVKMarkup(text string) []string {
	var hits []string
	i := 0
	for i < len(text) {
		if text[i] != '[' {
			i++
			continue
		}
		if i+1 < len(text) && text[i+1] == '[' {
			rest := text[i+2:]
			end := strings.Index(rest, "]]")
			if end < 0 {
				i++
				continue
			}
			inner := rest[:end]
			if !strings.Contains(inner, "[") {
				// Well-formed [[...]] (no nested "[" inside). Report it
				// whether or not it has a pipe: [[page|display]] is display
				// markup, [[page]] is an internal page reference — both are
				// real VK wiki markup the operator should know about.
				hits = append(hits, text[i:i+2+end+2])
			}
			// A "[" inside the inner is left untouched by strip (may be
			// nested/broken markup OR legitimate display text containing "[");
			// detection MUST NOT report it — reporting it would suggest
			// --strip-vk-markup, a flag that changes nothing for that marker.
			// Detection and strip must agree.
			i += 2 + end + 2
			continue
		}
		rest := text[i+1:]
		end := strings.Index(rest, "]")
		if end < 0 {
			i++
			continue
		}
		inner := rest[:end]
		// Same guard as the [[...]] branch: a "[" inside the inner is left
		// untouched by strip (nested/broken markup OR legitimate display text
		// containing "["), so detection must not report it.
		if !strings.Contains(inner, "[") && strings.Contains(inner, "|") {
			hits = append(hits, text[i:i+1+end+1])
		}
		i += 1 + end + 1
	}
	return hits
}

// detectAdMarkers returns the lines in text that contain at least one
// advertising-disclosure marker (Erid, Реклама., ИНН), or nil if none. The
// matched LINE is returned (not just the marker) so the warning can show
// the operator exactly what would be published. See adMarkers for the
// per-marker case-sensitivity decision.
func detectAdMarkers(text string) []string {
	var hits []string
	for _, line := range strings.Split(text, "\n") {
		for _, m := range adMarkers {
			var found bool
			if m.foldCase {
				found = strings.Contains(strings.ToLower(line), strings.ToLower(m.needle))
			} else {
				found = strings.Contains(line, m.needle)
			}
			if found {
				hits = append(hits, line)
				break
			}
		}
	}
	return hits
}
