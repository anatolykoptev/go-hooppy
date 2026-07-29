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

// adMarkerSubstrings are the Russian advertising-disclosure markers the tool
// scans for. They are NEVER auto-removed: a disclosure that genuinely is our
// advertising must stay, and the tool cannot tell the two cases apart. This
// table exists only to drive the warning, never a transformation.
var adMarkerSubstrings = []string{"Erid", "Реклама.", "ИНН"}

// stripVKMarkup converts VK wiki-link markup [url|text] to text, and the
// internal-page form [[page|display]] to display. It is deliberately
// conservative: a malformed marker (no closing bracket, no pipe, empty
// brackets, an unterminated [[) is left byte-untouched. Mangling text on the
// way into a live publishing queue is worse than leaving markup in.
//
// Parsing rules (decided from the markup grammar, not a regex, so a `|` inside
// the display text is handled correctly — the FIRST `|` is the separator):
//   - [url|text]        → text
//   - [[page|display]]  → display
//   - [[page]]          → left untouched (a page reference, not display text)
//   - [url]             → left untouched (no pipe = not a wiki-link)
//   - []                → left untouched
//   - unterminated [    → left untouched
//
// The function operates on a byte-by-byte scan with strings.Index for the
// closing bracket, so it never half-rewrites a malformed marker: if the close
// is missing, the opening bracket is copied verbatim and scanning continues.
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
				// No closing ]] — leave the '[' untouched, continue after it.
				b.WriteByte(text[i])
				i++
				continue
			}
			inner := rest[:end]
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
// are not markers — they are literal text).
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
			if strings.Contains(inner, "|") {
				hits = append(hits, text[i:i+2+end+2])
				i += 2 + end + 2
				continue
			}
			// [[page]] without a pipe is still VK wiki markup (internal page
			// reference) — report it so the operator knows it is there, even
			// though strip leaves it untouched.
			hits = append(hits, text[i:i+2+end+2])
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
		if strings.Contains(inner, "|") {
			hits = append(hits, text[i:i+1+end+1])
		}
		i += 1 + end + 1
	}
	return hits
}

// detectAdMarkers returns the lines in text that contain at least one
// advertising-disclosure marker (Erid, Реклама., ИНН), or nil if none. The
// matched LINE is returned (not just the marker) so the warning can show the
// operator exactly what would be published.
func detectAdMarkers(text string) []string {
	var hits []string
	for _, line := range strings.Split(text, "\n") {
		for _, marker := range adMarkerSubstrings {
			if strings.Contains(line, marker) {
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
//     extra cost.
//   - BATCH (--post-ids), flag OFF: N GetSearchPostEdit (one per post, for
//     detection) + ONE batch ImportSearchPost. The N edit fetches are the
//     cost of always-on detection — the import itself is still a single call.
//   - BATCH (--post-ids), flag ON: N GetSearchPostEdit + N ImportSearchPost
//     (one per post, each carrying its own transformed text). This is N import
//     calls instead of 1 — the flag changes the cost profile, which is why it
//     is opt-in.
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
		results := make([]hooppy.PostIDResponse, 0, len(ids))
		for _, id := range ids {
			edit, err := c.GetSearchPostEdit(ctx, id)
			if err != nil {
				fmt.Fprintf(errOut, "error: GetSearchPostEdit(%d): %v\n", id, err)
				return 1
			}
			text := ""
			if len(edit.Texts) > 0 {
				text = edit.Texts[0].Text
			}
			warnPostHygiene(errOut, id, text, args.stripVK)
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
				fmt.Fprintf(errOut, "error: ImportSearchPost(%d): %v\n", id, err)
				return 1
			}
			results = append(results, *resp)
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]interface{}{
			"strip_vk_markup": true,
			"per_post":        results,
		}); err != nil {
			fmt.Fprintf(errOut, "error encoding output: %v\n", err)
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
		warnPostHygiene(errOut, args.postID, text, args.stripVK)
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
			fmt.Fprintf(errOut, "error: GetSearchPostEdit(%d): %v\n", id, err)
			return 1
		}
		text := ""
		if len(edit.Texts) > 0 {
			text = edit.Texts[0].Text
		}
		warnPostHygiene(errOut, id, text, args.stripVK)
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
// operator can act without reading the source.
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
		fmt.Fprintf(errOut, "warn: post %d: %d VK wiki-link marker(s) detected; --strip-vk-markup converts [url|text] to text but routes each post through its own import request (N import calls instead of 1 batch call)\n", postID, len(vkHits))
	}
}
