// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devicegroups"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devices"
)

func TestAcceptance_ListDevices(t *testing.T) {
	c := accClient(t)

	d, err := devices.New(c).ListDevices(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices failed: %v", err)
	}
	t.Logf("Found %d devices", len(d))
}

func TestAcceptance_ListDevicesWithSort(t *testing.T) {
	c := accClient(t)

	d, err := devices.New(c).ListDevices(context.Background(), []string{"name:asc"}, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices with sort failed: %v", err)
	}
	t.Logf("Found %d devices (sorted by name asc)", len(d))
}

func TestAcceptance_GetDevice(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dev := devices.New(c)

	d, err := dev.ListDevices(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(d) == 0 {
		t.Skip("No devices available to read by ID")
	}

	device, err := dev.GetDevice(ctx, d[0].ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDevice failed: %v", err)
	}
	if device.ID != d[0].ID {
		t.Errorf("expected ID %q, got %q", d[0].ID, device.ID)
	}

	t.Logf("Read device: %s (%s), managed: %v, mdmCapable: %v", device.Name, device.ID, device.Managed, device.MDMCapable)
	if device.Hardware != nil {
		t.Logf("  Hardware: %s %s, serial: %s", device.Hardware.Make, device.Hardware.Model, device.Hardware.SerialNumber)
	}
	if device.OperatingSystem != nil {
		t.Logf("  OS: %s %s (build %s)", device.OperatingSystem.Name, device.OperatingSystem.Version, device.OperatingSystem.Build)
	}
}

func TestAcceptance_ListDeviceApplications(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dev := devices.New(c)

	d, err := dev.ListDevices(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(d) == 0 {
		t.Skip("No devices available")
	}

	apps, err := dev.ListDeviceApplications(ctx, d[0].ID, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceApplications failed: %v", err)
	}
	t.Logf("Device %s has %d applications", d[0].ID, len(apps))
}

func TestAcceptance_ListDeviceGroupsForDevice(t *testing.T) {
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

	groups, err := devicegroups.New(c).ListDeviceGroupsForDevice(ctx, d[0].ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroupsForDevice failed: %v", err)
	}
	t.Logf("Device %s belongs to %d groups", d[0].ID, len(groups))
	for _, g := range groups {
		t.Logf("  Group: %s (%s)", g.GroupName, g.GroupID)
	}
}
