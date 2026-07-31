package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/anatolykoptev/go-hooppy"
)

// runStopParsing implements `search stop`. It does NOT report success from the
// DELETE's own body: the server answers {"success":true} for a DELETE that
// cancels nothing (the suffix-less sibling, measured issue #94) and a real
// stop is asynchronous (~5s between stop-sent and is_parsing_in_progress
// going false). So the command re-reads the working oracle
// (GetParsingForm.is_parsing_in_progress) via StopParsingAndConfirm and
// reports the OBSERVED state:
//
//   - oracle idle          → {"success":true,"is_parsing_in_progress":false}, exit 0
//   - oracle still running → {"success":false,"is_parsing_in_progress":true}, exit 2
//     (the stop was ACCEPTED but the parse is still in progress at read time —
//     a partial outcome, not an error; the operator re-runs 'search status' to
//     confirm the transition. Exit 2, not 1, so a script can tell "the DELETE
//     failed" from "the stop was accepted but not yet observed idle". A polling
//     loop was rejected: it would claim to know the stop will eventually take
//     effect, more than one read knows.)
//   - DELETE failed        → stderr error, exit 1 (no stdout success)
//   - DELETE ok, oracle re-read failed → {"success":false,"unconfirmed":true},
//     exit 2 (the stop MAY have worked; never claim success unconfirmed. Exit 2:
//     the DELETE was accepted, only confirmation failed — a partial outcome,
//     not a DELETE error.)
//
// Exit-code convention (matches runScheduleQueue and the doctor --exit-code
// doc at main.go): 0 = complete, 1 = error (the DELETE itself failed), 2 =
// partial (accepted but not yet idle / unconfirmed). The prior code returned 1
// for all three non-idle conditions, so `search stop` on a genuinely running
// parse normally exited 1 and exited 0 mainly when it cancelled nothing — it
// reported failure exactly when it worked.
//
// This closes issue #114: the prior code printed {"success":true} after a nil
// error from StopParsing(out=nil) — the body was never read, and even read, a
// success:true does not mean the parse stopped. The command now reports only
// what the oracle observed.
func runStopParsing(ctx context.Context, c *hooppy.Client, out, errOut io.Writer) int {
	res, err := c.StopParsingAndConfirm(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "error: stop parsing: %v\n", err)
		return 1
	}
	if res.ConfirmErr != "" {
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(stopParsingOutput{Success: false, Unconfirmed: true})
		fmt.Fprintf(errOut, "stop request accepted, but parsing status could not be confirmed: %s\n", res.ConfirmErr)
		fmt.Fprintf(errOut, "re-run 'hooppy search status' to check is_parsing_in_progress\n")
		return 2
	}
	if res.IsParsingInProgress {
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(stopParsingOutput{Success: false, IsParsingInProgress: true})
		fmt.Fprintf(errOut, "stop request accepted, but parsing is still in progress — re-run 'hooppy search status' to confirm the transition (a stop is asynchronous server-side)\n")
		return 2
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(stopParsingOutput{Success: true, IsParsingInProgress: false})
	return 0
}

// stopParsingOutput is the JSON shape `search stop` emits on stdout. Every
// sibling runner in this file uses json.NewEncoder over a struct; the prior
// hand-built Fprintf string literals had already drifted from the MCP stop
// shape (the MCP carries is_parsing_in_progress on the in-progress branch,
// the CLI did not). A struct keeps the two surfaces aligned and lets the
// field set evolve without a per-branch string rewrite.
type stopParsingOutput struct {
	Success             bool `json:"success"`
	IsParsingInProgress bool `json:"is_parsing_in_progress"`
	Unconfirmed         bool `json:"unconfirmed,omitempty"`
}

// runCopySearchPost implements `search copy`. Extracted from the inline Run
// closure so the schedules-dropped guard (issue #111) can be falsified at the
// command level with a stub server: a test asserts NO request reached the
// server when the guard fires, not merely a non-zero exit (a command that
// published and then errored would pass an exit-only check).
func runCopySearchPost(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, postID, whenType, howType int, pages, schedules, date, hours, minutes string) int {
	payload, err := buildCopyPayload(postID, whenType, howType, pages, schedules, date, hours, minutes)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	resp, err := c.CopySearchPost(ctx, payload)
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

// runRewriteSearchPost implements `search rewrite`. Extracted from the inline
// Run closure for the same reason as runCopySearchPost: the schedules-dropped
// guard (issue #111) is falsified at the command level with a stub server
// asserting NO request reached the server when the guard fires. The per-post
// attachment download block is preserved verbatim from the prior inline form.
func runRewriteSearchPost(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, postID int, postIDs, text string, whenType, howType int, pages, schedules, date, hours, minutes string, noAttachments bool) int {
	payload, err := buildRewritePayload(postID, postIDs, text, whenType, howType, pages, schedules, date, hours, minutes)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	batch := postIDs != ""
	// Per-post attachment download only applies to the single-post form:
	// it fetches GetSearchPostEdit for --post-id and re-uploads photos.
	// A batch (--post-ids) spans multiple scraped posts, so there is no
	// single edit to fetch — skip the block (equivalent to --no-attachments).
	if !noAttachments && !batch {
		// By default, preserve ALL attachments from the scraped post:
		// - Photos: download from edit endpoint URLs → re-upload via UploadMedia
		//   (server doesn't download automatically; MediaItem must have id/name/folder/file_path)
		// - Other attachments (copyright, link, poll, etc.): pass through as-is
		edit, err := c.GetSearchPostEdit(ctx, postID)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		var mediaItems []interface{}
		var attachments []hooppy.Attachment
		for i, att := range edit.Attachments {
			if att.Type != "photo" && att.Type != "video" {
				// Non-photo attachment — pass through as-is
				attachments = append(attachments, att)
				continue
			}
			// Extract URL from the attachment data
			data, ok := att.Data.(map[string]interface{})
			if !ok {
				continue
			}
			photoURL, _ := data["url"].(string)
			if photoURL == "" {
				continue
			}
			// Download photo
			tmpPath := fmt.Sprintf("/tmp/hooppy_photo_%d_%d.jpg", postID, i)
			if err := downloadPhoto(photoURL, tmpPath); err != nil {
				fmt.Fprintf(errOut, "error: download photo %d: %v\n", i, err)
				return 1
			}
			// Upload with generated file_id (server uses it as id + name)
			media, err := c.UploadMedia(ctx, tmpPath, "")
			if err != nil {
				fmt.Fprintf(errOut, "error: %v\n", err)
				return 1
			}
			mediaItems = append(mediaItems, media.Photo)
			os.Remove(tmpPath)
		}
		if len(mediaItems) > 0 {
			attachments = append([]hooppy.Attachment{{Type: "photos", Data: mediaItems}}, attachments...)
		}
		if len(attachments) > 0 {
			payload.Attachments = attachments
		}
	}
	resp, err := c.RewriteSearchPost(ctx, payload)
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
