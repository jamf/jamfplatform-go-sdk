// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

// App Installers acceptance coverage — all 24 operations ingested from GitOps
// v2043.
//
// # Why the write surface is gated rather than exercised
//
// A deployment is not a config object: creating one installs software on every
// computer in its scope, and there is no dry-run. So the four deployment writes,
// the three installation retries and the version update are all behind
// JAMFPLATFORM_ACC_PRO_APP_INSTALLERS_WRITE_OK, and the reads that need a
// deployment are probed with an ID that cannot exist instead. A 404 there is the
// pass: it proves the path is routed and looking the ID up, which is what the
// generated method's URL construction and response decoding are being tested
// for. Provisioning real software installs to assert a decode is the wrong
// trade.
//
// # The client
//
// accClient, like every other pro test. It prefers environment scope, and the
// environment credential is verified to hold the applications capability these
// operations need. Whether a post-GA TENANT credential can reach them is
// unverified — the tenant credentials available on 2026-09-03 had all been
// revoked by the time the gateway opened, so this is one to re-check rather than
// assume; see the note on JAMFPLATFORM_ACC_PRO_APP_INSTALLERS_WRITE_OK below.

import (
	"context"
	"reflect"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
)

// noSuchDeployment is an ID no deployment can have. Used to reach the
// deployment-scoped reads without provisioning one.
//
// It must be a POSITIVE numeric string. "0" looks like the natural choice and is
// rejected before lookup: the deployment endpoints validate the identifier's
// shape first, answering
// 400 "deploymentId: id field must be string of positive numeric value or -1"
// (wire-verified 2026-09-03 on all four deployment-scoped reads). That is a 400,
// not the 404 these probes assert, so "0" would have made every one of them fail
// for the wrong reason. -1 is accepted by the validator but is Jamf Pro's
// conventional "none" sentinel, so a large positive value is the honest probe.
//
// computerId on the per-computer retry carries the identical constraint and the
// identical message, so noSuchComputer exists for the same reason.
const (
	noSuchDeployment = "999999999"
	noSuchComputer   = "999999999"
)

// appInstallersWriteGate names the opt-in for anything that mutates a
// deployment. Deliberately its own gate rather than the suite-wide destructive
// one: the blast radius here is software installed on real computers, which is
// larger than most things that variable covers.
const appInstallersWriteGate = "JAMFPLATFORM_ACC_PRO_APP_INSTALLERS_WRITE_OK"

// TestAcceptance_Pro_AppInstallers_FeatureState pins the feature-availability
// probe. Jamf Pro annotates it @InternalOnlyApiCall, and upstream published it
// anyway, so it is covered here to keep that decision honest: if it is ever
// withdrawn, this fails rather than the whole file quietly losing an operation.
func TestAcceptance_Pro_AppInstallers_FeatureState(t *testing.T) {
	p := pro.New(accClient(t))

	state, err := p.GetAppInstallersFeatureStateV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAppInstallersFeatureStateV1: %v", err)
	}
	t.Logf("cloudServicesEnabled=%v features=%v", state.CloudServicesEnabled, state.Features)
}

// TestAcceptance_Pro_AppInstallers_TitleReads walks the catalogue: the paginated
// title list, one title's detail, and that title's versions.
//
// The list is the only operation here with real data behind it on a fresh
// tenant, so it is also where pagination gets exercised — ListAppInstallerTitlesV1
// walks pages internally and the count it returns is the assertion.
func TestAcceptance_Pro_AppInstallers_TitleReads(t *testing.T) {
	p := pro.New(accClient(t))
	ctx := context.Background()

	titles, err := p.ListAppInstallerTitlesV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppInstallerTitlesV1: %v", err)
	}
	t.Logf("app installer titles: %d", len(titles))
	if len(titles) == 0 {
		t.Skip("catalogue is empty on this instance, so there is no title to read")
	}

	first := titles[0]
	if first.ID == "" {
		t.Fatalf("title 0 has no ID: %+v", first)
	}

	detail, err := p.GetAppInstallerTitleV1(ctx, first.ID, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAppInstallerTitleV1(%s): %v", first.ID, err)
	}
	t.Logf("title %s: %+v", first.ID, detail)

	versions, err := p.ListAppInstallerTitleVersionsV1(ctx, first.ID, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppInstallerTitleVersionsV1(%s): %v", first.ID, err)
	}
	t.Logf("title %s versions: totalCount=%v", first.ID, versions.TotalCount)
}

// TestAcceptance_Pro_AppInstallers_GlobalSettingsReads covers the settings
// singleton, its deployment-control defaults and its history.
//
// Read-only: UpdateAppInstallerGlobalSettingsV1 is a full replacement of a real
// tenant's App Installers configuration, so it is gated separately below rather
// than round-tripped here.
func TestAcceptance_Pro_AppInstallers_GlobalSettingsReads(t *testing.T) {
	p := pro.New(accClient(t))
	ctx := context.Background()

	settings, err := p.GetAppInstallerGlobalSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAppInstallerGlobalSettingsV1: %v", err)
	}
	t.Logf("global settings: %+v", settings)

	defaults, err := p.GetAppInstallerDeploymentControlsDefaultsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAppInstallerDeploymentControlsDefaultsV1: %v", err)
	}
	t.Logf("deployment-control defaults: %+v", defaults)

	history, err := p.ListAppInstallerGlobalSettingsHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppInstallerGlobalSettingsHistoryV1: %v", err)
	}
	t.Logf("global settings history entries: %d", len(history))
}

// TestAcceptance_Pro_AppInstallers_DeploymentReads covers the deployment list
// and, via an ID that cannot exist, the four deployment-scoped reads.
//
// A 404 on the scoped reads is the pass. It proves the generated method built
// the right URL and that the error decodes as an API error rather than a
// transport failure — which is what can actually regress. Asserting the decoded
// body of a real deployment would require creating one, and creating one
// installs software.
func TestAcceptance_Pro_AppInstallers_DeploymentReads(t *testing.T) {
	p := pro.New(accClient(t))
	ctx := context.Background()

	deployments, err := p.ListAppInstallerDeploymentsV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppInstallerDeploymentsV1: %v", err)
	}
	t.Logf("deployments: %d", len(deployments))

	// If a deployment happens to exist, read it properly — that is strictly
	// better evidence than the doomed-ID probe, and costs nothing.
	if len(deployments) > 0 && deployments[0].ID != "" {
		id := deployments[0].ID
		if got, err := p.GetAppInstallerDeploymentV1(ctx, id); err != nil {
			t.Errorf("GetAppInstallerDeploymentV1(%s): %v", id, err)
		} else {
			t.Logf("deployment %s: %+v", id, got)
		}
		if sum, err := p.GetAppInstallerDeploymentInstallationSummaryV1(ctx, id); err != nil {
			t.Errorf("GetAppInstallerDeploymentInstallationSummaryV1(%s): %v", id, err)
		} else {
			t.Logf("installation summary %s: %+v", id, sum)
		}
		if computers, err := p.ListAppInstallerDeploymentComputersV1(ctx, id, nil, ""); err != nil {
			t.Errorf("ListAppInstallerDeploymentComputersV1(%s): %v", id, err)
		} else {
			t.Logf("deployment %s computers: %d", id, len(computers))
		}
		if hist, err := p.ListAppInstallerDeploymentHistoryV1(ctx, id, nil, ""); err != nil {
			t.Errorf("ListAppInstallerDeploymentHistoryV1(%s): %v", id, err)
		} else {
			t.Logf("deployment %s history: %d", id, len(hist))
		}
		return
	}

	t.Logf("no deployment exists, so the scoped reads are probed with ID %s", noSuchDeployment)
	assertNotFound(t, "GetAppInstallerDeploymentV1", func() error {
		_, err := p.GetAppInstallerDeploymentV1(ctx, noSuchDeployment)
		return err
	})
	assertNotFound(t, "GetAppInstallerDeploymentInstallationSummaryV1", func() error {
		_, err := p.GetAppInstallerDeploymentInstallationSummaryV1(ctx, noSuchDeployment)
		return err
	})
	assertNotFound(t, "ListAppInstallerDeploymentComputersV1", func() error {
		_, err := p.ListAppInstallerDeploymentComputersV1(ctx, noSuchDeployment, nil, "")
		return err
	})
	assertNotFound(t, "ListAppInstallerDeploymentHistoryV1", func() error {
		_, err := p.ListAppInstallerDeploymentHistoryV1(ctx, noSuchDeployment, nil, "")
		return err
	})
}

// TestAcceptance_Pro_AppInstallers_Export covers the deployments export.
//
// A POST, but read-only in effect: it renders the deployment list rather than
// changing anything, which is why it is here and not behind the write gate. The
// method returns []byte because the operation declares no JSON response schema.
func TestAcceptance_Pro_AppInstallers_Export(t *testing.T) {
	p := pro.New(accClient(t))

	out, err := p.ExportAppInstallerDeploymentsV1(context.Background(), &pro.ExportParameters{}, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportAppInstallerDeploymentsV1: %v", err)
	}
	t.Logf("export returned %d bytes", len(out))
}

// TestAcceptance_Pro_AppInstallers_Writes exercises the real write surface under
// its gate: create, read back, update, history note, delete, and the three
// deployment-scoped reads against an actual deployment rather than an impossible
// ID.
//
// # Why this is safe to run, and what makes it safe
//
// A deployment normally installs software on every computer in its scope. Two
// spec-documented properties remove that entirely, and both are asserted rather
// than assumed:
//
//   - enabled defaults to false — "is deployment active or is it a draft"
//   - smartGroupId null means "the app installer will not be deployed"
//
// So the deployment created here is a disabled draft scoped to nothing. It is
// also SELF_SERVICE with MANUAL updates, which is the least automatic
// combination the two enums allow, and it is deleted on cleanup. If a future
// spec change makes either default active, the create below asserts the
// round-tripped values and will fail rather than silently install anything.
//
// Still gated despite that, because the gate is about the operation's potential
// rather than this particular body: anyone editing this test to add a
// smartGroupId or flip enabled needs the deliberate opt-in already in place.
func TestAcceptance_Pro_AppInstallers_Writes(t *testing.T) {
	requireWriteOptIn(t, appInstallersWriteGate,
		"Creates a real App Installer deployment. It is a disabled draft scoped to no smart group, so it "+
			"installs nothing, but the operation can install software on every computer in a scope and the "+
			"retries act on real installations.")

	p := pro.New(accClient(t))
	ctx := context.Background()

	titles, err := p.ListAppInstallerTitlesV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppInstallerTitlesV1: %v", err)
	}
	if len(titles) == 0 || titles[0].ID == "" {
		t.Skip("no app title to deploy")
	}
	title := titles[0]

	disabled := false
	name := "sdk-acc-appinst-" + runSuffix()
	created, err := p.CreateAppInstallerDeploymentV1(ctx, &pro.AppTitleDeployment{
		Name:           name,
		AppTitleID:     title.ID,
		DeploymentType: pro.AppTitleDeploymentDeploymentTypeSelfService,
		UpdateBehavior: pro.AppTitleDeploymentUpdateBehaviorManual,
		Enabled:        &disabled,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAppInstallerDeploymentV1: %v", err)
	}
	id := created.ID
	if id == "" {
		t.Fatalf("CreateAppInstallerDeploymentV1 returned no id (href=%q)", created.Href)
	}
	// The href names the Jamf Pro instance hostname and an /api prefix, not the
	// gateway, so it is not a URL this SDK can call. Recorded rather than used.
	t.Logf("create href points at the instance, not the gateway: %s", created.Href)
	t.Logf("created deployment %s (%s) for title %s", id, name, title.ID)
	cleanupDelete(t, "DeleteAppInstallerDeploymentV1", func() error {
		return p.DeleteAppInstallerDeploymentV1(context.Background(), id)
	})

	// The safety properties are assertions, not assumptions. If a spec change
	// ever makes a create active by default, this is what catches it before
	// anything installs.
	got, err := p.GetAppInstallerDeploymentV1(ctx, id)
	if err != nil {
		t.Fatalf("GetAppInstallerDeploymentV1(%s): %v", id, err)
	}
	if got.Enabled {
		t.Errorf("deployment %s came back ENABLED despite being created disabled — it may be installing software", id)
	}
	// "-1" is this API's no-assignment sentinel, the same one categoryId and
	// siteId document. An omitted smartGroupId reads back as "-1", never "", so
	// both spellings mean unscoped and anything else is a real smart group.
	if got.SmartGroupID != "" && got.SmartGroupID != "-1" {
		t.Errorf("deployment %s came back scoped to smart group %q despite being created unscoped", id, got.SmartGroupID)
	}
	if got.Name != name {
		t.Errorf("Name = %q, want %q", got.Name, name)
	}

	// The scoped reads, now against a real deployment — coverage the
	// impossible-ID probes in TestAcceptance_Pro_AppInstallers_DeploymentReads
	// cannot give, since they only ever prove the URL and the error decode.
	if sum, err := p.GetAppInstallerDeploymentInstallationSummaryV1(ctx, id); err != nil {
		t.Errorf("GetAppInstallerDeploymentInstallationSummaryV1(%s): %v", id, err)
	} else {
		t.Logf("installation summary: %+v", sum)
	}
	if computers, err := p.ListAppInstallerDeploymentComputersV1(ctx, id, nil, ""); err != nil {
		t.Errorf("ListAppInstallerDeploymentComputersV1(%s): %v", id, err)
	} else if len(computers) != 0 {
		t.Errorf("unscoped deployment reports %d computers, want 0 — it is targeting machines", len(computers))
	} else {
		t.Log("deployment targets 0 computers, as an unscoped draft should")
	}

	note := "sdk-acc app installer probe"
	if entry, err := p.CreateAppInstallerDeploymentHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{Note: note}); err != nil {
		t.Errorf("CreateAppInstallerDeploymentHistoryNoteV1(%s): %v", id, err)
	} else {
		t.Logf("history note created: %+v", entry)
		hist, err := p.ListAppInstallerDeploymentHistoryV1(ctx, id, nil, "")
		if err != nil {
			t.Errorf("ListAppInstallerDeploymentHistoryV1(%s): %v", id, err)
		} else {
			var found bool
			for _, h := range hist {
				if h.Note == note {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("the note just created is not in the %d history entries — the write did not round-trip", len(hist))
			} else {
				t.Logf("note round-tripped through %d history entries", len(hist))
			}
		}
	}

	// PUT is a full replacement, so every required field goes back with it.
	renamed := name + "-updated"
	updated, err := p.UpdateAppInstallerDeploymentV1(ctx, id, &pro.AppTitleDeployment{
		Name:           renamed,
		AppTitleID:     title.ID,
		DeploymentType: pro.AppTitleDeploymentDeploymentTypeSelfService,
		UpdateBehavior: pro.AppTitleDeploymentUpdateBehaviorManual,
		Enabled:        &disabled,
	})
	if err != nil {
		t.Errorf("UpdateAppInstallerDeploymentV1(%s): %v", id, err)
	} else {
		if updated.Name != renamed {
			t.Errorf("after update Name = %q, want %q", updated.Name, renamed)
		}
		if updated.Enabled {
			t.Errorf("deployment %s became ENABLED through the update", id)
		}
		t.Logf("renamed to %q", updated.Name)
	}

	// Retry against a deployment that targets nothing answers 404 with an empty
	// errors array, on a deployment GET returns 200 for in the same run
	// (wire-verified 2026-09-03, curl-direct). That is Jamf Pro's own response,
	// not the gateway's: the unrouted tell in this namespace is
	// 403 BAD_PERMISSIONS, which /pro/v1/app-installers/bogus-control returned
	// as the control. So the 404 means "nothing to retry", and the spec
	// documents no status for the empty case. Asserted rather than logged so
	// the day it starts answering 2xx is the day this test says so.
	assertNotFoundFor(t, "RetryAppInstallerDeploymentInstallationsV1 on an empty deployment", id, func() error {
		return p.RetryAppInstallerDeploymentInstallationsV1(ctx, id)
	})

	// Delete here rather than leaving it to cleanup, so the 404-after-delete is
	// asserted. cleanupDelete stays registered and tolerates the second delete.
	if err := p.DeleteAppInstallerDeploymentV1(ctx, id); err != nil {
		t.Fatalf("DeleteAppInstallerDeploymentV1(%s): %v", id, err)
	}
	assertNotFoundFor(t, "GetAppInstallerDeploymentV1 after delete", id, func() error {
		_, err := p.GetAppInstallerDeploymentV1(ctx, id)
		return err
	})

	// Global settings: a round-trip that writes back exactly what was read, so a
	// successful PUT changes nothing. The read afterwards is the assertion.
	before, err := p.GetAppInstallerGlobalSettingsV1(ctx)
	if err != nil {
		t.Fatalf("GetAppInstallerGlobalSettingsV1: %v", err)
	}
	if _, err := p.UpdateAppInstallerGlobalSettingsV1(ctx, before); err != nil {
		t.Errorf("UpdateAppInstallerGlobalSettingsV1 (no-op round-trip): %v", err)
	} else if after, err := p.GetAppInstallerGlobalSettingsV1(ctx); err != nil {
		t.Errorf("GetAppInstallerGlobalSettingsV1 after the round-trip: %v", err)
	} else if !reflect.DeepEqual(before, after) {
		t.Errorf("the no-op settings round-trip changed something:\n before %+v\n after  %+v", before, after)
	} else {
		t.Log("global settings survived a no-op round-trip unchanged")
	}

	if entry, err := p.CreateAppInstallerGlobalSettingsHistoryNoteV1(ctx, &pro.ObjectHistoryNote{Note: note}); err != nil {
		t.Errorf("CreateAppInstallerGlobalSettingsHistoryNoteV1: %v", err)
	} else {
		t.Logf("global settings history note created: %+v", entry)
	}

	// The idless retry acts across the whole tenant, so it is only ever reached
	// under the gate. On a tenant with nothing to retry it answers 404 the same
	// way the scoped one does — same empty errors array, same routed-and-refused
	// shape — so it is asserted on the same grounds.
	assertNotFound(t, "RetryAppInstallerInstallationsV1 with nothing to retry", func() error {
		return p.RetryAppInstallerInstallationsV1(ctx)
	})

	// Two operations remain unexercised even here, both needing a deployment
	// that actually targets machines: the per-computer retry and the version
	// update. Their URL construction and error decoding are covered by the
	// impossible-ID probes below.
	assertNotFoundFor(t, "RetryAppInstallerDeploymentComputerInstallationV1", noSuchDeployment, func() error {
		return p.RetryAppInstallerDeploymentComputerInstallationV1(ctx, noSuchDeployment, noSuchComputer)
	})
	assertNotFoundFor(t, "UpdateAppInstallerDeploymentVersionV1", noSuchDeployment, func() error {
		return p.UpdateAppInstallerDeploymentVersionV1(ctx, noSuchDeployment, &pro.AppTitleVersion{})
	})
}

// assertNotFoundFor is assertNotFound with the identifier named explicitly, for
// callers probing something other than noSuchDeployment.
func assertNotFoundFor(t *testing.T, op, id string, call func() error) {
	t.Helper()
	err := call()
	if err == nil {
		t.Errorf("%s succeeded for id %s, which should not resolve", op, id)
		return
	}
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Errorf("%s: non-API error, the request did not reach the endpoint: %v", op, err)
		return
	}
	if !apiErr.HasStatus(404) {
		t.Errorf("%s: want 404 for id %s, got %d: %s", op, id, apiErr.StatusCode, apiErr.Summary())
		return
	}
	t.Logf("%s: 404 for id %s, as expected", op, id)
}

// assertNotFound runs an operation expected to 404 for an identifier that cannot
// exist, and fails on anything else. The point is the URL and the error decode,
// not the absence itself: a 404 says the request reached the endpoint and the
// endpoint looked the ID up.
func assertNotFound(t *testing.T, op string, call func() error) {
	t.Helper()
	err := call()
	if err == nil {
		t.Errorf("%s(%s) succeeded for an ID that cannot exist", op, noSuchDeployment)
		return
	}
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Errorf("%s: non-API error, the request did not reach the endpoint: %v", op, err)
		return
	}
	if !apiErr.HasStatus(404) {
		t.Errorf("%s: want 404 for a nonexistent ID, got %d: %s", op, apiErr.StatusCode, apiErr.Summary())
		return
	}
	t.Logf("%s: 404 for a nonexistent ID, as expected", op)
}
