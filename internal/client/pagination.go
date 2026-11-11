// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// MinShrunkPageSize is the smallest page a walk shrinks to after a page the
// server could not answer in time. A page this small that still times out is
// not a page-size problem, so the walk stops shrinking and the page's requests
// get the transport's ordinary 504 retry instead.
const MinShrunkPageSize = 100

// ListAllPages fetches all pages from a paginated REST endpoint, requesting
// pageSize items per page. Callers must pass a pageSize the endpoint is
// known to honor: some Jamf endpoints silently clamp an oversized page-size
// to a lower server-enforced maximum instead of rejecting it, and this
// function has no way to detect that — it assumes the server returned
// exactly pageSize items on every non-final page when computing the next
// page's offset. Requesting more than the true maximum causes those
// endpoints to silently skip the untransferred tail of one page on the next
// request. See ListGroupsV1/V2 in jamfplatform/pro for a verified example
// (server caps at 2000 regardless of the requested page-size).
//
// A page the server could not answer in time (IsServerTimeout: a 504, or a
// response-header timeout) is fetched again at half the size, down to
// MinShrunkPageSize, resuming from the rows already held — see shrinkPage.
// The walk is the one place a smaller request can succeed where the identical
// one cannot, so its page requests opt out of the transport's 504 retry while
// they are above the floor; every other request keeps it.
func ListAllPages[T any](ctx context.Context, pageSize int, fetchPage func(ctx context.Context, page, pageSize int) ([]T, bool, error)) ([]T, error) {
	return walkPages(ctx, pageSize, 0, fetchPage)
}

// walkPages is ListAllPages with an optional limit: once limit items are held
// (limit > 0) the walk stops and returns exactly limit of them.
//
// Position is tracked as a row offset rather than a page number, because a
// shrink changes what a page number means: page 3 at 2000 rows is rows
// 6000–7999, page 3 at 1000 is rows 3000–3999. The offset advances by the
// page size requested, not by the rows returned, which is the page-index
// model the server uses and what the page++ walk did before shrinking existed.
func walkPages[T any](ctx context.Context, pageSize, limit int, fetchPage func(ctx context.Context, page, pageSize int) ([]T, bool, error)) ([]T, error) {
	if pageSize <= 0 {
		return nil, fmt.Errorf("pagination: page size must be positive, got %d", pageSize)
	}
	var allItems []T
	offset := 0
	for {
		page, skip := offset/pageSize, offset%pageSize
		pageCtx := ctx
		if pageSize > MinShrunkPageSize {
			pageCtx = withPageWalk(ctx)
		}
		items, hasMore, err := fetchPage(pageCtx, page, pageSize)
		if err != nil {
			// A caller's own deadline also surfaces as a timeout, and Go
			// cannot tell it from http.Client.Timeout by the error alone
			// (both match context.DeadlineExceeded), so the walk asks its own
			// context: a smaller page cannot extend the caller's bound.
			if smaller, ok := shrinkPage(err, pageSize); ok && ctx.Err() == nil {
				pageSize = smaller
				continue
			}
			return nil, err
		}
		if skip >= len(items) {
			items = nil
		} else {
			items = items[skip:]
		}
		if len(items) == 0 {
			break
		}
		allItems = append(allItems, items...)
		if limit > 0 && len(allItems) >= limit {
			return allItems[:limit], nil
		}
		if !hasMore {
			break
		}
		offset = (page + 1) * pageSize
	}
	return allItems, nil
}

// shrinkPage returns the page size to retry a page at after it failed with
// err, or false when err is not a server timeout or pageSize is already at
// MinShrunkPageSize — the caller then returns err as it would have.
//
// Halving, not a fixed step: a 2000-row page of Jamf Pro computer inventory
// was answered by the edge with a 504 at 90s where 1000 rows answered in 45s,
// so one halving is usually enough and the walk keeps as much of each round
// trip as the server can afford. An odd size halves to a size that does not
// divide the offset; walkPages skips the leading rows it already holds.
func shrinkPage(err error, pageSize int) (int, bool) {
	if !IsServerTimeout(err) || pageSize <= MinShrunkPageSize {
		return pageSize, false
	}
	return max(pageSize/2, MinShrunkPageSize), true
}

// IsServerTimeout reports whether err is the server not answering in time: a
// 504 response, or a timeout on the client side of the connection (a
// response-header timeout, or an http.Client.Timeout).
//
// It is a property of how much work the request asked for more often than of
// the network, which is why a page walk answers it with a smaller page rather
// than the identical request.
//
// A deadline on the caller's own context reports true as well: Go surfaces it
// and http.Client.Timeout as the same error, both matching
// context.DeadlineExceeded. A caller that needs to tell them apart checks its
// own ctx.Err(), as walkPages does.
func IsServerTimeout(err error) bool {
	if err == nil {
		return false
	}
	if apiErr := AsAPIError(err); apiErr != nil {
		return apiErr.StatusCode == http.StatusGatewayTimeout
	}
	return isTimeout(err) || errors.Is(err, context.DeadlineExceeded)
}

// pageWalkKey marks a request as a page of a walk that will shrink on a server
// timeout. jamfCheckRetry reads it to skip the 504 retry for that request only.
type pageWalkKey struct{}

func withPageWalk(ctx context.Context) context.Context {
	return context.WithValue(ctx, pageWalkKey{}, true)
}

// isPageWalk reports whether ctx belongs to a page request that shrinks
// instead of retrying a 504.
func isPageWalk(ctx context.Context) bool {
	v, _ := ctx.Value(pageWalkKey{}).(bool)
	return v
}

// RawPageWalk configures ListAllRaw.
type RawPageWalk struct {
	// PageSize is the number of rows requested per page. Required.
	PageSize int
	// PageSizeParam is the query parameter carrying PageSize; "page-size"
	// when empty. The page index is always sent as "page".
	PageSizeParam string
	// ResultsField is the envelope key holding the rows; "results" when empty.
	ResultsField string
	// Limit stops the walk once this many rows are held; 0 means no limit.
	Limit int
	// MaxPages bounds the walk against a server that ignores the page
	// parameter; 10000 when zero.
	MaxPages int
}

// ListAllRaw walks an offset-paginated collection at path and returns each
// row as raw JSON, for a caller that must keep fields the SDK's types do not
// model (a CLI printing the server's own document). query carries any other
// parameters, and is not modified.
//
// It shares ListAllPages' shrink-on-timeout behaviour. Unlike ListAllPages it
// ends on an EMPTY page, not on a hasNext flag or a short page: a short page
// is not evidence of the end on an endpoint that clamps an oversized page-size,
// and a generic walk cannot know which endpoints do. The cost is one extra
// request per walk.
func (c *Transport) ListAllRaw(ctx context.Context, path string, query url.Values, opts RawPageWalk) ([]json.RawMessage, error) {
	sizeParam := opts.PageSizeParam
	if sizeParam == "" {
		sizeParam = "page-size"
	}
	field := opts.ResultsField
	if field == "" {
		field = "results"
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 10000
	}
	requests := 0
	items, err := walkPages(ctx, opts.PageSize, opts.Limit, func(ctx context.Context, page, pageSize int) ([]json.RawMessage, bool, error) {
		requests++
		if requests > maxPages {
			return nil, false, fmt.Errorf("pagination: exceeded %d pages without reaching the end of %s; the server may not be honoring the page parameter", maxPages, path)
		}
		pq := url.Values{}
		for k, v := range query {
			pq[k] = v
		}
		pq.Set("page", strconv.Itoa(page))
		pq.Set(sizeParam, strconv.Itoa(pageSize))
		endpoint := path + "?" + pq.Encode()
		var envelope map[string]json.RawMessage
		if err := c.Do(ctx, http.MethodGet, endpoint, nil, &envelope); err != nil {
			return nil, false, err
		}
		var rows []json.RawMessage
		if raw, ok := envelope[field]; ok && len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &rows); err != nil {
				return nil, false, fmt.Errorf("pagination: %s: %q is not an array: %w", path, field, err)
			}
		}
		return rows, true, nil
	})
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	return items, nil
}

// ListAllCursorPages fetches all pages from a cursor-paginated REST endpoint,
// requesting pageSize items per page. fetchPage receives the cursor for the
// page to fetch — empty on the first call — and returns that page's items plus
// the cursor for the next page, empty when the page just returned was the last.
//
// Unlike ListAllPages this cannot skip data when the server clamps pageSize:
// the next page's position comes from the server's own cursor rather than from
// an offset the client computes, so a clamped page size costs extra round trips
// and nothing else. That is the whole reason cursor endpoints get their own
// walker instead of being forced into one of the offset styles — doing that
// would reintroduce exactly the silent-truncation risk documented above.
//
// An empty page is not a termination condition. Cursor endpoints may return one
// legitimately — a page whose every row was filtered out server-side still
// carries a cursor to the rows beyond it — so only an absent cursor ends the
// walk. That leaves a misbehaving server able to hand back the same cursor
// forever, which would hang the caller with no error, so a repeated cursor is
// treated as a protocol failure rather than trusted.
func ListAllCursorPages[T any](ctx context.Context, pageSize int, fetchPage func(ctx context.Context, cursor string, pageSize int) ([]T, string, error)) ([]T, error) {
	var allItems []T
	seen := make(map[string]bool)
	cursor := ""
	for {
		items, next, err := fetchPage(ctx, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		allItems = append(allItems, items...)
		if next == "" {
			return allItems, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("pagination cursor %q repeated after %d items: server is not advancing", next, len(allItems))
		}
		seen[next] = true
		cursor = next
	}
}
