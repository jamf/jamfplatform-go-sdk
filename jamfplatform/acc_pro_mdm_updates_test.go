// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// createWifiProfileFixture posts a minimal Wi-Fi .mobileconfig via the
// Classic mobile-device-configuration-profile endpoint and returns its
// numeric id. Registers a Classic-side cleanup so the profile is deleted
// when the test ends. Skips the caller (via t.Skip) if the tenant
// rejects the fixture — Wi-Fi payload validation varies across Pro
// versions.
func createWifiProfileFixture(t *testing.T) string {
	t.Helper()
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	suffix := runSuffix()
	name := "sdk-acc-wifi-" + suffix
	desc := "sdk-acc test — safe to delete"
	payload := proclassic.PayloadsXMLText(fmt.Sprintf(wifiProfilePayloadXML, suffix, suffix))

	req := &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{
			Name:        &name,
			Description: &desc,
			Payloads:    &payload,
		},
	}
	resp, err := pc.CreateMobileDeviceConfigurationProfileByID(ctx, "0", req)
	if err != nil {
		t.Skipf("could not create Wi-Fi fixture via Classic — RTS CRUD skipped: %v", err)
	}
	if resp == nil || resp.ID == nil {
		t.Skip("Classic Wi-Fi fixture returned no id — RTS CRUD skipped")
	}
	id := strconv.Itoa(*resp.ID)
	t.Cleanup(func() {
		if err := pc.DeleteMobileDeviceConfigurationProfileByID(context.Background(), id); err != nil {
			t.Logf("cleanup Wi-Fi fixture %s: %v", id, err)
		}
	})
	t.Logf("Created Wi-Fi fixture profile %s", id)
	return id
}

// wifiProfilePayloadXML is an XML-escaped .mobileconfig plist for a
// bare Wi-Fi profile. Two %s placeholders expand to suffix-derived
// identifiers so each test run gets a distinct PayloadUUID.
const wifiProfilePayloadXML = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>PayloadContent</key><array><dict><key>AutoJoin</key><true/><key>EncryptionType</key><string>WPA</string><key>HIDDEN_NETWORK</key><false/><key>IsHotspot</key><false/><key>PayloadDescription</key><string>sdk-acc test wifi</string><key>PayloadDisplayName</key><string>Wi-Fi</string><key>PayloadIdentifier</key><string>com.sdkacc.wifi.%[1]s</string><key>PayloadType</key><string>com.apple.wifi.managed</string><key>PayloadUUID</key><string>11111111-1111-1111-1111-%[1]s11</string><key>PayloadVersion</key><integer>1</integer><key>ProxyType</key><string>None</string><key>SSID_STR</key><string>sdk-acc-ssid</string></dict></array><key>PayloadDisplayName</key><string>sdk-acc-wifi</string><key>PayloadIdentifier</key><string>sdk-acc-wifi-%[2]s</string><key>PayloadRemovalDisallowed</key><false/><key>PayloadScope</key><string>System</string><key>PayloadType</key><string>Configuration</string><key>PayloadUUID</key><string>22222222-2222-2222-2222-%[2]s22</string><key>PayloadVersion</key><integer>1</integer></dict></plist>`

// Batch 8 — MDM + updates.
//
// Destructive endpoints (blank push, renew profile, deploy package, MDM
// commands, DDM sync) hit real enrolled devices and cannot be exercised
// safely with fabricated ids — the server historically 500s on bogus
// inputs rather than 4xx, so a transport-only probe doesn't prove
// correctness either. Those tests ship as permanent SKIPs with a
// documented reason; a manual curl verification against a disposable
// target is the recommended path if coverage is needed.
//
// Return-to-service is a stored configuration (not an MDM command
// target) — it does run full CRUD.

// --- apns-client-push-status -------------------------------------------

func TestAcceptance_Pro_MdmUpdates_ListApnsClientPushStatusesV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListApnsClientPushStatusesV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListApnsClientPushStatusesV1: %v", err)
	}
	t.Logf("APNs client push status entries: %d", len(items))
}

func TestAcceptance_Pro_MdmUpdates_GetEnableAllApnsClientsStatusV1(t *testing.T) {
	c := accClient(t)

	status, err := pro.New(c).GetEnableAllApnsClientsStatusV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		// 404 here is "no enable-all request has ever been made on this
		// instance", not a missing endpoint. Wire-verified 2026-09-03 on two
		// instances running the identical build 11.31.1-t1787060595569: one had
		// a real record (status COMPLETED, requestedTime 2026-08-30) and
		// answered 200; the other had never had a request and answered 404 with
		// an empty errors array. The endpoint reports the status OF a request,
		// so with no request there is nothing to report.
		//
		// Not tolerated blindly: the body must be the empty-errors 404 that
		// shape produces. Anything else still fails, and only EnableAllApnsClientsV1
		// — which is state-changing on real MDM clients and so never called here —
		// could make this return 200 on a fresh instance.
		if apiErr := jamfplatform.AsAPIError(err); apiErr != nil && apiErr.HasStatus(404) && len(apiErr.FieldErrors()) == 0 {
			t.Skip("no enable-all APNs request has been made on this instance, so there is no status to read (404)")
		}
		t.Fatalf("GetEnableAllApnsClientsStatusV1: %v", err)
	}
	t.Logf("Enable-all APNs clients status: %+v", status)
}

// EnableAllApnsClientsV1 + EnableApnsClientV1 are state-changing on real
// MDM clients; no probe path is safe.
func TestAcceptance_Pro_MdmUpdates_EnableAllApnsClientsV1(t *testing.T) {
	t.Skip("destructive (flips APNs state for every client on the tenant) — manual curl verification only")
}

func TestAcceptance_Pro_MdmUpdates_EnableApnsClientV1(t *testing.T) {
	t.Skip("destructive (flips APNs state on a real MDM-enrolled client) — manual curl verification only")
}

// --- declarative-device-management -------------------------------------

// DDM endpoints take a clientManagementId (UUID per device). The tenant
// may not have any enrolled DDM-capable device, and probing with a
// bogus id hits server 500s not 404s. Read-only probes only when data
// exists; Sync is destructive.

func TestAcceptance_Pro_MdmUpdates_ListDdmStatusItemsV1(t *testing.T) {
	// Plumbing-only: probe with a syntactic UUID; tolerate 400/404 or
	// empty response.
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	probeID := "00000000-0000-0000-0000-000000000000"
	_, err := p.ListDdmStatusItemsV1(ctx, probeID)
	if err == nil {
		t.Logf("ListDdmStatusItemsV1(%s): unexpectedly succeeded", probeID)
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("ListDdmStatusItemsV1(%s): %d — no DDM device, plumbing OK", probeID, apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("ListDdmStatusItemsV1(%s): %v", probeID, err)
}

func TestAcceptance_Pro_MdmUpdates_GetDdmStatusItemV1(t *testing.T) {
	t.Skip("requires a real DDM client management id AND a known status key — no scaffolding available")
}

func TestAcceptance_Pro_MdmUpdates_SyncDdmV1(t *testing.T) {
	t.Skip("destructive (re-sync MDM state for a real device) — manual curl only")
}

// --- managed-software-updates ------------------------------------------

func TestAcceptance_Pro_MdmUpdates_ListAvailableOsUpdatesV1(t *testing.T) {
	c := accClient(t)

	avail, err := pro.New(c).ListAvailableOsUpdatesV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAvailableOsUpdatesV1: %v", err)
	}
	t.Logf("Available OS updates: %+v", avail)
}

func TestAcceptance_Pro_MdmUpdates_ListManagedSoftwareUpdatePlansV1(t *testing.T) {
	c := accClient(t)

	plans, err := pro.New(c).ListManagedSoftwareUpdatePlansV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListManagedSoftwareUpdatePlansV1: %v", err)
	}
	t.Logf("Managed software update plans: %d", len(plans))
}

// TestAcceptance_Pro_MdmUpdates_GetGroupPlansV1 covers the one
// managed-software-update read that takes a spec-required query parameter.
// group-type is required: true with a two-value enum, so the generated method
// sends it unconditionally rather than dropping an empty one — see
// resolveQueryParams in tools/generate.
//
// The server does NOT enforce it, which is the finding worth recording:
// probed 2026-09-01 against group id 1 on Jamf Pro 11.31.1, all of
// group-type=COMPUTER_GROUP, group-type=MOBILE_DEVICE_GROUP, group-type= and
// the parameter omitted entirely answer 200 with the same body, while
// group-type=BOGUS is 400 INVALID_REQUEST_PARAMETER_TYPE naming the Java enum.
// So required-ness here is documentation, not validation, and the two group
// types are not distinguished on a group that has no plans. A 400 for the
// empty value appearing later means the server started enforcing it.
func TestAcceptance_Pro_MdmUpdates_GetGroupPlansV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	groups, err := p.ListComputerGroupsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputerGroupsV1: %v", err)
	}
	if len(groups) == 0 {
		t.Skip("no computer groups on this tenant")
	}
	id := groups[0].ID

	plans, err := p.GetManagedSoftwareUpdateGroupPlansV1(ctx, id, "COMPUTER_GROUP")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetManagedSoftwareUpdateGroupPlansV1(%s, COMPUTER_GROUP): %v", id, err)
	}
	t.Logf("Group %s has %d managed software update plans", id, len(plans.Results))

	// An out-of-enum value is the only rejection this endpoint has.
	_, err = p.GetManagedSoftwareUpdateGroupPlansV1(ctx, id, "BOGUS")
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusBadRequest) {
		skipOnServerError(t, err)
		t.Fatalf("GetManagedSoftwareUpdateGroupPlansV1(%s, BOGUS) = %v, want 400", id, err)
	}
	if d := apiErr.Details(); len(d) == 0 || d[0].Code != "INVALID_REQUEST_PARAMETER_TYPE" {
		t.Errorf("details = %+v, want INVALID_REQUEST_PARAMETER_TYPE", d)
	}

	// The empty value travels (that is the point of the required-param
	// emission) and the server accepts it, same as COMPUTER_GROUP.
	if _, err := p.GetManagedSoftwareUpdateGroupPlansV1(ctx, id, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetManagedSoftwareUpdateGroupPlansV1(%s, \"\") = %v, want nil: an empty group-type is accepted, so a failure here means the server began enforcing required-ness", id, err)
	}
}

// TestAcceptance_Pro_MdmUpdates_FeatureToggleRoundTripV1 reads the
// feature-toggle settings, writes them back unchanged, and reads status.
// Doesn't abandon — that's a one-way action.
func TestAcceptance_Pro_MdmUpdates_FeatureToggleRoundTripV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetManagedSoftwareUpdateFeatureToggleV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetManagedSoftwareUpdateFeatureToggleV1: %v", err)
	}
	t.Logf("Feature toggle: %+v", current)

	if _, err := p.UpdateManagedSoftwareUpdateFeatureToggleV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateManagedSoftwareUpdateFeatureToggleV1 round-trip: %v", err)
	}

	status, err := p.GetManagedSoftwareUpdateFeatureToggleStatusV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetManagedSoftwareUpdateFeatureToggleStatusV1: %v", err)
	}
	t.Logf("Feature toggle status: %+v", status)
}

func TestAcceptance_Pro_MdmUpdates_AbandonFeatureToggleV1(t *testing.T) {
	t.Skip("one-way destructive action (abandons an in-flight feature-toggle rollout) — manual curl only")
}

func TestAcceptance_Pro_MdmUpdates_CreatePlanV1(t *testing.T) {
	t.Skip("creating a managed software update plan initiates an MDM-driven update on real devices — manual curl only")
}

func TestAcceptance_Pro_MdmUpdates_CreateGroupPlanV1(t *testing.T) {
	t.Skip("creating a group plan initiates MDM-driven updates on every device in the group — manual curl only")
}

// TestAcceptance_Pro_MdmUpdates_ListUpdateStatusesV1 is read-only.
func TestAcceptance_Pro_MdmUpdates_ListUpdateStatusesV1(t *testing.T) {
	c := accClient(t)

	statuses, err := pro.New(c).ListManagedSoftwareUpdateStatusesV1(context.Background(), "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListManagedSoftwareUpdateStatusesV1: %v", err)
	}
	t.Logf("Update statuses: %d", len(statuses.Results))
}

// TestAcceptance_Pro_MdmUpdates_UpdateStatusesForRealComputer exercises
// the per-computer status endpoint against the first computer the tenant
// has. Skips if empty.
func TestAcceptance_Pro_MdmUpdates_UpdateStatusesForRealComputer(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	computers, err := p.ListComputersInventoryV4(ctx, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputersInventoryV4: %v", err)
	}
	if len(computers) == 0 {
		t.Skip("tenant has no computers")
	}
	id := computers[0].ID

	if _, err := p.GetManagedSoftwareUpdateStatusesForComputerV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetManagedSoftwareUpdateStatusesForComputerV1(%s): 404 — no status, plumbing OK", id)
			return
		}
		t.Fatalf("GetManagedSoftwareUpdateStatusesForComputerV1(%s): %v", id, err)
	}
}

// --- mdm commands ------------------------------------------------------

// TestAcceptance_Pro_MdmUpdates_ListMdmCommandsV2 — server requires a
// filter (400s with empty filter) despite the spec marking it optional.
// Style-guide violation; pass status==Pending as the default probe.
func TestAcceptance_Pro_MdmUpdates_ListMdmCommandsV2(t *testing.T) {
	c := accClient(t)

	cmds, err := pro.New(c).ListMdmCommandsV2(context.Background(), nil, "status==Pending")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListMdmCommandsV2: %v", err)
	}
	t.Logf("Recent MDM commands (status==Pending): %d", len(cmds))
}

// TestAcceptance_Pro_MdmUpdates_ListMdmCommandsV1 covers the operation v2121
// restored (upstream restored GET /v1/mdm/commands; it had been
// withdrawn at v1942 with /v2 named as successor). The v1 surface is a
// two-parameter point lookup rather than a paginated list, so it is not
// reachable through the v2 method and needs its own coverage.
//
// Three wire laws pinned here, all probed 2026-09-09 with a known-good
// control in the same invocation and each repeated:
//
//   - neither parameter is 400 with an empty errors array (no attribution),
//     although the spec marks both optional;
//   - both parameters together is 500, deterministic 2/2, although the spec
//     says "choose one of two parameters, but not both" — which should be a
//     400. That is a server defect, reported upstream, and asserted rather
//     than skipped so this test fails the day it is fixed;
//   - more than 40 uuids is 414 INVALID_SIZE, as the spec declares.
//
// The happy path takes a real uuid and managementId from the v2 list, so it
// provisions nothing.
func TestAcceptance_Pro_MdmUpdates_ListMdmCommandsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// v2 requires a filter; status==Pending may legitimately be empty, so fall
	// back to active==true before giving up on a fixture.
	var seed *pro.MDMCommand
	for _, filter := range []string{"status==Pending", "active==true"} {
		cmds, err := p.ListMdmCommandsV2(ctx, nil, filter)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListMdmCommandsV2(%s): %v", filter, err)
		}
		if len(cmds) > 0 {
			seed = &cmds[0]
			break
		}
	}
	if seed == nil {
		t.Skip("tenant has no MDM commands to look up — the v1 surface is a point lookup with no list form")
	}

	uuid := seed.UUID
	if uuid == "" {
		t.Fatalf("ListMdmCommandsV2 returned a command with no uuid: %+v", seed)
	}

	got, err := p.ListMdmCommandsV1(ctx, []string{uuid}, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListMdmCommandsV1(uuids=%s): %v", uuid, err)
	}
	if len(got) != 1 || got[0].UUID != uuid {
		t.Fatalf("ListMdmCommandsV1(uuids=%s) = %d commands, want exactly the one requested", uuid, len(got))
	}

	if seed.Client != nil && seed.Client.ManagementID != "" {
		mgmtID := seed.Client.ManagementID
		byClient, err := p.ListMdmCommandsV1(ctx, nil, mgmtID)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListMdmCommandsV1(client-management-id=%s): %v", mgmtID, err)
		}
		if len(byClient) == 0 {
			t.Fatalf("ListMdmCommandsV1(client-management-id=%s) returned nothing, but the command it came from names that client", mgmtID)
		}
		t.Logf("ListMdmCommandsV1(client-management-id=%s): %d commands", mgmtID, len(byClient))
	}

	// Neither parameter: 400, no attribution.
	_, err = p.ListMdmCommandsV1(ctx, nil, "")
	assertMdmCommandsV1Status(t, err, 400, "neither parameter")

	// Both parameters: 500 rather than the 400 the spec's own wording implies.
	// A limitation, not a fact about the SDK — flip this assertion when it is
	// fixed upstream.
	if seed.Client != nil && seed.Client.ManagementID != "" {
		_, err = p.ListMdmCommandsV1(ctx, []string{uuid}, seed.Client.ManagementID)
		assertMdmCommandsV1Status(t, err, 500, "both parameters (spec says choose one; server 500s instead of 400)")
	}

	// More than 40 uuids: 414 INVALID_SIZE.
	tooMany := make([]string, 41)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
	}
	_, err = p.ListMdmCommandsV1(ctx, tooMany, "")
	assertMdmCommandsV1Status(t, err, 414, "41 uuids")
}

// assertMdmCommandsV1Status fails unless err is an APIResponseError carrying
// want. A nil error is a failure too: every case this guards is one the server
// refuses today, so success means the wire law changed.
func assertMdmCommandsV1Status(t *testing.T, err error, want int, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("ListMdmCommandsV1 (%s): want HTTP %d, got success — the wire law changed, update this assertion", label, want)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ListMdmCommandsV1 (%s): want HTTP %d, got non-API error: %v", label, want, err)
	}
	if !apiErr.HasStatus(want) {
		t.Fatalf("ListMdmCommandsV1 (%s): want HTTP %d, got %v", label, want, err)
	}
	t.Logf("ListMdmCommandsV1 (%s): HTTP %d as expected", label, want)
}

// POST /api/pro/v2/mdm/commands is no longer covered. Jamf withdrew it from
// the published spec in the GA cleanup (upstream's spec change; the GET is
// retained), so the SDK no longer generates SendMdmCommandV2 and the request
// types MDMCommandRequest / MDMCommandClientRequest are gone with it.
//
// Three tests went with it, and one is a real loss worth restoring if a
// successor appears: CommandTypeEnumMatchesServer used the server's own type
// resolution failure ("known type ids = [A, B, …]") as an oracle for the
// MDMCommandType enum, which is the only wire confirmation that the generated
// constants match what the server accepts. MDMCommandType itself survives, from
// MDMCommand on the read side, so the enum is still emitted — just unverified.
// The other two (SendMdmCommandV2, EnhancedLogCollectionCommands) only probed
// the withdrawn path.

func TestAcceptance_Pro_MdmUpdates_SendMdmBlankPushV2(t *testing.T) {
	t.Skip("blank push to a real enrolled device — manual curl only")
}

func TestAcceptance_Pro_MdmUpdates_RenewMdmProfileV1(t *testing.T) {
	t.Skip("initiates MDM profile renewal on real enrolled devices — manual curl only")
}

func TestAcceptance_Pro_MdmUpdates_DeployPackageV1(t *testing.T) {
	t.Skip("installs a package on real managed computers — manual curl only")
}

// --- mdm-renewal -------------------------------------------------------

// mdm-renewal operates on real clientManagementIds (UUIDs) and rewriting
// those strategies pokes MDM state. All writes are SKIPd; reads use a
// syntactic UUID and tolerate the 404.

func TestAcceptance_Pro_MdmUpdates_GetMdmRenewalStrategiesV1(t *testing.T) {
	c := accClient(t)

	probeID := "00000000-0000-0000-0000-000000000000"
	_, err := pro.New(c).GetMdmRenewalStrategiesV1(context.Background(), probeID)
	if err == nil {
		t.Logf("GetMdmRenewalStrategiesV1(%s): unexpectedly succeeded", probeID)
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("GetMdmRenewalStrategiesV1(%s): %d — no such client, plumbing OK", probeID, apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("GetMdmRenewalStrategiesV1(%s): %v", probeID, err)
}

func TestAcceptance_Pro_MdmUpdates_GetMdmRenewalDeviceCommonDetailsV1(t *testing.T) {
	c := accClient(t)

	probeID := "00000000-0000-0000-0000-000000000000"
	_, err := pro.New(c).GetMdmRenewalDeviceCommonDetailsV1(context.Background(), probeID)
	if err == nil {
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("GetMdmRenewalDeviceCommonDetailsV1(%s): %d — no such client, plumbing OK", probeID, apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("GetMdmRenewalDeviceCommonDetailsV1(%s): %v", probeID, err)
}

func TestAcceptance_Pro_MdmUpdates_UpdateMdmRenewalDeviceCommonDetailsV1(t *testing.T) {
	t.Skip("mutates real MDM client renewal state — manual curl only")
}

func TestAcceptance_Pro_MdmUpdates_DeleteMdmRenewalStrategiesV1(t *testing.T) {
	t.Skip("clears renewal strategies for a real client — manual curl only")
}

// --- return-to-service -------------------------------------------------

// Return-to-service is a stored configuration (assigned to prestages,
// applied during iOS/iPadOS re-enrollment). Full CRUD is safe.

func TestAcceptance_Pro_MdmUpdates_ListReturnToServiceV1(t *testing.T) {
	c := accClient(t)

	res, err := pro.New(c).ListReturnToServiceConfigurationsV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListReturnToServiceConfigurationsV1: %v", err)
	}
	t.Logf("Return-to-service configurations: %d", res.TotalCount)
}

// TestAcceptance_Pro_MdmUpdates_ReturnToServiceCRUDV1 needs a valid
// wifiProfileId. The test creates a throwaway Wi-Fi mobile-device
// configuration profile via the Classic API, uses its id for the RTS
// CRUD cycle, then tears both down.
//
// The embedded .mobileconfig plist is a minimal unsigned Wi-Fi payload
// with a fake SSID; it's valid enough for Jamf Pro to store but will
// never be served to a real device. Override with an existing profile
// via JAMFPLATFORM_ACC_PRO_WIFI_PROFILE_ID to skip the Classic-side fixture.
func TestAcceptance_Pro_MdmUpdates_ReturnToServiceCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	wifiProfileID := accEnv("JAMFPLATFORM_ACC_PRO_WIFI_PROFILE_ID")
	if wifiProfileID == "" {
		wifiProfileID = createWifiProfileFixture(t)
	}

	name := "sdk-acc-rts-" + runSuffix()
	created, err := p.CreateReturnToServiceConfigurationV1(ctx, &pro.ReturnToServiceConfigurationRequest{
		DisplayName:   &name,
		WifiProfileID: &wifiProfileID,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateReturnToServiceConfigurationV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateReturnToServiceConfigurationV1 returned no ID")
	}
	cleanupDelete(t, "DeleteReturnToServiceConfigurationV1", func() error { return p.DeleteReturnToServiceConfigurationV1(ctx, created.ID) })
	t.Logf("Created return-to-service %s", created.ID)

	got, err := p.GetReturnToServiceConfigurationV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetReturnToServiceConfigurationV1(%s): %v", created.ID, err)
	}
	if got.DisplayName != name {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, name)
	}

	renamed := name + "-updated"
	updated, err := p.UpdateReturnToServiceConfigurationV1(ctx, created.ID, &pro.ReturnToServiceConfigurationRequest{
		DisplayName:   &renamed,
		WifiProfileID: &wifiProfileID,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateReturnToServiceConfigurationV1(%s): %v", created.ID, err)
	}
	if updated.DisplayName != renamed {
		t.Errorf("UpdateReturnToServiceConfigurationV1 DisplayName = %q, want %q", updated.DisplayName, renamed)
	}

	if err := p.DeleteReturnToServiceConfigurationV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteReturnToServiceConfigurationV1(%s): %v", created.ID, err)
	}

	_, err = p.GetReturnToServiceConfigurationV1(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetReturnToServiceConfigurationV1(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetReturnToServiceConfigurationV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

// --- destructive command probes (bogus ids / fake codes) ---------------

// TestAcceptance_Pro_MdmUpdates_DestructiveProbesV1 exercises three
// device-altering command endpoints with clearly-bogus inputs: enrollment
// profile download, management framework redeploy, app-config reinstall.
// Each real-id call would mutate device state; bogus ids confirm the
// transport path without touching any device.
func TestAcceptance_Pro_MdmUpdates_DestructiveProbesV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	tolerate := func(label string, err error) {
		t.Helper()
		if err == nil {
			t.Logf("%s: unexpectedly succeeded", label)
			return
		}
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("%s: status=%d — expected rejection", label, apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("%s: %v", label, err)
	}

	_, err := p.DownloadMobileDeviceEnrollmentProfileV1(ctx, "-1")
	tolerate("DownloadMobileDeviceEnrollmentProfileV1", err)

	_, err = p.RedeployJamfManagementFrameworkV1(ctx, "-1")
	tolerate("RedeployJamfManagementFrameworkV1", err)

	// Reinstall-app-config with a bogus code returns 500 with message
	// "There was an error while trying to issue App Config re-install
	// MDM Command" — the server upgrades a validation error into an
	// internal error instead of 4xx. Treat 500 as expected here rather
	// than glossing over it via skipOnServerError.
	fakeCode := "sdk-acc-fake-reinstall-code"
	if err := p.ReinstallMobileDeviceAppConfigV1(ctx, &pro.AppConfigReinstallCode{
		ReinstallCode: &fakeCode,
	}); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 400 || apiErr.StatusCode == 500) {
			t.Logf("ReinstallMobileDeviceAppConfigV1: status=%d — expected rejection of fake code (server mislabels as 500 instead of 4xx)", apiErr.StatusCode)
		} else {
			t.Fatalf("ReinstallMobileDeviceAppConfigV1: unexpected error: %v", err)
		}
	}
}
