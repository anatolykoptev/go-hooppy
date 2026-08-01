package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/anatolykoptev/go-hooppy"
)

// runListCrossPostings is the testable core of `hooppy crossposting list`.
// It follows the existing runner + emitList conventions: --all walks every
// page via ListAllCrossPostingsWithTotal and emits an AllListEnvelope
// (low-churn equality check — configured connections rarely change);
// otherwise a single page is fetched and a truncation warning is emitted
// when is_has_more is true. Data on stdout, warnings on stderr, exit 0 on
// success / 1 on error.
func runListCrossPostings(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, page int, all bool) int {
	if all {
		list, total, err := c.ListAllCrossPostingsWithTotal(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		// NewAllListEnvelope is the validation gate (unique ids == total);
		// the enriched map is the emitted payload, with enum names injected
		// on every row the way /edit does.
		if _, err := hooppy.NewAllListEnvelope(list, total, func(cp hooppy.CrossPosting) int { return cp.ID }); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		enriched, err := hooppy.EnrichedAllCrossPostingsMap(list, total)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "cross-posting connections", true, len(list), total, false, enriched)
	}
	resp, err := c.ListCrossPostings(ctx, page)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enriched, err := hooppy.EnrichedCrossPostingsMap(resp)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "cross-posting connections", false, len(resp.List), resp.TotalRows, resp.IsHasMore, enriched)
}

// runShowCrossPosting is the testable core of `hooppy crossposting show <id>`.
// It fetches the connection's full editable state (GET /cross-posting/{id}/edit)
// and emits the agent-facing presentation: the full raw body (all 95 keys
// preserved) with decoded enum names injected alongside the raw integers —
// "decode, do not translate away". Data on stdout, exit 0 on success / 1 on
// error.
func runShowCrossPosting(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, id int) int {
	if id == 0 {
		fmt.Fprintln(errOut, "error: crossposting show requires a connection ID (got 0)")
		return 1
	}
	resp, err := c.GetCrossPostingEdit(ctx, id)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enriched, err := hooppy.EnrichedCrossPostingEditMap(resp)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(enriched); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}

// runCrossPostingStats is the testable core of `hooppy crossposting stats <id>`.
// It fetches the connection's per-day statistics and emits them. A non-empty
// statistics array with all-zero counters is a REAL measurement (the engine
// ran and found nothing — the live state today); an EMPTY array is absent
// data (the engine has not run). The latter emits a stderr note so an operator
// does not read an empty result as "checked, found nothing". Data on stdout,
// exit 0 on success / 1 on error.
func runCrossPostingStats(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, id int) int {
	if id == 0 {
		fmt.Fprintln(errOut, "error: crossposting stats requires a connection ID (got 0)")
		return 1
	}
	resp, err := c.GetCrossPostingStatistics(ctx, id)
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
	if !resp.HasData() {
		fmt.Fprintln(errOut, "note: statistics array is empty — the engine has not run for this connection (absent data, not a zero measurement). A non-empty array with all-zero counters would be a real measurement.")
	}
	return 0
}
