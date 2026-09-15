// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// --- certificate-authority --------------------------------------------

func TestAcceptance_Pro_Security_CertificateAuthorityActiveV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	record, err := p.GetActiveCertificateAuthorityV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetActiveCertificateAuthorityV1: %v", err)
	}
	t.Logf("Active CA: subject=%q serial=%q", record.SubjectX500Principal, record.SerialNumber)

	der, err := p.DownloadActiveCertificateAuthorityDerV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadActiveCertificateAuthorityDerV1: %v", err)
	}
	if len(der) < 100 {
		t.Errorf("DER body too small: %d bytes", len(der))
	}

	pem, err := p.DownloadActiveCertificateAuthorityPemV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadActiveCertificateAuthorityPemV1: %v", err)
	}
	if !strings.Contains(string(pem), "BEGIN CERTIFICATE") {
		t.Errorf("PEM body does not look like PEM: %q", pem[:min(64, len(pem))])
	}
	t.Logf("Active CA DER %d bytes, PEM %d bytes", len(der), len(pem))
}

// --- adcs-settings (probe-only; needs real ADCS server) ---------------

func TestAcceptance_Pro_Security_AdcsSettingsProbeV1(t *testing.T) {
	c := accClient(t)

	probeID := "99999999"
	_, err := pro.New(c).GetAdcsSettingsV1(context.Background(), probeID)
	if err == nil {
		t.Logf("GetAdcsSettingsV1(%s) unexpectedly succeeded", probeID)
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("GetAdcsSettingsV1(%s): %d — plumbing OK", probeID, apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("GetAdcsSettingsV1(%s): %v", probeID, err)
}

// --- classic-ldap / ldap preview ---------------------------------------

func TestAcceptance_Pro_Security_LdapReadsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	if servers, err := p.ListLdapServersV1(ctx); err == nil {
		t.Logf("LDAP servers (v1/servers): %d", len(servers))
	} else {
		skipOnServerError(t, err)
		t.Errorf("ListLdapServersV1: %v", err)
	}
	if servers, err := p.ListLdapLdapServersV1(ctx); err == nil {
		t.Logf("LDAP servers (v1/ldap-servers): %d", len(servers))
	} else {
		skipOnServerError(t, err)
		t.Errorf("ListLdapLdapServersV1: %v", err)
	}
	if servers, err := p.ListLdapServersPreview(ctx); err == nil {
		t.Logf("LDAP servers (preview): %d", len(servers))
	} else {
		skipOnServerError(t, err)
		t.Errorf("ListLdapServersPreview: %v", err)
	}
	// group search — empty query returns all; if no servers, server 4xxs.
	if _, err := p.SearchLdapGroupsV1(ctx, ""); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if !errors.As(err, &apiErr) || apiErr.StatusCode >= 500 {
			skipOnServerError(t, err)
			t.Errorf("SearchLdapGroupsV1: %v", err)
		} else {
			t.Logf("SearchLdapGroupsV1 rejected (no LDAP configured): status=%d", apiErr.StatusCode)
		}
	}
}

// --- cloud providers (probe-only; need real Azure/LDAP/IdP) -----------

func TestAcceptance_Pro_Security_CloudAzureDefaultsV1(t *testing.T) {
	c := accClient(t)

	cfg, err := pro.New(c).GetCloudAzureDefaultServerConfigurationV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCloudAzureDefaultServerConfigurationV1: %v", err)
	}
	t.Logf("Cloud Azure defaults: %+v", cfg)
}

// The deprecated sibling of the defaults endpoint above. Upstream marked it
// deprecated without publishing a successor anywhere in the spec, so it is
// carried as-is; it is a static catalogue of the Azure AD attribute names Jamf
// maps by default.
func TestAcceptance_Pro_Security_CloudAzureDefaultMappingsV1(t *testing.T) {
	c := accClient(t)

	mappings, err := pro.New(c).GetCloudAzureDefaultMappingsV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCloudAzureDefaultMappingsV1: %v", err)
	}
	if mappings.UserID == "" || mappings.UserName == "" {
		t.Errorf("default mappings are missing the identity attributes: userId=%q userName=%q", mappings.UserID, mappings.UserName)
	}
	t.Logf("Cloud Azure default mappings: %+v", mappings)
}

func TestAcceptance_Pro_Security_ListCloudIdpV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListCloudIdpV1(context.Background(), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListCloudIdpV1: %v", err)
	}
	t.Logf("Cloud IdPs: %d", len(items))
}

func TestAcceptance_Pro_Security_CloudLdapDefaultsV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	for _, provider := range []string{"GOOGLE", "AZURE"} {
		if cfg, err := p.GetCloudLdapDefaultServerConfigurationV2(ctx, provider); err == nil {
			t.Logf("Cloud LDAP defaults for %s: %+v", provider, cfg)
		} else {
			var apiErr *jamfplatform.APIResponseError
			if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
				t.Logf("GetCloudLdapDefaultServerConfigurationV2(%s): %d", provider, apiErr.StatusCode)
			} else {
				skipOnServerError(t, err)
				t.Errorf("GetCloudLdapDefaultServerConfigurationV2(%s): %v", provider, err)
			}
		}
	}
}

// --- conditional access ------------------------------------------------

func TestAcceptance_Pro_Security_ConditionalAccessFeatureToggleV1(t *testing.T) {
	c := accClient(t)

	toggle, err := pro.New(c).GetConditionalAccessFeatureToggleV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetConditionalAccessFeatureToggleV1: %v", err)
	}
	t.Logf("Conditional access feature toggle: %+v", toggle)
}

// --- csa ---------------------------------------------------------------

func TestAcceptance_Pro_Security_CsaTenantIdV1(t *testing.T) {
	c := accClient(t)

	tenant, err := pro.New(c).GetCsaTenantIdV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCsaTenantIdV1: %v", err)
	}
	t.Logf("CSA tenant id info: %+v", tenant)
}

func TestAcceptance_Pro_Security_CsaTokenV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	_, err := p.GetCsaTokenV1(ctx)
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetCsaTokenV1: 404 — no CSA token registered, plumbing OK")
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetCsaTokenV1: %v", err)
	}
	t.Log("CSA token registered (not logged)")
}

// --- oidc --------------------------------------------------------------

func TestAcceptance_Pro_Security_OidcPublicV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	features, err := p.GetOidcPublicFeaturesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOidcPublicFeaturesV1: %v", err)
	}
	t.Logf("OIDC features: %+v", features)

	// public-key may 404 when OIDC isn't enabled
	if _, err := p.GetOidcPublicKeyV1(ctx); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetOidcPublicKeyV1: 404 — OIDC not fully configured, plumbing OK")
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetOidcPublicKeyV1: %v", err)
		}
	}

	// direct-idp-login-url: 404 when not set
	if _, err := p.GetOidcDirectIdpLoginUrlV1(ctx); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetOidcDirectIdpLoginUrlV1: 404 — not configured, plumbing OK")
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetOidcDirectIdpLoginUrlV1: %v", err)
		}
	}
}

// TestAcceptance_Pro_Security_GenerateOidcCertificateV1 regenerates
// the tenant's OIDC certificate. Caller has approved this as
// non-blocking in test.
func TestAcceptance_Pro_Security_GenerateOidcCertificateV1(t *testing.T) {
	c := accClient(t)

	if err := pro.New(c).GenerateOidcCertificateV1(context.Background()); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GenerateOidcCertificateV1: %v", err)
	}
	t.Log("Regenerated OIDC certificate")
}

func TestAcceptance_Pro_Security_DispatchOidcLoginV2(t *testing.T) {
	t.Skip("requires a real registered email + origin URL — plumbing only if you wire those up")
}

// --- sso settings ------------------------------------------------------

func TestAcceptance_Pro_Security_SsoSettingsV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	settings, err := p.GetSsoSettingsV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSsoSettingsV3: %v", err)
	}
	t.Logf("SSO configurationType=%s", settings.ConfigurationType)

	deps, err := p.GetSsoDependenciesV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetSsoDependenciesV3: %v", err)
	} else {
		t.Logf("SSO dependencies: %+v", deps)
	}

	body, err := p.DownloadSsoMetadataV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("DownloadSsoMetadataV3: 404 — no metadata configured, plumbing OK")
		} else {
			t.Errorf("DownloadSsoMetadataV3: %v", err)
		}
	} else {
		t.Logf("SSO metadata: %d bytes", len(body))
	}

	hist, err := p.ListSsoHistoryV3(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListSsoHistoryV3: %v", err)
	} else {
		t.Logf("SSO history: %d entries", len(hist))
	}
}

// TestAcceptance_Pro_Security_UpdateSsoSettingsV3 round-trips the
// current SSO config back unchanged. Same pattern used for other
// settings endpoints (enrollment V4, reenrollment, ADUE): GET →
// PUT the exact response body → verify no error. A server-side
// round-trip bug would surface as a 4xx/5xx here, but a no-op PUT
// on already-stored values won't alter live SSO behaviour.
func TestAcceptance_Pro_Security_UpdateSsoSettingsV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetSsoSettingsV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSsoSettingsV3: %v", err)
	}

	if _, err := p.UpdateSsoSettingsV3(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateSsoSettingsV3 round-trip (configurationType=%s): %v", current.ConfigurationType, err)
	}
	t.Logf("UpdateSsoSettingsV3 round-trip OK (configurationType=%s)", current.ConfigurationType)
}

// TestAcceptance_Pro_Security_DisableSsoV3 disables SSO on the tenant
// and then restores it — snapshot current settings via GET, POST the
// disable action, then PUT the snapshot back with ssoEnabled=true so
// the tenant is left in its original state. If SSO was already
// disabled before the test started, skips with a note (no meaningful
// side-effect to exercise).
func TestAcceptance_Pro_Security_DisableSsoV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	snapshot, err := p.GetSsoSettingsV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSsoSettingsV3 (pre-snapshot): %v", err)
	}
	if !snapshot.SsoEnabled {
		t.Skip("SSO already disabled on this tenant — nothing to disable; re-enable via the SSO settings UI before re-running")
	}

	// Restore SSO on exit regardless of the test outcome. This also
	// covers the case where DisableSsoV3 succeeded but later assertions
	// failed mid-flight.
	t.Cleanup(func() {
		restore := *snapshot
		restore.SsoEnabled = true
		if _, err := p.UpdateSsoSettingsV3(context.Background(), &restore); err != nil {
			t.Logf("cleanup restore SSO: %v", err)
		} else {
			t.Logf("Restored SSO (ssoEnabled=true, configurationType=%s)", snapshot.ConfigurationType)
		}
	})

	if err := p.DisableSsoV3(ctx); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DisableSsoV3: %v", err)
	}
	t.Log("SSO disabled — will be restored on cleanup")

	// Sanity: a fresh GET should now show ssoEnabled=false.
	post, err := p.GetSsoSettingsV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSsoSettingsV3 (post-disable): %v", err)
	}
	if post.SsoEnabled {
		t.Errorf("expected SsoEnabled=false after disable, got true")
	}
}

// --- sso-certificate ---------------------------------------------------

func TestAcceptance_Pro_Security_SsoCertificateV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	_, err := p.GetSsoCertificateV2(ctx)
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && (apiErr.HasStatus(404) || apiErr.HasStatus(400)) {
			t.Logf("GetSsoCertificateV2: %d — no SSO cert configured, plumbing OK", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("GetSsoCertificateV2: %v", err)
	}
	t.Log("SSO certificate present (details not logged)")

	body, err := p.DownloadSsoCertificateV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("DownloadSsoCertificateV2: %v", err)
	} else {
		t.Logf("SSO cert download: %d bytes", len(body))
	}
}

// TestAcceptance_Pro_Security_GenerateSsoCertificateV2 fires POST
// /v2/sso/cert which regenerates the SSO keystore certificate on the
// tenant. The caller has approved this as non-blocking in test.
func TestAcceptance_Pro_Security_GenerateSsoCertificateV2(t *testing.T) {
	c := accClient(t)

	resp, err := pro.New(c).GenerateSsoCertificateV2(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GenerateSsoCertificateV2: %v", err)
	}
	t.Logf("Generated SSO cert — details=%+v", resp)
}

// UpdateSsoCertificateV2 (PUT with a provided keystore) and
// DeleteSsoCertificateV2 remain skipped: PUT needs a real SAML
// keystore binary and DELETE tears down SSO login end-to-end.
func TestAcceptance_Pro_Security_UpdateSsoCertificateV2(t *testing.T) {
	t.Skip("PUT requires a real SAML keystore file upload — skip")
}

func TestAcceptance_Pro_Security_DeleteSsoCertificateV2(t *testing.T) {
	t.Skip("DELETE removes the SSO keystore, breaking SAML login — skip")
}

// --- sso-failover ------------------------------------------------------

func TestAcceptance_Pro_Security_SsoFailoverV1(t *testing.T) {
	c := accClient(t)

	data, err := pro.New(c).GetSsoFailoverV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 400 {
			t.Logf("GetSsoFailoverV1: 400 — no failover configured, plumbing OK")
			return
		}
		t.Fatalf("GetSsoFailoverV1: %v", err)
	}
	t.Logf("SSO failover data: %+v", data)
}

// TestAcceptance_Pro_Security_GenerateSsoFailoverV1 rotates the
// tenant's SSO failover URL. Caller has approved this as non-blocking
// in test — the old URL becomes invalid but the new one is usable.
func TestAcceptance_Pro_Security_GenerateSsoFailoverV1(t *testing.T) {
	c := accClient(t)

	data, err := pro.New(c).GenerateSsoFailoverV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GenerateSsoFailoverV1: %v", err)
	}
	if data.FailoverURL == "" {
		t.Error("GenerateSsoFailoverV1 returned empty FailoverURL")
	}
	t.Logf("SSO failover URL rotated (URL not logged)")
}

// --- LAPS (local admin password) settings ---------------------------------

// TestAcceptance_Pro_Security_LocalAdminPasswordSettingsV2 reads the
// tenant-wide LAPS configuration and round-trips a PUT with the
// unchanged values. Re-sending the current state is idempotent —
// rotation times don't change until an admin actively rotates a
// password, so there is no observable side-effect.
func TestAcceptance_Pro_Security_LocalAdminPasswordSettingsV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetLocalAdminPasswordSettingsV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetLocalAdminPasswordSettingsV2: %v", err)
	}
	t.Logf("LAPS settings: autoDeployEnabled=%v passwordRotationTime=%d autoRotateEnabled=%v autoRotateExpirationTime=%d",
		current.AutoDeployEnabled,
		current.PasswordRotationTime,
		current.AutoRotateEnabled,
		current.AutoRotateExpirationTime,
	)

	req := &pro.LapsSettingsRequestV2{
		AutoDeployEnabled:        current.AutoDeployEnabled,
		PasswordRotationTime:     current.PasswordRotationTime,
		AutoRotateEnabled:        current.AutoRotateEnabled,
		AutoRotateExpirationTime: current.AutoRotateExpirationTime,
	}
	updated, err := p.UpdateLocalAdminPasswordSettingsV2(ctx, req)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateLocalAdminPasswordSettingsV2 round-trip: %v", err)
	}
	if updated.PasswordRotationTime != current.PasswordRotationTime ||
		updated.AutoRotateExpirationTime != current.AutoRotateExpirationTime {
		t.Errorf("round-trip mismatch: got passwordRotationTime=%d autoRotateExpirationTime=%d, want %d/%d",
			updated.PasswordRotationTime, updated.AutoRotateExpirationTime,
			current.PasswordRotationTime, current.AutoRotateExpirationTime,
		)
	}
}

// --- LAPS (local admin password) per-device surface -----------------------
//
// New in the 11.30.0 spec alongside the settings endpoints already covered
// above. Every method here is a read except SetLocalAdminPasswordV2.
//
// Reading a password is not side-effect-free: the server records a VIEWED
// event in the device's LAPS history, attributed to the API client. That is
// an audit-trail append, not a state change, and it is the only way to
// exercise the endpoint at all — so these tests accept it. No password value
// is ever logged.

// lapsFixture finds a computer with at least one LAPS-capable account and
// returns its management id, the account, and whether that account has an
// issued password to read. It skips when no computer qualifies.
//
// An account whose rotation history holds only PENDING entries has no password
// yet — the device has not completed the rotation — and the password endpoints
// reject the read. A COMPLETED entry means one was issued. The fixture prefers
// such an account so the 200 path is actually exercised, and reports which
// case it settled for so the caller can assert accordingly.
func lapsFixture(t *testing.T) (mgmtID string, account pro.LapsUserV2, hasPassword bool) {
	t.Helper()
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	computers, err := p.ListComputersInventoryV4(ctx, []string{"GENERAL"}, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputersInventoryV4: %v", err)
	}

	var fallbackID string
	var fallbackAccount pro.LapsUserV2
	for _, comp := range computers {
		if comp.General == nil || comp.General.ManagementID == "" {
			continue
		}
		accounts, err := p.ListLocalAdminPasswordAccountsV2(ctx, comp.General.ManagementID)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListLocalAdminPasswordAccountsV2(%s): %v", comp.General.ManagementID, err)
		}
		for _, acct := range accounts.Results {
			if fallbackID == "" {
				fallbackID, fallbackAccount = comp.General.ManagementID, acct
			}
			history, err := p.ListLocalAdminPasswordAccountHistoryV2(ctx, comp.General.ManagementID, acct.Username)
			if err != nil {
				skipOnServerError(t, err)
				t.Fatalf("ListLocalAdminPasswordAccountHistoryV2(%s, %s): %v", comp.General.ManagementID, acct.Username, err)
			}
			for _, h := range history.Results {
				if h.RotationStatus == pro.LapsHistoryRotationStatusCompleted {
					return comp.General.ManagementID, acct, true
				}
			}
		}
	}
	if fallbackID == "" {
		t.Skip("no computer on this tenant has a LAPS-capable account — no probes possible")
	}
	t.Logf("no LAPS account has a completed rotation — using %q, whose password reads will be rejected", fallbackAccount.Username)
	return fallbackID, fallbackAccount, false
}

func TestAcceptance_Pro_Security_LocalAdminPasswordPendingRotationsV2(t *testing.T) {
	c := accClient(t)

	pending, err := pro.New(c).ListLocalAdminPasswordPendingRotationsV2(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListLocalAdminPasswordPendingRotationsV2: %v", err)
	}
	if pending.TotalCount != len(pending.Results) {
		t.Errorf("totalCount = %d but %d results returned", pending.TotalCount, len(pending.Results))
	}
	t.Logf("LAPS pending rotations: %d", pending.TotalCount)
	for _, r := range pending.Results {
		if r.LapsUser == nil {
			t.Error("pending rotation with nil lapsUser")
			continue
		}
		t.Logf("  pending: username=%q source=%s clientManagementId=%s created=%s",
			r.LapsUser.Username, r.LapsUser.UserSource, r.LapsUser.ClientManagementID, r.CreatedDate)
	}
}

// TestAcceptance_Pro_Security_LocalAdminPasswordAccountReads walks the whole
// per-device read surface: accounts, device history, and — for the first
// account — password, audit and history in both the username and the
// username+guid form.
func TestAcceptance_Pro_Security_LocalAdminPasswordAccountReads(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	mgmtID, account, hasPassword := lapsFixture(t)
	t.Logf("fixture: clientManagementId=%s username=%q guid=%s source=%s hasIssuedPassword=%v",
		mgmtID, account.Username, account.Guid, account.UserSource, hasPassword)

	// Device-wide history (viewed + rotation events).
	history, err := p.ListLocalAdminPasswordHistoryV2(ctx, mgmtID)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListLocalAdminPasswordHistoryV2(%s): %v", mgmtID, err)
	} else {
		t.Logf("device LAPS history: %d events", history.TotalCount)
	}

	// Per-account rotation history — a LAPS-capable account always has at
	// least the entry that made it capable.
	acctHistory, err := p.ListLocalAdminPasswordAccountHistoryV2(ctx, mgmtID, account.Username)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListLocalAdminPasswordAccountHistoryV2(%s, %s): %v", mgmtID, account.Username, err)
	} else {
		if acctHistory.TotalCount == 0 {
			t.Errorf("account %q has no rotation history", account.Username)
		}
		t.Logf("account history: %d entries", acctHistory.TotalCount)
	}

	// View audit — empty until someone reads the password, so no assertion
	// on the count; the read itself is what's under test.
	if audits, err := p.ListLocalAdminPasswordAuditsV2(ctx, mgmtID, account.Username); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListLocalAdminPasswordAuditsV2(%s, %s): %v", mgmtID, account.Username, err)
	} else {
		t.Logf("password view audit: %d entries", audits.TotalCount)
	}

	// Current password. When the account has a completed rotation this must
	// return a value; when it doesn't the server rejects the read with 400
	// carrying code NOT_FOUND (the status/code mismatch is Jamf's, verified on
	// 11.30.2), so tolerate 400 or 404 only in that state.
	assertLapsPassword(t, "GetLocalAdminPasswordV2", account.Username, hasPassword, func() (*pro.LapsPasswordResponseV2, error) {
		return p.GetLocalAdminPasswordV2(ctx, mgmtID, account.Username)
	})

	if account.Guid == "" {
		t.Log("account has no guid — skipping the guid-scoped variants")
		return
	}

	// The guid-scoped triplet addresses the same account by its directory
	// guid rather than its username; same shapes, separate paths.
	assertLapsPassword(t, "GetLocalAdminPasswordByGuidV2", account.Username, hasPassword, func() (*pro.LapsPasswordResponseV2, error) {
		return p.GetLocalAdminPasswordByGuidV2(ctx, mgmtID, account.Username, account.Guid)
	})

	if audits, err := p.ListLocalAdminPasswordAuditsByGuidV2(ctx, mgmtID, account.Username, account.Guid); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListLocalAdminPasswordAuditsByGuidV2(%s, %s, %s): %v", mgmtID, account.Username, account.Guid, err)
	} else {
		t.Logf("password view audit (by guid): %d entries", audits.TotalCount)
	}

	if h, err := p.ListLocalAdminPasswordAccountHistoryByGuidV2(ctx, mgmtID, account.Username, account.Guid); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListLocalAdminPasswordAccountHistoryByGuidV2(%s, %s, %s): %v", mgmtID, account.Username, account.Guid, err)
	} else {
		t.Logf("account history (by guid): %d entries", h.TotalCount)
	}
}

// assertLapsPassword checks a password read against whether the account has an
// issued password. The value is never logged — only its length.
func assertLapsPassword(t *testing.T, label, username string, hasPassword bool, read func() (*pro.LapsPasswordResponseV2, error)) {
	t.Helper()
	pw, err := read()
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if !hasPassword && errors.As(err, &apiErr) && (apiErr.HasStatus(400) || apiErr.HasStatus(404)) {
			t.Logf("%s(%s): %d — expected, account has no completed rotation so no password was issued; plumbing OK",
				label, username, apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Errorf("%s(%s): account has a completed rotation, read should have succeeded: %v", label, username, err)
		return
	}
	if !hasPassword {
		t.Logf("%s(%s): returned a password despite no completed rotation in history", label, username)
	}
	if pw.Password == "" {
		t.Errorf("%s(%s) returned 200 with an empty password", label, username)
		return
	}
	t.Logf("%s(%s): ok, %d characters (value not logged)", label, username, len(pw.Password))
}

// TestAcceptance_Pro_Security_LocalAdminPasswordBogusClient records how the
// surface answers an unknown clientManagementId, which is not uniform: the
// accounts list returns 200 with an empty collection while every other path
// 404s. A consumer treating "no accounts" as an error, or a 404 as "device
// doesn't exist", would be wrong on one of them.
func TestAcceptance_Pro_Security_LocalAdminPasswordBogusClient(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	const bogus = "00000000-0000-0000-0000-000000000000"

	accounts, err := p.ListLocalAdminPasswordAccountsV2(ctx, bogus)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListLocalAdminPasswordAccountsV2(bogus): expected 200 with an empty collection, got %v", err)
	}
	if len(accounts.Results) != 0 || accounts.TotalCount != 0 {
		t.Errorf("ListLocalAdminPasswordAccountsV2(bogus) returned %d accounts", accounts.TotalCount)
	}
	t.Log("ListLocalAdminPasswordAccountsV2(bogus): 200 with empty results — expected, no 404 for an unknown client ✓")

	notFound := []struct {
		label string
		call  func() error
	}{
		{"ListLocalAdminPasswordHistoryV2", func() error {
			_, err := p.ListLocalAdminPasswordHistoryV2(ctx, bogus)
			return err
		}},
		{"GetLocalAdminPasswordV2", func() error {
			_, err := p.GetLocalAdminPasswordV2(ctx, bogus, "nobody")
			return err
		}},
		{"ListLocalAdminPasswordAuditsV2", func() error {
			_, err := p.ListLocalAdminPasswordAuditsV2(ctx, bogus, "nobody")
			return err
		}},
		{"ListLocalAdminPasswordAccountHistoryV2", func() error {
			_, err := p.ListLocalAdminPasswordAccountHistoryV2(ctx, bogus, "nobody")
			return err
		}},
		{"GetLocalAdminPasswordByGuidV2", func() error {
			_, err := p.GetLocalAdminPasswordByGuidV2(ctx, bogus, "nobody", bogus)
			return err
		}},
		{"ListLocalAdminPasswordAuditsByGuidV2", func() error {
			_, err := p.ListLocalAdminPasswordAuditsByGuidV2(ctx, bogus, "nobody", bogus)
			return err
		}},
		{"ListLocalAdminPasswordAccountHistoryByGuidV2", func() error {
			_, err := p.ListLocalAdminPasswordAccountHistoryByGuidV2(ctx, bogus, "nobody", bogus)
			return err
		}},
		// Mutating, but safe against an id no device owns: the server has no
		// LAPS user to write to and rejects before touching anything.
		{"SetLocalAdminPasswordV2", func() error {
			_, err := p.SetLocalAdminPasswordV2(ctx, bogus, &pro.LapsUserPasswordRequestV2{})
			return err
		}},
	}
	for _, tc := range notFound {
		err := tc.call()
		if err == nil {
			t.Errorf("%s(bogus) succeeded — expected 404", tc.label)
			continue
		}
		var apiErr *jamfplatform.APIResponseError
		if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
			skipOnServerError(t, err)
			t.Errorf("%s(bogus): want 404, got %v", tc.label, err)
			continue
		}
		t.Logf("%s(bogus): 404 %s ✓", tc.label, apiErr.Summary())
	}
}
