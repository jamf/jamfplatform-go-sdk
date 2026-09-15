// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/ddmreport"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devices"
)

func TestAcceptance_GetDeviceDeclarationReportFiltered(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	d, err := devices.New(c).ListDevices(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(d) == 0 {
		t.Skip("No devices available")
	}

	results, err := ddmreport.New(c).GetDeviceDeclarationReportFiltered(ctx, d[0].ID, "active==true", nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceDeclarationReportFiltered failed: %v", err)
	}
	t.Logf("Device %s has %d filtered declarations", d[0].ID, len(results))
	for _, r := range results {
		t.Logf("  %s channel=%s type=%s status=%s validity=%s", r.DeclarationIdentifier, r.Channel, r.Type, r.Status, r.ValidityState)
	}
}

func TestAcceptance_GetDeviceChannels(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	d, err := devices.New(c).ListDevices(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(d) == 0 {
		t.Skip("No devices available")
	}

	result, err := ddmreport.New(c).GetDeviceChannels(ctx, d[0].ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceChannels failed: %v", err)
	}
	t.Logf("Device %s has %d channels: %v", result.DeviceID, len(result.Channels), result.Channels)
}

func TestAcceptance_ListDeclarationReportClientsFiltered(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dr := ddmreport.New(c)

	d, err := devices.New(c).ListDevices(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(d) == 0 {
		t.Skip("No devices available")
	}

	// filter is required: true with no default, so the generated method sends
	// it whatever the caller passes — "" would travel as filter= rather than
	// being dropped. Supply the same expression the sibling test uses.
	report, err := dr.GetDeviceDeclarationReportFiltered(ctx, d[0].ID, "active==true", nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceDeclarationReportFiltered failed: %v", err)
	}

	var declID string
	if len(report) > 0 {
		declID = report[0].DeclarationIdentifier
	}
	if declID == "" {
		t.Skip("No declarations found on any device channel")
	}

	results, err := dr.ListDeclarationReportClientsFiltered(ctx, declID, "active==true", nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeclarationReportClientsFiltered(%s) failed: %v", declID, err)
	}
	t.Logf("Declaration %s reported by %d filtered devices", declID, len(results))
	for _, r := range results {
		t.Logf("  device=%s channel=%s type=%s status=%s validity=%s", r.DeviceID, r.Channel, r.Type, r.Status, r.ValidityState)
	}
}
