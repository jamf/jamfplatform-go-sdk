// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"os"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devices"
)

func TestAcceptance_FileTokenCache(t *testing.T) {
	baseURL := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
	clientID := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID")
	clientSecret := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET")
	tenantID := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_ID")

	if baseURL == "" || clientID == "" || clientSecret == "" || tenantID == "" {
		t.Skip("missing required environment variables")
	}

	cacheDir := t.TempDir()
	ctx := context.Background()

	client1 := jamfplatform.NewClient(baseURL, clientID, clientSecret,
		jamfplatform.WithTenantID(tenantID),
		jamfplatform.WithFileTokenCache(cacheDir),
	)

	tok1, err := client1.AccessToken(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("first AccessToken call: %v", err)
	}
	if tok1.AccessToken == "" {
		t.Fatal("first AccessToken: expected non-empty token")
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("reading cache dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cache file, got %d", len(entries))
	}
	t.Logf("Cache file: %s", entries[0].Name())

	client2 := jamfplatform.NewClient(baseURL, clientID, clientSecret,
		jamfplatform.WithTenantID(tenantID),
		jamfplatform.WithFileTokenCache(cacheDir),
	)

	tok2, err := client2.AccessToken(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("second AccessToken call: %v", err)
	}
	if tok2.AccessToken != tok1.AccessToken {
		t.Error("expected second client to return the same cached token")
	}

	d, err := devices.New(client2).ListDevices(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices with cached token: %v", err)
	}
	t.Logf("Listed %d devices using cached token", len(d))
}
