// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestAcceptance_Classic_LDAPServerCertificateUsed sends certificate_used, the
// one LDAP connection field the spec declares only on ldap_server_post — where
// post-symmetry replaced the whole connection block with the read sibling's, so
// the field could not be sent — and reads it back through certificates_used,
// which the spec types as a string and the server nests as
// <certificate><configuration_name/><issuer/></certificate>.
//
// The server applies the certificate only when certificate_used follows the
// connection's other fields, and otherwise answers 201 and stores nothing, so
// this fails if config.fieldOrder stops putting it last.
func TestAcceptance_Classic_LDAPServerCertificateUsed(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuerCN := "sdk-acc-ldap-issuer-" + runSuffix()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: issuerCN},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: issuerCN},
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	name := "sdk-acc-ldap-cert-" + runSuffix()
	created, err := pc.CreateLDAPServerByID(ctx, "0", &proclassic.LdapServerPost{
		Connection: &proclassic.LdapServerPostConnection{
			Name:               new(name),
			Hostname:           new("ldap.example.test"),
			Port:               new(636),
			UseSsl:             new(true),
			IsEnabled:          new(false),
			ServerType:         new(proclassic.LdapServerPostConnectionServerTypeCustom),
			AuthenticationType: new(proclassic.LdapServerPostConnectionAuthenticationTypeNone),
			CertificateUsed:    new(base64.StdEncoding.EncodeToString(der)),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateLDAPServerByID: %v", err)
	}
	id := 0
	switch {
	case created.ID != nil:
		id = *created.ID
	case created.Connection != nil && created.Connection.ID != nil:
		id = *created.Connection.ID
	default:
		t.Fatalf("no ID: %+v", created)
	}
	cleanupDelete(t, "DeleteLDAPServerByID", func() error { return pc.DeleteLDAPServerByID(ctx, intToStr(id)) })

	var got *proclassic.LdapServer
	settleUntilFound(t, "LDAP server read-back", func() error {
		var err error
		got, err = pc.GetLDAPServerByID(ctx, intToStr(id))
		return err
	})
	if got.Connection == nil || got.Connection.CertificatesUsed == nil || got.Connection.CertificatesUsed.Certificate == nil {
		t.Fatalf("certificates_used is empty: %+v", got.Connection)
	}
	certs := *got.Connection.CertificatesUsed.Certificate
	if len(certs) != 1 {
		t.Fatalf("certificates_used has %d entries, want 1", len(certs))
	}
	if certs[0].ConfigurationName == nil || *certs[0].ConfigurationName != name {
		t.Errorf("configuration_name = %v, want the server name %q", certs[0].ConfigurationName, name)
	}
	if certs[0].Issuer == nil || !strings.Contains(*certs[0].Issuer, issuerCN) {
		t.Errorf("issuer = %v, want it to name %q", certs[0].Issuer, issuerCN)
	}
}
