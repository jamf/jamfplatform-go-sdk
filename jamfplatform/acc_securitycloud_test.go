// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/securitycloud"
)

// Jamf Security Cloud acceptance coverage.
//
// Every test here runs against the Security Cloud credential set
// (JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_*), never the Jamf Pro one — the two products have separate
// tenants and separate API clients, and neither credential reaches the other's
// surface (probed 2026-08-17: 403 BAD_PERMISSIONS in both directions).
//
// Two groups of endpoints are deliberately not exercised, each for a reason
// named at the test that skips it: paths the gateway does not route yet, and
// writes that would provision or reconfigure real infrastructure on a shared
// tenant.
//
// Activation profiles arrived in build v1993, which published the Security
// Cloud Enrollment API to external/ for the first time. Their coverage carries
// one deliberate asymmetry, explained at
// TestAcceptance_SecurityCloudActivationProfileLifecycle: deletion is a soft
// delete the read surface does not reflect, so every create this suite makes
// stays in the tenant's list for good and the create count is kept to one.

// jscName namespaces every artefact this suite creates, so a leftover from a
// crashed run is identifiable and safe to remove by hand.
func jscName(kind string) string {
	return "sdk-acc-" + kind + "-" + runSuffix()
}

// jscCleanupDelete registers a safety-net delete that stays quiet when the test
// already deleted the resource on its happy path. cleanupDelete would log the
// resulting 404 as a cleanup failure on every passing run, training the reader
// to ignore exactly the line that reports a real leak.
func jscCleanupDelete(t *testing.T, label string, fn func() error) {
	t.Helper()
	cleanupDelete(t, label, func() error {
		if err := fn(); err != nil && !isSecurityCloudNotFound(err) {
			return err
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// DNS — zones
// ---------------------------------------------------------------------------

// assertCreateHrefEmpty pins a server bug, not a capability: every Security
// Cloud create returns `href` (and a Location header) when the response is
// uncompressed, and returns `"href": null` with no Location header when it is
// gzipped. Wire-verified 2026-08-24, 12/12 deterministic either way — content
// encoding is mutating the response body.
//
// Go's net/http adds `Accept-Encoding: gzip` to every request and transparently
// decompresses, so **no Go caller can ever see href**. That is why the
// long-standing note "the server sends only id, never href" looked true: it was
// only ever observed through this SDK.
//
// So the ID is the only usable member, and this asserts that emptiness on
// purpose. It will fail the day the gateway stops varying the body by encoding,
// which is the signal to flip it into a real href assertion and to drop the
// caveat from CLAUDE.md.
func assertCreateHrefEmpty(t *testing.T, href, what string) {
	t.Helper()
	if href != "" {
		t.Errorf("%s href = %q, want empty: Go always sends Accept-Encoding: gzip and the gateway nulls href on the compressed path. A value here means that bug is fixed — assert the canonical path instead and update CLAUDE.md.", what, href)
	}
}

// jscEnsureGateways returns at least n dedicated ZTNA gateways, creating any
// shortfall itself. Four tests need a real gateway ID and no synthetic value
// works: DNS zones must point their name servers at one, and a grouped gateway
// needs two dedicated members (shared ones are refused with 422
// SHARED_GATEWAY_MEMBER). Those tests used to skip whenever the tenant happened
// to have none, which meant their create paths went unexercised on exactly the
// clean tenant a CI run starts from.
//
// Gateways it creates are `enabled: false` with `dedicatedIps` and no `ipsec`,
// which is the cheapest form the server accepts — no subnets, vendor or PSK —
// and they carry no traffic. Each is registered for deletion via
// jscCleanupDelete, so a run leaves the tenant as it found it.
//
// Creating one still provisions real infrastructure, so it stays behind
// JAMFPLATFORM_ACC_SECURITYCLOUD_GATEWAY_WRITE_OK. The difference from a bare skip is that on
// a tenant reserved for this suite nothing skips, and on a shared tenant the
// skip names the variable that would fix it rather than an absent fixture the
// reader cannot act on. Pre-existing gateways are used as-is and never deleted.
func jscEnsureGateways(t *testing.T, sc *securitycloud.Client, n int) []securitycloud.Gateway {
	t.Helper()
	ctx := context.Background()

	page, err := sc.ListZtnaGatewaysV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListZtnaGatewaysV1 failed: %v", err)
	}
	existing := page.Results
	if len(existing) >= n {
		return existing
	}
	if accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_GATEWAY_WRITE_OK") == "" {
		t.Skipf("need %d dedicated ZTNA gateways, tenant has %d, and creating one provisions real network egress — set JAMFPLATFORM_ACC_SECURITYCLOUD_GATEWAY_WRITE_OK to let this test create and delete its own on a tenant reserved for it", n, len(existing))
	}

	enabled := false
	for len(existing) < n {
		created, err := sc.CreateZtnaGatewayV1(ctx, &securitycloud.GatewayCreateRequest{
			Name:       jscName("fixture-gateway"),
			Datacenter: securitycloud.GatewayCreateRequestDatacenterEuWest2,
			Enabled:    &enabled,
			TenantIds:  jscTenantIDs(t),
			Contact: securitycloud.GatewayContact{
				Email: "sdk-acc@example.invalid",
				Name:  "SDK acceptance fixture",
			},
			DedicatedIps: &securitycloud.DedicatedIps{Enabled: true},
		})
		if err != nil {
			t.Fatalf("creating fixture gateway %d/%d failed: %v", len(existing)+1, n, err)
		}
		id := created.ID
		jscCleanupDelete(t, "fixture gateway "+id, func() error {
			return sc.DeleteZtnaGatewayV1(context.Background(), id)
		})
		// Create answers {id, href} (see the lifecycle test), so the full
		// Gateway has to be read back — callers here want its Name as well as
		// its ID, and the resolver test matches on Name.
		full, err := sc.GetZtnaGatewayV1(ctx, id)
		if err != nil {
			t.Fatalf("GetZtnaGatewayV1(%s) after creating a fixture gateway failed: %v", id, err)
		}
		existing = append(existing, *full)
	}
	return existing
}

func TestAcceptance_SecurityCloudDnsZoneLifecycle(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	// A zone's name servers must point at a real gateway ID; there is no
	// synthetic value the server accepts.
	gateways := jscEnsureGateways(t, sc, 1)

	created, err := sc.CreateDnsZoneV1(ctx, &securitycloud.ZoneWrite{
		Name:        jscName("zone"),
		Domains:     []string{"sdk-acc.invalid"},
		NameServers: []securitycloud.NameServer{{IP: "203.0.113.53", GatewayID: gateways[0].ID}},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDnsZoneV1 failed: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateDnsZoneV1 returned an empty ID")
	}
	assertCreateHrefEmpty(t, created.Href, "dns zone")
	jscCleanupDelete(t, "dns zone "+created.ID, func() error {
		return sc.DeleteDnsZoneV1(context.Background(), created.ID)
	})
	// Wire-verified 2026-08-17: the 201 body's href is null even though the
	// spec marks it required, and the Location header carries a bare ID rather
	// than the documented canonical URL. Only ID is usable.
	t.Logf("created zone id=%s href=%q", created.ID, created.Href)

	got, err := sc.GetDnsZoneV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDnsZoneV1(%s) failed: %v", created.ID, err)
	}
	if got.Name != jscName("zone") {
		t.Errorf("zone name = %q, want %q", got.Name, jscName("zone"))
	}

	renamed := jscName("zone") + "-renamed"
	if err := sc.UpdateDnsZoneV1(ctx, created.ID, &securitycloud.ZonePatch{Name: &renamed}); err != nil {
		t.Fatalf("UpdateDnsZoneV1(%s) failed: %v", created.ID, err)
	}
	got, err = sc.GetDnsZoneV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDnsZoneV1 after patch failed: %v", err)
	}
	if got.Name != renamed {
		t.Errorf("after merge-patch, name = %q, want %q", got.Name, renamed)
	}
	if len(got.Domains) != 1 || got.Domains[0] != "sdk-acc.invalid" {
		t.Errorf("merge-patch of name should leave domains untouched, got %v", got.Domains)
	}

	zones, err := sc.ListDnsZonesV1(ctx, "name:asc")
	if err != nil {
		t.Fatalf("ListDnsZonesV1 failed: %v", err)
	}
	t.Logf("tenant has %d DNS zones (totalCount=%d)", len(zones.Results), zones.TotalCount)
	var found bool
	for _, z := range zones.Results {
		if z.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("created zone %s missing from ListDnsZonesV1", created.ID)
	}

	if err := sc.DeleteDnsZoneV1(ctx, created.ID); err != nil {
		t.Fatalf("DeleteDnsZoneV1(%s) failed: %v", created.ID, err)
	}
	_, err = sc.GetDnsZoneV1(ctx, created.ID)
	if err == nil {
		t.Fatal("GetDnsZoneV1 succeeded after delete")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Errorf("after delete, want 404 APIResponseError, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// DNS — search domain singleton
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudSearchDomain(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	// The search domain is a tenant-wide singleton that affects name
	// resolution for enrolled devices, so the original value is captured and
	// restored rather than left at the probe value. An unset domain answers
	// 404 SEARCH_DOMAIN_NOT_SET, which is state, not failure.
	original, err := sc.GetDnsSearchDomainV1(ctx)
	switch {
	case err == nil:
		t.Logf("tenant search domain is currently %q — it will be restored", original.Suffix)
		defer func() {
			if err := sc.SetDnsSearchDomainV1(context.Background(), original); err != nil {
				t.Errorf("failed to restore the original search domain %q: %v", original.Suffix, err)
			}
		}()
	case isSecurityCloudNotFound(err):
		t.Log("tenant has no search domain configured — it will be cleared again after the test")
		defer func() {
			if err := sc.ClearDnsSearchDomainV1(context.Background()); err != nil {
				t.Errorf("failed to restore the unset search domain: %v", err)
			}
		}()
	default:
		skipOnServerError(t, err)
		t.Fatalf("GetDnsSearchDomainV1 failed: %v", err)
	}

	probe := "sdk-acc.invalid"
	if err := sc.SetDnsSearchDomainV1(ctx, &securitycloud.SearchDomain{Suffix: probe}); err != nil {
		t.Fatalf("SetDnsSearchDomainV1 failed: %v", err)
	}
	got, err := sc.GetDnsSearchDomainV1(ctx)
	if err != nil {
		t.Fatalf("GetDnsSearchDomainV1 after set failed: %v", err)
	}
	if got.Suffix != probe {
		t.Errorf("search domain = %q, want %q", got.Suffix, probe)
	}

	if err := sc.ClearDnsSearchDomainV1(ctx); err != nil {
		t.Fatalf("ClearDnsSearchDomainV1 failed: %v", err)
	}
	if _, err := sc.GetDnsSearchDomainV1(ctx); !isSecurityCloudNotFound(err) {
		t.Errorf("after clear, want 404 SEARCH_DOMAIN_NOT_SET, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// DNS — custom hostname mappings
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudCustomHostnameMappings(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	// PUT replaces the whole list, so the tenant's existing mappings are
	// captured and written back on the way out.
	original, err := sc.GetDnsCustomHostnameMappingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDnsCustomHostnameMappingsV1 failed: %v", err)
	}
	t.Logf("tenant has %d custom hostname mappings", original.TotalCount)
	defer func() {
		restore := original.Results
		if len(restore) == 0 {
			// Clearing is the exact restore of an empty list, and is the only
			// safe way to exercise ClearDnsCustomHostnameMappingsV1 — on a
			// tenant that owns real mappings it would delete them.
			if err := sc.ClearDnsCustomHostnameMappingsV1(context.Background()); err != nil {
				t.Errorf("failed to clear the probe mapping: %v", err)
			}
			return
		}
		if err := sc.ReplaceDnsCustomHostnameMappingsV1(context.Background(), &restore); err != nil {
			t.Errorf("failed to restore the tenant's %d original mappings: %v", len(restore), err)
		}
	}()

	probe := append(append([]securitycloud.Mapping{}, original.Results...), securitycloud.Mapping{
		Hostname: "sdk-acc.invalid",
		ARecords: &[]string{"203.0.113.10"},
	})
	if err := sc.ReplaceDnsCustomHostnameMappingsV1(ctx, &probe); err != nil {
		t.Fatalf("ReplaceDnsCustomHostnameMappingsV1 failed: %v", err)
	}

	got, err := sc.GetDnsCustomHostnameMappingsV1(ctx)
	if err != nil {
		t.Fatalf("GetDnsCustomHostnameMappingsV1 after replace failed: %v", err)
	}
	var found bool
	for _, m := range got.Results {
		if m.Hostname == "sdk-acc.invalid" {
			found = true
			var aRecords []string
			if m.ARecords != nil {
				aRecords = *m.ARecords
			}
			t.Logf("probe mapping round-tripped: aRecords=%v secureDns=%v ztna=%v",
				aRecords, boolValue(m.SecureDns), boolValue(m.Ztna))
		}
	}
	if !found {
		t.Error("probe mapping missing after ReplaceDnsCustomHostnameMappingsV1")
	}
}

// ---------------------------------------------------------------------------
// ZTNA — reads
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudZtnaReads(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	gatewayPage, err := sc.ListZtnaGatewaysV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListZtnaGatewaysV1 failed: %v", err)
	}
	gateways := gatewayPage.Results
	t.Logf("%d ZTNA gateways", len(gateways))
	if len(gateways) > 0 {
		g, err := sc.GetZtnaGatewayV1(ctx, gateways[0].ID)
		if err != nil {
			t.Fatalf("GetZtnaGatewayV1(%s) failed: %v", gateways[0].ID, err)
		}
		t.Logf("gateway %s: name=%q datacenter=%q enabled=%v", g.ID, g.Name, g.Datacenter, g.Enabled)
	}

	shared, err := sc.ListZtnaSharedGatewaysV1(ctx)
	if err != nil {
		t.Fatalf("ListZtnaSharedGatewaysV1 failed: %v", err)
	}
	t.Logf("%d shared gateways (totalCount=%d)", len(shared.Results), shared.TotalCount)

	grouped, err := sc.ListZtnaGroupedGatewaysV1(ctx)
	if err != nil {
		t.Fatalf("ListZtnaGroupedGatewaysV1 failed: %v", err)
	}
	t.Logf("%d grouped gateways", len(grouped.Results))
	if len(grouped.Results) > 0 {
		gg, err := sc.GetZtnaGroupedGatewayV1(ctx, grouped.Results[0].ID)
		if err != nil {
			t.Fatalf("GetZtnaGroupedGatewayV1(%s) failed: %v", grouped.Results[0].ID, err)
		}
		t.Logf("grouped gateway %s: strategy=%s members=%v", gg.ID, gg.RoutingStrategy, gg.GatewayIds)
	}

	apps, err := sc.ListZtnaAppsV1(ctx)
	if err != nil {
		t.Fatalf("ListZtnaAppsV1 failed: %v", err)
	}
	t.Logf("%d ZTNA apps", len(apps))
	if len(apps) > 0 {
		a, err := sc.GetZtnaAppV1(ctx, apps[0].ID)
		if err != nil {
			t.Fatalf("GetZtnaAppV1(%s) failed: %v", apps[0].ID, err)
		}
		t.Logf("app %s: name=%v category=%q hostnames=%v", a.ID, a.Name, a.CategoryName, a.Hostnames)
	}

	predefined, err := sc.ListZtnaPredefinedAppsV1(ctx)
	if err != nil {
		t.Fatalf("ListZtnaPredefinedAppsV1 failed: %v", err)
	}
	t.Logf("%d predefined app templates", len(predefined.Results))
}

// ---------------------------------------------------------------------------
// ZTNA — access policy (app) lifecycle
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudZtnaAppLifecycle(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	// categoryName is validated against the *displayName* of the tenant's own
	// category list, not a fixed enum, so it has to be read first.
	cats, err := sc.ListContentCategoriesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListContentCategoriesV1 failed: %v", err)
	}
	if len(cats.Results) == 0 {
		t.Skip("tenant exposes no content categories — an app needs a categoryName")
	}
	// Prefer the neutral catch-all when the tenant has it; the categories are a
	// content-filtering taxonomy and the first entry alphabetically is "Adult".
	category := cats.Results[0].DisplayName
	for _, c := range cats.Results {
		if c.DisplayName == "Uncategorized" {
			category = c.DisplayName
			break
		}
	}

	name := jscName("app")
	// routing.type DIRECT keeps the policy off any gateway, so creating it
	// changes no network path.
	created, err := sc.CreateZtnaAppV1(ctx, &securitycloud.AppCreateRequest{
		Name:         &name,
		CategoryName: category,
		Hostnames:    &[]string{"sdk-acc-app.invalid"},
		Assignments:  securitycloud.Assignments{Inclusions: securitycloud.AssignmentsInclusions{AllUsers: true}},
		Routing:      securitycloud.Routing{Type: "DIRECT"},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateZtnaAppV1 failed: %v", err)
	}
	jscCleanupDelete(t, "ztna app "+created.ID, func() error {
		return sc.DeleteZtnaAppV1(context.Background(), created.ID)
	})
	// 201 with the spec-declared {id, href}. The server used to answer with the
	// whole App, which config.json encoded as a responseType override; build
	// v1439 brought it in line with the spec and the override is gone.
	if created.ID == "" {
		t.Fatal("CreateZtnaAppV1 returned no ID")
	}
	assertCreateHrefEmpty(t, created.Href, "ztna app")
	t.Logf("created app %s", created.ID)

	renamed := name + "-renamed"
	if err := sc.UpdateZtnaAppV1(ctx, created.ID, &securitycloud.AppPatchRequest{Name: &renamed}); err != nil {
		t.Fatalf("UpdateZtnaAppV1(%s) failed: %v", created.ID, err)
	}
	got, err := sc.GetZtnaAppV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetZtnaAppV1 after patch failed: %v", err)
	}
	// App.name became *string in build v1582: the spec now declares it nullable
	// (and required), matching the wire — a predefined-template app returns
	// name:null. A custom app like this one must still carry a real name, so a
	// nil here is a regression, not the nullable case.
	if got.Name == nil {
		t.Fatal("app name is nil after a name patch; only predefined-template apps return name null")
	}
	if *got.Name != renamed {
		t.Errorf("app name = %q, want %q", *got.Name, renamed)
	}
	if len(got.Hostnames) != 1 || got.Hostnames[0] != "sdk-acc-app.invalid" {
		t.Errorf("merge-patch of name should leave hostnames untouched, got %v", got.Hostnames)
	}

	if err := sc.DeleteZtnaAppV1(ctx, created.ID); err != nil {
		t.Fatalf("DeleteZtnaAppV1(%s) failed: %v", created.ID, err)
	}
	if _, err := sc.GetZtnaAppV1(ctx, created.ID); !isSecurityCloudNotFound(err) {
		t.Errorf("after delete, want 404, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ZTNA — grouped gateway lifecycle
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudZtnaGroupedGatewayLifecycle(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	gateways := jscEnsureGateways(t, sc, 2)

	name := jscName("grouped")
	created, err := sc.CreateZtnaGroupedGatewayV1(ctx, &securitycloud.GroupedGatewayCreateRequest{
		Name:            name,
		GatewayIds:      []string{gateways[0].ID, gateways[1].ID},
		TenantIds:       jscTenantIDs(t),
		RoutingStrategy: "NEAREST",
		// Required on create even for NEAREST, where the server ignores it
		// (spec v1401, wire-confirmed). Leaving the Go zero value in place
		// earns a 400 — see the RecoveryDelay validation test below.
		RecoveryDelayInSec: 3600,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateZtnaGroupedGatewayV1 failed: %v", err)
	}
	jscCleanupDelete(t, "grouped gateway "+created.ID, func() error {
		return sc.DeleteZtnaGroupedGatewayV1(context.Background(), created.ID)
	})
	if created.ID == "" {
		t.Fatal("CreateZtnaGroupedGatewayV1 returned no ID")
	}
	assertCreateHrefEmpty(t, created.Href, "ztna grouped gateway")

	renamed := name + "-renamed"
	if err := sc.UpdateZtnaGroupedGatewayV1(ctx, created.ID, &securitycloud.GroupedGatewayPatchRequest{Name: &renamed}); err != nil {
		t.Fatalf("UpdateZtnaGroupedGatewayV1(%s) failed: %v", created.ID, err)
	}
	got, err := sc.GetZtnaGroupedGatewayV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetZtnaGroupedGatewayV1 after patch failed: %v", err)
	}
	if got.Name != renamed {
		t.Errorf("grouped gateway name = %q, want %q", got.Name, renamed)
	}
	if len(got.GatewayIds) != 2 {
		t.Errorf("merge-patch of name should leave gatewayIds untouched, got %v", got.GatewayIds)
	}

	if err := sc.DeleteZtnaGroupedGatewayV1(ctx, created.ID); err != nil {
		t.Fatalf("DeleteZtnaGroupedGatewayV1(%s) failed: %v", created.ID, err)
	}
}

// jscTenantIDs returns the Security Cloud tenant ID list for a create body,
// skipping the test when there is none to give.
//
// Security Cloud creates take tenantIds, and the ID cannot be derived from the
// client: initJSCAcceptanceClient falls back to the environment credential when
// no dedicated Security Cloud set is configured, and Client.Scope() then reports
// an ENVIRONMENT id — a different identifier space, which the server rejects with
// 400 "No mapping found for one of the supplied ids". Nor is it discoverable:
// probed 2026-09-03, no Security Cloud read exposes a tenant id on this surface
// (shared gateways return only id and name, and every other collection was
// empty).
//
// So the honest answer is to skip. Sending []string{""} — which is what reading
// the unset variable directly did — produces that same 400 at request validation,
// before any of the field constraints a test is actually asserting, so the
// failure reads as a spec/wire disagreement rather than as a missing fixture.
// That is exactly how TestAcceptance_SecurityCloudZtnaGroupedGatewayRecoveryDelay
// failed on 2026-09-03.
//
// # Two dead ends, so nobody retries them
//
// GetCsaTenantIdV1 and GetM2MTenantIDV1 both look like the answer — they return a
// real tenant UUID for the caller's Jamf Pro environment, and tenantIds is
// documented as "validated against the caller's organization". They are not, and
// they are the same dead end twice: probed 2026-09-03, GET /pro/v1/csa/tenant-id
// and GET /pro/v1/m2m/tenant-id return the IDENTICAL value, and a Security Cloud
// credential cannot call either — the pro namespace answers with an HTML page for
// it. Nor is there any securitycloud operation with "tenant" in its path or name;
// the only two types in that package carrying a tenantId are uem-connect's
// ConnectorConfig and JamfProConnectorCreateRequest, where the field is the Jamf
// Pro tenant a connector points AT rather than the Security Cloud tenant itself.
// Probed 2026-09-03 on the
// environment credential's Security Cloud tenant, a grouped-gateway create
// answered 400 "No mapping found for one of the supplied ids" for ALL FOUR
// combinations: CSA id + real shared gateways, bogus id + real gateways, CSA id +
// bogus gateways, and bogus + bogus. Bogus-and-bogus answering the same as
// real-and-real proves the 400 is not about which ids were supplied.
//
// The cause is the tenant, not the id: that Security Cloud tenant is not
// provisioned for ZTNA — its apps, zones and grouped-gateway collections are all
// empty. The control in the same invocation settles it, the JSC sandbox tenant
// with its own credential answering 422 SHARED_GATEWAY_MEMBER, "Grouped Gateway
// members must be dedicated gateways", which is exactly what these tests assert.
//
// So the environment credential covers Security Cloud for 26 of 27 tests — device
// groups, DNS, categories, activation profiles and uem-connect all pass on it —
// and only the ZTNA gateway creates need the dedicated Security Cloud credential
// set. Configure it if that coverage is wanted; do not try to synthesise the
// tenant id.
func jscTenantIDs(t *testing.T) []string {
	t.Helper()

	// Coherence, not just presence. The ID has to be the tenant the CLIENT
	// authenticates as, so reading the variable alone is a footgun: set it
	// without the rest of the Security Cloud credential set and
	// initJSCAcceptanceClient falls back to the environment credential while
	// these bodies carry a different tenant's ID — which is the exact mismatch
	// that produced the original 400. accJSCCredSource records which credential
	// the client was built from, so that combination skips instead.
	id := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID")
	if id == "" {
		t.Skip("needs the JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_* credential set: Security Cloud creates take tenantIds, and the ID is neither derivable from the client nor discoverable from any read on this surface")
	}
	if want := "securitycloud tenant " + id; accJSCCredSource != want {
		t.Skipf("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID is set but the client was built from %q, so the body would name a tenant the credential does not authenticate as; set the whole JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_* set or none of it", accJSCCredSource)
	}
	return []string{id}
}

// TestAcceptance_SecurityCloudZtnaGroupedGatewayRecoveryDelay pins the
// recoveryDelayInSec constraint the ztna spec gained in GitOps build v1401 and
// that was wire-confirmed on 2026-08-21: the field is required on create for
// every routing strategy, and its value must be one of five discrete durations.
//
// It runs unconditionally because it cannot provision anything. gatewayIds are
// deliberately *shared* gateways, which the server refuses with 422
// SHARED_GATEWAY_MEMBER — so the valid-value case proves the request cleared
// field validation without ever creating a grouped gateway. Field validation
// runs before that business rule, which is what makes the whole matrix
// reachable from an empty tenant.
func TestAcceptance_SecurityCloudZtnaGroupedGatewayRecoveryDelay(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	shared, err := sc.ListZtnaSharedGatewaysV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListZtnaSharedGatewaysV1 failed: %v", err)
	}
	if len(shared.Results) < 2 {
		t.Skipf("need 2 shared gateways to build a rejectable member list, tenant has %d", len(shared.Results))
	}
	members := []string{shared.Results[0].ID, shared.Results[1].ID}

	// Resolved once, here, rather than inside req(): req is called from subtest
	// goroutines but closes over the PARENT t, and t.Skip must run on the
	// goroutine of the test it is skipping — otherwise the runtime.Goexit lands
	// in the wrong place and the subtest is recorded as a failure instead.
	tenantIDs := jscTenantIDs(t)

	req := func(delay int) *securitycloud.GroupedGatewayCreateRequest {
		return &securitycloud.GroupedGatewayCreateRequest{
			Name:               jscName("grouped-delay"),
			GatewayIds:         members,
			TenantIds:          tenantIDs,
			RoutingStrategy:    "ACTIVE_STANDBY",
			RecoveryDelayInSec: delay,
		}
	}

	// The Go zero value is not a legal duration. Before v1401 the spec declared
	// `default: 0` and the field was optional, so a caller who left it unset got
	// a working grouped gateway; now they get a 400. Asserted explicitly because
	// that is the whole breaking change.
	t.Run("zero value rejected", func(t *testing.T) {
		_, err := sc.CreateZtnaGroupedGatewayV1(ctx, req(0))
		assertRecoveryDelayRejected(t, err)
	})

	t.Run("non-enum value rejected", func(t *testing.T) {
		_, err := sc.CreateZtnaGroupedGatewayV1(ctx, req(60))
		assertRecoveryDelayRejected(t, err)
	})

	// Every legal duration must clear field validation and fall through to the
	// shared-member business rule. A 400 here means the server's accepted set
	// has drifted from the spec's enum.
	for _, delay := range []int{300, 1800, 3600, 10800, 28800} {
		t.Run(fmt.Sprintf("%d accepted", delay), func(t *testing.T) {
			created, err := sc.CreateZtnaGroupedGatewayV1(ctx, req(delay))
			if err == nil {
				jscCleanupDelete(t, "grouped gateway "+created.ID, func() error {
					return sc.DeleteZtnaGroupedGatewayV1(context.Background(), created.ID)
				})
				t.Fatalf("create with shared gateway members unexpectedly succeeded (id %s) — SHARED_GATEWAY_MEMBER is no longer enforced, so this test can now provision real grouped gateways and must be redesigned", created.ID)
			}
			apiErr := jamfplatform.AsAPIError(err)
			if apiErr == nil || !apiErr.HasStatus(422) {
				t.Fatalf("recoveryDelayInSec=%d: want 422 SHARED_GATEWAY_MEMBER (field validation passed), got %v", delay, err)
			}
			if fields := apiErr.FieldErrors(); len(fields["recoveryDelayInSec"]) > 0 {
				t.Errorf("recoveryDelayInSec=%d rejected on the field itself: %v", delay, fields["recoveryDelayInSec"])
			}
			t.Logf("recoveryDelayInSec=%d -> %s", delay, apiErr.Summary())
		})
	}
}

// assertRecoveryDelayRejected asserts a grouped-gateway create failed on
// recoveryDelayInSec specifically, not on some other field. The distinction
// matters: a 400 attributed elsewhere would mean the probe stopped testing
// what it claims to.
func assertRecoveryDelayRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("CreateZtnaGroupedGatewayV1 unexpectedly succeeded — an illegal recoveryDelayInSec was accepted, and a grouped gateway may exist on the tenant")
	}
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil || !apiErr.HasStatus(400) {
		t.Fatalf("want 400, got %v", err)
	}
	if got := apiErr.FieldErrors()["recoveryDelayInSec"]; len(got) == 0 {
		t.Fatalf("400 did not attribute the failure to recoveryDelayInSec; FieldErrors = %v", apiErr.FieldErrors())
	}
	t.Logf("rejected: %s", apiErr.Summary())
}

// jscGatewayWriteOK gates the two gateway write tests. Creating a gateway
// provisions real network egress — a datacenter allocation, an IPSec tunnel
// endpoint — and deleting one severs traffic for every access policy routed
// through it, so this must never run against a tenant anyone depends on. The
// full lifecycle was wire-verified on the JSC sandbox on 2026-08-20
// (create with ipsec, GET, four PATCHes, delete, 404 after) and the tenant was
// left exactly as found, which is what makes an opt-in test worth having here
// rather than a permanent skip.
func jscGatewayWriteOK(t *testing.T) {
	t.Helper()
	if accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_GATEWAY_WRITE_OK") == "" {
		t.Skip("gated behind JAMFPLATFORM_ACC_SECURITYCLOUD_GATEWAY_WRITE_OK — gateway writes provision real network egress and deleting one severs traffic for every access policy routed through it; opt in only on a tenant reserved for it")
	}
}

// TestAcceptance_SecurityCloudZtnaGatewayLifecycle exercises the gateway
// create/get/patch/delete cycle with an IPSec configuration, which is the only
// way to reach ConnectionConfigLeftResponse / ConnectionConfigRightResponse —
// no gateway carries an ipsec block unless this test made it.
//
// The gateway is created with enabled:false so it never carries traffic, and
// its subnets sit in documentation ranges.
func TestAcceptance_SecurityCloudZtnaGatewayLifecycle(t *testing.T) {
	jscGatewayWriteOK(t)
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	// lifetimeInSec became int64 in build v1424 (the spec added format: int64).
	suite := func(lifetime int64) securitycloud.CipherSuiteConfig {
		return securitycloud.CipherSuiteConfig{
			Encryption:    []string{"aes256"},
			Integrity:     []string{"sha256"},
			DhGroups:      []string{"modp2048"},
			LifetimeInSec: lifetime,
		}
	}

	name := jscName("gateway")
	enabled := false
	created, err := sc.CreateZtnaGatewayV1(ctx, &securitycloud.GatewayCreateRequest{
		Name:       name,
		Datacenter: securitycloud.GatewayCreateRequestDatacenterEuWest2,
		Enabled:    &enabled,
		TenantIds:  jscTenantIDs(t),
		Contact: securitycloud.GatewayContact{
			Email: "sdk-acc@example.invalid",
			Name:  "SDK acceptance",
		},
		Ipsec: &securitycloud.GatewayIpSecRequest{
			KeyExchange: "ikev2",
			Ike:         suite(28800),
			Esp:         suite(3600),
			Left: securitycloud.ConnectionConfigLeftRequest{
				ID:      "sdk-acc.example.invalid",
				Host:    "%any",
				Subnets: []string{"10.99.0.0/24"},
				Secret:  &[]string{"SdkAcceptancePsk1234"}[0],
			},
			// Two right subnets, deliberately. Build v1424 dropped maxItems:1
			// from right (and only right), and the server agrees — see the
			// cardinality assertions below. Creating with two is what proves the
			// relaxation is real rather than spec-only.
			Right: securitycloud.ConnectionConfigRightRequest{
				ID:      "peer.sdk-acc.example.invalid",
				Host:    "203.0.113.10",
				Subnets: []string{"203.0.113.0/24", "198.51.100.0/24"},
				Vendor:  securitycloud.ConnectionConfigRightRequestVendorCisco,
			},
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateZtnaGatewayV1 failed: %v", err)
	}
	jscCleanupDelete(t, "ztna gateway "+created.ID, func() error {
		return sc.DeleteZtnaGatewayV1(context.Background(), created.ID)
	})

	// The 201 body is the spec-declared CreateResponse. This reverses what the
	// server did until build v1439, when it answered with the whole Gateway —
	// config.json carried a responseType override for that, now removed. The
	// shape change is independent of content encoding (wire-verified on both
	// the gzip and identity paths); only href's population varies, and see
	// assertCreateHrefEmpty for why Go never sees it.
	if created.ID == "" {
		t.Fatal("CreateZtnaGatewayV1 returned an empty ID")
	}
	assertCreateHrefEmpty(t, created.Href, "ztna gateway")

	got, err := sc.GetZtnaGatewayV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetZtnaGatewayV1(%s) failed: %v", created.ID, err)
	}
	if got.Ipsec == nil {
		t.Fatal("GetZtnaGatewayV1 returned no ipsec block")
	}
	// The secret is write-only and must never come back on a read.
	t.Logf("ipsec left:  %+v", got.Ipsec.Left)
	t.Logf("ipsec right: %+v", got.Ipsec.Right)
	if got.Ipsec.Right.Vendor != securitycloud.ConnectionConfigRightResponseVendorCisco {
		t.Errorf("right.vendor = %q, want Cisco", got.Ipsec.Right.Vendor)
	}
	// Both right subnets must survive the round trip. Wire-verified 2026-08-21:
	// right accepts many, left is still capped at one, and build v1424's schema
	// change removed maxItems from right alone — an asymmetry worth pinning
	// because nothing in the spec explains it.
	if len(got.Ipsec.Right.Subnets) != 2 {
		t.Errorf("right.subnets = %v, want both subnets preserved — v1424 dropped maxItems:1 from right, so a single-element result means the server still caps it", got.Ipsec.Right.Subnets)
	}
	if len(got.Ipsec.Left.Subnets) != 1 {
		t.Errorf("left.subnets = %v, want exactly 1", got.Ipsec.Left.Subnets)
	}

	// The other half of the asymmetry: two *left* subnets are refused. This
	// needs an otherwise-valid request, because the deep IPSec check does not
	// run while a required top-level field is missing — which is why it lives
	// here rather than in the ungated rejections test, whose cases all fail
	// earlier. The rejection means nothing is provisioned.
	tooManyLeft := &securitycloud.GatewayCreateRequest{
		Name:       jscName("gateway-leftpair"),
		Datacenter: securitycloud.GatewayCreateRequestDatacenterEuWest2,
		Enabled:    &enabled,
		TenantIds:  jscTenantIDs(t),
		Contact:    securitycloud.GatewayContact{Email: "sdk-acc@example.invalid", Name: "SDK acceptance"},
		Ipsec: &securitycloud.GatewayIpSecRequest{
			KeyExchange: "ikev2",
			Ike:         suite(28800),
			Esp:         suite(3600),
			Left: securitycloud.ConnectionConfigLeftRequest{
				ID:      "sdk-acc.example.invalid",
				Host:    "%any",
				Subnets: []string{"10.99.0.0/24", "10.98.0.0/24"},
				Secret:  &[]string{"SdkAcceptancePsk1234"}[0],
			},
			Right: securitycloud.ConnectionConfigRightRequest{
				ID:      "peer.sdk-acc.example.invalid",
				Host:    "203.0.113.10",
				Subnets: []string{"203.0.113.0/24"},
				Vendor:  securitycloud.ConnectionConfigRightRequestVendorCisco,
			},
		},
	}
	if extra, err := sc.CreateZtnaGatewayV1(ctx, tooManyLeft); err == nil {
		jscCleanupDelete(t, "ztna gateway "+extra.ID, func() error {
			return sc.DeleteZtnaGatewayV1(context.Background(), extra.ID)
		})
		t.Error("two left subnets were accepted — left's maxItems:1 is no longer enforced, so the spec should drop it there too")
	} else if apiErr := jamfplatform.AsAPIError(err); apiErr == nil || !apiErr.HasStatus(400) {
		t.Errorf("two left subnets: want 400, got %v", err)
	} else {
		// Reported against `ipsec`, not `ipsec.left.subnets` — the array-level
		// checks surface at the block, so a caller gets no path to the field.
		t.Logf("two left subnets -> %s", apiErr.Summary())
	}

	// A whole-block ipsec patch still works, and is the shape a caller uses to
	// replace cipher suites and endpoints together. Omitting left.secret
	// preserves the existing PSK — the secret can be rotated, never cleared.
	full := func(espLifetime int64) *securitycloud.GatewayIpSecPatchRequest {
		return &securitycloud.GatewayIpSecPatchRequest{
			KeyExchange: &[]string{"ikev2"}[0],
			Ike:         &[]securitycloud.CipherSuiteConfig{suite(28800)}[0],
			Esp:         &[]securitycloud.CipherSuiteConfig{suite(espLifetime)}[0],
			Left: &securitycloud.ConnectionConfigPatchLeftRequest{
				ID:      &[]string{"sdk-acc.example.invalid"}[0],
				Host:    &[]string{"%any"}[0],
				Subnets: &[]string{"10.99.0.0/24"},
			},
			Right: &securitycloud.ConnectionConfigPatchRightRequest{
				ID:      &[]string{"peer.sdk-acc.example.invalid"}[0],
				Host:    &[]string{"203.0.113.10"}[0],
				Subnets: &[]string{"10.98.0.0/16"},
				Vendor:  &[]string{securitycloud.ConnectionConfigPatchRightRequestVendorCisco}[0],
			},
		}
	}
	if err := sc.UpdateZtnaGatewayV1(ctx, created.ID, &securitycloud.GatewayPatchRequest{
		Ipsec: full(7200),
	}); err != nil {
		t.Fatalf("UpdateZtnaGatewayV1 full ipsec patch failed: %v", err)
	}
	after, err := sc.GetZtnaGatewayV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetZtnaGatewayV1 after patch failed: %v", err)
	}
	if after.Ipsec == nil || after.Ipsec.Esp.LifetimeInSec != 7200 {
		t.Errorf("after ipsec patch, esp.lifetimeInSec = %v, want 7200", after.Ipsec)
	}
	if after.Ipsec.Ike.LifetimeInSec != 28800 {
		t.Errorf("ipsec patch clobbered ike: lifetimeInSec = %d, want 28800", after.Ipsec.Ike.LifetimeInSec)
	}

	// The partial ipsec merge-patch. The server has always deep-merged this
	// correctly (curl-verified 2026-08-20) but it was unreachable from Go until
	// build v1416 gave PATCH its own all-optional GatewayIpSecPatchRequest:
	// GatewayPatchRequest.ipsec previously reused the POST-shaped
	// GatewayIpSecRequest, whose required fields are non-pointer, so a partial
	// value marshalled keyExchange:"" plus empty sub-objects and earned a 400
	// "Request body is missing or malformed." Where an earlier revision of this
	// test pinned that 400 so it would fail the day the shape landed, this
	// asserts the capability instead — esp is replaced and every sibling
	// survives, which is the whole point of the new schema.
	if err := sc.UpdateZtnaGatewayV1(ctx, created.ID, &securitycloud.GatewayPatchRequest{
		Ipsec: &securitycloud.GatewayIpSecPatchRequest{
			Esp: &[]securitycloud.CipherSuiteConfig{suite(3600)}[0],
		},
	}); err != nil {
		t.Fatalf("partial ipsec merge-patch failed — the all-optional PATCH shape landed in v1416 and should make this reachable: %v", err)
	}
	merged, err := sc.GetZtnaGatewayV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetZtnaGatewayV1 after partial patch failed: %v", err)
	}
	if merged.Ipsec == nil {
		t.Fatal("partial ipsec patch dropped the whole ipsec block")
	}
	if merged.Ipsec.Esp.LifetimeInSec != 3600 {
		t.Errorf("partial patch did not apply: esp.lifetimeInSec = %d, want 3600", merged.Ipsec.Esp.LifetimeInSec)
	}
	// Everything not named in the patch must survive the deep merge. These are
	// the assertions that would have caught a server-side replace-not-merge.
	if merged.Ipsec.Ike.LifetimeInSec != 28800 {
		t.Errorf("partial patch clobbered ike: lifetimeInSec = %d, want 28800", merged.Ipsec.Ike.LifetimeInSec)
	}
	if merged.Ipsec.KeyExchange != "ikev2" {
		t.Errorf("partial patch clobbered keyExchange: %q, want ikev2", merged.Ipsec.KeyExchange)
	}
	if merged.Ipsec.Left == nil || merged.Ipsec.Left.ID != "sdk-acc.example.invalid" {
		t.Errorf("partial patch clobbered left: %+v", merged.Ipsec.Left)
	}
	if merged.Ipsec.Right == nil || merged.Ipsec.Right.Vendor != securitycloud.ConnectionConfigRightResponseVendorCisco {
		t.Errorf("partial patch clobbered right: %+v", merged.Ipsec.Right)
	}

	// Rotating the PSK alone is the use case the new PATCH schema exists for —
	// its own description says "supply only `left.secret` to rotate the
	// pre-shared key". The secret is write-only so the new value cannot be read
	// back; what is checkable is that the call is accepted and clobbers nothing,
	// which is what would break if `left` were replaced rather than merged.
	if err := sc.UpdateZtnaGatewayV1(ctx, created.ID, &securitycloud.GatewayPatchRequest{
		Ipsec: &securitycloud.GatewayIpSecPatchRequest{
			Left: &securitycloud.ConnectionConfigPatchLeftRequest{
				Secret: &[]string{"SdkAcceptancePskRotated9876"}[0],
			},
		},
	}); err != nil {
		t.Fatalf("secret-only ipsec rotation failed: %v", err)
	}
	rotated, err := sc.GetZtnaGatewayV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetZtnaGatewayV1 after secret rotation failed: %v", err)
	}
	if rotated.Ipsec == nil || rotated.Ipsec.Left == nil {
		t.Fatal("secret-only rotation dropped the ipsec left block")
	}
	if rotated.Ipsec.Left.ID != "sdk-acc.example.invalid" || len(rotated.Ipsec.Left.Subnets) != 1 {
		t.Errorf("secret-only rotation clobbered the rest of left: %+v", rotated.Ipsec.Left)
	}
	if rotated.Ipsec.Esp.LifetimeInSec != 3600 {
		t.Errorf("secret-only rotation clobbered esp: lifetimeInSec = %d, want 3600", rotated.Ipsec.Esp.LifetimeInSec)
	}

	// A merge-patch that touches nothing but the name leaves ipsec intact.
	if err := sc.UpdateZtnaGatewayV1(ctx, created.ID, &securitycloud.GatewayPatchRequest{
		Name: &[]string{name + "-renamed"}[0],
	}); err != nil {
		t.Fatalf("UpdateZtnaGatewayV1 name-only patch failed: %v", err)
	}
	renamed, err := sc.GetZtnaGatewayV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetZtnaGatewayV1 after name-only patch failed: %v", err)
	}
	if renamed.Ipsec == nil {
		t.Error("a name-only patch dropped the ipsec configuration")
	}

	// Removing ipsec and clearing the PSK are both refused (wire-verified
	// 2026-08-20). IPSEC_REMOVAL_NOT_SUPPORTED is not in the published spec
	// yet; the pending restructure adds it.
	if err := sc.UpdateZtnaGatewayV1(ctx, created.ID, &securitycloud.GatewayPatchRequest{}); err != nil {
		t.Fatalf("empty merge-patch failed: %v", err)
	}

	if err := sc.DeleteZtnaGatewayV1(ctx, created.ID); err != nil {
		t.Fatalf("DeleteZtnaGatewayV1(%s) failed: %v", created.ID, err)
	}
	if _, err := sc.GetZtnaGatewayV1(ctx, created.ID); !isSecurityCloudNotFound(err) {
		t.Errorf("after delete, want 404, got %v", err)
	}
}

// TestAcceptance_SecurityCloudZtnaGatewayIpSecRejections pins the server-side
// IPSec rules that no spec states, all wire-verified 2026-08-20. Every case
// here is a rejection, so nothing is provisioned and the test is safe to run
// ungated — the required top-level fields are deliberately omitted so a
// gateway can never be created even if a rule stops being enforced.
func TestAcceptance_SecurityCloudZtnaGatewayIpSecRejections(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	suite := securitycloud.CipherSuiteConfig{
		Encryption:    []string{"aes256"},
		Integrity:     []string{"sha256"},
		DhGroups:      []string{"modp2048"},
		LifetimeInSec: 3600,
	}
	ipsec := func(leftSubnet, vendor string) *securitycloud.GatewayIpSecRequest {
		return &securitycloud.GatewayIpSecRequest{
			KeyExchange: "ikev2",
			Ike:         suite,
			Esp:         suite,
			Left: securitycloud.ConnectionConfigLeftRequest{
				ID: "sdk-acc.example.invalid", Host: "%any", Subnets: []string{leftSubnet},
			},
			Right: securitycloud.ConnectionConfigRightRequest{
				ID: "peer.sdk-acc.example.invalid", Host: "203.0.113.10",
				Subnets: []string{"10.98.0.0/16"}, Vendor: vendor,
			},
		}
	}

	// No name / datacenter / tenantIds / contact: creation is impossible.
	for _, tc := range []struct {
		name    string
		ipsec   *securitycloud.GatewayIpSecRequest
		wantMsg string
	}{
		{"public left subnet", ipsec("8.8.8.0/24", securitycloud.ConnectionConfigRightRequestVendorCisco), "private range"},
		// Deliberate literals: these are the values a caller would type without
		// the constants, and the point is that the server refuses them.
		{"lowercase vendor", ipsec("10.99.0.0/24", "cisco"), "malformed"},
		{"unknown vendor", ipsec("10.99.0.0/24", "NotAVendor"), "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sc.CreateZtnaGatewayV1(ctx, &securitycloud.GatewayCreateRequest{Ipsec: tc.ipsec})
			if err == nil {
				t.Fatal("CreateZtnaGatewayV1 unexpectedly succeeded — a gateway may have been provisioned, check the tenant")
			}
			apiErr := jamfplatform.AsAPIError(err)
			if apiErr == nil || !apiErr.HasStatus(400) {
				t.Fatalf("want 400, got %v", err)
			}
			if !strings.Contains(strings.ToLower(apiErr.Summary()), tc.wantMsg) {
				t.Errorf("400 summary = %q, want it to mention %q", apiErr.Summary(), tc.wantMsg)
			}
			t.Logf("%s -> %s", tc.name, apiErr.Summary())
		})
	}
}

// ---------------------------------------------------------------------------
// Content categories
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudContentCategories(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	cats, err := sc.ListContentCategoriesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListContentCategoriesV1 failed: %v", err)
	}
	if len(cats.Results) == 0 {
		t.Fatal("ListContentCategoriesV1 returned no categories — the catalogue is a per-tenant system list and should never be empty")
	}
	t.Logf("%d content categories (totalCount=%d), first: id=%s name=%q displayName=%q",
		len(cats.Results), cats.TotalCount, cats.Results[0].ID, cats.Results[0].Name, cats.Results[0].DisplayName)
}

// ---------------------------------------------------------------------------
// Activation profiles (Security Cloud Enrollment API)
// ---------------------------------------------------------------------------

// jscActivationProfileCaps returns a capability set the server accepts.
// networkSecurity and vulnerabilityManagement are coupled — both on or both off
// or the create is refused — which the schema does not say. See the
// PublicApiCapabilities godoc.
func jscActivationProfileCaps(note string) securitycloud.PublicApiCapabilities {
	on := true
	return securitycloud.PublicApiCapabilities{
		NetworkSecurity:         &on,
		VulnerabilityManagement: &on,
		Note:                    &note,
	}
}

// TestAcceptance_SecurityCloudActivationProfileLifecycle covers all six
// operations of the Enrollment API in one pass, and is the only test here that
// creates anything.
//
// That is on purpose. DeleteActivationProfilesV1 is a soft delete and neither
// GetActivationProfileV1 nor ListActivationProfilesV1 reflects it, so a profile
// this suite creates stays in the tenant's list permanently no matter how
// carefully the test cleans up. One create per run is the floor — the create
// path cannot be covered without it — and everything else that could be
// asserted with an extra create is folded into this one body instead.
//
// Two of the three constraints the spec declares but the server does not
// enforce ride along on that single create:
//
//   - platforms carries three entries against a declared maxItems of 2, and
//     succeeds, because the bound is applied after de-duplication.
//   - the capability note is 256 characters against a declared maxLength of
//     255, and succeeds.
//
// Both will fail the day upstream enforces its own schema, which is the signal
// to move the assertion rather than delete it. The third — additionalProperties
// false, also unenforced — is not expressible through a generated struct and
// lives only in the PublicApiCreateActivationProfileRequest godoc.
func TestAcceptance_SecurityCloudActivationProfileLifecycle(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	name := jscName("actprof")
	created, err := sc.CreateActivationProfileV1(ctx, &securitycloud.PublicApiCreateActivationProfileRequest{
		Origin: securitycloud.PublicApiCreateActivationProfileRequestOriginPublicApi,
		Name:   name,
		// Three entries, declared maximum two: de-duplicated to iOS+MAC before
		// the size check runs.
		Platforms: []string{
			securitycloud.PublicApiCreateActivationProfileRequestPlatformsIOS,
			securitycloud.PublicApiCreateActivationProfileRequestPlatformsMac,
			securitycloud.PublicApiCreateActivationProfileRequestPlatformsIOS,
		},
		// 256 characters, declared maximum 255.
		Capabilities: jscActivationProfileCaps(strings.Repeat("z", 256)),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateActivationProfileV1 failed: %v", err)
	}
	if created.Code == "" {
		t.Fatal("CreateActivationProfileV1 returned an empty code")
	}
	code := created.Code
	t.Logf("created activation profile %s (name %q)", code, name)

	// The 201 body is an ActivationProfile — {code} — and not the
	// ActivationProfileResponse {id, href} the spec declares, which is why the
	// operation carries a responseType override. A non-empty code proves the
	// override is still right; if upstream ever starts sending {id, href} this
	// decodes to an empty struct and the check above fails, which is the signal
	// to drop the override rather than work around it. Note this is NOT the
	// href-injection gzip bug assertCreateHrefEmpty pins elsewhere — verified
	// 2026-09-01 with and without Accept-Encoding: gzip, the field is simply
	// never sent.

	// Registered even though the happy path deletes: a mid-test failure should
	// still mark the profile deleted, since a live orphan is scoped to real
	// enrollments.
	jscCleanupDelete(t, "activation profile "+code, func() error {
		return sc.DeleteActivationProfilesV1(context.Background(), &securitycloud.BulkDeleteActivationProfilesRequest{Codes: []string{code}})
	})

	got, err := sc.GetActivationProfileV1(ctx, code)
	if err != nil {
		t.Fatalf("GetActivationProfileV1(%s) failed: %v", code, err)
	}
	if got.Code != code {
		t.Errorf("GetActivationProfileV1 returned code %q, want %q", got.Code, code)
	}
	// The read model is a code and nothing else — no name, capabilities,
	// platforms or state — so nothing the create sent can be verified by
	// reading it back. If a field ever appears here, the create assertions
	// above should start checking it.

	list, err := sc.ListActivationProfilesV1(ctx, securitycloud.PublicApiCreateActivationProfileRequestOriginPublicApi)
	if err != nil {
		t.Fatalf("ListActivationProfilesV1 failed: %v", err)
	}
	if !jscHasActivationProfile(list, code) {
		t.Errorf("created profile %s missing from ListActivationProfilesV1 (%d profiles)", code, len(list.ActivationProfiles))
	}

	// Pause and resume answer 204 and are idempotent — a second pause on an
	// already-paused profile is another 204, not a 409 — and neither has any
	// observable effect, because the read model carries no state. So this
	// covers the call, not the outcome.
	for _, step := range []struct {
		label string
		fn    func(context.Context, string) error
	}{
		{"PauseActivationProfileV1", sc.PauseActivationProfileV1},
		{"PauseActivationProfileV1 (repeat)", sc.PauseActivationProfileV1},
		{"ResumeActivationProfileV1", sc.ResumeActivationProfileV1},
		{"ResumeActivationProfileV1 (repeat)", sc.ResumeActivationProfileV1},
	} {
		if err := step.fn(ctx, code); err != nil {
			t.Fatalf("%s(%s) failed: %v", step.label, code, err)
		}
	}

	if err := sc.DeleteActivationProfilesV1(ctx, &securitycloud.BulkDeleteActivationProfilesRequest{Codes: []string{code}}); err != nil {
		t.Fatalf("DeleteActivationProfilesV1(%s) failed: %v", code, err)
	}

	// The soft delete, pinned in all three of its observable parts. Every
	// assertion below is a limitation, not a capability: each should be
	// inverted the day the server starts reporting deletion on its read
	// surface, and none should be deleted.
	if _, err := sc.GetActivationProfileV1(ctx, code); err != nil {
		t.Errorf("GetActivationProfileV1(%s) after delete = %v, want 200: deletion is a soft delete the item read does not reflect. A 404 here means that changed — invert this assertion and drop the caveat from the ActivationProfile godoc.", code, err)
	}
	after, err := sc.ListActivationProfilesV1(ctx, securitycloud.PublicApiCreateActivationProfileRequestOriginPublicApi)
	if err != nil {
		t.Fatalf("ListActivationProfilesV1 after delete failed: %v", err)
	}
	if !jscHasActivationProfile(after, code) {
		t.Errorf("deleted profile %s absent from ListActivationProfilesV1: the list is expected to keep returning deleted profiles. Its disappearing means the server now filters them — invert this assertion and drop the caveat from the ActivationProfile godoc.", code)
	}
	// A write is the only surface that reveals the deleted state, and it does
	// so in the service's own envelope rather than the ApiError shape the spec
	// declares, so Details() is empty and the body is all there is to match on.
	err = sc.PauseActivationProfileV1(ctx, code)
	var apiErr *jamfplatform.APIResponseError
	switch {
	case err == nil:
		t.Errorf("PauseActivationProfileV1(%s) after delete succeeded, want 409 STATE_CONFLICT", code)
	case !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusConflict):
		t.Errorf("PauseActivationProfileV1(%s) after delete = %v, want 409", code, err)
	case !strings.Contains(apiErr.Body, "already deleted"):
		t.Errorf("PauseActivationProfileV1(%s) after delete body = %q, want it to name the profile as already deleted", code, apiErr.Body)
	}
}

// jscHasActivationProfile reports whether a listing carries the given code.
func jscHasActivationProfile(list *securitycloud.ActivationProfilesResponse, code string) bool {
	for _, p := range list.ActivationProfiles {
		if p.Code == code {
			return true
		}
	}
	return false
}

// TestAcceptance_SecurityCloudActivationProfileRejections pins the Enrollment
// API's validation surface, and provisions nothing: every request is doomed by
// field validation, which runs before anything is created.
//
// Three of these disagree with the spec and are recorded here so the
// disagreement fails loudly if either side moves:
//
//   - an out-of-enum origin is reported as "Origin not provided." even though
//     it was provided, which misstates the cause;
//   - the list operation's invalid-origin error is code INVALID_FIELD with no
//     field attribution, where the spec documents INVALID_PARAMETER on
//     field "origin";
//   - an empty capabilities object is refused in the service's own error
//     envelope rather than the ApiError shape the spec declares for 400, so
//     the SDK parses no structured details out of it at all.
func TestAcceptance_SecurityCloudActivationProfileRejections(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	valid := func() *securitycloud.PublicApiCreateActivationProfileRequest {
		return &securitycloud.PublicApiCreateActivationProfileRequest{
			Origin:       securitycloud.PublicApiCreateActivationProfileRequestOriginPublicApi,
			Name:         jscName("actprof-reject"),
			Platforms:    []string{securitycloud.PublicApiCreateActivationProfileRequestPlatformsIOS},
			Capabilities: jscActivationProfileCaps("rejection probe"),
		}
	}

	creates := []struct {
		name  string
		field string
		want  string
		mutID func(*securitycloud.PublicApiCreateActivationProfileRequest)
	}{
		{
			// Origin is a required non-pointer string, so an empty one is
			// marshaled as "origin": "" rather than omitted — the wire never
			// sees the key absent through this SDK. The absent-key form is a
			// different message ("Missing required attribute origin.",
			// observed by hand with curl on 2026-09-01) and is unreachable
			// here; if this assertion ever starts seeing it, the field gained
			// omitempty and a caller can now send a body the spec forbids.
			name:  "origin empty",
			field: "origin",
			want:  "Origin not provided.",
			mutID: func(r *securitycloud.PublicApiCreateActivationProfileRequest) { r.Origin = "" },
		},
		{
			// Present and out of enum, reported identically to empty. The
			// description misreports the cause in this case — it was provided;
			// asserting the exact string is what makes a fix visible.
			name:  "origin out of enum",
			field: "origin",
			want:  "Origin not provided.",
			mutID: func(r *securitycloud.PublicApiCreateActivationProfileRequest) { r.Origin = "UI" },
		},
		{
			name:  "name blank",
			field: "name",
			want:  "must not be blank",
			mutID: func(r *securitycloud.PublicApiCreateActivationProfileRequest) { r.Name = "" },
		},
		{
			name:  "name over maxLength",
			field: "name",
			want:  "size must be between 0 and 100",
			mutID: func(r *securitycloud.PublicApiCreateActivationProfileRequest) {
				r.Name = strings.Repeat("n", 101)
			},
		},
		{
			name:  "platforms empty",
			field: "platforms",
			want:  "must not be empty",
			mutID: func(r *securitycloud.PublicApiCreateActivationProfileRequest) { r.Platforms = []string{} },
		},
		{
			// Attributed to "platforms[]", brackets included, not "platforms".
			name:  "platforms member out of enum",
			field: "platforms[]",
			want:  "Only 'iOS' or 'MAC' platforms are accepted",
			mutID: func(r *securitycloud.PublicApiCreateActivationProfileRequest) {
				r.Platforms = []string{"ANDROID"}
			},
		},
		{
			// The coupling the schema does not declare.
			name:  "networkSecurity without vulnerabilityManagement",
			field: "capabilities",
			want:  "networkSecurity and vulnerabilityManagement must both be enabled or both disabled",
			mutID: func(r *securitycloud.PublicApiCreateActivationProfileRequest) {
				on := true
				r.Capabilities = securitycloud.PublicApiCapabilities{NetworkSecurity: &on}
			},
		},
	}
	for _, tc := range creates {
		t.Run("create/"+tc.name, func(t *testing.T) {
			req := valid()
			tc.mutID(req)
			_, err := sc.CreateActivationProfileV1(ctx, req)
			jscAssertFieldError(t, err, tc.field, tc.want)
		})
	}

	t.Run("create/capabilities empty", func(t *testing.T) {
		req := valid()
		req.Capabilities = securitycloud.PublicApiCapabilities{}
		_, err := sc.CreateActivationProfileV1(ctx, req)
		// minProperties: 1 is enforced, but as a business rule in the
		// service's own envelope, so there is nothing structured to match.
		jscAssertRawError(t, err, http.StatusBadRequest, "INVALID_INPUT")
	})

	t.Run("delete-multiple/codes empty", func(t *testing.T) {
		err := sc.DeleteActivationProfilesV1(ctx, &securitycloud.BulkDeleteActivationProfilesRequest{Codes: []string{}})
		jscAssertFieldError(t, err, "codes", "size must be between 1 and 100")
	})

	t.Run("delete-multiple/codes over maximum", func(t *testing.T) {
		codes := make([]string, 101)
		for i := range codes {
			codes[i] = fmt.Sprintf("sdk-acc-nosuch-%03d", i)
		}
		err := sc.DeleteActivationProfilesV1(ctx, &securitycloud.BulkDeleteActivationProfilesRequest{Codes: codes})
		jscAssertFieldError(t, err, "codes", "size must be between 1 and 100")
	})

	t.Run("delete-multiple/unknown code is silently skipped", func(t *testing.T) {
		// 204 for a code that does not exist, with no body and so no per-code
		// result. A caller cannot tell a delete that took from one the server
		// skipped — the reason DeleteActivationProfilesV1 returning nil is not
		// evidence of a deletion.
		if err := sc.DeleteActivationProfilesV1(ctx, &securitycloud.BulkDeleteActivationProfilesRequest{
			Codes: []string{"sdk-acc-nosuch-" + runSuffix()},
		}); err != nil {
			t.Errorf("DeleteActivationProfilesV1 on an unknown code = %v, want nil: unknown codes are silently skipped", err)
		}
	})

	t.Run("list/origin out of enum", func(t *testing.T) {
		// Spec documents INVALID_PARAMETER on field "origin"; the server sends
		// INVALID_FIELD with no field at all.
		_, err := sc.ListActivationProfilesV1(ctx, "BOGUS")
		var apiErr *jamfplatform.APIResponseError
		if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusBadRequest) {
			t.Fatalf("ListActivationProfilesV1(BOGUS) = %v, want 400", err)
		}
		details := apiErr.Details()
		if len(details) != 1 {
			t.Fatalf("ListActivationProfilesV1(BOGUS) details = %+v, want exactly one", details)
		}
		if details[0].Code != "INVALID_FIELD" {
			t.Errorf("code = %q, want INVALID_FIELD (the spec documents INVALID_PARAMETER; a change here means upstream aligned one side or the other)", details[0].Code)
		}
		if details[0].Field != "" {
			t.Errorf("field = %q, want empty: the spec documents field \"origin\" and the server attributes nothing. A value here means the server now attributes it.", details[0].Field)
		}
	})

	t.Run("list/origin empty", func(t *testing.T) {
		// origin is required: true, so the generated method sends it
		// unconditionally — `origin=` with an empty value, not a URL with the
		// parameter missing. That distinction is the whole point of the
		// required-param emission: an absent origin is refused in the
		// framework's own envelope ("Required parameter 'origin' is not
		// present.", no ApiError details, nothing naming the caller's
		// mistake), while an empty one reaches the endpoint's own validation
		// and comes back in the declared shape.
		//
		// So this test doubles as the wire-side guard on the zero-value guard
		// being gone: if a future generator change reintroduces it, the error
		// reverts to the raw envelope and the details assertion here fails.
		_, err := sc.ListActivationProfilesV1(ctx, "")
		var apiErr *jamfplatform.APIResponseError
		if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusBadRequest) {
			t.Fatalf("ListActivationProfilesV1(\"\") = %v, want 400", err)
		}
		if strings.Contains(apiErr.Body, "is not present") {
			t.Fatalf("origin was dropped from the URL instead of sent empty — the required-param zero-value guard is back: %q", apiErr.Body)
		}
		details := apiErr.Details()
		if len(details) != 1 || details[0].Code != "INVALID_FIELD" {
			t.Errorf("details = %+v, want one INVALID_FIELD: an empty origin is a value the endpoint rejects, same as an out-of-enum one", details)
		}
	})

	t.Run("get/unknown code", func(t *testing.T) {
		if _, err := sc.GetActivationProfileV1(ctx, "sdk-acc-nosuch-"+runSuffix()); !isSecurityCloudNotFound(err) {
			t.Errorf("GetActivationProfileV1 on an unknown code = %v, want 404", err)
		}
	})

	t.Run("pause/unknown code", func(t *testing.T) {
		if err := sc.PauseActivationProfileV1(ctx, "sdk-acc-nosuch-"+runSuffix()); !isSecurityCloudNotFound(err) {
			t.Errorf("PauseActivationProfileV1 on an unknown code = %v, want 404", err)
		}
	})

	t.Run("resume/unknown code", func(t *testing.T) {
		if err := sc.ResumeActivationProfileV1(ctx, "sdk-acc-nosuch-"+runSuffix()); !isSecurityCloudNotFound(err) {
			t.Errorf("ResumeActivationProfileV1 on an unknown code = %v, want 404", err)
		}
	})
}

// jscAssertFieldError requires a 400 carrying an ApiError detail attributed to
// field with the given description. Asserting the description and not just the
// field is deliberate: several of these strings misstate their own cause, and a
// silent rewording upstream is exactly what this suite should surface.
func jscAssertFieldError(t *testing.T, err error, field, wantDescription string) {
	t.Helper()
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *jamfplatform.APIResponseError with status 400", err)
	}
	if !apiErr.HasStatus(http.StatusBadRequest) {
		t.Fatalf("status = %d, want 400 (body %q)", apiErr.StatusCode, apiErr.Body)
	}
	for _, d := range apiErr.Details() {
		if d.Field == field && d.Description == wantDescription {
			return
		}
	}
	t.Errorf("no detail on field %q describing %q; got %+v", field, wantDescription, apiErr.Details())
}

// jscAssertRawError requires the given status and a substring of the raw body,
// for the responses that arrive in the service's own error envelope rather than
// the ApiError shape the spec declares. Nothing structured is parseable out of
// those, so the body is the only assertion available — and the fact that it is
// the only one available is itself worth pinning.
func jscAssertRawError(t *testing.T, err error, wantStatus int, wantBodySubstring string) {
	t.Helper()
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *jamfplatform.APIResponseError", err)
	}
	if !apiErr.HasStatus(wantStatus) {
		t.Errorf("status = %d, want %d (body %q)", apiErr.StatusCode, wantStatus, apiErr.Body)
	}
	if !strings.Contains(apiErr.Body, wantBodySubstring) {
		t.Errorf("body = %q, want it to contain %q", apiErr.Body, wantBodySubstring)
	}
	if d := apiErr.Details(); len(d) != 0 {
		t.Errorf("details = %+v, want none: this response arrives in the service's own envelope, which the SDK cannot parse. Details appearing means upstream moved it to the declared ApiError shape — assert them instead.", d)
	}
}

// ---------------------------------------------------------------------------
// Device groups
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudDeviceGroupLifecycle(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	created, err := sc.CreateDeviceGroupV1(ctx, &securitycloud.CreateGroupRequest{Name: jscName("group")})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroupV1 failed: %v", err)
	}
	jscCleanupDelete(t, "device group "+created.ID, func() error {
		return sc.DeleteDeviceGroupV1(context.Background(), created.ID)
	})
	if created.ID == "" {
		t.Fatal("CreateDeviceGroupV1 returned an empty ID")
	}

	got, err := sc.GetDeviceGroupV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDeviceGroupV1(%s) failed: %v", created.ID, err)
	}
	if got.Name != jscName("group") {
		t.Errorf("group name = %q, want %q", got.Name, jscName("group"))
	}

	// The list and update steps moved from v1 to v2 rather than being dropped.
	// v2082 withdrew GET /v1/groups and PUT /v1/groups/{groupId} from the
	// published spec, so ListDeviceGroupsV1 and UpdateDeviceGroupV1 no longer
	// exist — the withdrawal was ingestable only because PUT /v2/groups/{id}
	// started working on 2026-09-04, which is what the securitycloud-devices
	// hold had been waiting for. POST /v1/groups and GET/DELETE
	// /v1/groups/{groupId} survive the withdrawal, which is why the rest of
	// this lifecycle is still v1.
	groups, err := sc.ListDeviceGroupsV2(ctx)
	if err != nil {
		t.Fatalf("ListDeviceGroupsV2 failed: %v", err)
	}
	// The item type is deliberately not Group: the implicit "Default Group"
	// entry comes back with no id, so the list schema cannot require one
	// (build v1865).
	var found bool
	for _, g := range groups.Groups {
		if g.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("created group %s missing from ListDeviceGroupsV2 (%d groups)", created.ID, len(groups.Groups))
	}

	// The v2 update answers 204 with no body, where the withdrawn v1 update
	// answered 200 with the updated group. So the rename is asserted by reading
	// it back rather than off the response — which is the stronger assertion
	// anyway, and the one that caught this endpoint being genuinely fixed
	// rather than merely answering 2xx.
	renamed := jscName("group") + "-renamed"
	if err := sc.UpdateDeviceGroupV2(ctx, created.ID, &securitycloud.UpdateGroupRequest{Name: renamed}); err != nil {
		t.Fatalf("UpdateDeviceGroupV2(%s) failed: %v", created.ID, err)
	}
	after, err := sc.GetDeviceGroupV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDeviceGroupV1(%s) after update failed: %v", created.ID, err)
	}
	if after.Name != renamed {
		t.Errorf("after UpdateDeviceGroupV2 the name is %q, want %q — the update answered 204 without applying", after.Name, renamed)
	}

	if err := sc.DeleteDeviceGroupV1(ctx, created.ID); err != nil {
		t.Fatalf("DeleteDeviceGroupV1(%s) failed: %v", created.ID, err)
	}
	if _, err := sc.GetDeviceGroupV1(ctx, created.ID); !isSecurityCloudNotFound(err) {
		t.Errorf("after delete, want 404, got %v", err)
	}
}

// TestAcceptance_SecurityCloudDeviceGroupsV2 no longer tolerates a 403: the
// gateway routes /v2/.../groups as of 2026-08-20 (wire-verified on eu, tenant
// the JSC sandbox, where it returns the {groups: []} envelope). It was 403
// BAD_PERMISSIONS when the surface was first generated, so a 403 resurfacing
// here means routing regressed or the region in use lags eu — either way that
// is worth a failure rather than a skip.
func TestAcceptance_SecurityCloudDeviceGroupsV2(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	groups, err := sc.ListDeviceGroupsV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroupsV2 failed: %v", err)
	}
	t.Logf("v2 returned %d device groups", len(groups.Groups))
}

// TestAcceptance_SecurityCloudUpdateDeviceGroupV2 pins the capability that
// PUT /v2/groups/{groupId} finally has. It has pinned two defects before this,
// and the history is why the assertion is worded the way it is.
//
// The path answered 403 BAD_PERMISSIONS — this namespace's unrouted tell — for
// five weeks, and this test asserted that 403 so the suite would fail when it
// changed. It changed at 12:29Z on 2026-09-03, when the gateway authorization
// rule deployed: the request began clearing authorization and reaching the
// service, which then answered 404 NOT_FOUND for a group that demonstrably
// existed. So the test was rewritten to assert the 404 and wait for a 2xx.
//
// **It got the 2xx on 2026-09-04.** The fix landed between 12:51 and 13:33 BST:
// a probe at 12:51 still got the 404, and one at 13:33 succeeded. Verified 3/3
// by curl with the payloads quoted in WIRE-FACTS, and the assertion below is
// the read-back rather than the status, because a 204 that changes nothing
// would be worse than the 404 it replaced.
//
// That is what lifted the securitycloud-devices hold. The spec had withdrawn
// GET /v1/groups and PUT /v1/groups/{groupId} at v1942, and the SDK held the
// spec at v1897 because the v1 update was the only device-group update that
// worked — withdrawing it would have cost the capability outright. With v2
// working the withdrawal costs nothing, so the spec is ingested at v2082 and
// UpdateDeviceGroupV1 is gone. Note the control this test used to run through
// UpdateDeviceGroupV1 went with it; GET /v2/groups is the control now.
//
// Do not weaken any branch to a skip. A blanket tolerance is what hid this
// class of gap for five weeks, and a regression here has a named owner: a 403
// is authorization (#265 rolled back), a 404 is the service handler, and a 204
// that does not persist is the handler accepting writes it discards.
//
// One caution retained from the original: an early probe returned 500 on both
// v1 and v2, which reads as "v2 is routed and merely faulting" — the opposite
// of the truth. Repeat a routing probe before believing a single result.
func TestAcceptance_SecurityCloudUpdateDeviceGroupV2(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	name := jscName("group-v2put")
	created, err := sc.CreateDeviceGroupV1(ctx, &securitycloud.CreateGroupRequest{Name: name})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroupV1 failed: %v", err)
	}
	jscCleanupDelete(t, "device group "+created.ID, func() error {
		return sc.DeleteDeviceGroupV1(context.Background(), created.ID)
	})

	// Control: the group is visible to the same v2 surface being written
	// through, in the same invocation. This is what made the old 404 a
	// contradiction rather than a plausible answer, and it still rules out a
	// failure below being blamed on the group, the credential or the tenant.
	before, err := sc.ListDeviceGroupsV2(ctx)
	if err != nil {
		t.Fatalf("control ListDeviceGroupsV2 failed, so the result below is not interpretable: %v", err)
	}
	var listed bool
	for _, g := range before.Groups {
		if g.ID == created.ID {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("control: GET /v2/groups does not list %s, so a write failure below would be ambiguous", created.ID)
	}

	renamed := name + "-v2applied"
	if err := sc.UpdateDeviceGroupV2(ctx, created.ID, &securitycloud.UpdateGroupRequest{Name: renamed}); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if !errors.As(err, &apiErr) {
			t.Fatalf("UpdateDeviceGroupV2 failed with a non-API error: %v", err)
		}
		switch {
		case apiErr.HasStatus(403):
			t.Fatalf("UpdateDeviceGroupV2 is back to 403 — the gateway authorization rule appears to have "+
				"been withdrawn or rolled back. That is an authorization regression, not a service defect: %v", err)
		case apiErr.HasStatus(404):
			t.Fatalf("UpdateDeviceGroupV2 is back to 404 for a group GET /v2/groups listed in this same "+
				"invocation — the service handler defect fixed on 2026-09-04 has regressed. The v1 update this "+
				"used to fall back on no longer exists, so device groups now have NO working update: %v", err)
		default:
			t.Fatalf("UpdateDeviceGroupV2 returned %v, want 204. Re-probe with controls in the same invocation "+
				"before adjusting this test.", err)
		}
	}

	// The assertion that matters. A 204 is not evidence the write applied, and
	// an accepted-then-discarded write is the failure mode a status check
	// cannot see.
	after, err := sc.GetDeviceGroupV1(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDeviceGroupV1(%s) after the v2 update failed: %v", created.ID, err)
	}
	if after.Name != renamed {
		t.Fatalf("UpdateDeviceGroupV2 answered 204 but the name is still %q, want %q — the handler is accepting "+
			"writes and discarding them, which is worse than the 404 this test used to assert", after.Name, renamed)
	}
	t.Logf("PUT /v2/groups/%s applied and persisted: name is %q", created.ID, after.Name)
}

// ---------------------------------------------------------------------------
// UEM Connect
// ---------------------------------------------------------------------------

func TestAcceptance_SecurityCloudUemConnectReads(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	connectorPage, err := sc.ListUemConnectorsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Fatalf("403 on uem-connect: uem-connect is a separate capability from device-groups and the JSC "+
				"sandbox has credentials that hold one and not the other — point JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID at "+
				"one granted uem-connect:read (or the legacy read:jsc:all). Underlying error: %v", err)
		}
		t.Fatalf("ListUemConnectorsV1 failed: %v", err)
	}
	connectors := connectorPage.Results
	t.Logf("%d UEM connectors", len(connectors))
	if len(connectors) == 0 {
		t.Skip("tenant has no UEM connector configured")
	}
	id := connectors[0].ID

	got, err := sc.GetUemConnectorV1(ctx, id)
	if err != nil {
		t.Fatalf("GetUemConnectorV1(%s) failed: %v", id, err)
	}
	t.Logf("connector %s: vendor=%s url=%s connected=%v enabled=%v scheduled=%v",
		got.ID, got.Vendor, got.URL, got.Connected, got.Enabled, got.Scheduled)

	settings, err := sc.GetUemConnectorSyncSettingsV1(ctx, id)
	if err != nil {
		t.Fatalf("GetUemConnectorSyncSettingsV1(%s) failed: %v", id, err)
	}
	t.Logf("sync settings: refreshRateMinutes=%d concurrentSyncEnabled=%v deviceRiskTagging=%v",
		settings.RefreshRateMinutes, settings.ConcurrentSyncEnabled, settings.DeviceRiskTagging)

	runs, err := sc.ListUemConnectorSyncRunsV1(ctx, id)
	if err != nil {
		t.Fatalf("ListUemConnectorSyncRunsV1(%s) failed: %v", id, err)
	}
	t.Logf("%d sync runs", len(runs))
	for _, r := range runs {
		t.Logf("  run %s: status=%s type=%s synced=%d errored=%d deleted=%d", r.TransactionID, r.Status, r.RefreshType, r.Synced, r.Errored, r.Deleted)
	}
}

// TestAcceptance_SecurityCloudUemConnectSyncSettingsValidation pins the
// validation ordering on PUT sync-settings, and it needs no fixture at all: it
// works against a `configId` that does not exist.
//
// That is only possible because field validation runs BEFORE resource
// resolution on this operation, which is the fact the test exists to hold in
// place. An earlier record had it the other way round — five bogus-id PUTs were
// observed answering 404 on 2026-09-01 — and if that ever becomes true again,
// every field constraint on SyncSettings stops being probeable without a live
// connector, which is expensive (see the skip below). So both halves are
// asserted here: an out-of-enum value must 422, and the same body with an
// in-enum value must 404. One without the other proves nothing.
func TestAcceptance_SecurityCloudUemConnectSyncSettingsValidation(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	// A body that is otherwise complete: vendor, autoDeviceDeletion and
	// deviceFieldMappings are the three required properties.
	body := func(refresh int64) *securitycloud.SyncSettings {
		return &securitycloud.SyncSettings{
			Vendor:              securitycloud.SyncSettingsVendorJamfPro,
			AutoDeviceDeletion:  securitycloud.SyncSettingsAutoDeviceDeletionDeletedOrRetired,
			DeviceFieldMappings: securitycloud.DeviceFieldMappings{},
			RefreshRateMinutes:  &refresh,
		}
	}

	const bogusID = "bogus000000000000000000"

	// 90 is not in {60, 120, 240, 480, 720, 1440}.
	err := sc.UpdateUemConnectorSyncSettingsV1(ctx, bogusID, body(90))
	if err == nil {
		t.Fatal("UpdateUemConnectorSyncSettingsV1 succeeded against a nonexistent configId — impossible; re-probe before touching this test")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("out-of-enum refreshRateMinutes gave a non-API error: %v", err)
	}
	if apiErr.HasStatus(403) {
		t.Fatalf("403 on uem-connect: this credential holds neither uem-connect:read/update nor the legacy "+
			"update:jsc:all, so it cannot exercise UEM Connect at all. The JSC sandbox has more than one "+
			"credential and they differ in capability — point JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID at one granted "+
			"uem-connect. Underlying error: %v", err)
	}
	if !apiErr.HasStatus(422) {
		t.Fatalf("out-of-enum refreshRateMinutes returned %v, want 422 VALIDATION_FAILED. A 404 here means "+
			"resource resolution has moved ahead of field validation on this PUT, which invalidates the "+
			"doomed-request technique for every SyncSettings constraint — re-probe and record it before "+
			"weakening this assertion", err)
	}
	// The accepted set is leaked in the description and `field` is null, so
	// FieldErrors() gets nothing — assert on the prose deliberately.
	if !strings.Contains(apiErr.Error(), "1440") {
		t.Errorf("422 description does not name the allowed values, want the accepted set leaked in the prose: %v", err)
	}
	t.Logf("out-of-enum refreshRateMinutes on a nonexistent configId: 422, validation precedes resolution (%v)", err)

	// Same body, in-enum value: validation passes, so the lookup runs and fails.
	err = sc.UpdateUemConnectorSyncSettingsV1(ctx, bogusID, body(1440))
	if err == nil {
		t.Fatal("UpdateUemConnectorSyncSettingsV1 succeeded against a nonexistent configId — impossible")
	}
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("in-enum refreshRateMinutes on a nonexistent configId returned %v, want 404 NOT_FOUND. "+
			"Together with the 422 above this is what proves the ordering; a 422 here would mean the body "+
			"is being rejected for some other reason and the first assertion proves nothing", err)
	}
	t.Logf("in-enum refreshRateMinutes on a nonexistent configId: 404, resolution reached (%v)", err)
}

// TestAcceptance_SecurityCloudUemConnectVendorMismatch pins v2005's
// `422 VENDOR_MISMATCH`: the body `vendor` must equal the connector's stored
// vendor, it selects which vendor-specific fields apply rather than which
// connector is updated, and a connector's vendor cannot be changed here.
//
// This is a write test that is safe against a live connector, which is why it is
// not behind the skip below. The request is rejected whole — verified by reading
// the settings back — so nothing is mutated. The body is nonetheless built from
// the connector's *current* configuration rather than from convenient literals,
// so that if the server ever stops enforcing the match, the write that gets
// through is a no-op instead of a silent reconfiguration of somebody's live UEM
// link.
func TestAcceptance_SecurityCloudUemConnectVendorMismatch(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	page, err := sc.ListUemConnectorsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Fatalf("403 on uem-connect: this credential holds no uem-connect grant — see "+
				"TestAcceptance_SecurityCloudUemConnectSyncSettingsValidation for which env var to repoint: %v", err)
		}
		t.Fatalf("ListUemConnectorsV1 failed: %v", err)
	}
	if len(page.Results) == 0 {
		t.Skip("tenant has no UEM connector, and VENDOR_MISMATCH is checked after resource resolution so a bogus configId cannot reach it — needs a tenant with a connector")
	}
	c := page.Results[0]

	before, err := sc.GetUemConnectorSyncSettingsV1(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetUemConnectorSyncSettingsV1(%s) failed: %v", c.ID, err)
	}

	// Faithful round-trip of the current state, with only `vendor` wrong. Note
	// the read shape nests autoDeviceDeletion under syncConfig while the write
	// shape has it top-level.
	req := &securitycloud.SyncSettings{
		Vendor:                   before.Vendor,
		RefreshRateMinutes:       &before.RefreshRateMinutes,
		Scheduled:                &before.Scheduled,
		DeviceRiskTagging:        &before.DeviceRiskTagging,
		DeviceUnmanagedThreshold: &before.DeviceUnmanagedThreshold,
		ConcurrentSyncEnabled:    &before.ConcurrentSyncEnabled,
		GroupSettings:            before.GroupSettings,
	}
	if before.SyncConfig != nil {
		req.AutoDeviceDeletion = before.SyncConfig.AutoDeviceDeletion
		req.DisableSyncOnAuthError = &before.SyncConfig.DisableSyncOnAuthError
	}
	if before.DeviceFieldMappings != nil {
		req.DeviceFieldMappings = *before.DeviceFieldMappings
	}

	// Any vendor other than the stored one.
	wrong := securitycloud.SyncSettingsVendorIntune
	if before.Vendor == wrong {
		wrong = securitycloud.SyncSettingsVendorJamfPro
	}
	req.Vendor = wrong

	err = sc.UpdateUemConnectorSyncSettingsV1(ctx, c.ID, req)
	if err == nil {
		t.Fatalf("UpdateUemConnectorSyncSettingsV1(%s) accepted vendor %q against a connector stored as %q — "+
			"the server has stopped enforcing VENDOR_MISMATCH, which v2005's spec introduced. The write was "+
			"a no-op by construction, but flip this test to assert whatever the new behaviour is rather than "+
			"deleting it", c.ID, wrong, before.Vendor)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("vendor mismatch gave a non-API error: %v", err)
	}
	if !apiErr.HasStatus(422) {
		t.Fatalf("vendor mismatch returned %v, want 422 VENDOR_MISMATCH", err)
	}
	if !strings.Contains(apiErr.Error(), "VENDOR_MISMATCH") {
		t.Errorf("422 does not carry code VENDOR_MISMATCH: %v", err)
	}
	// `field` is null on this one, so the two vendor names live only in the
	// description. FieldErrors() still returns an entry — keyed on the empty
	// string — so the assertion has to be that no *named* field is attributed,
	// not that the map is empty.
	for field := range apiErr.FieldErrors() {
		if field != "" {
			t.Errorf("VENDOR_MISMATCH now attributes field %q — it was unattributed on 2026-09-02; record the change", field)
		}
	}
	t.Logf("vendor %q against stored %q: 422 VENDOR_MISMATCH (%v)", wrong, before.Vendor, err)

	after, err := sc.GetUemConnectorSyncSettingsV1(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetUemConnectorSyncSettingsV1(%s) after the rejected write failed: %v", c.ID, err)
	}
	// The vendor is the only thing the rejected request tried to change, so it is
	// the only equality that can be asserted: a UEM connector is shared
	// infrastructure and an operator editing it in the console between these two
	// reads is normal — observed 2026-09-02, when refreshRateMinutes and
	// groupMappings both moved under the suite mid-session. Drift in anything
	// else is logged, not failed, or this test becomes flaky for the wrong
	// reason.
	if after.Vendor != before.Vendor {
		t.Fatalf("the rejected write changed the stored vendor %q→%q — the rejection is not atomic",
			before.Vendor, after.Vendor)
	}
	if after.RefreshRateMinutes != before.RefreshRateMinutes ||
		after.Scheduled != before.Scheduled || after.DeviceUnmanagedThreshold != before.DeviceUnmanagedThreshold {
		t.Logf("connector drifted during the test (refresh %d→%d scheduled %v→%v threshold %d→%d) — "+
			"the rejected write cannot have caused it, since it carried the current values; someone is "+
			"editing this connector concurrently",
			before.RefreshRateMinutes, after.RefreshRateMinutes,
			before.Scheduled, after.Scheduled, before.DeviceUnmanagedThreshold, after.DeviceUnmanagedThreshold)
	}
	t.Log("rejected write left the stored vendor unchanged")
}

// jscProUemTenantID returns the platform tenant identifier of the Jamf Pro
// instance a UEM connector should sync with, for the `authStrategy: M2M`
// provisioning path.
//
// M2M is what makes this test self-sufficient: the caller supplies a tenant ID
// and no credentials at all, and Jamf Security Cloud provisions its own API role
// and integration ("JSC Connector") on that tenant. The previous shape used
// `JAMF_PRO_OAUTH` and minted the role, integration and client-credential pair
// itself through the SDK — no longer possible, because Jamf withdrew
// /v1/api-roles, /v1/api-integrations and /v1/api-role-privileges from the
// published spec in the GA cleanup (upstream's spec change) with
// credential management moving to Jamf Account. M2M reaches the same place
// without them and without a secret in an env var.
//
// Three sources, in order:
//
//   - JAMFPLATFORM_ACC_SECURITYCLOUD_UEM_PRO_TENANT_ID, when the target instance is not the
//     one the suite's own Jamf Pro credential reaches.
//   - GET /api/pro/v1/m2m/tenant-id, which asks the instance for its own
//     platform tenant ID. This is the one that works whichever scope the suite
//     settled on — an environment-scoped credential reports no tenant ID of its
//     own (Client.Scope returns the environment), so the instance has to be
//     asked. Needs the m2m:read capability.
//   - the client's own scope, when it is tenant-scoped: that value is the
//     platform tenant ID of the Jamf Pro this suite is pointed at, and costs no
//     request. Only a fallback, for a credential without m2m:read.
//
// Needs the Jamf Pro credential set (JAMFPLATFORM_* or JAMFPLATFORM_ACC_ENVIRONMENT_*),
// which is a different product and a different tenant from the Security Cloud
// one — see errAccJSCCredsUnset.
func jscProUemTenantID(t *testing.T) string {
	t.Helper()

	if id := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_UEM_PRO_TENANT_ID"); id != "" {
		t.Logf("Jamf Pro tenant %s (from JAMFPLATFORM_ACC_SECURITYCLOUD_UEM_PRO_TENANT_ID)", id)
		return id
	}

	c := accClient(t)
	info, err := pro.New(c).GetM2MTenantIDV1(context.Background())
	if err == nil && info.TenantID != nil && *info.TenantID != "" {
		t.Logf("Jamf Pro tenant %s (from GET /v1/m2m/tenant-id)", *info.TenantID)
		return *info.TenantID
	}
	if err != nil {
		t.Logf("GetM2MTenantIDV1: %v — falling back to the client's own scope", err)
	}

	if kind, id := c.Scope(); kind == jamfplatform.ScopeTenant && id != "" {
		t.Logf("Jamf Pro tenant %s (from the suite's tenant-scoped credential)", id)
		return id
	}

	t.Skip("cannot determine the target Jamf Pro platform tenant ID: /v1/m2m/tenant-id is unavailable (grant m2m:read) and this suite is not tenant-scoped — set JAMFPLATFORM_ACC_SECURITYCLOUD_UEM_PRO_TENANT_ID")
	return ""
}

// TestAcceptance_SecurityCloudUemConnectCreate drives CreateUemConnectorV1 with
// a real, fully-populated request: `authStrategy: M2M` plus the platform tenant
// ID of a Jamf Pro instance, which is the provisioning path that needs no
// credentials from the caller at all (see jscProUemTenantID).
//
// Three things this pins that no published spec states, all wire-verified:
//
//   - Deserialization and field validation run **ahead of** the singleton
//     pre-check, which is what makes any of this probeable on an occupied
//     tenant: an unknown `vendor` answers 422 where a well-formed request
//     answers 409 (2026-08-31).
//   - `authStrategy` is required. v1882 finally declares it, so the generated
//     field is a non-pointer string and absence is no longer expressible — the
//     empty string a forgetful caller sends is refused by the enum coercion with
//     a 422. Absence itself is still a **500 INTERNAL_ERROR** on the wire
//     (2026-08-21, re-verified 2026-08-31), reachable only by hand-rolled JSON.
//   - `M2M` requires `tenantId` and rejects its absence with
//     `422 "tenantId: must not be null"` (2026-08-28) — so field validation is
//     reachable without supplying anything secret.
//   - `url` is **not** required for `M2M`, though v1882 lists it in the
//     variant's `required` set. Omitted and empty-string both clear field
//     validation and the server derives the real URL from the named tenant
//     (2026-08-31). That is what makes the generated non-pointer `URL string`
//     safe: an M2M caller who never sets it sends `"url": ""` and is accepted.
//     For the credential strategies `url` genuinely is required —
//     `JAMF_PRO_OAUTH` without one answers `422 ": invalid auth configuration
//     for Jamf PRO"`, unattributed.
//   - A tenant may hold **one connector, full stop**. A complete, correct
//     request answers 409 CONNECTOR_CONFIG_ALREADY_EXISTS whenever any
//     connector exists, whatever its vendor — the message says "incompatible
//     UEM vendor" but INTUNE and JAMF_PRO are refused identically, and the
//     check fires before credential validation.
//
// So on a tenant that already has a connector this exercises the create path
// right up to the singleton pre-check without provisioning anything. On a tenant
// with none it really does create one, which is why the success branch deletes
// it immediately and then fails: that tenant can support the full lifecycle and
// this test should be widened rather than silently starting to mutate a
// connector other tests read.
func TestAcceptance_SecurityCloudUemConnectCreate(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	tenantID := jscProUemTenantID(t)

	// v1882 split the create body into a vendor-discriminated union, so the
	// Jamf Pro contract is its own schema and the request is built in two
	// halves: the discriminator on the envelope, and the typed variant behind
	// it. The vendor has to be set in both — the envelope routes on it, the
	// variant is what actually serialises.
	req := func() *securitycloud.ConnectorCreateRequestBody {
		return &securitycloud.ConnectorCreateRequestBody{
			Vendor: securitycloud.ConnectorCreateRequestBodyVendorJamfPro,
			JAMFPRO: &securitycloud.JamfProConnectorCreateRequest{
				Vendor:       securitycloud.ConnectorCreateRequestBodyVendorJamfPro,
				AuthStrategy: securitycloud.JamfProConnectorCreateRequestAuthStrategyM2m,
				TenantID:     &tenantID,
			},
		}
	}

	// Ordered deliberately: prove the spec-shaped request is unusable, then that
	// authStrategy alone promotes the failure to real validation, then that a
	// complete request clears validation entirely.
	t.Run("an empty authStrategy is a 422, not the 500 an absent one gives", func(t *testing.T) {
		// v1882 makes authStrategy required, so it generates as a non-pointer
		// string and the typed request can no longer express absence — which is
		// the point. Absent still returns 500 INTERNAL_ERROR on the wire
		// (re-verified 2026-08-31); the empty string a caller who forgets the
		// field now sends is refused properly, by the enum coercion, with the
		// field unattributed. So the required-ness converts an unactionable 500
		// into a diagnosable 422, and the 500 is only reachable by hand-rolling
		// the JSON.
		bare := req()
		bare.JAMFPRO.AuthStrategy = ""
		bare.JAMFPRO.TenantID = nil
		bare.JAMFPRO.URL = "https://example.jamfcloud.com"
		_, err := sc.CreateUemConnectorV1(ctx, bare)
		if err == nil {
			t.Fatal("a create with an empty authStrategy succeeded — a connector may now exist on the tenant")
		}
		apiErr := jamfplatform.AsAPIError(err)
		if apiErr == nil {
			t.Fatalf("want an API error, got %v", err)
		}
		if !apiErr.HasStatus(422) {
			t.Fatalf("want 422 VALIDATION_FAILED for an empty authStrategy, got %d (%s)", apiErr.StatusCode, apiErr.Summary())
		}
		t.Logf("empty authStrategy -> %s", apiErr.Summary())
	})

	t.Run("an unknown vendor is a 422, and it precedes the singleton check", func(t *testing.T) {
		// Load-bearing for every other subtest here: a tenant holds one
		// connector at most, so a well-formed create answers 409 whatever else
		// is wrong with it. This proves deserialization runs first, which is
		// what makes field-level probing possible on an occupied tenant. The
		// 422 also quotes the server's own subtype registry, so it is the
		// authoritative vendor list — and it is where the drift between the
		// spec's enum and the server's would first show.
		unknown := &securitycloud.ConnectorCreateRequestBody{Vendor: "BOGUS_VENDOR"}
		_, err := sc.CreateUemConnectorV1(ctx, unknown)
		if err == nil {
			t.Fatal("a create with an unknown vendor succeeded")
		}
		apiErr := jamfplatform.AsAPIError(err)
		if apiErr == nil || !apiErr.HasStatus(422) {
			t.Fatalf("want 422 ahead of the 409 singleton check, got %v", err)
		}
		t.Logf("unknown vendor -> %s", apiErr.Summary())
	})

	t.Run("M2M without a tenantId is a 422", func(t *testing.T) {
		noTenant := req()
		noTenant.JAMFPRO.TenantID = nil
		_, err := sc.CreateUemConnectorV1(ctx, noTenant)
		if err == nil {
			t.Fatal("a create with no tenantId succeeded — an unprovisioned connector may now exist on the tenant")
		}
		apiErr := jamfplatform.AsAPIError(err)
		if apiErr == nil || !apiErr.HasStatus(422) {
			t.Fatalf("want 422 VALIDATION_FAILED once authStrategy is present, got %v", err)
		}
		t.Logf("M2M without tenantId -> %s", apiErr.Summary())
	})

	t.Run("JAMF_PRO_OAUTH without a url is a 422", func(t *testing.T) {
		// The mirror of the M2M case below: url is optional for M2M and
		// required here, which the spec's flat `required: [vendor, url,
		// authStrategy]` cannot express. Credentials are deliberately bogus —
		// the auth-configuration check fires on shape, before anything is
		// dialled, so nothing is provisioned and no real secret is needed.
		oauth := req()
		oauth.JAMFPRO.AuthStrategy = securitycloud.JamfProConnectorCreateRequestAuthStrategyJamfProOauth
		oauth.JAMFPRO.TenantID = nil
		oauth.JAMFPRO.DeviceSyncAuth = &securitycloud.JamfProCredentials{
			ClientID:     &[]string{"not-a-real-client"}[0],
			ClientSecret: &[]string{"not-a-real-secret"}[0],
		}
		_, err := sc.CreateUemConnectorV1(ctx, oauth)
		if err == nil {
			t.Fatal("a JAMF_PRO_OAUTH create with no url succeeded — a connector may now exist on the tenant")
		}
		apiErr := jamfplatform.AsAPIError(err)
		if apiErr == nil || !apiErr.HasStatus(422) {
			t.Fatalf("want 422 for a credential strategy with no url, got %v", err)
		}
		t.Logf("JAMF_PRO_OAUTH without url -> %s", apiErr.Summary())
	})

	t.Run("a complete request clears validation", func(t *testing.T) {
		// req() leaves URL unset, so this also pins that the required-but-unused
		// `url` reaches the server as "" on the M2M path and is accepted: the
		// request gets past field validation to the singleton check.
		created, err := sc.CreateUemConnectorV1(ctx, req())
		if err == nil {
			// The tenant had no connector, so this made one. Remove it before
			// anything else observes it, then fail: the full lifecycle is now
			// reachable here and deserves real coverage. Note the deletion also
			// has to undo the API role and integration M2M provisioned on the
			// Jamf Pro side, which the SDK can no longer do — check the target
			// instance for a leftover "JSC Connector" integration.
			id := created.ID
			jscCleanupDelete(t, "uem connector "+id, func() error {
				return sc.DeleteUemConnectorV1(context.Background(), id)
			})
			t.Fatalf("CreateUemConnectorV1 succeeded (id %s) — this tenant holds no other connector, so enablement, sync-settings and sync runs are all exercisable now; widen this test rather than leaving the create unasserted", id)
		}
		apiErr := jamfplatform.AsAPIError(err)
		if apiErr == nil || !apiErr.HasStatus(409) {
			t.Fatalf("want 409 CONNECTOR_CONFIG_ALREADY_EXISTS (validation passed, singleton refused), got %v", err)
		}
		// A 409 here and a 422 above together prove tenantId was structurally
		// accepted: the request got past field validation to a state check,
		// which is the furthest a shared tenant allows.
		t.Logf("complete request -> %s", apiErr.Summary())
	})
}

// TestAcceptance_SecurityCloudUemConnectWrites documents why the remaining UEM
// Connect writes stay unexercised. Create is covered by
// TestAcceptance_SecurityCloudUemConnectCreate; these are the ones that act on
// whichever connector the tenant already owns.
func TestAcceptance_SecurityCloudUemConnectWrites(t *testing.T) {
	accSecurityCloudClient(t)
	t.Skip("every remaining UEM Connect write mutates the tenant's existing connector, which is a live link to a real UEM instance whose credentials are write-only and therefore unrestorable: DeleteUemConnectorV1 would destroy it permanently (the clientSecret cannot be read back to recreate it); EnableUemConnectorV1 / DisableUemConnectorV1 toggle whether it syncs a real device fleet; UpdateUemConnectorSyncSettingsV1 is a documented full replacement whose read shape (ConnectorConfig, syncConfig nested) differs from its write shape (SyncSettings, those fields top-level), so a round-trip that mis-maps one field silently resets the connector's configuration; TriggerUemConnectorSyncV1 and CancelUemConnectorSyncV1 start and abort a real inventory sync against the connected instance; DeployActivationProfileToUemV1 pushes an activation profile into that instance's device fleet. Exercising these needs a tenant whose connector is disposable — note a tenant may hold only one, so it cannot be a tenant that also has a connector worth keeping.")
}

// ---------------------------------------------------------------------------
// Resolvers and Apply (upsert)
// ---------------------------------------------------------------------------

// TestAcceptance_SecurityCloudResolvers checks every read-only resolver against
// live data. Security Cloud offers no RSQL filter and no search parameter on any
// list endpoint, so all six resolvers run in clientFilter mode — the match
// happens in memory, which makes "does the list element really carry this name
// field" a wire question rather than a spec one.
func TestAcceptance_SecurityCloudResolvers(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	// Content categories are a per-tenant system catalogue, so this resolver is
	// always exercisable — and it is the one a caller needs before creating a
	// ZTNA app, whose categoryName validates against displayName.
	cats, err := sc.ListContentCategoriesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListContentCategoriesV1 failed: %v", err)
	}
	if len(cats.Results) > 0 {
		want := cats.Results[0]
		id, err := sc.ResolveContentCategoryV1IDByName(ctx, want.DisplayName)
		if err != nil {
			t.Fatalf("ResolveContentCategoryV1IDByName(%q) failed: %v", want.DisplayName, err)
		}
		if id != want.ID {
			t.Errorf("resolved category ID = %q, want %q", id, want.ID)
		}
		got, err := sc.ResolveContentCategoryV1ByName(ctx, want.DisplayName)
		if err != nil {
			t.Fatalf("ResolveContentCategoryV1ByName(%q) failed: %v", want.DisplayName, err)
		}
		if got.ID != want.ID {
			t.Errorf("resolved category = %q, want %q", got.ID, want.ID)
		}
	}

	// ZTNA gateways and apps resolve across pages; grouped gateways do not
	// (their list endpoint declares no page params), so both transport walks
	// get exercised.
	gatewayPage, err := sc.ListZtnaGatewaysV1(ctx)
	if err != nil {
		t.Fatalf("ListZtnaGatewaysV1 failed: %v", err)
	}
	gateways := gatewayPage.Results
	if len(gateways) > 0 {
		id, err := sc.ResolveZtnaGatewayV1IDByName(ctx, gateways[0].Name)
		if err != nil {
			t.Fatalf("ResolveZtnaGatewayV1IDByName(%q) failed: %v", gateways[0].Name, err)
		}
		if id != gateways[0].ID {
			t.Errorf("resolved gateway ID = %q, want %q", id, gateways[0].ID)
		}
	}

	grouped, err := sc.ListZtnaGroupedGatewaysV1(ctx)
	if err != nil {
		t.Fatalf("ListZtnaGroupedGatewaysV1 failed: %v", err)
	}
	if len(grouped.Results) > 0 {
		got, err := sc.ResolveZtnaGroupedGatewayV1ByName(ctx, grouped.Results[0].Name)
		if err != nil {
			t.Fatalf("ResolveZtnaGroupedGatewayV1ByName(%q) failed: %v", grouped.Results[0].Name, err)
		}
		if got.ID != grouped.Results[0].ID {
			t.Errorf("resolved grouped gateway = %q, want %q", got.ID, grouped.Results[0].ID)
		}
	}

	// An app created from a predefinedAppId has name null on the wire, so only
	// a named app is resolvable — pick one rather than assuming the first has a
	// name.
	apps, err := sc.ListZtnaAppsV1(ctx)
	if err != nil {
		t.Fatalf("ListZtnaAppsV1 failed: %v", err)
	}
	var namedApp *securitycloud.App
	for i := range apps {
		if apps[i].Name != nil && *apps[i].Name != "" {
			namedApp = &apps[i]
			break
		}
	}
	if namedApp == nil {
		t.Logf("no ZTNA app has a name (all %d are predefined-template apps, which return name null) — ResolveZtnaAppV1ByName is covered by the app lifecycle test instead", len(apps))
	} else {
		id, err := sc.ResolveZtnaAppV1IDByName(ctx, *namedApp.Name)
		if err != nil {
			t.Fatalf("ResolveZtnaAppV1IDByName(%q) failed: %v", *namedApp.Name, err)
		}
		if id != namedApp.ID {
			t.Errorf("resolved app ID = %q, want %q", id, namedApp.ID)
		}
	}

	// A name nothing owns must be a 404, not an empty string — Apply branches
	// on exactly this to decide create-vs-update.
	absent := jscName("does-not-exist")
	if _, err := sc.ResolveDnsZoneV1IDByName(ctx, absent); !isSecurityCloudNotFound(err) {
		t.Errorf("resolving an absent DNS zone: want 404 APIResponseError, got %v", err)
	}
	// Only the v2 device-group resolver remains: v2082 withdrew GET /v1/groups,
	// so ResolveDeviceGroupV1IDByName went with it. The v2 resolver unwraps the
	// {groups: []} envelope and is the only resultsField user in the package, so
	// a regression there would otherwise be invisible — which is why it keeps a
	// dedicated absent-name assertion rather than relying on the Apply test.
	if _, err := sc.ResolveDeviceGroupV2IDByName(ctx, absent); !isSecurityCloudNotFound(err) {
		t.Errorf("resolving an absent device group (v2): want 404 APIResponseError, got %v", err)
	}

	// An empty name is a caller bug, and must not degrade into "match the first
	// element with no name" — which is exactly what a predefined ZTNA app would
	// be.
	if _, err := sc.ResolveZtnaAppV1IDByName(ctx, ""); err == nil {
		t.Error("resolving an empty app name succeeded; want an error")
	}
}

// TestAcceptance_SecurityCloudApplyDeviceGroup exercises both Apply branches on
// the cheapest resource in the family: create-when-absent, then
// update-when-present with the same name.
//
// Only ApplyDeviceGroupV2 remains. v2082 withdrew GET /v1/groups, which took
// ResolveDeviceGroupV1IDByName and with it ApplyDeviceGroupV1 — there is no v1
// list left to resolve a name against. The surviving Apply resolves through the
// {groups: []} envelope, creates through POST /v1/groups and updates through
// PUT /v2/groups/{groupId}, so its update branch is the endpoint fixed on
// 2026-09-04; before that fix this branch could not have worked at all.
func TestAcceptance_SecurityCloudApplyDeviceGroup(t *testing.T) {
	sc := accSecurityCloudClient(t)

	for _, tc := range []struct {
		version string
		apply   func(context.Context, *securitycloud.CreateGroupRequest) (string, bool, error)
	}{
		{"v2", sc.ApplyDeviceGroupV2},
	} {
		t.Run(tc.version, func(t *testing.T) {
			ctx := context.Background()
			name := jscName("apply-group-" + tc.version)

			id, created, err := tc.apply(ctx, &securitycloud.CreateGroupRequest{Name: name})
			if err != nil {
				skipOnServerError(t, err)
				t.Fatalf("ApplyDeviceGroup%s (create branch) failed: %v", tc.version, err)
			}
			jscCleanupDelete(t, "device group "+id, func() error {
				return sc.DeleteDeviceGroupV1(context.Background(), id)
			})
			if !created {
				t.Errorf("first Apply of %q reported created=false; nothing owned that name", name)
			}
			if id == "" {
				t.Fatalf("ApplyDeviceGroup%s returned an empty ID on the create branch", tc.version)
			}

			// Second Apply with the same name must resolve to the same resource
			// and take the update path — a create here would leave a duplicate
			// behind, which is the failure mode Apply exists to prevent.
			sameID, created, err := tc.apply(ctx, &securitycloud.CreateGroupRequest{Name: name})
			if err != nil {
				t.Fatalf("ApplyDeviceGroup%s (update branch) failed: %v", tc.version, err)
			}
			if created {
				t.Error("second Apply reported created=true; it should have resolved the existing group")
			}
			if sameID != id {
				t.Errorf("second Apply returned ID %q, want %q", sameID, id)
			}
		})
	}
}

// TestAcceptance_SecurityCloudApplyDnsZone covers the Apply variant that
// converts the create request into a different update type (ZoneWrite →
// ZonePatch) by JSON round-trip. A field the patch type does not carry would be
// dropped silently, so the update branch checks the zone still holds what was
// sent.
func TestAcceptance_SecurityCloudApplyDnsZone(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	gateways := jscEnsureGateways(t, sc, 1)

	name := jscName("apply-zone")
	req := &securitycloud.ZoneWrite{
		Name:        name,
		Domains:     []string{"sdk-acc-apply.invalid"},
		NameServers: []securitycloud.NameServer{{IP: "203.0.113.53", GatewayID: gateways[0].ID}},
	}

	id, created, err := sc.ApplyDnsZoneV1(ctx, req)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ApplyDnsZoneV1 (create branch) failed: %v", err)
	}
	jscCleanupDelete(t, "dns zone "+id, func() error {
		return sc.DeleteDnsZoneV1(context.Background(), id)
	})
	if !created {
		t.Errorf("first Apply of %q reported created=false", name)
	}

	// Same name, changed domains: must update in place, and the round-trip
	// through ZonePatch must carry the new domain list.
	req.Domains = []string{"sdk-acc-apply.invalid", "sdk-acc-apply-2.invalid"}
	sameID, created, err := sc.ApplyDnsZoneV1(ctx, req)
	if err != nil {
		t.Fatalf("ApplyDnsZoneV1 (update branch) failed: %v", err)
	}
	if created {
		t.Error("second Apply reported created=true; it should have resolved the existing zone")
	}
	if sameID != id {
		t.Errorf("second Apply returned ID %q, want %q", sameID, id)
	}
	got, err := sc.GetDnsZoneV1(ctx, id)
	if err != nil {
		t.Fatalf("GetDnsZoneV1 after Apply update failed: %v", err)
	}
	if len(got.Domains) != 2 {
		t.Errorf("after Apply update, zone has domains %v; the ZoneWrite→ZonePatch round-trip dropped them", got.Domains)
	}
	if len(got.NameServers) != 1 {
		t.Errorf("after Apply update, zone has name servers %+v; want the one that was sent", got.NameServers)
	}
}

// TestAcceptance_SecurityCloudApplyZtnaApp covers Apply on a resource whose
// name field is an optional pointer, which the generator has to unwrap before
// it can resolve.
func TestAcceptance_SecurityCloudApplyZtnaApp(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	cats, err := sc.ListContentCategoriesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListContentCategoriesV1 failed: %v", err)
	}
	if len(cats.Results) == 0 {
		t.Skip("tenant exposes no content categories — an app needs a categoryName")
	}
	category := cats.Results[0].DisplayName
	for _, c := range cats.Results {
		if c.DisplayName == "Uncategorized" {
			category = c.DisplayName
			break
		}
	}

	name := jscName("apply-app")
	req := &securitycloud.AppCreateRequest{
		Name:         &name,
		CategoryName: category,
		Hostnames:    &[]string{"sdk-acc-apply-app.invalid"},
		Assignments:  securitycloud.Assignments{Inclusions: securitycloud.AssignmentsInclusions{AllUsers: true}},
		Routing:      securitycloud.Routing{Type: "DIRECT"},
	}

	id, created, err := sc.ApplyZtnaAppV1(ctx, req)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ApplyZtnaAppV1 (create branch) failed: %v", err)
	}
	jscCleanupDelete(t, "ztna app "+id, func() error {
		return sc.DeleteZtnaAppV1(context.Background(), id)
	})
	if !created {
		t.Errorf("first Apply of %q reported created=false", name)
	}

	req.Hostnames = &[]string{"sdk-acc-apply-app.invalid", "sdk-acc-apply-app-2.invalid"}
	sameID, created, err := sc.ApplyZtnaAppV1(ctx, req)
	if err != nil {
		t.Fatalf("ApplyZtnaAppV1 (update branch) failed: %v", err)
	}
	if created {
		t.Error("second Apply reported created=true; it should have resolved the existing app")
	}
	if sameID != id {
		t.Errorf("second Apply returned ID %q, want %q", sameID, id)
	}
	got, err := sc.GetZtnaAppV1(ctx, id)
	if err != nil {
		t.Fatalf("GetZtnaAppV1 after Apply update failed: %v", err)
	}
	if len(got.Hostnames) != 2 {
		t.Errorf("after Apply update, app has hostnames %v; the AppCreateRequest→AppPatchRequest round-trip dropped them", got.Hostnames)
	}
	if got.Name == nil || *got.Name == "" {
		t.Error("after Apply update, app name is nil or empty; the pointer name field was lost")
	}
}

// TestAcceptance_SecurityCloudApplyZtnaGroupedGateway covers the last Apply
// whose writes are safe on a shared tenant — a grouped gateway is metadata over
// existing gateways, so creating and deleting one moves no traffic by itself.
func TestAcceptance_SecurityCloudApplyZtnaGroupedGateway(t *testing.T) {
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	gateways := jscEnsureGateways(t, sc, 2)

	name := jscName("apply-grouped")
	req := &securitycloud.GroupedGatewayCreateRequest{
		Name:               name,
		GatewayIds:         []string{gateways[0].ID, gateways[1].ID},
		TenantIds:          jscTenantIDs(t),
		RoutingStrategy:    "NEAREST",
		RecoveryDelayInSec: 3600,
	}

	id, created, err := sc.ApplyZtnaGroupedGatewayV1(ctx, req)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ApplyZtnaGroupedGatewayV1 (create branch) failed: %v", err)
	}
	jscCleanupDelete(t, "grouped gateway "+id, func() error {
		return sc.DeleteZtnaGroupedGatewayV1(context.Background(), id)
	})
	if !created {
		t.Errorf("first Apply of %q reported created=false", name)
	}

	req.RoutingStrategy = "RANDOM"
	sameID, created, err := sc.ApplyZtnaGroupedGatewayV1(ctx, req)
	if err != nil {
		t.Fatalf("ApplyZtnaGroupedGatewayV1 (update branch) failed: %v", err)
	}
	if created {
		t.Error("second Apply reported created=true; it should have resolved the existing grouped gateway")
	}
	if sameID != id {
		t.Errorf("second Apply returned ID %q, want %q", sameID, id)
	}
	got, err := sc.GetZtnaGroupedGatewayV1(ctx, id)
	if err != nil {
		t.Fatalf("GetZtnaGroupedGatewayV1 after Apply update failed: %v", err)
	}
	if got.RoutingStrategy != "RANDOM" {
		t.Errorf("after Apply update, routingStrategy = %q, want RANDOM", got.RoutingStrategy)
	}
}

// TestAcceptance_SecurityCloudApplyZtnaGateway exercises both Apply branches.
// Gated for the same reason as the lifecycle test: the create branch provisions
// real network egress.
func TestAcceptance_SecurityCloudApplyZtnaGateway(t *testing.T) {
	jscGatewayWriteOK(t)
	sc := accSecurityCloudClient(t)
	ctx := context.Background()

	enabled := false
	req := &securitycloud.GatewayCreateRequest{
		Name:       jscName("apply-gateway"),
		Datacenter: securitycloud.GatewayCreateRequestDatacenterEuWest2,
		Enabled:    &enabled,
		TenantIds:  jscTenantIDs(t),
		Contact: securitycloud.GatewayContact{
			Email: "sdk-acc@example.invalid",
			Name:  "SDK acceptance",
		},
		Ipsec: &securitycloud.GatewayIpSecRequest{
			KeyExchange: "ikev2",
			Ike: securitycloud.CipherSuiteConfig{
				Encryption: []string{"aes256"}, Integrity: []string{"sha256"},
				DhGroups: []string{"modp2048"}, LifetimeInSec: 28800,
			},
			Esp: securitycloud.CipherSuiteConfig{
				Encryption: []string{"aes256"}, Integrity: []string{"sha256"},
				DhGroups: []string{"modp2048"}, LifetimeInSec: 3600,
			},
			Left: securitycloud.ConnectionConfigLeftRequest{
				ID: "sdk-acc.example.invalid", Host: "%any",
				Subnets: []string{"10.99.0.0/24"},
				Secret:  &[]string{"SdkAcceptancePsk1234"}[0],
			},
			Right: securitycloud.ConnectionConfigRightRequest{
				ID: "peer.sdk-acc.example.invalid", Host: "203.0.113.10",
				Subnets: []string{"10.98.0.0/16"},
				Vendor:  securitycloud.ConnectionConfigRightRequestVendorCisco,
			},
		},
	}

	id, created, err := sc.ApplyZtnaGatewayV1(ctx, req)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ApplyZtnaGatewayV1 create failed: %v", err)
	}
	jscCleanupDelete(t, "ztna gateway "+id, func() error {
		return sc.DeleteZtnaGatewayV1(context.Background(), id)
	})
	if !created {
		t.Errorf("first Apply reported created=false for a fresh name")
	}
	// The ID comes from the 201 body, which is the spec-declared CreateResponse
	// since build v1439 (it used to be the whole Gateway).
	if id == "" {
		t.Fatal("ApplyZtnaGatewayV1 returned an empty ID on create")
	}

	req.Datacenter = securitycloud.GatewayCreateRequestDatacenterEuWest1
	sameID, createdAgain, err := sc.ApplyZtnaGatewayV1(ctx, req)
	if err != nil {
		t.Fatalf("ApplyZtnaGatewayV1 update failed: %v", err)
	}
	if createdAgain {
		t.Errorf("second Apply reported created=true for an existing name")
	}
	if sameID != id {
		t.Errorf("second Apply returned ID %q, want %q", sameID, id)
	}
	got, err := sc.GetZtnaGatewayV1(ctx, id)
	if err != nil {
		t.Fatalf("GetZtnaGatewayV1 after Apply update failed: %v", err)
	}
	if got.Datacenter != securitycloud.GatewayDatacenterEuWest1 {
		t.Errorf("after Apply update, datacenter = %q, want eu-west-1", got.Datacenter)
	}
}

// isSecurityCloudNotFound reports whether err is a 404 from any Security Cloud
// service. It exists because the family does not speak one error dialect: DNS,
// ZTNA and UEM Connect answer the gateway's {httpStatus, traceId, errors[]}
// envelope, while device groups answer an entirely different one ({message,
// error, logref, statusCode}) that leaves Details() and FieldErrors() empty. The
// status code is the only field both of them populate.
func isSecurityCloudNotFound(err error) bool {
	var apiErr *jamfplatform.APIResponseError
	return errors.As(err, &apiErr) && apiErr.HasStatus(http.StatusNotFound)
}

// boolValue renders an optional bool for a log line without printing a pointer.
func boolValue(b *bool) bool {
	return b != nil && *b
}

// jscClientWithHeaders builds a *fresh* Security Cloud client carrying extra
// request headers, bypassing the shared singleton.
//
// It exists only for TestAcceptance_SecurityCloudUemConnectAcceptNegotiation.
// `WithHeaders` applies to every request the client makes, so a restrictive
// `Accept` cannot be attached to the suite-wide client without breaking every
// other test on it. It skips and fails on exactly the same conditions
// accSecurityCloudClient does, and deliberately does not memoise: each caller
// wants its own header set.
func jscClientWithHeaders(t *testing.T, h http.Header) *securitycloud.Client {
	t.Helper()

	baseURL := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_BASE_URL")
	if baseURL == "" {
		baseURL = accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
	}
	clientID := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID")
	clientSecret := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_SECRET")
	tenantID := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID")
	if baseURL == "" || clientID == "" || clientSecret == "" || tenantID == "" {
		t.Skipf("Skipping Security Cloud acceptance test: %v", errAccJSCCredsUnset)
	}

	opts := append(accTraceOpts(),
		jamfplatform.WithTenantID(tenantID),
		jamfplatform.WithHeaders(h),
	)
	return securitycloud.New(jamfplatform.NewClient(baseURL, clientID, clientSecret, opts...))
}

// TestAcceptance_SecurityCloudUemConnectAcceptNegotiation pins where content
// negotiation sits in uem-connect's request pipeline, which is what v2018's only
// spec change turns on.
//
// v2005 declared a `406` on all twelve uem-connect operations. v2018 deleted it
// from the seven that answer 204/202, keeping it on exactly the five that write a
// 2xx response body. Wire-probed 2026-09-02, that is correct and the reason is
// structural: **`406` is decided when the response body is serialized, so it is
// the last check in the pipeline, not the first.** The full order is
//
//	Content-Type (415) → body validation (422) → resource resolution (404) →
//	business rules (422 VENDOR_MISMATCH) → response serialization (406)
//
// which means an unsatisfiable `Accept` is invisible on an operation that never
// writes a body, and is *also* invisible on the five that do whenever an earlier
// check fires first. Both halves below are therefore fixture-free — the negative
// half needs no connector because the 404 is the point, and the positive half
// uses the collection GET, which always resolves.
//
// The SDK itself is unexposed: no generated method sets `Accept`, so it always
// gets JSON. This test reaches the behaviour only by way of `WithHeaders`, which
// does not reserve `Accept` — the same door a reverse proxy would come through.
func TestAcceptance_SecurityCloudUemConnectAcceptNegotiation(t *testing.T) {
	ctx := context.Background()

	const bogusID = "bogus000000000000000000"

	pdf := http.Header{}
	pdf.Set("Accept", "application/pdf")
	sc := jscClientWithHeaders(t, pdf)

	// Positive half: the collection GET writes a body, so serialization is
	// reached and negotiation fails. This is the one operation the 406 probe
	// used at v2005, and the only reason it looked like a first-class check.
	_, err := sc.ListUemConnectorsV1(ctx)
	if err == nil {
		t.Fatal("ListUemConnectorsV1 succeeded with Accept: application/pdf — the server has stopped " +
			"negotiating content on an operation whose spec declares 406; re-probe and record it")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Accept: application/pdf gave a non-API error: %v", err)
	}
	if apiErr.HasStatus(403) {
		t.Fatalf("403 on uem-connect: this credential holds no uem-connect grant — see "+
			"TestAcceptance_SecurityCloudUemConnectSyncSettingsValidation for which env var to repoint: %v", err)
	}
	if !apiErr.HasStatus(406) {
		t.Fatalf("Accept: application/pdf on ListUemConnectorsV1 returned %v, want 406 NOT_ACCEPTABLE", err)
	}
	if !strings.Contains(apiErr.Error(), "NOT_ACCEPTABLE") {
		t.Errorf("406 does not carry code NOT_ACCEPTABLE: %v", err)
	}
	t.Logf("Accept: application/pdf on the collection GET: 406 NOT_ACCEPTABLE (%v)", err)

	// Negative half: a 204 operation never serializes a body, so the same
	// unsatisfiable Accept is never consulted and resource resolution answers
	// first. A 406 here would mean negotiation had moved ahead of resolution,
	// making v2018's removal wrong — which is the change this test guards.
	err = sc.DisableUemConnectorV1(ctx, bogusID)
	if err == nil {
		t.Fatalf("DisableUemConnectorV1(%q) succeeded against a nonexistent configId — impossible", bogusID)
	}
	if !errors.As(err, &apiErr) {
		t.Fatalf("bodiless op with Accept: application/pdf gave a non-API error: %v", err)
	}
	if apiErr.HasStatus(406) {
		t.Fatalf("DisableUemConnectorV1 answered 406 with Accept: application/pdf — content negotiation now "+
			"precedes resource resolution, so v2018's removal of the 406 from the seven bodiless operations "+
			"is wrong and should be reported upstream: %v", err)
	}
	if !apiErr.HasStatus(404) {
		t.Fatalf("DisableUemConnectorV1(%q) returned %v, want 404 NOT_FOUND", bogusID, err)
	}
	t.Logf("Accept: application/pdf on a 204 operation with a bogus configId: 404, negotiation not reached (%v)", err)

	// uem-connect has Jackson's XML converter registered and its own spec does
	// not know, so `application/xml` is satisfiable where the spec says only
	// `application/json` is produced. Pinned as a *limitation*: the assertion is
	// that this is not yet a 406, and it fails the day upstream restricts the
	// converter — at which point flip it to expect 406 rather than deleting it.
	xml := http.Header{}
	xml.Set("Accept", "application/xml")
	scXML := jscClientWithHeaders(t, xml)

	_, err = scXML.ListUemConnectorsV1(ctx)
	if err == nil {
		t.Fatal("ListUemConnectorsV1 decoded an XML body as JSON — impossible; the transport uses " +
			"json.Unmarshal, so a 200 here means the server answered JSON despite Accept: application/xml")
	}
	if errors.As(err, &apiErr) && apiErr.HasStatus(406) {
		t.Fatalf("Accept: application/xml now answers 406. The spec has said all along that these "+
			"operations produce only application/json, and the server has finally agreed — this test pinned "+
			"the disagreement, so flip it to assert the 406 rather than deleting it: %v", err)
	}
	t.Logf("Accept: application/xml still yields an XML body the JSON decoder rejects, not a 406 — "+
		"the spec's produces-only-JSON claim remains wrong (%v)", err)
}
