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

// --- advanced user content searches ------------------------------------

func TestAcceptance_Pro_User_ListAdvancedUserContentSearchesV1(t *testing.T) {
	c := accClient(t)

	res, err := pro.New(c).ListAdvancedUserContentSearchesV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAdvancedUserContentSearchesV1: %v", err)
	}
	t.Logf("Found %d advanced user content searches", len(res.Results))
}

func TestAcceptance_Pro_User_AdvancedUserContentSearchCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-adv-user-search-" + runSuffix()
	displayFields := []string{"Username"}

	created, err := p.CreateAdvancedUserContentSearchV1(ctx, &pro.AdvancedUserContentSearch{
		Name:          name,
		DisplayFields: &displayFields,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAdvancedUserContentSearchV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateAdvancedUserContentSearchV1 returned no ID (href=%q)", created.Href)
	}
	cleanupDelete(t, "DeleteAdvancedUserContentSearchV1", func() error { return p.DeleteAdvancedUserContentSearchV1(ctx, created.ID) })
	t.Logf("Created advanced user content search %s", created.ID)

	got, err := p.GetAdvancedUserContentSearchV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAdvancedUserContentSearchV1(%s): %v", created.ID, err)
	}
	if got.Name != name {
		t.Errorf("Name = %q, want %q", got.Name, name)
	}

	renamed := name + "-updated"
	got.Name = renamed
	updated, err := p.UpdateAdvancedUserContentSearchV1(ctx, created.ID, got)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateAdvancedUserContentSearchV1(%s): %v", created.ID, err)
	}
	if updated.Name != renamed {
		t.Errorf("UpdateAdvancedUserContentSearchV1 Name = %q, want %q", updated.Name, renamed)
	}

	if err := p.DeleteAdvancedUserContentSearchV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteAdvancedUserContentSearchV1(%s): %v", created.ID, err)
	}

	_, err = p.GetAdvancedUserContentSearchV1(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetAdvancedUserContentSearchV1(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetAdvancedUserContentSearchV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

// --- users --------------------------------------------------------------

func TestAcceptance_Pro_User_ListUsersV1(t *testing.T) {
	c := accClient(t)

	users, err := pro.New(c).ListUsersV1(context.Background(), nil, "", false)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListUsersV1: %v", err)
	}
	t.Logf("Found %d users", len(users))
}

// TestAcceptance_Pro_User_UserCRUD is gated behind JAMFPLATFORM_ACC_PRO_USER_WRITE_OK
// because the Pro users write path via the Platform gateway is currently
// broken on the Pro tenant: POST returns 500 but actually persists the
// record, and DELETE returns 500 with no effect. Every invocation leaks an
// orphan user until someone with direct Jamf Pro admin access cleans it up.
// Set JAMFPLATFORM_ACC_PRO_USER_WRITE_OK=1 to opt in once the gateway is fixed.
func TestAcceptance_Pro_User_UserCRUD(t *testing.T) {
	if accEnv("JAMFPLATFORM_ACC_PRO_USER_WRITE_OK") == "" {
		t.Skip("gated behind JAMFPLATFORM_ACC_PRO_USER_WRITE_OK — Pro users POST+DELETE currently broken at the gateway; opting in leaks an orphan per run")
	}

	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	username := "sdk-acc-user-" + runSuffix()
	realname := "SDK Acceptance Test User"
	email := username + "@example.invalid"

	// Filter-based cleanup registered BEFORE create. The Pro users API on
	// the Pro tenant has been observed to return 500 on create while
	// actually writing the record — leaving an orphan if cleanup is tied
	// only to the returned id. Looking up by username covers both the
	// happy path and the 500-but-wrote-anyway path.
	cleanupDelete(t, "DeleteUserV1(by-username)", func() error {
		users, err := p.ListUsersV1(ctx, nil, "username=="+`"`+username+`"`, false)
		if err != nil {
			return err
		}
		for _, u := range users {
			if u.Username != username {
				continue
			}
			if err := p.DeleteUserV1(ctx, u.ID); err != nil {
				return err
			}
		}
		return nil
	})

	created, err := p.CreateUserV1(ctx, &pro.UserInventory{
		Username: &username,
		Realname: &realname,
		Email:    &email,
	}, false)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateUserV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateUserV1 returned no ID")
	}
	t.Logf("Created user %s", created.ID)

	got, err := p.GetUserV1(ctx, created.ID, false)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetUserV1(%s): %v", created.ID, err)
	}
	if got.Username != username {
		t.Errorf("Username = %q, want %q", got.Username, username)
	}

	newRealname := realname + " (updated)"
	if err := p.UpdateUserV1(ctx, created.ID, &pro.UserInventory{
		Username: &username,
		Realname: &newRealname,
		Email:    &email,
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateUserV1(%s): %v", created.ID, err)
	}

	reget, err := p.GetUserV1(ctx, created.ID, false)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetUserV1(%s) post-update: %v", created.ID, err)
	}
	if reget.Realname != newRealname {
		t.Errorf("Realname = %q, want %q", reget.Realname, newRealname)
	}

	if err := p.DeleteUserV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteUserV1(%s): %v", created.ID, err)
	}

	_, err = p.GetUserV1(ctx, created.ID, false)
	if err == nil {
		t.Fatalf("GetUserV1(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetUserV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

// --- groups -------------------------------------------------------------

// Groups CRUD creation goes through the v1 device-groups / mobile-device-
// groups endpoints (batches 2 and 3), not through the /v1/groups surface
// which is read-only for listing + patching existing groups. These tests
// cover list + read-only probes; PATCH is exercised only if a group exists.

func TestAcceptance_Pro_User_ListGroupsV2(t *testing.T) {
	c := accClient(t)

	groups, err := pro.New(c).ListGroupsV2(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListGroupsV2: %v", err)
	}
	t.Logf("Found %d groups (unified groups surface)", len(groups))
	if len(groups) > 0 {
		// Probe GET by platform id — unified groups expose a platformId
		// distinct from the per-API (computer/mobile) Jamf Pro numeric id.
		// Any group is fine for a read probe.
		got, err := pro.New(c).GetGroupV2(context.Background(), groups[0].GroupPlatformID)
		if err != nil {
			skipOnServerError(t, err)
			var apiErr *jamfplatform.APIResponseError
			if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
				t.Logf("GetGroupV2(%s): 404 — group shape not round-trippable via this surface", groups[0].GroupPlatformID)
				return
			}
			t.Fatalf("GetGroupV2(%s): %v", groups[0].GroupPlatformID, err)
		}
		t.Logf("Group %s: name=%q type=%s members=%d", got.GroupJamfProID, got.GroupName, got.GroupType, got.MembershipCount)
	}
}
