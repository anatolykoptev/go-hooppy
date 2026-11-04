package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
)

// runImport is the thin CLI adapter of `hooppy search import` over
// ops.ImportSearchPost. The op owns validation, the when-type target
// checks, the per-post resolve+publish loop, the VK-markup strip and the
// hygiene scan; this function maps the result to stdout/stderr and an exit
// code: 0 on success, 1 on error, 2 on partial (batch where some posts
// succeeded and some failed), 64 on a local usage refusal.
//
// The batch is N independent resolve+publish pairs (client-side), NOT one
// server-side batch — a failure at post K of N leaves posts 1..K-1 already
// live in the queue, so the runner ALWAYS encodes a per-post result array
// to stdout (for batch) and exits non-zero only AFTER stdout is written.
// Do NOT re-simplify this to an early return — that would hide published
// posts from stdout and invite a duplicate-spawning re-run.
func runImport(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, in ops.ImportSearchPostInput) int {
	res, err := ops.ImportSearchPost(ctx, c, in)
	if err != nil {
		if code, handled := printOpError(err, errOut); handled {
			return code
		}
	}
	if res == nil {
		// Outside the op's own contract (it guarantees a populated result
		// once the loop ran): treat as a plain error, never dereference.
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	batch := in.SearchPostIDs != nil && len(*in.SearchPostIDs) > 0
	// The op ran its per-post loop: res is populated whenever execution
	// began. Print each post's hygiene findings, then its failure note.
	for _, oc := range res.PerPost {
		warnPostHygiene(errOut, oc, res.StripVKMarkup)
		if oc.Status == "failed" {
			fmt.Fprintf(errOut, "error: %s\n", oc.Error)
		}
	}
	if !batch {
		oc := res.PerPost[0]
		switch oc.Status {
		case "created":
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(&res.PostIDResponse); err != nil {
				fmt.Fprintf(errOut, "error encoding output: %v\n", err)
				return 1
			}
			return 0
		case "created_no_id":
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if encErr := enc.Encode(map[string]interface{}{
				"id":             0,
				"status":         "created_no_id",
				"search_post_id": oc.SearchPostID,
			}); encErr != nil {
				fmt.Fprintf(errOut, "error encoding output: %v\n", encErr)
				return 1
			}
			fmt.Fprintf(errOut, "warn: the post was accepted but the server returned no id — it probably exists and cannot be addressed from this response; reconcile before re-running\n")
			return 0
		default:
			// "failed": the per-post loop above already printed this
			// outcome's error — a second print would duplicate the line.
			return 1
		}
	}
	// ALWAYS encode the full result, even on partial failure — stdout is
	// the operator's record of what landed. Exit non-zero only AFTER the
	// encode, so a caller parsing stdout never loses already-published ids.
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(res); encErr != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", encErr)
		return 1
	}
	var ppe *hooppy.PartialPostError
	if errors.As(err, &ppe) {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 2
	}
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return 0
}

// warnPostHygiene writes a post's hygiene findings to errOut (stderr). The
// findings were computed inside ops.ImportSearchPost on the ORIGINAL
// resolved text (before strip): VK wiki-link markup markers raw, and the
// full lines carrying advertising disclosures. When VK markup is detected
// but --strip-vk-markup is off, the warning names the flag so the operator
// can act without reading the source — and names it "free": every post
// (single or batch) is already its own POST /posts call, so stripping
// changes nothing about the request count.
func warnPostHygiene(errOut io.Writer, oc ops.ImportPostOutcome, stripVK bool) {
	for _, raw := range oc.VKMarkup {
		fmt.Fprintf(errOut, "warn: post %d: VK wiki-link markup found: %s\n", oc.SearchPostID, raw)
	}
	for _, line := range oc.AdMarkers {
		fmt.Fprintf(errOut, "warn: post %d: advertising-disclosure marker found on line: %q\n", oc.SearchPostID, line)
	}
	if len(oc.VKMarkup) > 0 && !stripVK {
		fmt.Fprintf(errOut, "warn: post %d: %d VK wiki-link marker(s) detected; --strip-vk-markup converts [url|text] to text at no extra cost (the text is transformed client-side before the publish; request count is unchanged)\n", oc.SearchPostID, len(oc.VKMarkup))
	}
}
