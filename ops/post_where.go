package ops

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/anatolykoptev/go-hooppy"
)

// DefaultPostWhereSource is the social network queried when the caller names
// none: VK (source id 1), the network the "find every VK copy of a queued
// post" workflow needs. The result always echoes the source it asked about.
const DefaultPostWhereSource = int(hooppy.SourceVK)

// PostWhereInput is the single source of the `posts where` /
// hooppy_post_where parameter set (the post id is positional on the CLI and
// an explicit `id` field on MCP, like schedules update).
type PostWhereInput struct {
	SourceID *int `json:"source_id,omitempty" jsonschema:"Social network (source) id to report on: 1=VK, 2=OK, 3=Facebook, 7=Instagram, 9=Telegram. The API answers for ONE network per call. Omit for VK (1)." flag:"source" flagusage:"social network (source) id: 1=VK, 2=OK, 3=Facebook, 7=Instagram, 9=Telegram (default 1 = VK)"`
}

// PostWherePage is one destination page of the post with where it stands.
type PostWherePage struct {
	PageID       int             `json:"page_id"`
	Name         string          `json:"name,omitempty"`
	PageURL      string          `json:"page_url,omitempty"` // the community/channel itself
	AccountID    int             `json:"account_id,omitempty"`
	AccountName  string          `json:"account_name,omitempty"`
	PublishedURL string          `json:"published_url,omitempty"` // the post's public record on that page; empty = not published there (yet)
	Planned      json.RawMessage `json:"planned,omitempty"`       // «отложка» marker as the API sends it
	Errors       []string        `json:"errors,omitempty"`        // last publish-attempt errors, as the API words them
}

// PostWhereResult is the per-page answer for one post in one network.
type PostWhereResult struct {
	PostID     int             `json:"post_id"`
	SourceID   int             `json:"source_id"`
	SourceName string          `json:"source_name"`
	Pages      []PostWherePage `json:"pages"`
	Note       string          `json:"note,omitempty"`
}

// PostWhere reports, per destination page of one network, where a post is
// (to be) published and — once published — its public URL. It validates
// locally (positive post id, positive source), asks GET /items/social/pages
// and flattens the account-grouped answer into one row per page, joining the
// page-keyed URL / planned / error maps onto it.
//
// A key present in a posts_* map but absent from the pages list (a page the
// API lists nowhere else) still becomes a row, so no URL or error is dropped.
// "No pages for this object." (the API's HTTP 500 for a post without pages
// in that network) is a normal answer — an empty Pages with a Note — not a
// failure. Local refusals are *ValidationError.
func PostWhere(ctx context.Context, c *hooppy.Client, id int, in PostWhereInput) (*PostWhereResult, error) {
	if id <= 0 {
		return nil, &ValidationError{msg: "id is required and must be a positive post id (got " + strconv.Itoa(id) + ")"}
	}
	source := DefaultPostWhereSource
	if in.SourceID != nil {
		if *in.SourceID <= 0 {
			return nil, errFieldf(in, "SourceID", "must be a positive social network id (got %d)", *in.SourceID)
		}
		source = *in.SourceID
	}
	out := &PostWhereResult{PostID: id, SourceID: source, SourceName: hooppy.SourceID(source).String(), Pages: []PostWherePage{}}

	resp, err := c.GetPostPublishedPages(ctx, id, source)
	if err != nil {
		var ae *hooppy.APIError
		if errors.As(err, &ae) && strings.Contains(ae.Message+string(ae.Body), "No pages for this object") {
			out.Note = "the post has no pages in this social network"
			return out, nil
		}
		return nil, err
	}

	seen := map[string]bool{}
	for _, acc := range resp.SocialPagesByAccounts {
		for _, pg := range acc.Pages {
			key := strconv.Itoa(pg.ID)
			seen[key] = true
			out.Pages = append(out.Pages, PostWherePage{
				PageID:       pg.ID,
				Name:         pg.Name,
				PageURL:      pg.Link,
				AccountID:    acc.Account.ID,
				AccountName:  acc.Account.Name,
				PublishedURL: resp.PostsPublishedLinks[key],
				Planned:      resp.PostsPublishedPlanned[key],
				Errors:       resp.PostsErrors[key],
			})
		}
	}
	// Keys the pages list does not carry: surface them rather than drop them.
	extra := map[string]bool{}
	for k := range resp.PostsPublishedLinks {
		extra[k] = true
	}
	for k := range resp.PostsPublishedPlanned {
		extra[k] = true
	}
	for k := range resp.PostsErrors {
		extra[k] = true
	}
	var extraKeys []string
	for k := range extra {
		if !seen[k] {
			extraKeys = append(extraKeys, k)
		}
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		pid, _ := strconv.Atoi(k)
		out.Pages = append(out.Pages, PostWherePage{
			PageID:       pid,
			PublishedURL: resp.PostsPublishedLinks[k],
			Planned:      resp.PostsPublishedPlanned[k],
			Errors:       resp.PostsErrors[k],
		})
	}
	return out, nil
}
