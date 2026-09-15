// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"testing"
	"time"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Diagnostic / introspection endpoints added in Jamf Pro 11.28.0:
//   - GET /v1/last-login
//   - GET /v1/m2m/tenant-id
//   - GET /v1/user-sessions/active
//   - GET /v1/user-sessions/count
//
// All are read-only and side-effect-free, so they exercise on every
// run.

func TestAcceptance_Pro_LastLoginV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	resp, err := p.GetLastLoginV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetLastLoginV1: %v", err)
	}
	if resp == nil {
		t.Fatal("GetLastLoginV1: nil response (transport returns a pointer to a zero-value struct on 2xx)")
	}
	if resp.LastLogin.IsZero() {
		t.Log("GetLastLoginV1: zero lastLogin — no recorded last login for this principal")
		return
	}
	t.Logf("LastLoginV1: lastLogin=%s", resp.LastLogin.Format(time.RFC3339))
}

func TestAcceptance_Pro_M2MTenantIDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	resp, err := p.GetM2MTenantIDV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetM2MTenantIDV1: %v", err)
	}
	if resp == nil || resp.TenantID == nil || *resp.TenantID == "" {
		t.Fatalf("GetM2MTenantIDV1: empty tenant id in response")
	}
	t.Logf("M2M tenant id: %s", *resp.TenantID)
}

func TestAcceptance_Pro_UserSessionsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	active, err := p.ListActiveUserSessionsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListActiveUserSessionsV1: %v", err)
	}
	t.Logf("ListActiveUserSessionsV1: %d sessions", len(active))

	count, err := p.GetActiveUsersCountV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetActiveUsersCountV1: %v", err)
	}
	if count == nil {
		t.Fatalf("GetActiveUsersCountV1: nil response")
	}
	t.Logf("GetActiveUsersCountV1: %v active users", count.ActiveUserCount)
}
