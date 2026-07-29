package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/anatolykoptev/go-hooppy"
)

// importArgs carries the `search import` subcommand flags into the testable
// runImport core, decoupled from cobra flag binding and os.Exit.
type importArgs struct {
	postID        int
	postIDs       string
	whenType      int
	howType       int
	schedules     string
	noAttachments bool
	stripVK       bool
}

// perPostResult is one entry in the strip-batch stdout "per_post" array. It
// records the outcome of every post attempted so a re-run after a partial
// failure has the information to skip what already landed: a "created" entry
// carries the published post id; a "failed" entry carries the error. The
// strip-batch path is NOT atomic (it issues N import calls instead of 1), so
// unlike the single-call paths, work already done MUST NOT be discarded on a
// later failure — see runImport's doc comment.
type perPostResult struct {
	SearchPostID int    `json:"search_post_id"`
	Status       string `json:"status"` // "created" or "failed"
	PostID       int    `json:"post_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

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
// Parsing rules (decided from the markup grammar, not a regex, so a `|` inside
// the display text is handled correctly — the FIRST `|` is the separator):
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
//   - inner containing "[" → left untouched (nested/broken markup such as
//     "[[a|[c|d]]]" or "[url|[x|y]]"; a "[" inside the
//     inner means the marker was never valid, and
//     transforming it is non-idempotent)
//
// The function operates on a byte-by-byte scan with strings.Index for the
// closing bracket, so it never half-rewrites a malformed marker: if the close
// is missing, the opening bracket(s) are copied verbatim and scanning
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
				// A "[" inside a [[...]] inner means nested/broken markup
				// ("[[a|[c|d]]]"). Leave the whole marker byte-untouched so
				// it is not half-rewritten or made non-idempotent.
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
			// A "[" inside a [url|text] inner means nested/broken markup
			// ("[url|[x|y]]" would otherwise lose the outer bracket and
			// become non-idempotent: pass1="[x|y]", pass2="y"). Leave the
			// whole marker byte-untouched.
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

// detectVKMarkup returns the raw [url|text] and [[page|display]] markers found
// in the text, or nil if none. Unterminated brackets are NOT reported (they
// are not markers — they are literal text). A "[" inside a marker's inner
// (nested/broken markup such as "[[a|[c|d]]]" or "[url|[x|y]]") is also NOT
// reported: stripVKMarkup leaves such a marker byte-untouched (it is not a
// valid VK marker), so reporting it would warn "VK markup found" and suggest
// --strip-vk-markup — a flag that changes nothing. Detection and strip must
// agree on what is a marker. [[page]] without a pipe IS reported: it is valid
// VK wiki markup (an internal page reference), just without display text, and
// the operator should know it is there even though strip leaves it untouched.
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
			// A "[" inside the inner means nested/broken markup
			// ("[[a|[c|d]]]"); stripVKMarkup leaves it byte-untouched, so
			// detection MUST NOT report it — reporting it would suggest
			// --strip-vk-markup, a flag that changes nothing for a malformed
			// marker. Detection and strip must agree.
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
		// Same guard as the [[...]] branch: a "[" inside the inner means
		// nested/broken markup ("[url|[x|y]]"); strip leaves it untouched,
		// so detection must not report it.
		if !strings.Contains(inner, "[") && strings.Contains(inner, "|") {
			hits = append(hits, text[i:i+1+end+1])
		}
		i += 1 + end + 1
	}
	return hits
}

// detectAdMarkers returns the lines in text that contain at least one
// advertising-disclosure marker (Erid, Реклама., ИНН), or nil if none. The
// matched LINE is returned (not just the marker) so the warning can show the
// operator exactly what would be published. See adMarkers for the per-marker
// case-sensitivity decision.
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

// runImport is the testable core of `hooppy search import`. It validates
// flags, fetches each source post's text for hygiene warnings (always on),
// optionally strips VK wiki-link markup, and publishes. Warnings go to errOut
// (stderr); the JSON result goes to out (stdout). Returns the process exit
// code (0 on success, 1 on error).
//
// Cost profile (the operator must see this, never hidden):
//   - SINGLE post (--post-id): one GetSearchPostEdit + one ImportSearchPost,
//     same as today. Detection runs on the already-fetched edit text at no
//     extra cost. Stripping is also free here — the single path already sends
//     client-side text, so the flag changes the text and nothing else.
//   - BATCH (--post-ids), flag OFF: N GetSearchPostEdit (one per post, for
//     detection) + ONE batch ImportSearchPost. The N edit fetches are the
//     cost of always-on detection — the import itself is still a single call.
//   - BATCH (--post-ids), flag ON: N GetSearchPostEdit + N ImportSearchPost
//     (one per post, each carrying its own transformed text). This is N import
//     calls instead of 1 — the flag changes the cost profile, which is why it
//     is opt-in. Unlike the other paths (which are a single API call and so
//     atomic), this path is NOT atomic: a failure at post K of N leaves
//     posts 1..K-1 already live in the queue. runImport therefore continues
//     past a per-post failure, ALWAYS encodes a per-post result array to
//     stdout (each post marked "created" with its id or "failed" with its
//     error), and exits non-zero only AFTER stdout is written. Do NOT
//     re-simplify this back to an early return — that would hide published
//     posts from stdout and invite a duplicate-spawning re-run.
//
// Attachment delivery divergence (the operator must see this, never hidden).
// This is MEASURED, not inferred from the text parallel — the three-row
// probe below is the grounding, and the explicit-attachment code on the
// strip path is LOAD-BEARING: sending attachments: [] on the single form
// produces a post with NO attachments. Someone reading that code as
// redundant complexity and deleting it would silently publish photo-less
// posts. The comment is what stops them.
//
// Measured against the live endpoint (PUT /posts/import, attachments field
// inspected on the created post):
//
//   - batch (ids "a,b"), attachments []            → 2 photos each (server fetched)
//   - single (ids "a"),  attachments explicit/edit → 1 photo
//   - single (ids "a"),  attachments []            → 0 photos
//
// So: the BATCH form auto-fetches attachments server-side; the SINGLE form
// does not, and sends nothing unless the client sends it. The same
// form-dependent asymmetry already documented for text, now confirmed for
// attachments.
//   - BATCH, flag OFF: the batch import sends NO attachments on the wire —
//     the server downloads photos async from the source ids it receives
//     (is_attachments_in_process). See buildImportPayload: the batch payload
//     never sets Attachments.
//   - BATCH, flag ON: the per-post path MUST use the single-id import form
//     (SearchPostID, not SearchPostIDs) because the batch form cannot express
//     per-post text — one texts array for N ids is a broadcast that blanks
//     posts 2..N (the same constraint buildRewritePayload refuses). The
//     single-id form does NOT auto-fetch attachments server-side (measured
//     above), so each per-post request MUST send its attachments explicitly,
//     read from the edit response (SearchPostEditAttachments). Sending []
//     instead would publish each post with no photos. So turning on a
//     TEXT-hygiene flag also changes ATTACHMENT delivery. This is NOT fixable
//     on the strip path without a per-post-text-capable batch endpoint,
//     which the API does not offer; the divergence is stated on stderr on
//     every strip-mode batch so it is never hit unknowingly.
//   - SINGLE post: both flag-off and flag-on send attachments explicitly from
//     the edit — the single form does NOT auto-fetch (measured: single +
//     attachments:[] → 0 photos), so the explicit send is required, not
//     redundant. No divergence between flag-off and flag-on there.
func runImport(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, args importArgs) int {
	// Validate flags via the existing builder (reuses the tested validation).
	payload, err := buildImportPayload(args.postID, args.postIDs, args.whenType, args.howType, args.schedules)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	batch := args.postIDs != ""

	// --- BATCH + strip: route each post through the per-post import path ---
	if batch && args.stripVK {
		ids, err := parseIntListErr(args.postIDs)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(errOut, "warn: --strip-vk-markup: routing %d post(s) through the per-post import path (%d import requests instead of 1 batch call)\n", len(ids), len(ids))
		// The per-post single-id form cannot express "let the server fetch
		// attachments from the ids" the way the batch form does, so each
		// request sends its attachments explicitly from the edit response.
		// State it on every strip-mode batch so the divergence is never hit
		// unknowingly — turning on a text-hygiene flag also changes
		// attachment delivery. See the runImport doc comment for the why.
		fmt.Fprintf(errOut, "warn: --strip-vk-markup: attachments are sent explicitly per post (read from each edit response), NOT fetched server-side from the source ids as the batch form does — a text-hygiene flag also changes attachment delivery on a batch\n")
		// This path is NOT atomic: it issues N import calls instead of 1
		// batch call, so a failure at post K of N leaves posts 1..K-1 already
		// live in the queue. Returning early before encoding would hide the
		// published posts from stdout and invite a re-run that duplicates
		// them. So: continue past any per-post failure, record every outcome
		// (created with its id, or failed with its error), ALWAYS encode the
		// full result to stdout, and only THEN exit non-zero if any post
		// failed. A re-run reads stdout to skip what already landed.
		results := make([]perPostResult, 0, len(ids))
		anyFailed := false
		for _, id := range ids {
			edit, err := c.GetSearchPostEdit(ctx, id)
			if err != nil {
				anyFailed = true
				fmt.Fprintf(errOut, "error: GetSearchPostEdit(%d): %v\n", id, err)
				results = append(results, perPostResult{SearchPostID: id, Status: "failed", Error: fmt.Sprintf("GetSearchPostEdit: %v", err)})
				continue
			}
			text := ""
			if len(edit.Texts) > 0 {
				text = edit.Texts[0].Text
			}
			warnPostHygiene(errOut, id, text, args.stripVK, true)
			transformed := stripVKMarkup(text)
			perPostPayload := hooppy.CopySearchPostPayload{
				SearchPostID:        id,
				PublicationWhenType: args.whenType,
				PublicationHowType:  args.howType,
				SchedulesIDs:        payload.SchedulesIDs,
				Texts:               []hooppy.PostText{{Text: transformed, SourceID: 0}},
			}
			if !args.noAttachments {
				perPostPayload.Attachments = hooppy.SearchPostEditAttachments(edit.Attachments)
			}
			resp, err := c.ImportSearchPost(ctx, perPostPayload)
			if err != nil {
				anyFailed = true
				fmt.Fprintf(errOut, "error: ImportSearchPost(%d): %v\n", id, err)
				results = append(results, perPostResult{SearchPostID: id, Status: "failed", Error: fmt.Sprintf("ImportSearchPost: %v", err)})
				continue
			}
			results = append(results, perPostResult{SearchPostID: id, Status: "created", PostID: resp.ID})
		}
		// ALWAYS encode the full result, even on partial failure — stdout is
		// the operator's record of what landed. Exit non-zero only AFTER the
		// encode, so a caller parsing stdout never loses already-published ids.
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]interface{}{
			"strip_vk_markup": true,
			"per_post":        results,
		}); err != nil {
			fmt.Fprintf(errOut, "error encoding output: %v\n", err)
			return 1
		}
		if anyFailed {
			return 1
		}
		return 0
	}

	// --- SINGLE post: fetch edit, detect, optionally strip, one import ---
	if !batch {
		edit, err := c.GetSearchPostEdit(ctx, args.postID)
		if err != nil {
			fmt.Fprintf(errOut, "error: GetSearchPostEdit(%d): %v\n", args.postID, err)
			return 1
		}
		text := ""
		if len(edit.Texts) > 0 {
			text = edit.Texts[0].Text
		}
		warnPostHygiene(errOut, args.postID, text, args.stripVK, false)
		if args.stripVK {
			text = stripVKMarkup(text)
		}
		payload.Texts = []hooppy.PostText{{Text: text, SourceID: 0}}
		if !args.noAttachments {
			payload.Attachments = hooppy.SearchPostEditAttachments(edit.Attachments)
		}
		resp, err := c.ImportSearchPost(ctx, payload)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(errOut, "error encoding output: %v\n", err)
			return 1
		}
		return 0
	}

	// --- BATCH, flag OFF: fetch each post for detection, then one batch import ---
	ids, err := parseIntListErr(args.postIDs)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	for _, id := range ids {
		edit, err := c.GetSearchPostEdit(ctx, id)
		if err != nil {
			// A detection-read failure must NOT abort the import. This GET
			// exists only to produce a hygiene warning; the import itself
			// (the single batch call below) does not need it. Before this
			// path existed the import made zero such calls, so failing the
			// whole batch on a detection read would turn N optional reads
			// into N fatal dependencies. Warn on stderr naming the post and
			// stating it was not scanned, so a silent gap in coverage is not
			// mistaken for a clean scan — then continue.
			fmt.Fprintf(errOut, "warn: post %d: detection read failed (%v); post was NOT scanned for VK markup or ad markers, import continues\n", id, err)
			continue
		}
		text := ""
		if len(edit.Texts) > 0 {
			text = edit.Texts[0].Text
		}
		warnPostHygiene(errOut, id, text, args.stripVK, true)
	}
	resp, err := c.ImportSearchPost(ctx, payload)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}

// warnPostHygiene scans a post's text and writes warnings to errOut (stderr).
// VK wiki-link markup and advertising-disclosure markers are reported
// regardless of whether --strip-vk-markup is set. When VK markup is detected
// but the flag is OFF, the warning names the flag and states the cost so the
// operator can act without reading the source. The cost message is MODE-AWARE:
// stripping a SINGLE post costs no extra API call (the single path already
// sends client-side text), so the warning tells a single-post caller the flag
// is free; only a BATCH fans out to N import calls, so only the batch warning
// names the N-calls cost. Telling a single-post caller the flag "costs one API
// call per post" would discourage a free correctness win.
func warnPostHygiene(errOut io.Writer, postID int, text string, stripVK, batch bool) {
	vkHits := detectVKMarkup(text)
	for _, raw := range vkHits {
		fmt.Fprintf(errOut, "warn: post %d: VK wiki-link markup found: %s\n", postID, raw)
	}
	adLines := detectAdMarkers(text)
	for _, line := range adLines {
		fmt.Fprintf(errOut, "warn: post %d: advertising-disclosure marker found on line: %q\n", postID, line)
	}
	if len(vkHits) > 0 && !stripVK {
		if batch {
			fmt.Fprintf(errOut, "warn: post %d: %d VK wiki-link marker(s) detected; --strip-vk-markup converts [url|text] to text but routes each post through its own import request (N import calls instead of 1 batch call)\n", postID, len(vkHits))
		} else {
			fmt.Fprintf(errOut, "warn: post %d: %d VK wiki-link marker(s) detected; --strip-vk-markup converts [url|text] to text at no extra cost (a single post is already one import call)\n", postID, len(vkHits))
		}
	}
}
