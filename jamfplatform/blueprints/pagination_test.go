// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package blueprints

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestListBlueprints_MultiPage(t *testing.T) {
	// Pages are served at the size requested, so the test holds at whatever
	// ceiling ListBlueprints uses. The service clamps at 2000 and the walk stops
	// on a short page, so a request above 2000 would read the clamped first page
	// as the last one (docs/WIRE-FACTS.md).
	const total = 2050
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	callCount := 0
	mux.HandleFunc("/blueprints/v1/blueprints", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page-size"))
		if size != 2000 {
			t.Errorf("page-size = %d, want 2000", size)
		}
		items := []map[string]any{}
		for i := page * size; i < (page+1)*size && i < total; i++ {
			items = append(items, map[string]any{"id": fmt.Sprintf("bp-%d", i)})
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"results": items, "totalCount": total})
	})

	bps, err := c.ListBlueprints(context.Background(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(bps) != total {
		t.Fatalf("got %d blueprints, want %d", len(bps), total)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
}
