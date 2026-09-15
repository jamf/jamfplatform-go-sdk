// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestNetworkSegmentPost_CreateSendsRelatedEntityFields locks in that the
// Building / Department / DistributionPoint / DistributionServer / SwuServer /
// URL fields make it onto the wire on Create. Spec for `network_segment_post`
// omits these (Classic spec bug); we inject them via schemaAdditions so
// callers can actually populate the override toggles' companion references.
// Drop this test only if the spec itself starts carrying the fields.
func TestNetworkSegmentPost_CreateSendsRelatedEntityFields(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/proclassic/networksegments/id/0", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`<network_segment><id>42</id></network_segment>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	base := jamfplatform.NewClient(srv.URL, "test-id", "test-secret", jamfplatform.WithTenantID("t-test"))
	pc := proclassic.New(base)

	name := "HQ Net"
	start, end := "10.0.0.0", "10.0.0.255"
	overrideB, overrideD := true, true
	building, department := "HQ", "IT"
	dp, ds, swu, segURL := "Main DP", "Main DS", "swu.example.com", "https://example.com"
	req := &proclassic.NetworkSegmentPost{
		Name:                &name,
		StartingAddress:     &start,
		EndingAddress:       &end,
		OverrideBuildings:   &overrideB,
		OverrideDepartments: &overrideD,
		Building:            &building,
		Department:          &department,
		DistributionPoint:   &dp,
		DistributionServer:  &ds,
		SwuServer:           &swu,
		URL:                 &segURL,
	}
	if _, err := pc.CreateNetworkSegmentByID(context.Background(), "0", req); err != nil {
		t.Fatalf("CreateNetworkSegmentByID: %v", err)
	}

	wantFragments := []string{
		"<building>HQ</building>",
		"<department>IT</department>",
		"<distribution_point>Main DP</distribution_point>",
		"<distribution_server>Main DS</distribution_server>",
		"<swu_server>swu.example.com</swu_server>",
		"<url>https://example.com</url>",
		"<override_buildings>true</override_buildings>",
		"<override_departments>true</override_departments>",
	}
	for _, frag := range wantFragments {
		if !strings.Contains(gotBody, frag) {
			t.Errorf("request body missing %q\nbody: %s", frag, gotBody)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(gotBody), "<network_segment>") {
		t.Errorf("request body root must be <network_segment>, got: %s", gotBody)
	}
}

// TestNetworkSegment_DecodesRelatedEntityFields locks in the read side: the
// six related-entity fields on a GET response populate the typed
// NetworkSegment struct. Read side already worked before the schemaAdditions
// fix — this is a regression guard.
func TestNetworkSegment_DecodesRelatedEntityFields(t *testing.T) {
	body := `<network_segment>
<id>42</id>
<name>HQ Net</name>
<starting_address>10.0.0.0</starting_address>
<ending_address>10.0.0.255</ending_address>
<override_buildings>true</override_buildings>
<override_departments>true</override_departments>
<building>HQ</building>
<department>IT</department>
<distribution_point>Main DP</distribution_point>
<distribution_server>Main DS</distribution_server>
<swu_server>swu.example.com</swu_server>
<url>https://example.com</url>
</network_segment>`

	var seg proclassic.NetworkSegment
	if err := xml.Unmarshal([]byte(body), &seg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	checks := []struct {
		name string
		got  *string
		want string
	}{
		{"Building", seg.Building, "HQ"},
		{"Department", seg.Department, "IT"},
		{"DistributionPoint", seg.DistributionPoint, "Main DP"},
		{"DistributionServer", seg.DistributionServer, "Main DS"},
		{"SwuServer", seg.SwuServer, "swu.example.com"},
		{"URL", seg.URL, "https://example.com"},
	}
	for _, c := range checks {
		if c.got == nil {
			t.Errorf("%s: nil, want %q", c.name, c.want)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, *c.got, c.want)
		}
	}
}
