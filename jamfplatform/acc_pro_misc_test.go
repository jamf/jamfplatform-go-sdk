// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Misc Pro endpoints that don't cluster with any other resource theme:
// startup-status, mobile-devices detail (oneOf/discriminator round-trip
// check), change-user-password (expected-rejection probe).

func TestAcceptance_Pro_GetStartupStatus(t *testing.T) {
	c := accClient(t)

	status, err := pro.New(c).GetStartupStatus(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetStartupStatus failed: %v", err)
	}
	t.Logf("Startup status: step=%s stepCode=%s percentage=%d", status.Step, status.StepCode, status.Percentage)
}

// TestAcceptance_Pro_ListMobileDevicesDetail exercises the oneOf/discriminator
// path: the response carries a paginated slice of MobileDeviceResponse where
// each element is one of the iOS / tvOS / visionOS / watchOS variants keyed by
// the deviceType discriminator. The generated UnmarshalJSON dispatches each
// element to the matching variant pointer.
//
// The switch is keyed on the generated constants rather than string literals so
// that a value the spec renames or drops fails the build, instead of quietly
// becoming a case that can never match. It covers every member of
// MobileDeviceResponseDeviceTypeValues(), and the default arm is what stops a
// newly-added variant from going unnoticed — visionOS was already in the union
// and absent from this switch before the constants existed.
func TestAcceptance_Pro_ListMobileDevicesDetail(t *testing.T) {
	c := accClient(t)

	devices, err := pro.New(c).ListMobileDevicesDetailV2(context.Background(), nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListMobileDevicesDetailV2: %v", err)
	}
	t.Logf("Found %d mobile devices", len(devices))
	for i, d := range devices {
		if i >= 5 {
			break
		}
		switch d.DeviceType {
		case pro.MobileDeviceResponseDeviceTypeIOS:
			if d.IOS == nil {
				t.Errorf("device[%d] DeviceType=%s but IOS variant is nil", i, d.DeviceType)
			}
		case pro.MobileDeviceResponseDeviceTypeTvOS:
			if d.TvOS == nil {
				t.Errorf("device[%d] DeviceType=%s but TvOS variant is nil", i, d.DeviceType)
			}
		case pro.MobileDeviceResponseDeviceTypeVisionOS:
			if d.VisionOS == nil {
				t.Errorf("device[%d] DeviceType=%s but VisionOS variant is nil", i, d.DeviceType)
			}
		case pro.MobileDeviceResponseDeviceTypeWatchOS:
			if d.WatchOS == nil {
				t.Errorf("device[%d] DeviceType=%s but WatchOS variant is nil", i, d.DeviceType)
			}
		default:
			t.Errorf("device[%d] DeviceType=%q is outside MobileDeviceResponseDeviceTypeValues() — the spec gained a variant this test does not dispatch", i, d.DeviceType)
		}
		t.Logf("device[%d] type=%s", i, d.DeviceType)
	}
}

// TestAcceptance_Pro_ChangeUserPassword intentionally calls with a
// clearly-wrong current password and expects the API to reject. The
// alternative — actually rotating a credential — would lock out either the
// OAuth API client (our test auth) or an admin user. The test still
// exercises the transport path and payload encoding end-to-end.
func TestAcceptance_Pro_ChangeUserPassword(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	err := pro.New(c).ChangeUserPasswordV1(ctx, &pro.ChangePassword{
		CurrentPassword: "sdk-acc-clearly-not-valid-" + runSuffix(),
		NewPassword:     "sdk-acc-unused",
	})
	if err == nil {
		t.Fatal("expected server to reject wrong currentPassword, got nil error (did credentials actually rotate?)")
	}
	if apiErr, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
		t.Logf("ChangeUserPasswordV1 rejected as expected: status=%d", apiErr.StatusCode)
		return
	}
	t.Logf("ChangeUserPasswordV1 rejected as expected: %v", err)
}
