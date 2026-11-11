// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// pagedServer serves rows 0..total-1 as {"results":[{"n":i},...]} pages and
// answers 504 for any (page, page-size) pair in fail, once per pair, or on
// every request whose page-size is above alwaysFailAbove (when non-zero).
type pagedServer struct {
	mu              sync.Mutex
	total           int
	fail            map[[2]int]bool
	alwaysFailAbove int
	calls           map[[2]int]int
	sizes           []int
}

func (s *pagedServer) handler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("page-size"))
	key := [2]int{page, size}
	s.mu.Lock()
	s.calls[key]++
	s.sizes = append(s.sizes, size)
	failNow := s.fail[key] || (s.alwaysFailAbove > 0 && size > s.alwaysFailAbove)
	delete(s.fail, key)
	s.mu.Unlock()
	if failNow {
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte("<html><body>504 Gateway Timeout</body></html>"))
		return
	}
	rows := []map[string]int{}
	for i := page * size; i < (page+1)*size && i < s.total; i++ {
		rows = append(rows, map[string]int{"n": i})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"totalCount": s.total, "results": rows})
}

func newPagedTransport(t *testing.T, s *pagedServer) *Transport {
	t.Helper()
	if s.calls == nil {
		s.calls = map[[2]int]int{}
	}
	srv, mux := newTestServer(t)
	mux.HandleFunc("/api/rows", s.handler)
	return NewTransportWithUserAgent(srv.URL, "id", "secret", "test",
		WithMinRequestInterval(0),
		WithRetryPolicy(time.Millisecond, time.Millisecond, 3))
}

func rowNumbers(t *testing.T, raw []json.RawMessage) []int {
	t.Helper()
	out := make([]int, len(raw))
	for i, r := range raw {
		var v struct{ N int }
		if err := json.Unmarshal(r, &v); err != nil {
			t.Fatal(err)
		}
		out[i] = v.N
	}
	return out
}

func assertSequence(t *testing.T, got []int, total int) {
	t.Helper()
	if len(got) != total {
		t.Fatalf("got %d rows, want %d", len(got), total)
	}
	for i, n := range got {
		if n != i {
			t.Fatalf("row %d = %d, want %d (rows duplicated or skipped)", i, n, i)
		}
	}
}

// A 504 on a page above the floor is answered with a smaller page, never the
// identical one: each oversized size is sent exactly once.
func TestListAllRaw_ShrinksInsteadOfRetryingAGatewayTimeout(t *testing.T) {
	s := &pagedServer{total: 1200, alwaysFailAbove: 500}
	c := newPagedTransport(t, s)

	var shrinks []string
	got, err := c.ListAllRaw(context.Background(), "/api/rows", nil, RawPageWalk{PageSize: 2000,
		OnShrink: func(from, to int) { shrinks = append(shrinks, fmt.Sprintf("%d->%d", from, to)) }})
	if err != nil {
		t.Fatal(err)
	}
	assertSequence(t, rowNumbers(t, got), 1200)
	if fmt.Sprint(shrinks) != "[2000->1000 1000->500]" {
		t.Errorf("shrinks = %v", shrinks)
	}
	if n := s.calls[[2]int{0, 2000}]; n != 1 {
		t.Errorf("page 0 at 2000 sent %d times, want 1", n)
	}
	if n := s.calls[[2]int{0, 1000}]; n != 1 {
		t.Errorf("page 0 at 1000 sent %d times, want 1", n)
	}
}

// Two shrinks mid-walk, the second to a size that does not divide the offset:
// the rows already held are skipped, not fetched twice.
func TestListAllRaw_ResumesFromTheRowsAlreadyHeld(t *testing.T) {
	s := &pagedServer{total: 1500, fail: map[[2]int]bool{{1, 750}: true, {3, 375}: true}}
	c := newPagedTransport(t, s)

	got, err := c.ListAllRaw(context.Background(), "/api/rows", nil, RawPageWalk{PageSize: 750})
	if err != nil {
		t.Fatal(err)
	}
	assertSequence(t, rowNumbers(t, got), 1500)
}

// At the floor there is nothing smaller to ask for, so the page gets the
// transport's ordinary 504 retry and the walk then fails with the 504.
func TestListAllRaw_RetriesAtTheFloorThenSurfacesTheTimeout(t *testing.T) {
	s := &pagedServer{total: 50, alwaysFailAbove: 1}
	c := newPagedTransport(t, s)

	_, err := c.ListAllRaw(context.Background(), "/api/rows", nil, RawPageWalk{PageSize: MinShrunkPageSize})
	if !IsServerTimeout(err) {
		t.Fatalf("err = %v, want a server timeout", err)
	}
	if n := s.calls[[2]int{0, MinShrunkPageSize}]; n != 4 {
		t.Errorf("floor page sent %d times, want 4 (1 + 3 retries)", n)
	}
}

// A request that is not a page of a walk keeps the 504 retry.
func TestDo_GatewayTimeoutOutsideAWalkIsStillRetried(t *testing.T) {
	var calls int
	srv, mux := newTestServer(t)
	mux.HandleFunc("/api/one", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	})
	c := NewTransportWithUserAgent(srv.URL, "id", "secret", "test", WithMinRequestInterval(0),
		WithRetryPolicy(time.Millisecond, time.Millisecond, 3))

	var out map[string]string
	if err := c.Do(context.Background(), http.MethodGet, "/api/one", nil, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestListAllRaw_LimitStopsTheWalk(t *testing.T) {
	s := &pagedServer{total: 1000}
	c := newPagedTransport(t, s)

	got, err := c.ListAllRaw(context.Background(), "/api/rows", nil, RawPageWalk{PageSize: 100, Limit: 250})
	if err != nil {
		t.Fatal(err)
	}
	assertSequence(t, rowNumbers(t, got), 250)
	if len(s.sizes) != 3 {
		t.Errorf("sent %d requests, want 3", len(s.sizes))
	}
}

func TestListAllRaw_EmptyCollectionIsAnEmptySlice(t *testing.T) {
	c := newPagedTransport(t, &pagedServer{total: 0})
	got, err := c.ListAllRaw(context.Background(), "/api/rows", nil, RawPageWalk{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want an empty non-nil slice", got)
	}
}

// The typed walk shares the shrink: a 504 from fetchPage halves the size.
func TestListAllPages_ShrinksOnServerTimeout(t *testing.T) {
	var sizes []int
	items, err := ListAllPages(context.Background(), 400, func(ctx context.Context, page, pageSize int) ([]int, bool, error) {
		sizes = append(sizes, pageSize)
		if !isPageWalk(ctx) && pageSize > MinShrunkPageSize {
			t.Errorf("page at %d not marked as a walk", pageSize)
		}
		if pageSize > 200 {
			return nil, false, &APIResponseError{StatusCode: http.StatusGatewayTimeout}
		}
		var out []int
		for i := page * pageSize; i < (page+1)*pageSize && i < 500; i++ {
			out = append(out, i)
		}
		return out, (page+1)*pageSize < 500, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSequence(t, items, 500)
	if fmt.Sprint(sizes[:2]) != "[400 200]" {
		t.Errorf("sizes = %v, want to start [400 200]", sizes)
	}
}

// A deadline on the caller's own context is not a page-size problem.
func TestListAllPages_DoesNotShrinkPastTheCallersDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := ListAllPages(ctx, 2000, func(context.Context, int, int) ([]int, bool, error) {
		calls++
		return nil, false, fmt.Errorf("get: %w", context.DeadlineExceeded)
	})
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want the deadline after 1", err, calls)
	}
}

func TestIsServerTimeout(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{&APIResponseError{StatusCode: 504}, true},
		{fmt.Errorf("wrapped: %w", &APIResponseError{StatusCode: 504}), true},
		{&APIResponseError{StatusCode: 502}, false},
		{context.DeadlineExceeded, true},
		{errors.New("boom"), false},
	} {
		if got := IsServerTimeout(tc.err); got != tc.want {
			t.Errorf("IsServerTimeout(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// WithHTTPClient replaces the chain that carried the User-Agent wrapper; the
// configured agent must survive it.
func TestWithHTTPClient_KeepsTheUserAgent(t *testing.T) {
	var got []string
	srv, mux := newTestServer(t)
	mux.HandleFunc("/api/ua", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
		_ = json.NewEncoder(w).Encode(map[string]string{})
	})
	c := NewTransportWithUserAgent(srv.URL, "id", "secret", "jamf-cli/9.9",
		WithHTTPClient(&http.Client{}), WithMinRequestInterval(0))

	var out map[string]string
	if err := c.Do(context.Background(), http.MethodGet, "/api/ua", nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "jamf-cli/9.9" {
		t.Fatalf("User-Agent = %v, want [jamf-cli/9.9]", got)
	}
}
