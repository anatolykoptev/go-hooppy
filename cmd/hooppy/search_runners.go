package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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

// runRewriteSearchPost implements `search rewrite`. The runner shape (writers
// as parameters, an int exit code) comes from the guard work on main: the
// schedules-dropped guard (issue #111) is falsified at the command level with
// a stub server asserting NO request reached the server when the guard fires.
//
// The BODY is this branch's shape, not main's. main's runner downloaded each
// photo from GET /posts-search/{id}/edit and re-uploaded it before publishing;
// after the collapse onto resolve+publish that work lives in the library
// (ResolveSearchPost maps the edit endpoint's read-shape attachments to the
// write shape), so re-applying it here would re-upload every attachment twice.
// The partial-batch contract is likewise this branch's: a *PartialPostError
// carries a populated result, which goes to stdout before exit 2 so a caller
// can skip the already-published ids on a re-run (same dedup-via-stdout design
// as runImport).
//
// Exit codes (repo convention): 0=complete, 1=error, 2=partial.
func runRewriteSearchPost(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, postID int, postIDs, text string, whenType, howType int, pages, schedules, date, hours, minutes string, noAttachments bool) int {
	payload, err := buildRewritePayload(postID, postIDs, text, whenType, howType, pages, schedules, date, hours, minutes)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	payload.NoAttachments = noAttachments
	resp, err := c.RewriteSearchPost(ctx, payload)
	if err != nil {
		var ppe *hooppy.PartialPostError
		if errors.As(err, &ppe) {
			// Partial batch: print the populated result (what landed) to
			// stdout, the error to stderr, exit 2 (partial).
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			_ = enc.Encode(resp)
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 2
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
