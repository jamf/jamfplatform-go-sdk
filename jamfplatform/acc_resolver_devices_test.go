// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devices"
)

// Devices are enrolled, not created via API. Read-only probe.

func TestAcceptance_ResolveDeviceIDByName_NotFound(t *testing.T) {
	c := accClient(t)
	dev := devices.New(c)
	_, err := dev.ResolveDeviceIDByName(context.Background(), "sdk-does-not-exist-dev-"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveDeviceIDByName_Existing(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dev := devices.New(c)

	d, err := dev.ListDevices(ctx, nil, "")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(d) == 0 {
		t.Skip("no devices — skipping")
	}
	first := d[0]
	gotID, err := dev.ResolveDeviceIDByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.Name, gotID)
}

func TestAcceptance_ResolveDeviceByName_Existing(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dev := devices.New(c)

	d, err := dev.ListDevices(ctx, nil, "")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(d) == 0 {
		t.Skip("no devices — skipping")
	}
	first := d[0]
	got, err := dev.ResolveDeviceByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.ID != first.ID {
		t.Errorf("typed ID = %q, want %q", got.ID, first.ID)
	}
	if got.Name != first.Name {
		t.Errorf("typed Name = %q, want %q", got.Name, first.Name)
	}
	t.Logf("resolved typed %q → %s ✓", first.Name, got.ID)
}

// ─── Device serial number resolvers ─────────────────────────────────────────

func TestAcceptance_ResolveDeviceIDBySerialNumber_NotFound(t *testing.T) {
	c := accClient(t)
	dev := devices.New(c)
	_, err := dev.ResolveDeviceIDBySerialNumber(context.Background(), "SDKNOTEXIST"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveDeviceIDBySerialNumber_Existing(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dev := devices.New(c)

	d, err := dev.ListDevices(ctx, nil, "")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(d) == 0 {
		t.Skip("no devices — skipping")
	}
	first := d[0]
	gotID, err := dev.ResolveDeviceIDBySerialNumber(ctx, first.SerialNumber)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved serial %q → %s ✓", first.SerialNumber, gotID)
}

func TestAcceptance_ResolveDeviceBySerialNumber_Existing(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dev := devices.New(c)

	d, err := dev.ListDevices(ctx, nil, "")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(d) == 0 {
		t.Skip("no devices — skipping")
	}
	first := d[0]
	got, err := dev.ResolveDeviceBySerialNumber(ctx, first.SerialNumber)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.ID != first.ID {
		t.Errorf("typed ID = %q, want %q", got.ID, first.ID)
	}
	if got.SerialNumber != first.SerialNumber {
		t.Errorf("typed SerialNumber = %q, want %q", got.SerialNumber, first.SerialNumber)
	}
	t.Logf("resolved typed serial %q → %s ✓", first.SerialNumber, got.ID)
}
