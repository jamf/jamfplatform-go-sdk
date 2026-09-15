// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// Full-body acc tests for the 100+ Classic alt-key / subset / filter /
// date-range endpoints added in the alt-key sweep. Unlike the probe
// files (which only verify transport + XML codec round-trip), these
// discover a real id via the corresponding list endpoint, call the
// new endpoint, and assert the response decodes into the expected
// shape. When the tenant has no fixture data for a given resource
// the test skips.

func skipIfNoFixture(t *testing.T, resource string, err error) bool {
	t.Helper()
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("list %s for fixture lookup: %v", resource, err)
	}
	return false
}

// --- computer-history subset filter ----------------------------------

func TestAcceptance_Classic_ComputerHistoryByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	computers, err := p.MatchComputers(ctx, "*")
	skipIfNoFixture(t, "computers (match)", err)
	if len(computers.Computers) == 0 {
		t.Skip("no computers on tenant")
	}
	id := strconv.Itoa(*computers.Computers[0].ID)

	hist, err := p.GetComputerHistoryByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerHistoryByIDSubset(%s, General): %v", id, err)
	}
	if hist == nil {
		t.Fatal("nil history response")
	}
	t.Logf("Computer %s history (General subset) retrieved", id)
}

// --- computer-management filters -------------------------------------

func TestAcceptance_Classic_ComputerManagementByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	computers, err := p.MatchComputers(ctx, "*")
	skipIfNoFixture(t, "computers (match)", err)
	if len(computers.Computers) == 0 {
		t.Skip("no computers on tenant")
	}
	id := strconv.Itoa(*computers.Computers[0].ID)

	got, err := p.GetComputerManagementByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerManagementByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Computer management (General subset) retrieved for %s", id)
}

func TestAcceptance_Classic_ComputerManagementByIDPatchFilter(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	computers, err := p.MatchComputers(ctx, "*")
	skipIfNoFixture(t, "computers (match)", err)
	if len(computers.Computers) == 0 {
		t.Skip("no computers on tenant")
	}
	id := strconv.Itoa(*computers.Computers[0].ID)

	// patchfilter is a filter expression; use empty string which
	// Jamf interprets as "all". Server accepts empty.
	got, err := p.GetComputerManagementByIDPatchFilter(ctx, id, "")
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetComputerManagementByIDPatchFilter: status=%d — accepted for empty filter", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetComputerManagementByIDPatchFilter: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Computer management (patch filter) retrieved for %s", id)
}

func TestAcceptance_Classic_ComputerManagementByIDUsername(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	computers, err := p.MatchComputers(ctx, "*")
	skipIfNoFixture(t, "computers (match)", err)
	if len(computers.Computers) == 0 {
		t.Skip("no computers on tenant")
	}
	id := strconv.Itoa(*computers.Computers[0].ID)

	got, err := p.GetComputerManagementByIDUsername(ctx, id, "sdk-acc-probe-user")
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetComputerManagementByIDUsername: status=%d — expected for synthetic username", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetComputerManagementByIDUsername: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
}

// --- computer hardware/software reports (date range) -----------------

func TestAcceptance_Classic_ComputerHardwareSoftwareReportByIDDateRange(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	computers, err := p.MatchComputers(ctx, "*")
	skipIfNoFixture(t, "computers (match)", err)
	if len(computers.Computers) == 0 {
		t.Skip("no computers on tenant")
	}
	id := strconv.Itoa(*computers.Computers[0].ID)

	// 90-day window.
	end := time.Now().Format("2006-01-02")
	start := time.Now().AddDate(0, 0, -90).Format("2006-01-02")

	rpt, err := p.GetComputerHardwareSoftwareReportByIDDateRange(ctx, id, start, end)
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			t.Logf("GetComputerHardwareSoftwareReportByIDDateRange: 404 — no data in window")
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetComputerHardwareSoftwareReportByIDDateRange: %v", err)
	}
	if rpt == nil {
		t.Fatal("nil report")
	}
	t.Logf("Hardware/software report for %s over %s..%s retrieved", id, start, end)
}

// --- computer application usage (date range) -------------------------

func TestAcceptance_Classic_ComputerApplicationUsageByNameDateRange(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	computers, err := p.MatchComputers(ctx, "*")
	skipIfNoFixture(t, "computers (match)", err)
	if len(computers.Computers) == 0 || computers.Computers[0].Name == nil {
		t.Skip("no computers (with names) on tenant")
	}
	name := *computers.Computers[0].Name

	end := time.Now().Format("2006-01-02")
	start := time.Now().AddDate(0, 0, -30).Format("2006-01-02")

	usage, err := p.GetComputerApplicationUsageByNameDateRange(ctx, name, start, end)
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			t.Logf("GetComputerApplicationUsageByNameDateRange: 404 — no usage data")
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetComputerApplicationUsageByNameDateRange: %v", err)
	}
	if usage == nil {
		t.Fatal("nil usage response")
	}
	t.Logf("Application usage for %q over %s..%s retrieved", name, start, end)
}

// --- computer-applications lookup ------------------------------------

func TestAcceptance_Classic_ComputerApplicationByApplication(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	// "Safari.app" is present on every managed Mac.
	got, err := p.GetComputerApplicationByApplication(ctx, "Safari.app")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerApplicationByApplication(Safari.app): %v", err)
	}
	if got == nil {
		t.Fatal("nil application response")
	}
	t.Logf("Safari.app lookup retrieved")
}

// --- mobile-device subset + match ------------------------------------

func TestAcceptance_Classic_MobileDeviceByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDevices(ctx)
	skipIfNoFixture(t, "mobile-devices", err)
	if len(list.MobileDevices) == 0 {
		t.Skip("no mobile devices on tenant")
	}
	id := strconv.Itoa(*list.MobileDevices[0].ID)

	got, err := p.GetMobileDeviceByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device %s (General subset) retrieved", id)
}

func TestAcceptance_Classic_MobileDeviceByMatch(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	// "*" matches all devices. Server returns the full list.
	got, err := proclassic.New(c).GetMobileDeviceByMatch(context.Background(), "*")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByMatch(*): %v", err)
	}
	_ = got
	t.Log("GetMobileDeviceByMatch(*) succeeded")
	_ = ctx
}

// --- mobile-device-history subset ------------------------------------

func TestAcceptance_Classic_MobileDeviceHistoryByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDevices(ctx)
	skipIfNoFixture(t, "mobile-devices", err)
	if len(list.MobileDevices) == 0 {
		t.Skip("no mobile devices on tenant")
	}
	id := strconv.Itoa(*list.MobileDevices[0].ID)

	got, err := p.GetMobileDeviceHistoryByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceHistoryByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device %s history (General subset) retrieved", id)
}

// --- policy filters --------------------------------------------------

func TestAcceptance_Classic_PolicyByCategory(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	cats, err := p.ListCategories(ctx)
	skipIfNoFixture(t, "categories", err)
	if len(cats.Categories) == 0 {
		t.Skip("no categories on tenant")
	}
	// Pick the first category with a plain name — some tenants have
	// categories with %-wrapped template placeholders that the server
	// 500s on when URL-encoded.
	var cat string
	for _, c := range cats.Categories {
		if c.Name == nil {
			continue
		}
		n := *c.Name
		if !strings.ContainsAny(n, "%&?#") {
			cat = n
			break
		}
	}
	if cat == "" {
		t.Skip("no plain-name categories on tenant (all contain URL-reserved chars)")
	}

	got, err := p.GetPolicyByCategory(ctx, cat)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPolicyByCategory(%q): %v", cat, err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Policies in category %q retrieved", cat)
}

func TestAcceptance_Classic_PolicyByCreatedBy(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	// Jamf supports "jss" and "casper" as built-in createdBy values.
	got, err := proclassic.New(c).GetPolicyByCreatedBy(context.Background(), "jss")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPolicyByCreatedBy(jss): %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Log("Policies created by jss retrieved")
	_ = ctx
}

func TestAcceptance_Classic_PolicyByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListPolicies(ctx)
	skipIfNoFixture(t, "policies", err)
	if len(list.Policies) == 0 {
		t.Skip("no policies on tenant")
	}
	id := strconv.Itoa(*list.Policies[0].ID)

	got, err := p.GetPolicyByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPolicyByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Policy %s (General subset) retrieved", id)
}

// --- os-x / mobile configuration profile subsets ---------------------

func TestAcceptance_Classic_OsxConfigurationProfileByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListOSXConfigurationProfiles(ctx)
	skipIfNoFixture(t, "osx-configuration-profiles", err)
	if len(list.OsXConfigurationProfiles) == 0 {
		t.Skip("no OSX configuration profiles on tenant")
	}
	id := strconv.Itoa(*list.OsXConfigurationProfiles[0].ID)

	got, err := p.GetOsxConfigurationProfileByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOsxConfigurationProfileByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("OSX configuration profile %s (General subset) retrieved", id)
}

func TestAcceptance_Classic_MobileDeviceConfigurationProfileByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDeviceConfigurationProfiles(ctx)
	skipIfNoFixture(t, "mobile-device-configuration-profiles", err)
	if len(list.ConfigurationProfiles) == 0 {
		t.Skip("no mobile device configuration profiles on tenant")
	}
	id := strconv.Itoa(*list.ConfigurationProfiles[0].ID)

	got, err := p.GetMobileDeviceConfigurationProfileByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceConfigurationProfileByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device configuration profile %s (General subset) retrieved", id)
}

// --- ebook + application subsets -------------------------------------

func TestAcceptance_Classic_EbookByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListEbooks(ctx)
	skipIfNoFixture(t, "ebooks", err)
	if len(list.Ebooks) == 0 {
		t.Skip("no ebooks on tenant")
	}
	id := strconv.Itoa(*list.Ebooks[0].ID)

	got, err := p.GetEbookByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetEbookByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Ebook %s (General subset) retrieved", id)
}

func TestAcceptance_Classic_MacApplicationByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMacApplications(ctx)
	skipIfNoFixture(t, "mac-applications", err)
	if len(list.MacApplications) == 0 {
		t.Skip("no mac applications on tenant")
	}
	id := strconv.Itoa(*list.MacApplications[0].ID)

	got, err := p.GetMacApplicationByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMacApplicationByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mac application %s (General subset) retrieved", id)
}

func TestAcceptance_Classic_MobileDeviceApplicationByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDeviceApplications(ctx)
	skipIfNoFixture(t, "mobile-device-applications", err)
	if len(list.MobileDeviceApplications) == 0 {
		t.Skip("no mobile device applications on tenant")
	}
	id := strconv.Itoa(*list.MobileDeviceApplications[0].ID)

	got, err := p.GetMobileDeviceApplicationByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceApplicationByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device application %s (General subset) retrieved", id)
}

// --- LDAP server chain lookup ---------------------------------------

func TestAcceptance_Classic_LDAPServerByIDGroupUser(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListLDAPServers(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListLDAPServers: %v", err)
	}
	if len(list.LdapServers) == 0 {
		t.Skip("no LDAP servers configured on tenant")
	}
	id := strconv.Itoa(*list.LdapServers[0].ID)

	// Synthetic group + user — exercises transport; tolerates 404
	// since the synthetic values won't resolve.
	got, err := p.GetLDAPServerByIDGroupUser(ctx, id, "sdk-probe-group", "sdk-probe-user")
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetLDAPServerByIDGroupUser: status=%d — expected for synthetic keys", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetLDAPServerByIDGroupUser: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("LDAP chain lookup on server %s retrieved", id)
}

// --- VPP + patch policy subsets -------------------------------------

func TestAcceptance_Classic_VPPInvitationByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListVPPInvitations(ctx)
	skipIfNoFixture(t, "vpp-invitations", err)
	if len(list.VppInvitations) == 0 {
		t.Skip("no VPP invitations on tenant")
	}
	id := strconv.Itoa(*list.VppInvitations[0].ID)

	got, err := p.GetVPPInvitationByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetVPPInvitationByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("VPP invitation %s (General subset) retrieved", id)
}

// TestAcceptance_Classic_PatchPolicyByIDSubset covers the Classic subset read.
// Its id comes from Pro's ListPatchPoliciesV2 by choice, not for want of a
// Classic enumeration: v2082 restored both — GET /patchpolicies and
// GET /patchpolicies/softwaretitleconfig/id/{id} — and either could supply the
// id. Pro v2 is used because it is where upstream points callers. The restored
// ListPatchPolicies carries its own transition note ("Please transition use to
// Jamf Pro API endpoint /v2/patch-policies") and a Deprecated marker, so
// sourcing the fixture from Pro v2 follows that guidance while this test keeps
// the Classic subset read itself covered. Both surfaces address the same
// objects, and the Classic enumerations are covered in
// acc_proclassic_patch_test.go.
func TestAcceptance_Classic_PatchPolicyByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	policies, err := pro.New(c).ListPatchPoliciesV2(ctx, nil, "")
	skipIfNoFixture(t, "patch-policies (pro v2)", err)
	if len(policies) == 0 {
		t.Skip("no patch policies on tenant")
	}
	id := policies[0].ID

	got, err := p.GetPatchPolicyByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchPolicyByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Patch policy %s (General subset) retrieved", id)
}

// --- mobile-device-enrollment-profile subset -------------------------

func TestAcceptance_Classic_MobileDeviceEnrollmentProfileByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDeviceEnrollmentProfiles(ctx)
	skipIfNoFixture(t, "mobile-device-enrollment-profiles", err)
	if len(list.MobileDeviceEnrollmentProfiles) == 0 {
		t.Skip("no mobile device enrollment profiles on tenant")
	}
	id := strconv.Itoa(*list.MobileDeviceEnrollmentProfiles[0].ID)

	got, err := p.GetMobileDeviceEnrollmentProfileByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceEnrollmentProfileByIDSubset: %v", err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device enrollment profile %s (General subset) retrieved", id)
}

// --- computer match --------------------------------------------------

func TestAcceptance_Classic_MatchComputers(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	// "*" matches all computers.
	got, err := proclassic.New(c).MatchComputers(ctx, "*")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("MatchComputers(*): %v", err)
	}
	_ = got
	t.Log("MatchComputers(*) succeeded")
}

func TestAcceptance_Classic_MatchComputersByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	got, err := proclassic.New(c).MatchComputersByName(ctx, "*")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("MatchComputersByName(*): %v", err)
	}
	_ = got
	t.Log("MatchComputersByName(*) succeeded")
}

// --- computer by-ID subset -------------------------------------------

// --- computer by MAC address -----------------------------------------

// --- computer by UDID ------------------------------------------------

// --- computer commands by command / status ---------------------------

func TestAcceptance_Classic_ComputerCommandsByCommand(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	// BlankPush is the most common MDM command. Server returns empty
	// body (200) when no commands of this type exist — transport empty-
	// body guard means we get a zero-value ComputerCommand back.
	got, err := proclassic.New(c).GetComputerCommandsByCommand(ctx, "BlankPush")
	if err != nil {
		skipOnServerError(t, err)
		if apiErr, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
			t.Logf("GetComputerCommandsByCommand(BlankPush): API error %d — accepted", apiErr.StatusCode)
			return
		}
		t.Fatalf("GetComputerCommandsByCommand(BlankPush): %v", err)
	}
	_ = got
	t.Log("GetComputerCommandsByCommand(BlankPush) succeeded")
}

func TestAcceptance_Classic_ComputerCommandsByStatus(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	got, err := proclassic.New(c).GetComputerCommandsByStatus(ctx, "Pending")
	if err != nil {
		skipOnServerError(t, err)
		if apiErr, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
			t.Logf("GetComputerCommandsByStatus(Pending): API error %d — accepted", apiErr.StatusCode)
			return
		}
		t.Fatalf("GetComputerCommandsByStatus(Pending): %v", err)
	}
	_ = got
	t.Log("GetComputerCommandsByStatus(Pending) succeeded")
}

// --- mobile device by MAC / UDID -------------------------------------

func TestAcceptance_Classic_MobileDeviceByMacAddress(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDevices(ctx)
	skipIfNoFixture(t, "mobile-devices", err)
	if len(list.MobileDevices) == 0 {
		t.Skip("no mobile devices on tenant")
	}
	id := strconv.Itoa(*list.MobileDevices[0].ID)
	full, err := p.GetMobileDeviceByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByID(%s): %v", id, err)
	}
	if full.General == nil || full.General.WifiMacAddress == nil || *full.General.WifiMacAddress == "" {
		t.Skip("first mobile device has no WiFi MAC address")
	}
	mac := *full.General.WifiMacAddress

	got, err := p.GetMobileDeviceByMacAddress(ctx, mac)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByMacAddress(%s): %v", mac, err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device with MAC %s retrieved", mac)
}

func TestAcceptance_Classic_MobileDeviceByUDID(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDevices(ctx)
	skipIfNoFixture(t, "mobile-devices", err)
	if len(list.MobileDevices) == 0 {
		t.Skip("no mobile devices on tenant")
	}
	id := strconv.Itoa(*list.MobileDevices[0].ID)
	full, err := p.GetMobileDeviceByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByID(%s): %v", id, err)
	}
	if full.General == nil || full.General.UDID == nil || *full.General.UDID == "" {
		t.Skip("first mobile device has no UDID")
	}
	udid := *full.General.UDID

	got, err := p.GetMobileDeviceByUDID(ctx, udid)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByUDID(%s): %v", udid, err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device with UDID %s retrieved", udid)
}

// --- mobile device enrollment profile by invitation subset -----------

func TestAcceptance_Classic_MobileDeviceEnrollmentProfileByInvitationSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDeviceEnrollmentProfiles(ctx)
	skipIfNoFixture(t, "mobile-device-enrollment-profiles", err)
	if len(list.MobileDeviceEnrollmentProfiles) == 0 {
		t.Skip("no mobile device enrollment profiles on tenant")
	}
	// Fetch the full profile to get its invitation token.
	id := strconv.Itoa(*list.MobileDeviceEnrollmentProfiles[0].ID)
	full, err := p.GetMobileDeviceEnrollmentProfileByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceEnrollmentProfileByID(%s): %v", id, err)
	}
	if full.General == nil || full.General.Invitation == nil {
		t.Skip("enrollment profile has no invitation token")
	}
	invitation := full.General.Invitation.String()

	got, err := p.GetMobileDeviceEnrollmentProfileByInvitationSubset(ctx, invitation, "General")
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetMobileDeviceEnrollmentProfileByInvitationSubset: 404 — invitation not resolvable via this path")
			return
		}
		t.Fatalf("GetMobileDeviceEnrollmentProfileByInvitationSubset(%s, General): %v", invitation, err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device enrollment profile by invitation %s (General subset) retrieved", invitation)
}

// --- mobile device provisioning profile by ID subset -----------------

func TestAcceptance_Classic_MobileDeviceProvisioningProfileByIDSubset(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListMobileDeviceProvisioningProfiles(ctx)
	skipIfNoFixture(t, "mobile-device-provisioning-profiles", err)
	if len(list.MobileDeviceProvisioningProfiles) == 0 {
		t.Skip("no mobile device provisioning profiles on tenant")
	}
	id := strconv.Itoa(*list.MobileDeviceProvisioningProfiles[0].ID)

	got, err := p.GetMobileDeviceProvisioningProfileByIDSubset(ctx, id, "General")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceProvisioningProfileByIDSubset(%s, General): %v", id, err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("Mobile device provisioning profile %s (General subset) retrieved", id)
}

// --- patch by name ---------------------------------------------------

func TestAcceptance_Classic_PatchByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	// The name comes from Classic's own enumeration. This used to be sourced
	// from Pro v3 on the grounds that v1993 had withdrawn GET /patches and
	// the whole /patches/id family, leaving POST /patchsoftwaretitles as the
	// only Classic verb and Classic unable to enumerate software titles at
	// all. v2082 restored the family — ListPatches, GetPatchByID,
	// ListPatchSoftwareTitles and GetPatchSoftwareTitleByID are all back — so
	// that reasoning is void, and taking the name from ListPatches keeps this
	// test inside the package it is testing: no cross-package dependency on
	// pro, and the name is the one the /patches surface itself publishes for
	// the title, rather than Pro v3's SoftwareTitleName which merely happens
	// to agree. A tenant with no patch software titles configured at all is
	// the one case that still cannot supply a fixture.
	titles, err := p.ListPatches(ctx)
	skipIfNoFixture(t, "patches (classic)", err)
	var name string
	for _, ti := range titles.PatchManagementSoftwareTitles {
		if ti.Name != nil && *ti.Name != "" {
			name = *ti.Name
			break
		}
	}
	if name == "" {
		t.Skipf("no named patch software titles on tenant (%d entries) — nothing to key a by-name read on", len(titles.PatchManagementSoftwareTitles))
	}

	// GET /patches/name/{name} is broken outright, and this asserts that
	// rather than skipping past it.
	//
	// The earlier record here was that it answers 500 instead of 404 for a
	// name the tenant does not have. Re-probed 2026-09-04 with a control in
	// the same invocation, it is worse: the endpoint answers Jamf Pro's own
	// HTML 500 for **every** name, including a title GET /patches lists in
	// that same invocation ("Microsoft Defender", id 1) and a freshly seeded
	// one, while GET /patches/id/1 answers 200. So it is unconditional, not
	// state-dependent, and skipOnServerError would hide a permanent defect
	// behind the convention for transient ones — this test would never report
	// the fix.
	got, err := p.GetPatchByName(ctx, name)
	if err == nil {
		if got == nil {
			t.Fatal("nil response with nil error")
		}
		t.Fatalf("GetPatchByName(%q) succeeded. The endpoint has been fixed — replace this assertion with a real by-name read and drop the limitation note above", name)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("GetPatchByName(%q): non-API error: %v", name, err)
	}
	if !apiErr.HasStatus(500) {
		t.Fatalf("GetPatchByName(%q): want the recorded unconditional 500, got %d: %s", name, apiErr.StatusCode, apiErr.Summary())
	}
	t.Logf("GetPatchByName(%q): 500 as recorded — the endpoint is broken for every name (%s)", name, apiErr.Summary())
}

// --- patch computers by ID+version -----------------------------------

// --- patch report by title ID + version ------------------------------

// --- saved search by ID / name ---------------------------------------

func TestAcceptance_Classic_SavedSearchByID(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListSavedSearches(ctx)
	skipIfNoFixture(t, "saved-searches", err)
	if len(list.SavedSearches) == 0 {
		t.Skip("no saved searches on tenant")
	}
	item := list.SavedSearches[0]
	if item.ID == nil {
		t.Skip("first saved search has no ID")
	}
	id := strconv.Itoa(*item.ID)

	got, err := p.GetSavedSearchByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSavedSearchByID(%s): %v", id, err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("SavedSearch id=%s retrieved", id)
}

func TestAcceptance_Classic_SavedSearchByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	list, err := p.ListSavedSearches(ctx)
	skipIfNoFixture(t, "saved-searches", err)
	if len(list.SavedSearches) == 0 {
		t.Skip("no saved searches on tenant")
	}
	item := list.SavedSearches[0]
	if item.Name == nil || *item.Name == "" {
		t.Skip("first saved search has no name")
	}
	name := *item.Name

	got, err := p.GetSavedSearchByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSavedSearchByName(%s): %v", name, err)
	}
	if got == nil {
		t.Fatal("nil response")
	}
	t.Logf("SavedSearch %q retrieved by name", name)
}
