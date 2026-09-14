// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 21 — misc reads. All 14 endpoints are read-only metadata
// probes (health, versions, locales, zones, cloud info, jamf-package,
// static user groups, device-extension-attribute + device-group
// lookups). No lifecycle — just confirm they resolve and decode.

func TestAcceptance_Pro_MiscReadsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	if err := p.HealthCheckV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("HealthCheckV1: %v", err)
	}
	t.Log("HealthCheckV1: 204")

	status, err := p.GetHealthStatusV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetHealthStatusV1: %v", err)
	}
	t.Logf("HealthStatus: %+v", status)

	ver, err := p.GetJamfProVersionV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfProVersionV1: %v", err)
	}
	t.Logf("Jamf Pro version: %+v", ver)

	info, err := p.GetJamfProInformationV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfProInformationV2: %v", err)
	}
	t.Logf("Jamf Pro information v2 retrieved: %+v", info)

	locales, err := p.ListLocalesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListLocalesV1: %v", err)
	}
	t.Logf("Locales: %d", len(locales))

	tzs, err := p.ListTimeZonesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListTimeZonesV1: %v", err)
	}
	t.Logf("Time zones: %d", len(tzs))

	// GET /api/pro/v2/environment-type is no longer covered. Jamf removed the
	// operation from the published spec in the GA cleanup and it has never
	// had a rule in the gateway's authorization policy, so it answered 403
	// BAD_PERMISSIONS for every credential this suite has ever used. With the
	// path gone from the spec the SDK no longer generates GetEnvironmentTypeV2,
	// and there is nothing left to assert. See the GA cleanup notes in CLAUDE.md.

	cloud, err := p.GetCloudInformationV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCloudInformationV1: %v", err)
	}
	t.Logf("Cloud info: %+v", cloud)

	codes, err := p.ListAppStoreCountryCodesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppStoreCountryCodesV1: %v", err)
	}
	t.Logf("App-store country codes retrieved: %+v", codes)
}

func TestAcceptance_Pro_JamfPackageV1V2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// `application` query is required by the endpoint. PROTECT is a
	// known app that every tenant should have an answer for.
	const app = "PROTECT"

	v1, err := p.ListJamfPackagesV1(ctx, app)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfPackagesV1: %v", err)
	}
	t.Logf("JamfPackage v1 for %q: %d entries", app, len(v1))

	v2, err := p.GetJamfPackageV2(ctx, app)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfPackageV2: %v", err)
	}
	t.Logf("JamfPackage v2 for %q retrieved: %+v", app, v2)
}

func TestAcceptance_Pro_StaticUserGroupsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	groups, err := p.ListStaticUserGroupsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListStaticUserGroupsV1: %v", err)
	}
	t.Logf("Static user groups: %d", len(groups))

	// Probe GET by-id with a bogus id — tolerate 404 since we don't
	// assume any group exists on the tenant.
	if _, err := p.GetStaticUserGroupV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			t.Logf("GetStaticUserGroupV1(-1): 404 — expected for bogus id")
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetStaticUserGroupV1: %v", err)
		}
	}
}

func TestAcceptance_Pro_DeviceExtensionAttributesPreview(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	// Spec's default for `select` is "name"; server 400s when omitted.
	attrs, err := pro.New(c).ListDeviceExtensionAttributesPreview(ctx, "name")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceExtensionAttributesPreview: %v", err)
	}
	t.Logf("Mobile device extension attributes (preview): %+v", attrs)
}

func TestAcceptance_Pro_DeviceGroupsForDeviceV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// Probe with a bogus device id — tolerate 404 so we don't need a
	// known-managed-device fixture.
	if _, err := p.GetDeviceGroupsForDeviceV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetDeviceGroupsForDeviceV1(-1): status=%d — expected for bogus device id", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceGroupsForDeviceV1: %v", err)
	}
	t.Log("GetDeviceGroupsForDeviceV1(-1) unexpectedly succeeded")
}
