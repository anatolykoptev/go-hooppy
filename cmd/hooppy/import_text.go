package main

import (
	"context"
	"encoding/json"
	"errors"
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
//
// PostID is serialized WITHOUT omitempty so a "created" entry ALWAYS carries
// its id field, even when the id is 0. Omitting the field on a zero id would
// make stdout lossy exactly where it must not be: a re-run reads stdout to
// deduplicate, and a "created" record with no post_id is un-deduplicatable.
// A zero id is reported as the distinct status "created_no_id" rather than
// "created" — a created post whose identity is unknown is not the same thing
// as a created post, and the operator must be able to tell them apart from
// the record alone.
type perPostResult struct {
	SearchPostID int    `json:"search_post_id"`
	Status       string `json:"status"` // "created", "created_no_id", or "failed"
	PostID       int    `json:"post_id"`
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

// detectVKMarkup returns the raw [url|text] and [[page|display]] markers found
// in the text, or nil if none. Unterminated brackets are NOT reported (they
// are not markers — they are literal text). A "[" inside a marker's inner is
// also NOT reported: stripVKMarkup leaves such a marker byte-untouched (the
// inner "[" may be nested/broken markup such as "[[a|[c|d]]]" OR a legitimate
// display text containing "[" — see stripVKMarkup's doc comment for the
// trade-off), so reporting it would warn "VK markup found" and suggest
// --strip-vk-markup — a flag that changes nothing for that marker. Detection
// and strip must agree on what is a marker. [[page]] without a pipe IS
// reported: it is valid VK wiki markup (an internal page reference), just
// without display text, and the operator should know it is there even though
// strip leaves it untouched.
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

// runImport is the testable core of `hooppy search import`. It resolves each
// scraped post via ResolveSearchPost, runs hygiene warnings (always on),
// optionally strips VK wiki-link markup, and publishes via PublishPost.
// Warnings go to errOut (stderr); the JSON result goes to out (stdout).
// Returns the process exit code: 0 on success, 1 on error, 2 on partial
// (batch where some posts succeeded and some failed).
//
// The batch is N independent resolve+publish pairs (client-side), NOT one
// server-side batch. This avoids the server's batch-specific defects
// (form-dependent text/attachment behaviour) and gives each post its own
// original text. A failure at post K of N leaves posts 1..K-1 already live
// in the queue — runImport continues past a per-post failure, ALWAYS encodes
// a per-post result array to stdout (for batch), and exits 2 (partial) only
// AFTER stdout is written. Do NOT re-simplify this to an early return —
// that would hide published posts from stdout and invite a duplicate-spawning
// re-run.
//
// Exit codes (repo convention): 0=complete, 1=error, 2=partial. A batch
// where some posts failed exits 2; a batch where ALL posts failed exits 1
// (complete failure, not partial). Each failed post is named on stderr.
func runImport(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, args importArgs) int {
	// Validate flags via the existing builder (reuses the tested validation).
	payload, err := buildImportPayload(args.postID, args.postIDs, args.whenType, args.howType, args.schedules)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	batch := args.postIDs != ""

	// Resolve the id list: single -> [postID], batch -> parsed slice.
	var ids []int
	if batch {
		ids, err = parseIntListErr(args.postIDs)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
	} else {
		ids = []int{args.postID}
	}

	target := hooppy.PublishTarget{
		PublicationWhenType: args.whenType,
		PublicationHowType:  args.howType,
		SchedulesIDs:        payload.SchedulesIDs,
	}

	// Single post: resolve, warn, optionally strip, publish, emit PostIDResponse.
	if !batch {
		content, err := c.ResolveSearchPost(ctx, args.postID)
		if err != nil {
			fmt.Fprintf(errOut, "error: ResolveSearchPost(%d): %v\n", args.postID, err)
			return 1
		}
		text := ""
		if len(content.Texts) > 0 {
			text = content.Texts[0].Text
		}
		warnPostHygiene(errOut, args.postID, text, args.stripVK)
		if args.stripVK {
			text = stripVKMarkup(text)
			content.Texts = []hooppy.PostText{{Text: text, SourceID: 0}}
		}
		if args.noAttachments {
			content.Attachments = nil
		}
		resp, err := c.PublishPost(ctx, content, target)
		if err != nil {
			// Through the shared helper, so this arm agrees with the other
			// two single-post runners on all three details, not just the
			// exit code: the stdout record, the stderr warning telling the
			// operator to reconcile before re-running, and treating an
			// encode failure as an error rather than discarding it. This
			// was the arm that said least while being the one most likely
			// reached from a batch workflow.
			if code, handled := reportCreateNoID(err, out, errOut, args.postID); handled {
				return code
			}
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

	// Batch: N independent resolve+publish pairs. NOT atomic — a failure at
	// post K leaves posts 1..K-1 live. Continue past per-post failures,
	// record every outcome, ALWAYS encode stdout, then exit 2 (partial) if
	// any failed but not all (exit 1 if all failed).
	results := make([]perPostResult, 0, len(ids))
	anyFailed := false
	anySucceeded := false
	for _, id := range ids {
		content, err := c.ResolveSearchPost(ctx, id)
		if err != nil {
			anyFailed = true
			fmt.Fprintf(errOut, "error: ResolveSearchPost(%d): %v\n", id, err)
			results = append(results, perPostResult{SearchPostID: id, Status: "failed", Error: fmt.Sprintf("ResolveSearchPost: %v", err)})
			continue
		}
		text := ""
		if len(content.Texts) > 0 {
			text = content.Texts[0].Text
		}
		warnPostHygiene(errOut, id, text, args.stripVK)
		if args.stripVK {
			text = stripVKMarkup(text)
			content.Texts = []hooppy.PostText{{Text: text, SourceID: 0}}
		}
		if args.noAttachments {
			content.Attachments = nil
		}
		resp, err := c.PublishPost(ctx, content, target)
		if err != nil {
			var cnid *hooppy.CreateNoIDError
			if errors.As(err, &cnid) {
				results = append(results, perPostResult{SearchPostID: id, Status: "created_no_id", PostID: 0})
				anySucceeded = true
				continue
			}
			anyFailed = true
			fmt.Fprintf(errOut, "error: PublishPost(%d): %v\n", id, err)
			results = append(results, perPostResult{SearchPostID: id, Status: "failed", Error: fmt.Sprintf("PublishPost: %v", err)})
			continue
		}
		anySucceeded = true
		results = append(results, perPostResult{SearchPostID: id, Status: "created", PostID: resp.ID})
	}
	// ALWAYS encode the full result, even on partial failure — stdout is
	// the operator's record of what landed. Exit non-zero only AFTER the
	// encode, so a caller parsing stdout never loses already-published ids.
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]interface{}{
		"strip_vk_markup": args.stripVK,
		"per_post":        results,
	}); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	if anyFailed && !anySucceeded {
		return 1 // all failed = error, not partial
	}
	if anyFailed {
		return 2 // partial: some succeeded, some failed
	}
	return 0
}

// warnPostHygiene scans a post's text and writes warnings to errOut (stderr).
// VK wiki-link markup and advertising-disclosure markers are reported
// regardless of whether --strip-vk-markup is set. When VK markup is detected
// but the flag is OFF, the warning names the flag so the operator can act
// without reading the source.
//
// The flag is FREE in request-count terms: after the resolve+publish collapse,
// every post (single or batch) is already its own POST /posts call, so
// stripping VK markup changes nothing about the number of API calls — it
// only transforms the text client-side before the publish. The prior
// "N import calls instead of 1 batch call" cost message described a batch
// shape that no longer exists (there is no server-side batch call; the batch
// is N client-side resolve+publish pairs).
func warnPostHygiene(errOut io.Writer, postID int, text string, stripVK bool) {
	vkHits := detectVKMarkup(text)
	for _, raw := range vkHits {
		fmt.Fprintf(errOut, "warn: post %d: VK wiki-link markup found: %s\n", postID, raw)
	}
	adLines := detectAdMarkers(text)
	for _, line := range adLines {
		fmt.Fprintf(errOut, "warn: post %d: advertising-disclosure marker found on line: %q\n", postID, line)
	}
	if len(vkHits) > 0 && !stripVK {
		fmt.Fprintf(errOut, "warn: post %d: %d VK wiki-link marker(s) detected; --strip-vk-markup converts [url|text] to text at no extra cost (the text is transformed client-side before the publish; request count is unchanged)\n", postID, len(vkHits))
	}
}
