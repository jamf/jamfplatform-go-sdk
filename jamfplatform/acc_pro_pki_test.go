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

// Batch 14 — PKI integrations (digicert + venafi). Both require external
// CA infrastructure (DigiCert TLM or Venafi TPP with valid
// client-certs/refresh-tokens). Tenants without a fixture will 404 on
// GET-by-id and 400 on create without valid credentials. Tests probe
// the happy path, fall back to 4xx-tolerance, and fail only on 5xx or
// client bugs.

// --- digicert ---------------------------------------------------------

func TestAcceptance_Pro_PKI_DigicertTLMProbe(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// The API exposes no list endpoint — callers must know the id.
	// Probing with a known-bad id proves routing works; a real fixture
	// would be needed to exercise the full lifecycle.
	_, err := p.GetDigicertTrustLifecycleManagerV1(ctx, "-1")
	if err == nil {
		t.Log("GetDigicertTrustLifecycleManagerV1(-1) unexpectedly succeeded")
		return
	}
	if apiErr, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
		if apiErr.StatusCode == 404 {
			t.Logf("DigiCert TLM not configured on this tenant (404) — expected")
			return
		}
		if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("DigiCert TLM probe rejected: status=%d", apiErr.StatusCode)
			return
		}
	}
	skipOnServerError(t, err)
	t.Fatalf("GetDigicertTrustLifecycleManagerV1: %v", err)
}

// CheckDigicertTrustLifecycleManagerPrivilegesV1 answers 204 when the linked
// DigiCert account holds every permission needed to deploy certificates, and
// 403 with the missing permission names when it does not. Sibling sub-paths
// under the same id (/dependencies, /connection-status) do reach Pro, so the
// 204 in config's expectedStatus is spec-derived, not wire-verified.
//
// The path IS routed — wire-verified 2026-08-29, correcting a note that had
// claimed otherwise since 2026-08-16. An environment-scoped credential reached
// Jamf Pro and got 400 NOT_FOUND on the bogus id (pretty-printed, so Pro's own
// answer), while two tenant-scoped credentials — one EU, one US — were refused
// 403 by the gateway. The rule is gated on `digicert-settings:read`
// (the gateway's authorization policy carries a rule for it), so the 403 is a
// capability those credentials lack.
//
// That makes the old blanket `4xx -> log and return` branch actively misleading:
// it collapsed a gateway refusal, a Pro validation verdict and a genuine DigiCert
// permission failure into one tolerated outcome. They are separated below, and an
// environment-scoped client — proven to get past the gateway — treats a 403 as
// fatal rather than tolerable.
func TestAcceptance_Pro_PKI_DigicertPrivilegeCheck(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	err := p.CheckDigicertTrustLifecycleManagerPrivilegesV1(ctx, "-1")
	if err == nil {
		t.Log("CheckDigicertTrustLifecycleManagerPrivilegesV1(-1): 204, account holds all permissions")
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		skipOnServerError(t, err)
		t.Fatalf("CheckDigicertTrustLifecycleManagerPrivilegesV1: %v", err)
	}
	switch {
	case apiErr.HasStatus(403):
		if kind, _ := c.Scope(); kind == jamfplatform.ScopeEnvironment {
			t.Fatalf("privilege-check: 403 on an environment-scoped client, which was wire-verified to reach Jamf Pro on this path on 2026-08-29 — the credential has lost digicert-settings:read, or the policy changed: %v", err)
		}
		t.Skipf("privilege-check: 403 on a tenant-scoped client — this credential lacks digicert-settings:read; an environment-scoped credential reaches Jamf Pro here: %v", err)
	case apiErr.StatusCode >= 400 && apiErr.StatusCode < 500:
		t.Logf("privilege-check rejected by Jamf Pro on a bogus id: status=%d %s — expected without a DigiCert TLM fixture", apiErr.StatusCode, apiErr.Summary())
	default:
		skipOnServerError(t, err)
		t.Fatalf("CheckDigicertTrustLifecycleManagerPrivilegesV1: %v", err)
	}
}

func TestAcceptance_Pro_PKI_DigicertValidateCertificate(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// Endpoint returns 204 when the payload's certificate is a valid
	// DigiCert client cert. With an empty payload the server will
	// reject with 4xx — that's expected; we only guard against 5xx and
	// transport failures.
	err := p.ValidateDigicertClientCertificateV1(ctx, &pro.Certificate{
		Filename: "probe.p12",
		Data:     []byte{},
	})
	if err == nil {
		t.Log("ValidateDigicertClientCertificateV1 accepted empty payload")
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("ValidateDigicertClientCertificateV1 rejected: status=%d — expected without a real DigiCert fixture", apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("ValidateDigicertClientCertificateV1: %v", err)
}

// --- venafi -----------------------------------------------------------

func TestAcceptance_Pro_PKI_VenafiProbe(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// No list endpoint. Probe with a known-bad id to verify routing.
	if _, err := p.GetVenafiV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("Venafi probe: status=%d — expected on tenants without a Venafi CA", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetVenafiV1(-1): %v", err)
		}
	}

	// Connection-status and dependent-profiles follow the same probe
	// pattern.
	if _, err := p.GetVenafiConnectionStatusV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("Venafi connection-status probe: status=%d", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetVenafiConnectionStatusV1: %v", err)
		}
	}

	if _, err := p.GetVenafiDependentProfilesV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("Venafi dependent-profiles probe: status=%d", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetVenafiDependentProfilesV1: %v", err)
		}
	}

	if _, err := p.GetVenafiJamfPublicKeyV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("Venafi jamf-public-key probe: status=%d", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetVenafiJamfPublicKeyV1: %v", err)
		}
	}

	if _, err := p.GetVenafiProxyTrustStoreV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("Venafi proxy-trust-store probe: status=%d", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetVenafiProxyTrustStoreV1: %v", err)
		}
	}
}

func TestAcceptance_Pro_PKI_VenafiLifecycle(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// The server accepts a minimal name-only payload — refresh-token
	// validation is deferred to actual TPP calls. Exercise the full
	// CRUD lifecycle against a placeholder record; cleanup deletes it.
	name := "sdk-acc-venafi-" + runSuffix()
	created, err := p.CreateVenafiV1(ctx, &pro.VenafiCaRecord{Name: name})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("CreateVenafiV1 rejected: status=%d — tenant may require richer payload", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("CreateVenafiV1: %v", err)
	}
	id := created.ID
	t.Logf("Created Venafi CA record id=%s", id)
	cleanupDelete(t, "Venafi "+id, func() error { return p.DeleteVenafiV1(ctx, id) })

	got, err := p.GetVenafiV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetVenafiV1(%s): %v", id, err)
	}
	if got.Name != name {
		t.Errorf("name round-trip mismatch: got %q, want %q", got.Name, name)
	}

	// PATCH rejects response-only fields (refreshTokenConfigured).
	// Send the minimal writable subset instead of echoing the full GET.
	if _, err := p.UpdateVenafiV1(ctx, id, &pro.VenafiCaRecord{Name: name + "-upd"}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateVenafiV1: %v", err)
	}

	if _, err := p.CreateVenafiHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test venafi history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateVenafiHistoryNoteV1: %v", err)
	}

	hist, err := p.ListVenafiHistoryV1(ctx, id, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListVenafiHistoryV1: %v", err)
	}
	t.Logf("Venafi history: %d entries", len(hist))
}
