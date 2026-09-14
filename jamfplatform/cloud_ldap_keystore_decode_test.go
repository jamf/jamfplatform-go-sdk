// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform_test

import (
	"encoding/json"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
)

// TestCloudLdapKeystore_TimezonelessDecode confirms that the timezone-less
// ISO-8601 expirationDate Jamf Pro emits decodes without error.
//
// Jamf Pro emits "2028-05-15T13:41:46" (no Z, no offset). Go's RFC3339
// decoder rejects this with "cannot parse "" as "Z07:00"", aborting the
// entire body decode. The fix retypes ExpirationDate to *string via the
// pro spec fieldTypeOverrides.
//
// Wire-probed 2026-05-30 against the Pro tenant, GET cloud-ldaps/1030.
func TestCloudLdapKeystore_TimezonelessDecode_LdapConfigurationResponse(t *testing.T) {
	const wantDate = "2028-05-15T13:41:46"
	fixture := `{
		"server": {
			"keystore": {
				"expirationDate": "` + wantDate + `",
				"type": "PKCS12",
				"fileName": "bestone-google-ldap.p12",
				"subject": "ST=California, C=US, OU=Google Workspace, CN=LDAP Client"
			}
		}
	}`

	var resp pro.LdapConfigurationResponse
	if err := json.Unmarshal([]byte(fixture), &resp); err != nil {
		t.Fatalf("decode LdapConfigurationResponse with timezone-less expirationDate: %v", err)
	}
	if resp.Server == nil || resp.Server.Keystore == nil {
		t.Fatal("Server.Keystore is nil after decode")
	}
	if resp.Server.Keystore.ExpirationDate == nil {
		t.Fatal("ExpirationDate is nil after decode")
	}
	// Round-trip: re-marshal and confirm the raw string value is preserved.
	b, err := json.Marshal(resp.Server.Keystore.ExpirationDate)
	if err != nil {
		t.Fatalf("marshal ExpirationDate: %v", err)
	}
	if got, want := string(b), `"`+wantDate+`"`; got != want {
		t.Fatalf("ExpirationDate round-trip = %s, want %s", got, want)
	}
}

// TestCloudLdapKeystore_TimezonelessDecode_CloudLdapKeystore confirms the
// same for the direct CloudLdapKeystore response returned by VerifyLdapKeystoreV1.
func TestCloudLdapKeystore_TimezonelessDecode_CloudLdapKeystore(t *testing.T) {
	const wantDate = "2028-05-15T13:41:46"
	fixture := `{
		"expirationDate": "` + wantDate + `",
		"type": "PKCS12",
		"fileName": "bestone-google-ldap.p12",
		"subject": "ST=California, C=US, OU=Google Workspace, CN=LDAP Client"
	}`

	var ks pro.CloudLdapKeystore
	if err := json.Unmarshal([]byte(fixture), &ks); err != nil {
		t.Fatalf("decode CloudLdapKeystore with timezone-less expirationDate: %v", err)
	}
	if ks.ExpirationDate == nil {
		t.Fatal("ExpirationDate is nil after decode")
	}
	// Round-trip: re-marshal and confirm the raw string value is preserved.
	b, err := json.Marshal(ks.ExpirationDate)
	if err != nil {
		t.Fatalf("marshal ExpirationDate: %v", err)
	}
	if got, want := string(b), `"`+wantDate+`"`; got != want {
		t.Fatalf("ExpirationDate round-trip = %s, want %s", got, want)
	}
}
