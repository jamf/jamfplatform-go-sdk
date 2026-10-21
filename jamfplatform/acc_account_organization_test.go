// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/account"
)

// The Jamf Account Organization API (GitOps v2495) is organization-scoped like
// the rest of the account package. Its namespace was unmounted on the prod
// gateway until at least 2026-09-30 and answers from 2026-10-21.

const nilUUID = "00000000-0000-0000-0000-000000000000"

func TestAcceptance_AccountOrganizationReads(t *testing.T) {
	ac := account.New(accOrgClient(t))
	ctx := context.Background()

	envs, err := ac.ListEnvironments(ctx)
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	t.Logf("organization has %d environments", len(envs))
	known := map[string]bool{}
	for _, e := range envs {
		if e.ID == "" || e.Href == "" || e.Name == "" {
			t.Errorf("environment %+v is missing a required field", e)
		}
		known[e.ID] = true
	}

	tenants, err := ac.ListTenants(ctx)
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	t.Logf("organization has %d tenants", len(tenants))
	for _, tn := range tenants {
		if tn.ID == "" {
			t.Errorf("tenant %+v has an empty ID", tn)
		}
		// environmentId is how an environment's membership is read: the
		// environment itself does not carry its tenants.
		if tn.EnvironmentID != nil && !known[*tn.EnvironmentID] {
			t.Errorf("tenant %s names environment %s, absent from ListEnvironments", tn.ID, *tn.EnvironmentID)
		}
	}

	assertListBodyShape(t, accOrgClient(t), "organization", "v1", "/environments", "envelope")
	assertListBodyShape(t, accOrgClient(t), "organization", "v1", "/tenants", "envelope")
}

// TestAcceptance_AccountEnvironmentLifecycle creates one environment and folds
// every write assertion into it. AssignTenants is only ever sent with an
// identifier that cannot belong to the organization: assigning a real tenant
// cannot be undone over this API (there is no unassign), and an environment
// holding a tenant can never be deleted.
func TestAcceptance_AccountEnvironmentLifecycle(t *testing.T) {
	requireWriteOptIn(t, "JAMFPLATFORM_ACC_ORGANIZATION_WRITE_OK",
		"Creating an environment writes to a real customer record; this one is deleted again, but only an empty environment can be.")

	ac := account.New(accOrgClient(t))
	ctx := context.Background()

	name := fmt.Sprintf("sdk-acc-%d", rand.IntN(1_000_000))
	region := account.MetaRegionUs
	// Surrounding whitespace is trimmed and a mixed-case label is accepted.
	created, err := ac.CreateEnvironment(ctx, &account.CreateEnvironmentRequest{
		Name:       "  " + name + "  ",
		Label:      "sandbox",
		MetaRegion: &region,
	})
	if err != nil {
		t.Fatalf("CreateEnvironment(%s): %v", name, err)
	}
	id := created.ID
	if id == "" {
		t.Fatal("CreateEnvironment returned an empty ID")
	}
	deleted := false
	cleanupDelete(t, "environment "+name, func() error {
		if deleted {
			return nil
		}
		return ac.DeleteEnvironment(ctx, id)
	})
	if created.Name != name {
		t.Errorf("created name = %q, want %q (trimmed)", created.Name, name)
	}
	if derefStr(created.Label) != account.EnvironmentLabelSandbox {
		t.Errorf("created label = %q, want the upper-case canonical form %q", derefStr(created.Label), account.EnvironmentLabelSandbox)
	}

	find := func() *account.Environment {
		envs, err := ac.ListEnvironments(ctx)
		if err != nil {
			t.Fatalf("ListEnvironments: %v", err)
		}
		for i := range envs {
			if envs[i].ID == id {
				return &envs[i]
			}
		}
		return nil
	}
	if got := find(); got == nil {
		t.Fatalf("created environment %s absent from ListEnvironments", id)
	}

	// PUT is a full replacement of name and label; the response has no body.
	if err := ac.UpdateEnvironment(ctx, id, &account.UpdateEnvironmentRequest{Name: name + "-renamed", Label: account.EnvironmentLabelTrial}); err != nil {
		t.Fatalf("UpdateEnvironment: %v", err)
	}
	got := find()
	if got == nil {
		t.Fatalf("environment %s vanished after update", id)
	}
	if got.Name != name+"-renamed" || derefStr(got.Label) != account.EnvironmentLabelTrial {
		t.Errorf("after update: name=%q label=%q", got.Name, derefStr(got.Label))
	}
	// metaRegion is fixed at creation and is not part of the update request.
	if got.MetaRegion == nil || *got.MetaRegion != region {
		t.Errorf("metaRegion after update = %v, want %q", got.MetaRegion, region)
	}

	// Field validation precedes tenant resolution: an empty list is refused...
	err = ac.AssignTenants(ctx, id, &account.AssignTenantsRequest{TenantIds: []string{}})
	requireOrgAPIError(t, "AssignTenants(empty)", err, 400, "FIELD_VALIDATION")
	// ...and an identifier outside the organization is refused with its own code.
	err = ac.AssignTenants(ctx, id, &account.AssignTenantsRequest{TenantIds: []string{nilUUID}})
	requireOrgAPIError(t, "AssignTenants(unknown)", err, 400, "UNKNOWN_TENANT")

	if err := ac.DeleteEnvironment(ctx, id); err != nil {
		t.Fatalf("DeleteEnvironment: %v", err)
	}
	deleted = true
	if find() != nil {
		t.Errorf("environment %s still listed after delete", id)
	}
}

// TestAcceptance_AccountEnvironmentValidation sends only doomed requests, so it
// creates nothing and needs no write opt-in.
func TestAcceptance_AccountEnvironmentValidation(t *testing.T) {
	ac := account.New(accOrgClient(t))
	ctx := context.Background()

	_, err := ac.CreateEnvironment(ctx, &account.CreateEnvironmentRequest{Name: "", Label: account.EnvironmentLabelSandbox})
	requireOrgAPIError(t, "CreateEnvironment(empty name)", err, 400, "FIELD_VALIDATION")

	// A label or region outside the enum is not a field error: it fails JSON
	// binding, so the server reports the body as unparseable with no field.
	_, err = ac.CreateEnvironment(ctx, &account.CreateEnvironmentRequest{Name: "x", Label: "NOPE"})
	requireOrgAPIError(t, "CreateEnvironment(bad label)", err, 400, "MALFORMED_REQUEST_BODY")
	bad := "MARS"
	_, err = ac.CreateEnvironment(ctx, &account.CreateEnvironmentRequest{Name: "x", Label: account.EnvironmentLabelSandbox, MetaRegion: &bad})
	requireOrgAPIError(t, "CreateEnvironment(bad region)", err, 400, "MALFORMED_REQUEST_BODY")
}

// TestAcceptance_AccountEnvironmentUnknownIDIs500 pins a disagreement with the
// spec, which declares 404 for an unknown environment on PUT, DELETE and the
// tenants PUT. Wire-verified 2026-10-21 with a control (ListEnvironments at 200
// in the same invocation): an all-zero UUID, a string that is not a UUID, and an
// already-deleted environment all answer a deterministic
// `500 UPSTREAM_ERROR "The request could not be completed"`. It asserts the 500
// rather than skipping, so it fails the day the server answers 404 and names
// what to write in its place. 500 is retryable, so each call spends the retry
// budget.
func TestAcceptance_AccountEnvironmentUnknownIDIs500(t *testing.T) {
	ac := account.New(accOrgClient(t))
	ctx := context.Background()

	if _, err := ac.ListEnvironments(ctx); err != nil {
		t.Fatalf("control ListEnvironments: %v", err)
	}

	for name, call := range map[string]func() error{
		"UpdateEnvironment": func() error {
			return ac.UpdateEnvironment(ctx, nilUUID, &account.UpdateEnvironmentRequest{Name: "x", Label: account.EnvironmentLabelTrial})
		},
		"DeleteEnvironment": func() error { return ac.DeleteEnvironment(ctx, nilUUID) },
		"AssignTenants": func() error {
			return ac.AssignTenants(ctx, nilUUID, &account.AssignTenantsRequest{TenantIds: []string{nilUUID}})
		},
	} {
		requireOrgAPIError(t, name+"(unknown id)", call(), 500, "UPSTREAM_ERROR")
	}
}

func requireOrgAPIError(t *testing.T, op string, err error, status int, code string) {
	t.Helper()
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Errorf("%s: want API error %d %s, got %v", op, status, code, err)
		return
	}
	if !apiErr.HasStatus(status) {
		t.Errorf("%s: status %d, want %d", op, apiErr.StatusCode, status)
	}
	for _, d := range apiErr.Details() {
		if d.Code == code {
			return
		}
	}
	t.Errorf("%s: no error detail with code %s in %v", op, code, apiErr.Details())
}
