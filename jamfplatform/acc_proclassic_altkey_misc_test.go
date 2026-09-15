// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

// Create probes for Classic endpoints that have no feasible round-trip
// CRUD acceptance test on a shared tenant — either there is no DELETE
// (HealthcareListenerRule), the call is destructive on real devices
// (bogus-id PatchPolicy), or creating requires real external credentials
// the harness can't synthesize (VPP/DEP tokens, upstream patch sources).
//
// Every other alternate-key method (Get/Create/Update/Delete by name,
// id, mac, serial, udid, uuid, …) is already covered two ways: a 1:1
// mock-server unit test in the proclassic package pins its URL + codec,
// and a real TestAcceptance_Classic_FooCRUD round-trips it against a live
// tenant with a populated body and asserts 404-after-delete. The former
// tolerant "4xx-or-201" smoke probes added no coverage on top of that and
// — because several Classic endpoints silently accept an empty body and
// mint a blank-named ("Untitled") record without cleanup — leaked a stray
// tenant record on every run. They were removed; only the genuinely
// unique probes below remain.
//
// Each surviving probe exercises the transport + codec path end-to-end,
// is tolerant of a server-side rejection (4xx), and either registers a
// best-effort delete in t.Cleanup for any returned id or t.Fatals when
// cleanup is impossible — so the tenant stays leak-free between runs.

import (
	"context"
	"errors"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// probeCreateHandleErr interprets the err returned by a probe-create
// call: treats 5xx as skip, any other APIResponseError as "rejected as
// expected" (returns rejected=true), and anything else as a transport
// failure (t.Fatal). Returns rejected=false when err is nil — caller
// must then clean up the stray resource.
func probeCreateHandleErr(t *testing.T, resource string, err error) (rejected bool) {
	t.Helper()
	if err == nil {
		return false
	}
	skipOnServerError(t, err)
	if _, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
		return true
	}
	t.Fatalf("%s transport error: %v", resource, err)
	return false
}

// --- Create probes ---
//
// Most create-probes have been superseded by their real CRUD counterparts
// (TestAcceptance_Classic_FooCRUD), which round-trip create → get →
// update → delete with populated bodies and assert 404-after-delete.
// The probes that remain target endpoints where full CRUD is infeasible
// on a shared tenant: no DELETE endpoint (HealthcareListenerRule),
// destructive on real devices (bogus-id form for PatchPolicy), or
// requires real external credentials the test harness can't synthesize
// (VPP tokens, DEP tokens, upstream patch sources). Each such probe
// either registers Cleanup for any returned id or t.Fatals when cleanup
// is impossible, so the tenant stays leak-free between runs.

// TestAcceptance_Classic_ProbeCreate_CreateHealthcareListenerRuleByID — the
// Classic spec doesn't expose a DELETE for healthcare_listener_rule, so a
// stray record can't be cleaned up by the SDK. Treat unexpected
// acceptance as a hard failure so the operator can manually purge and
// reassess the probe.
func TestAcceptance_Classic_ProbeCreate_CreateHealthcareListenerRuleByID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()
	created, err := pc.CreateHealthcareListenerRuleByID(ctx, "0", &proclassic.HealthcareListenerRule{})
	if probeCreateHandleErr(t, "CreateHealthcareListenerRuleByID", err) {
		return
	}
	id := 0
	if created != nil && created.ID != nil {
		id = *created.ID
	}
	t.Fatalf("empty-body create unexpectedly succeeded (id=%d) — no DELETE endpoint, manual cleanup required", id)
}

// CreateJsonWebTokenConfigurationByID — covered by
// TestAcceptance_Classic_JsonWebTokenConfigurationCRUD.
// CreateMobileDeviceByID — covered by TestAcceptance_Classic_MobileDeviceCRUD.
// CreateMobileDeviceInvitationByID — covered by
// TestAcceptance_Classic_MobileDeviceInvitationCRUD.

func TestAcceptance_Classic_ProbeCreate_CreateMobileDeviceProvisioningProfileByID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()
	created, err := pc.CreateMobileDeviceProvisioningProfileByID(ctx, "0", &proclassic.MobileDeviceProvisioningProfile{})
	if probeCreateHandleErr(t, "CreateMobileDeviceProvisioningProfileByID", err) {
		return
	}
	if created != nil && created.ID != nil {
		id := *created.ID
		t.Cleanup(func() {
			if err := pc.DeleteMobileDeviceProvisioningProfileByID(ctx, intToStr(id)); err != nil {
				t.Logf("cleanup: DeleteMobileDeviceProvisioningProfileByID(%d): %v", id, err)
			}
		})
		t.Logf("probe-create accepted empty body; id=%d queued for cleanup", id)
	}
}

func TestAcceptance_Classic_ProbeCreate_CreatePatchPolicyBySoftwareTitleConfigID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()
	created, err := pc.CreatePatchPolicyBySoftwareTitleConfigID(ctx, "999999999", &proclassic.PatchPolicy{})
	if probeCreateHandleErr(t, "CreatePatchPolicyBySoftwareTitleConfigID", err) {
		return
	}
	if created != nil && created.ID != nil {
		id := *created.ID
		t.Cleanup(func() {
			if err := pc.DeletePatchPolicyByID(ctx, intToStr(id)); err != nil {
				t.Logf("cleanup: DeletePatchPolicyByID(%d): %v", id, err)
			}
		})
		t.Logf("probe-create accepted empty body; id=%d queued for cleanup", id)
	}
}

func TestAcceptance_Classic_ProbeCreate_CreatePatchSoftwareTitleByID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()
	created, err := pc.CreatePatchSoftwareTitleByID(ctx, "0", &proclassic.PatchSoftwareTitle{})
	if probeCreateHandleErr(t, "CreatePatchSoftwareTitleByID", err) {
		return
	}
	if created != nil && created.ID != nil {
		id := *created.ID
		// POST is the only /patchsoftwaretitles verb the SDK still generates,
		// so cleanup goes through Pro v3 — it addresses the same object by the
		// same id and deleting there also removes the Classic record.
		t.Cleanup(func() {
			if err := pro.New(c).DeletePatchSoftwareTitleConfigurationV3(ctx, intToStr(id)); err != nil {
				t.Logf("cleanup: DeletePatchSoftwareTitleConfigurationV3(%d): %v", id, err)
			}
		})
		t.Logf("probe-create accepted empty body; id=%d queued for cleanup", id)
	}
}

func TestAcceptance_Classic_ProbeCreate_CreateVPPAccountByID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()
	created, err := pc.CreateVPPAccountByID(ctx, "0", &proclassic.VppAccount{})
	if probeCreateHandleErr(t, "CreateVPPAccountByID", err) {
		return
	}
	if created != nil && created.ID != nil {
		id := *created.ID
		t.Cleanup(func() {
			if err := pc.DeleteVPPAccountByID(ctx, intToStr(id)); err != nil {
				t.Logf("cleanup: DeleteVPPAccountByID(%d): %v", id, err)
			}
		})
		t.Logf("probe-create accepted empty body; id=%d queued for cleanup", id)
	}
}

func TestAcceptance_Classic_ProbeCreate_CreateVPPAssignmentByID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()
	created, err := pc.CreateVPPAssignmentByID(ctx, "0", &proclassic.VppAssignmentPost{})
	if probeCreateHandleErr(t, "CreateVPPAssignmentByID", err) {
		return
	}
	if created != nil && created.ID != nil {
		id := *created.ID
		t.Cleanup(func() {
			if err := pc.DeleteVPPAssignmentByID(ctx, intToStr(id)); err != nil {
				t.Logf("cleanup: DeleteVPPAssignmentByID(%d): %v", id, err)
			}
		})
		t.Logf("probe-create accepted empty body; id=%d queued for cleanup", id)
	}
}
