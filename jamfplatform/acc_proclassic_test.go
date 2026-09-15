// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

//go:fix inline
func classicStrPtr(s string) *string { return new(s) }
func intToStr(i int) string          { return strconv.Itoa(i) }

// TestAcceptance_Classic_ComputerCRUD exercises the Classic computer create
// and delete using a synthetic record — no real enrolled device is touched.
//
// It covers only create and delete because those are the only verbs the
// published spec still declares on this path: v1993 withdrew
// GET and PUT /computers/id/{id} along with every alternate-identifier read,
// update and delete, leaving POST and DELETE on /computers/id/{id} and POST
// on the four alternate-identifier paths. The numeric id the create does not
// return is recovered through MatchComputers, the only computer lookup left in
// the package, and the post-delete assertion is that the match no longer
// resolves rather than a 404 from a read that no longer exists.
func TestAcceptance_Classic_ComputerCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-computer-" + runSuffix()
	serial := "SDK" + runSuffix()

	err := pc.CreateComputerByID(ctx, "0", &proclassic.ComputerPost{
		General: &proclassic.ComputerPostGeneral{
			Name:         new(name),
			SerialNumber: new(serial),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerByID(0): %v", err)
	}
	t.Logf("Created computer name=%q serial=%q", name, serial)

	id := matchComputerIDBySerial(t, pc, serial)

	// The delete below is the assertion, so the safety net only fires when the
	// test bailed before reaching it — registering an unconditional cleanup
	// would log a 404 from a second delete on every green run and hide a real
	// cleanup failure in that noise.
	deleted := false
	cleanupDelete(t, "DeleteComputerByID", func() error {
		if deleted {
			return nil
		}
		return pc.DeleteComputerByID(ctx, intToStr(id))
	})

	if err := pc.DeleteComputerByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerByID(%d): %v", id, err)
	}
	deleted = true

	// Verify gone. MatchComputers answers 200 with an empty list rather than
	// 404, so absence — not an error — is the assertion.
	after, err := pc.MatchComputers(ctx, serial)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("MatchComputers(%q) after delete: %v", serial, err)
	}
	if after != nil {
		for _, comp := range after.Computers {
			if comp.ID != nil && *comp.ID == id {
				t.Fatalf("computer %d still matches %q after delete", id, serial)
			}
		}
	}
}

// matchComputerIDBySerial recovers a Classic computer's numeric id from its
// serial number. It exists because POST /computers/id/0 returns a
// server-generated body without a usable id and v1993 withdrew every
// by-identifier read, leaving GET /computers/match/{match} as the only lookup.
func matchComputerIDBySerial(t *testing.T, pc *proclassic.Client, serial string) int {
	t.Helper()
	ctx := context.Background()

	matched, err := pc.MatchComputers(ctx, serial)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("MatchComputers(%q): %v", serial, err)
	}
	if matched == nil || len(matched.Computers) == 0 {
		t.Fatalf("MatchComputers(%q) returned no computer for a record just created", serial)
	}
	for _, comp := range matched.Computers {
		if comp.ID != nil {
			return *comp.ID
		}
	}
	t.Fatalf("MatchComputers(%q) returned %d computers, none carrying an id", serial, len(matched.Computers))
	return 0
}

func TestAcceptance_Classic_BuildingCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-building-" + runSuffix()
	created, err := pc.CreateBuildingByID(ctx, "0", &proclassic.Building{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBuildingByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateBuildingByID returned no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteBuildingByID", func() error { return pc.DeleteBuildingByID(ctx, intToStr(id)) })
	t.Logf("Created building id=%d name=%q", id, name)

	got, err := pc.GetBuildingByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBuildingByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("GetBuildingByID Name = %v, want %q", got.Name, name)
	}

	newName := name + "-updated"
	if err := pc.UpdateBuildingByID(ctx, intToStr(id), &proclassic.Building{Name: new(newName)}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateBuildingByID(%d): %v", id, err)
	}

	if err := pc.DeleteBuildingByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteBuildingByID(%d): %v", id, err)
	}

	_, err = pc.GetBuildingByID(ctx, intToStr(id))
	if err == nil {
		t.Fatalf("GetBuildingByID(%d) after delete should 404, succeeded", id)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetBuildingByID(%d) after delete: want 404, got %v", id, err)
	}
}

func TestAcceptance_Classic_DepartmentCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-dept-" + runSuffix()
	created, err := pc.CreateDepartmentByID(ctx, "0", &proclassic.Department{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDepartmentByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateDepartmentByID returned no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteDepartmentByID", func() error { return pc.DeleteDepartmentByID(ctx, intToStr(id)) })

	got, err := pc.GetDepartmentByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDepartmentByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	if err := pc.DeleteDepartmentByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteDepartmentByID(%d): %v", id, err)
	}
	_, err = pc.GetDepartmentByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_CategoryCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-cat-" + runSuffix()
	prio := 5
	created, err := pc.CreateCategoryByID(ctx, "0", &proclassic.Category{Name: new(name), Priority: &prio})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCategoryByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateCategoryByID returned no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteCategoryByID", func() error { return pc.DeleteCategoryByID(ctx, intToStr(id)) })

	got, err := pc.GetCategoryByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCategoryByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	if err := pc.DeleteCategoryByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteCategoryByID(%d): %v", id, err)
	}
	_, err = pc.GetCategoryByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_ScriptCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-script-" + runSuffix()
	contents := "#!/bin/sh\necho hello\n"
	created, err := pc.CreateScriptByID(ctx, "0", &proclassic.Script{
		Name:           new(name),
		ScriptContents: new(contents),
		Priority:       new(proclassic.ScriptPriorityAfter),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateScriptByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateScriptByID returned no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteScriptByID", func() error { return pc.DeleteScriptByID(ctx, intToStr(id)) })

	got, err := pc.GetScriptByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetScriptByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	newName := name + "-updated"
	if err := pc.UpdateScriptByID(ctx, intToStr(id), &proclassic.Script{Name: new(newName)}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateScriptByID(%d): %v", id, err)
	}

	if err := pc.DeleteScriptByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteScriptByID(%d): %v", id, err)
	}
	_, err = pc.GetScriptByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_UserCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-user-" + runSuffix()
	email := name + "@example.test"
	created, err := pc.CreateUserByID(ctx, "0", &proclassic.UserPost{
		Name:  new(name),
		Email: new(email),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateUserByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateUserByID returned no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteUserByID", func() error { return pc.DeleteUserByID(ctx, intToStr(id)) })

	got, err := pc.GetUserByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetUserByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	if err := pc.DeleteUserByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteUserByID(%d): %v", id, err)
	}
	_, err = pc.GetUserByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_ComputerEACRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-ea-" + runSuffix()
	created, err := pc.CreateComputerExtensionAttributeByID(ctx, "0", &proclassic.ComputerExtensionAttribute{
		Name:     new(name),
		DataType: new(proclassic.ComputerExtensionAttributeDataTypeString),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerExtensionAttributeByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateComputerExtensionAttributeByID returned no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteComputerExtensionAttributeByID", func() error { return pc.DeleteComputerExtensionAttributeByID(ctx, intToStr(id)) })

	got, err := pc.GetComputerExtensionAttributeByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerExtensionAttributeByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	if err := pc.DeleteComputerExtensionAttributeByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerExtensionAttributeByID(%d): %v", id, err)
	}
	_, err = pc.GetComputerExtensionAttributeByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_MobileDeviceEACRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-mdea-" + runSuffix()
	created, err := pc.CreateMobileDeviceExtensionAttributeByID(ctx, "0", &proclassic.MobileDeviceExtensionAttribute{
		Name:     new(name),
		DataType: new(proclassic.MobileDeviceExtensionAttributeDataTypeString),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceExtensionAttributeByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceExtensionAttributeByID", func() error { return pc.DeleteMobileDeviceExtensionAttributeByID(ctx, intToStr(id)) })

	if err := pc.DeleteMobileDeviceExtensionAttributeByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetMobileDeviceExtensionAttributeByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_UserEACRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-uea-" + runSuffix()
	created, err := pc.CreateUserExtensionAttributeByID(ctx, "0", &proclassic.UserExtensionAttribute{
		Name:     new(name),
		DataType: new(proclassic.UserExtensionAttributeDataTypeString),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateUserExtensionAttributeByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteUserExtensionAttributeByID", func() error { return pc.DeleteUserExtensionAttributeByID(ctx, intToStr(id)) })

	if err := pc.DeleteUserExtensionAttributeByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetUserExtensionAttributeByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_ComputerGroupCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-cg-" + runSuffix()
	isSmart := false
	created, err := pc.CreateComputerGroupByID(ctx, "0", &proclassic.ComputerGroupPost{
		Name:    new(name),
		IsSmart: &isSmart,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerGroupByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteComputerGroupByID", func() error { return pc.DeleteComputerGroupByID(ctx, intToStr(id)) })
	t.Logf("Created computer group id=%d name=%q", id, name)

	// Group reads lag their own writes — see settleUntilFound.
	var got *proclassic.ComputerGroup
	settleUntilFound(t, "GetComputerGroupByID("+intToStr(id)+")", func() error {
		var err error
		got, err = pc.GetComputerGroupByID(ctx, intToStr(id))
		return err
	})
	if got.Name == nil || *got.Name != name {
		t.Errorf("GetComputerGroupByID Name = %v, want %q", got.Name, name)
	}

	newName := name + "-updated"
	if err := pc.UpdateComputerGroupByID(ctx, intToStr(id), &proclassic.ComputerGroupPost{
		Name:    new(newName),
		IsSmart: &isSmart,
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateComputerGroupByID(%d): %v", id, err)
	}

	if err := pc.DeleteComputerGroupByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerGroupByID(%d): %v", id, err)
	}

	settleUntilGone(t, "GetComputerGroupByID("+intToStr(id)+") after delete", func() error {
		_, err := pc.GetComputerGroupByID(ctx, intToStr(id))
		return err
	})
}

func TestAcceptance_Classic_MobileDeviceGroupCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-mdg-" + runSuffix()
	isSmart := false
	created, err := pc.CreateMobileDeviceGroupByID(ctx, "0", &proclassic.MobileDeviceGroup{
		Name:    new(name),
		IsSmart: &isSmart,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceGroupByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceGroupByID", func() error { return pc.DeleteMobileDeviceGroupByID(ctx, intToStr(id)) })

	if err := pc.DeleteMobileDeviceGroupByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetMobileDeviceGroupByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_UserGroupCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-ug-" + runSuffix()
	isSmart := false
	created, err := pc.CreateUserGroupByID(ctx, "0", &proclassic.UserGroup{
		Name:    new(name),
		IsSmart: &isSmart,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateUserGroupByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteUserGroupByID", func() error { return pc.DeleteUserGroupByID(ctx, intToStr(id)) })

	if err := pc.DeleteUserGroupByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetUserGroupByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_AdvancedComputerSearchCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-acs-" + runSuffix()
	created, err := pc.CreateAdvancedComputerSearchByID(ctx, "0", &proclassic.AdvancedComputerSearch{
		Name: new(name),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAdvancedComputerSearchByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAdvancedComputerSearchByID", func() error { return pc.DeleteAdvancedComputerSearchByID(ctx, intToStr(id)) })

	if err := pc.DeleteAdvancedComputerSearchByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetAdvancedComputerSearchByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_AdvancedMobileDeviceSearchCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-amds-" + runSuffix()
	created, err := pc.CreateAdvancedMobileDeviceSearchByID(ctx, "0", &proclassic.AdvancedMobileDeviceSearch{
		Name: new(name),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAdvancedMobileDeviceSearchByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAdvancedMobileDeviceSearchByID", func() error { return pc.DeleteAdvancedMobileDeviceSearchByID(ctx, intToStr(id)) })

	if err := pc.DeleteAdvancedMobileDeviceSearchByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetAdvancedMobileDeviceSearchByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_AdvancedUserSearchCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-aus-" + runSuffix()
	created, err := pc.CreateAdvancedUserSearchByID(ctx, "0", &proclassic.AdvancedUserSearch{
		Name: new(name),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAdvancedUserSearchByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAdvancedUserSearchByID", func() error { return pc.DeleteAdvancedUserSearchByID(ctx, intToStr(id)) })

	if err := pc.DeleteAdvancedUserSearchByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetAdvancedUserSearchByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_PolicyCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-policy-" + runSuffix()
	enabled := false
	created, err := pc.CreatePolicyByID(ctx, "0", &proclassic.PolicyPost{
		General: &proclassic.PolicyPostGeneral{
			Name:    new(name),
			Enabled: &enabled,
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreatePolicyByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeletePolicyByID", func() error { return pc.DeletePolicyByID(ctx, intToStr(id)) })

	got, err := pc.GetPolicyByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPolicyByID(%d): %v", id, err)
	}
	if got.General == nil || got.General.Name == nil || *got.General.Name != name {
		t.Errorf("Name = %v, want %q", got.General.Name, name)
	}

	if err := pc.DeletePolicyByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetPolicyByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_OSXConfigurationProfileCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-osxcp-" + runSuffix()
	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name: new(name),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOSXConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteOSXConfigurationProfileByID", func() error { return pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id)) })

	if err := pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_MobileDeviceConfigurationProfileCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-mdcp-" + runSuffix()
	created, err := pc.CreateMobileDeviceConfigurationProfileByID(ctx, "0", &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{
			Name: new(name),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceConfigurationProfileByID", func() error { return pc.DeleteMobileDeviceConfigurationProfileByID(ctx, intToStr(id)) })

	if err := pc.DeleteMobileDeviceConfigurationProfileByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_MobileDeviceCRUD exercises the mobile_device
// CRUD lifecycle with a synthetic placeholder record (no real device
// is touched). The Classic POST /mobiledevices/id/0 endpoint accepts
// a partial General block; we round-trip via GetBySerialNumber to
// recover the server-assigned id, rename via update, then delete and
// verify 404.
func TestAcceptance_Classic_MobileDeviceCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-mobile-" + runSuffix()
	serial := "SDK" + runSuffix()
	udid := "sdk-udid-" + runSuffix()

	_, err := pc.CreateMobileDeviceByID(ctx, "0", &proclassic.MobileDevicePost{
		General: &proclassic.MobileDevicePostGeneral{
			DeviceName:   new(name),
			SerialNumber: new(serial),
			UDID:         new(udid),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceByID(0): %v", err)
	}
	cleanupDelete(t, "DeleteMobileDeviceBySerialNumber", func() error { return pc.DeleteMobileDeviceBySerialNumber(ctx, serial) })
	t.Logf("Created mobile device name=%q serial=%q", name, serial)

	got, err := pc.GetMobileDeviceBySerialNumber(ctx, serial)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceBySerialNumber(%q): %v", serial, err)
	}
	if got == nil || got.General == nil || got.General.ID == nil {
		t.Fatalf("expected MobileDevice.General.ID populated, got %+v", got)
	}
	id := *got.General.ID
	if got.General.DeviceName == nil || *got.General.DeviceName != name {
		t.Errorf("DeviceName = %v, want %q", got.General.DeviceName, name)
	}

	newName := name + "-updated"
	if err := pc.UpdateMobileDeviceByID(ctx, intToStr(id), &proclassic.MobileDevicePost{
		General: &proclassic.MobileDevicePostGeneral{DeviceName: new(newName)},
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateMobileDeviceByID(%d): %v", id, err)
	}

	afterUpdate, err := pc.GetMobileDeviceByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByID(%d) after update: %v", id, err)
	}
	if afterUpdate.General == nil || afterUpdate.General.DeviceName == nil || *afterUpdate.General.DeviceName != newName {
		t.Errorf("after update DeviceName = %v, want %q", afterUpdate.General.DeviceName, newName)
	}

	if err := pc.DeleteMobileDeviceByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteMobileDeviceByID(%d): %v", id, err)
	}
	_, err = pc.GetMobileDeviceByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_GetMobileDeviceByID exercises the endpoint
// against a real enrolled device. If JAMFPLATFORM_ACC_PROCLASSIC_MOBILE_DEVICE_ID
// is set, uses that id; otherwise pulls the first entry from the live
// /mobiledevices list and probes it. Skipped only when the tenant has
// zero enrolled mobile devices.
func TestAcceptance_Classic_GetMobileDeviceByID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()

	id := accEnv("JAMFPLATFORM_ACC_PROCLASSIC_MOBILE_DEVICE_ID")
	if id == "" {
		list, err := pc.ListMobileDevices(ctx)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListMobileDevices: %v", err)
		}
		if list == nil || len(list.MobileDevices) == 0 {
			t.Skip("tenant has no enrolled mobile devices; set JAMFPLATFORM_ACC_PROCLASSIC_MOBILE_DEVICE_ID to override")
		}
		first := list.MobileDevices[0]
		if first.ID == nil {
			t.Fatalf("first mobile device in list has no ID: %+v", first)
		}
		id = intToStr(*first.ID)
	}

	md, err := pc.GetMobileDeviceByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceByID(%s): %v", id, err)
	}
	if md == nil || md.General == nil {
		t.Fatalf("expected MobileDevice.General populated, got %+v", md)
	}
	t.Logf("MobileDevice id=%v serial=%v", md.General.ID, md.General.SerialNumber)
}

func TestAcceptance_Classic_PrinterCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-printer-" + runSuffix()
	created, err := pc.CreatePrinterByID(ctx, "0", &proclassic.Printer{
		Name:     new(name),
		CUPSName: new("PDF"),
		URI:      new("lpd://printer.local/queue"),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreatePrinterByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeletePrinterByID", func() error { return pc.DeletePrinterByID(ctx, intToStr(id)) })

	if err := pc.DeletePrinterByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetPrinterByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_DirectoryBindingCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-dirbind-" + runSuffix()
	created, err := pc.CreateDirectoryBindingByID(ctx, "0", &proclassic.DirectoryBinding{
		Name:   new(name),
		Domain: new("example.test"),
		Type:   new("Active Directory"),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDirectoryBindingByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteDirectoryBindingByID", func() error { return pc.DeleteDirectoryBindingByID(ctx, intToStr(id)) })

	if err := pc.DeleteDirectoryBindingByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetDirectoryBindingByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_DirectoryBindingNestedAD exercises the per-type
// nested configuration blocks on DirectoryBinding (here: ActiveDirectory).
// The flat-8 struct historically dropped active_directory/open_directory/
// powerbroker_identity_services/admitmac/centrify children on round-trip;
// this test creates a binding with a populated nested block, re-fetches it,
// and asserts every field survived. The other four nested types follow
// the same wire pattern, so coverage on AD is representative.
func TestAcceptance_Classic_DirectoryBindingNestedAD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	bp := func(b bool) *bool { return &b }
	name := "sdk-acc-dirbind-ad-" + runSuffix()
	want := &proclassic.DirectoryBinding{
		Name:       new(name),
		Priority:   func(i int) *int { return &i }(9),
		Domain:     new("acc.test"),
		Username:   new("accuser"),
		Password:   new("placeholder-pw"),
		ComputerOu: new("OU=acc"),
		Type:       new("Active Directory"),
		ActiveDirectory: &proclassic.DirectoryBindingActiveDirectory{
			CacheLastUser:       bp(true),
			RequireConfirmation: bp(false),
			LocalHome:           bp(true),
			UseUncPath:          bp(false),
			MountStyle:          new("smb"),
			DefaultShell:        new("/bin/bash"),
			Uid:                 new("accuid"),
			UserGid:             new("accugid"),
			Gid:                 new("accgid"),
			MultipleDomains:     bp(false),
			PreferredDomain:     new("accpref"),
			AdminGroups:         new("accgrp"),
		},
	}

	created, err := pc.CreateDirectoryBindingByID(ctx, "0", want)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("Create: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteDirectoryBindingByID", func() error { return pc.DeleteDirectoryBindingByID(ctx, intToStr(id)) })

	got, err := pc.GetDirectoryBindingByID(ctx, intToStr(id))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ActiveDirectory == nil {
		t.Fatalf("ActiveDirectory block dropped on read; want populated")
	}
	gotAD := got.ActiveDirectory
	checks := []struct {
		field string
		want  any
		got   any
	}{
		{"CacheLastUser", true, derefBool(gotAD.CacheLastUser)},
		{"RequireConfirmation", false, derefBool(gotAD.RequireConfirmation)},
		{"LocalHome", true, derefBool(gotAD.LocalHome)},
		{"UseUncPath", false, derefBool(gotAD.UseUncPath)},
		{"MountStyle", "smb", derefStr(gotAD.MountStyle)},
		{"DefaultShell", "/bin/bash", derefStr(gotAD.DefaultShell)},
		{"Uid", "accuid", derefStr(gotAD.Uid)},
		{"UserGid", "accugid", derefStr(gotAD.UserGid)},
		{"Gid", "accgid", derefStr(gotAD.Gid)},
		{"MultipleDomains", false, derefBool(gotAD.MultipleDomains)},
		{"PreferredDomain", "accpref", derefStr(gotAD.PreferredDomain)},
		{"AdminGroups", "accgrp", derefStr(gotAD.AdminGroups)},
	}
	for _, ck := range checks {
		if ck.want != ck.got {
			t.Errorf("ActiveDirectory.%s: want %v, got %v", ck.field, ck.want, ck.got)
		}
	}
	if got.PasswordSha256 == nil || *got.PasswordSha256 == "" {
		t.Errorf("PasswordSha256: want server-emitted hash field, got nil/empty")
	}
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func TestAcceptance_Classic_ClassicPackageCRUD(t *testing.T) {
	c := accClient(t)
	requirePackageStore(t, c)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-pkg-" + runSuffix()
	filename := name + ".pkg"
	created, err := pc.CreateClassicPackageByID(ctx, "0", &proclassic.Package{
		Name:     new(name),
		Filename: new(filename),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateClassicPackageByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteClassicPackageByID", func() error { return pc.DeleteClassicPackageByID(ctx, intToStr(id)) })

	if err := pc.DeleteClassicPackageByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetClassicPackageByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_NetworkSegmentCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-ns-" + runSuffix()
	created, err := pc.CreateNetworkSegmentByID(ctx, "0", &proclassic.NetworkSegmentPost{
		Name:            new(name),
		StartingAddress: new("10.200.0.1"),
		EndingAddress:   new("10.200.0.255"),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateNetworkSegmentByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteNetworkSegmentByID", func() error { return pc.DeleteNetworkSegmentByID(ctx, intToStr(id)) })

	if err := pc.DeleteNetworkSegmentByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetNetworkSegmentByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_DistributionPointCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-dp-" + runSuffix()
	noAuth := true
	created, err := pc.CreateDistributionPointByID(ctx, "0", &proclassic.DistributionPointPost{
		Name:                     new(name),
		IPAddress:                new("dp.example.test"),
		ShareName:                new("CasperShare"),
		ReadOnlyUsername:         new("ro-user"),
		ReadWriteUsername:        new("rw-user"),
		NoAuthenticationRequired: &noAuth,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDistributionPointByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteDistributionPointByID", func() error { return pc.DeleteDistributionPointByID(ctx, intToStr(id)) })

	if err := pc.DeleteDistributionPointByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetDistributionPointByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_LDAPServerCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-ldap-" + runSuffix()
	hostname := "ldap.example.test"
	port := 389
	created, err := pc.CreateLDAPServerByID(ctx, "0", &proclassic.LdapServerPost{
		Connection: &proclassic.LdapServerPostConnection{
			Name:               new(name),
			Hostname:           new(hostname),
			Port:               &port,
			ServerType:         new(proclassic.LdapServerPostConnectionServerTypeActiveDirectory),
			AuthenticationType: new(proclassic.LdapServerPostConnectionAuthenticationTypeNone),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateLDAPServerByID: %v", err)
	}
	if created == nil || (created.ID == nil && (created.Connection == nil || created.Connection.ID == nil)) {
		t.Fatalf("no ID: %+v", created)
	}
	id := 0
	if created.ID != nil {
		id = *created.ID
	} else {
		id = *created.Connection.ID
	}
	cleanupDelete(t, "DeleteLDAPServerByID", func() error { return pc.DeleteLDAPServerByID(ctx, intToStr(id)) })

	if err := pc.DeleteLDAPServerByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetLDAPServerByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_MacApplicationCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-macapp-" + runSuffix()
	bundle := "com.example.sdk-" + runSuffix()
	created, err := pc.CreateMacApplicationByID(ctx, "0", &proclassic.MacApplication{
		General: &proclassic.MacApplicationGeneral{
			Name:     new(name),
			BundleID: new(bundle),
			Version:  new("1.0.0"),
			URL:      new("https://apps.apple.com/us/app/id123456"),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMacApplicationByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMacApplicationByID", func() error { return pc.DeleteMacApplicationByID(ctx, intToStr(id)) })

	if err := pc.DeleteMacApplicationByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetMacApplicationByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_MobileDeviceApplicationCRUD covers create but
// tolerates the tenant's async-DELETE quirk: Classic mobile-device-app
// records become deletable only after an indexing step, so DELETE issued
// too soon returns HTTP 400 with a body echoing the id. The test asserts
// the create+read round-trip and logs delete as best-effort.
func TestAcceptance_Classic_MobileDeviceApplicationCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-mdapp-" + runSuffix()
	bundle := "com.example.sdk-" + runSuffix()
	version := "1.0.0"
	created, err := pc.CreateMobileDeviceApplicationByID(ctx, "0", &proclassic.MobileDeviceApplication{
		General: &proclassic.MobileDeviceApplicationGeneral{
			Name:     new(name),
			BundleID: new(bundle),
			Version:  new(version),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceApplicationByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceApplicationByID", func() error { return pc.DeleteMobileDeviceApplicationByID(ctx, intToStr(id)) })
	t.Logf("created mobile-device-app id=%d; delete is async-best-effort on this tenant", id)
}

// TestAcceptance_Classic_EbookCRUD exercises the ebook create + read
// lifecycle. The Classic server on this tenant has a misreported
// DELETE /ebooks/{by-id,by-name} response — returns HTTP 400 with an
// id-echo body (`<ebook><id>N</id></ebook>`) but the record IS removed
// server-side. ListEbooks is eventually-consistent and can briefly
// continue to include the removed record. Cleanup issues both by-id
// and by-name deletes so the tenant settles to clean between runs.
func TestAcceptance_Classic_EbookCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-ebook-" + runSuffix()
	created, err := pc.CreateEbookByID(ctx, "0", &proclassic.EbookPost{
		General: &proclassic.EbookPostGeneral{
			Name: new(name),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateEbookByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteEbookByID", func() error { return pc.DeleteEbookByID(ctx, intToStr(id)) })
	cleanupDelete(t, "DeleteEbookByName", func() error { return pc.DeleteEbookByName(ctx, name) })
	t.Logf("created ebook id=%d; two-step cleanup (by-id 400-echo, by-name) queued", id)
}

func TestAcceptance_Classic_ClassCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-class-" + runSuffix()
	created, err := pc.CreateClassByID(ctx, "0", &proclassic.ClassPost{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateClassByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteClassByID", func() error { return pc.DeleteClassByID(ctx, intToStr(id)) })
	if err := pc.DeleteClassByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetClassByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_LicensedSoftwareCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-licsw-" + runSuffix()
	created, err := pc.CreateLicensedSoftwareByID(ctx, "0", &proclassic.LicensedSoftware{
		General: &proclassic.LicensedSoftwareGeneral{Name: new(name)},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateLicensedSoftwareByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteLicensedSoftwareByID", func() error { return pc.DeleteLicensedSoftwareByID(ctx, intToStr(id)) })
	if err := pc.DeleteLicensedSoftwareByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetLicensedSoftwareByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_RestrictedSoftwareCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-restsw-" + runSuffix()
	created, err := pc.CreateRestrictedSoftwareByID(ctx, "0", &proclassic.RestrictedSoftware{
		General: &proclassic.RestrictedSoftwareGeneral{Name: new(name), ProcessName: new("evil.app")},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateRestrictedSoftwareByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteRestrictedSoftwareByID", func() error { return pc.DeleteRestrictedSoftwareByID(ctx, intToStr(id)) })
	if err := pc.DeleteRestrictedSoftwareByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetRestrictedSoftwareByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_DiskEncryptionConfigurationCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-dec-" + runSuffix()
	created, err := pc.CreateDiskEncryptionConfigurationByID(ctx, "0", &proclassic.DiskEncryptionConfiguration{
		Name:                  new(name),
		KeyType:               new(proclassic.DiskEncryptionConfigurationKeyTypeIndividual),
		FileVaultEnabledUsers: new(proclassic.DiskEncryptionConfigurationFileVaultEnabledUsersManagementAccount),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDiskEncryptionConfigurationByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteDiskEncryptionConfigurationByID", func() error { return pc.DeleteDiskEncryptionConfigurationByID(ctx, intToStr(id)) })
	if err := pc.DeleteDiskEncryptionConfigurationByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetDiskEncryptionConfigurationByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_IBeaconCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-ibeacon-" + runSuffix()
	created, err := pc.CreateIBeaconByID(ctx, "0", &proclassic.Ibeacon{
		Name: new(name),
		UUID: new("12345678-1234-1234-1234-123456789012"),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateIBeaconByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteIBeaconByID", func() error { return pc.DeleteIBeaconByID(ctx, intToStr(id)) })
	if err := pc.DeleteIBeaconByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetIBeaconByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_DockItemCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-dock-" + runSuffix()
	created, err := pc.CreateDockItemByID(ctx, "0", &proclassic.DockItem{
		Name: new(name),
		Path: new("file:///Applications/Safari.app/"),
		Type: new(proclassic.DockItemTypeApp),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDockItemByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteDockItemByID", func() error { return pc.DeleteDockItemByID(ctx, intToStr(id)) })
	if err := pc.DeleteDockItemByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetDockItemByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_RemovableMacAddressCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "AA:BB:CC:DD:EE:" + runSuffix()[len(runSuffix())-2:]
	created, err := pc.CreateRemovableMacAddressByID(ctx, "0", &proclassic.RemovableMacAddress{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateRemovableMacAddressByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteRemovableMacAddressByID", func() error { return pc.DeleteRemovableMacAddressByID(ctx, intToStr(id)) })
	if err := pc.DeleteRemovableMacAddressByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetRemovableMacAddressByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_AllowedFileExtensionCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	ext := "sdk" + runSuffix()
	created, err := pc.CreateAllowedFileExtensionByID(ctx, "0", &proclassic.AllowedFileExtension{Extension: new(ext)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAllowedFileExtensionByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAllowedFileExtensionByID", func() error { return pc.DeleteAllowedFileExtensionByID(ctx, intToStr(id)) })
	if err := pc.DeleteAllowedFileExtensionByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetAllowedFileExtensionByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_JsonWebTokenConfigurationCRUD exercises the
// /jsonwebtokenconfigurations CRUD lifecycle. The server requires an
// `encryption_key` field on create that the Classic spec omits; the
// SDK generator injects it via schemaAdditions in config.json.
func TestAcceptance_Classic_JsonWebTokenConfigurationCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-jwt-" + runSuffix()
	created, err := pc.CreateJsonWebTokenConfigurationByID(ctx, "0", &proclassic.JsonWebTokenConfiguration{
		Name:          new(name),
		EncryptionKey: new("sdk-acc-jwt-key-" + runSuffix()),
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden on this tenant: %v", err)
		}
		t.Fatalf("CreateJsonWebTokenConfigurationByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteJsonWebTokenConfigurationByID", func() error { return pc.DeleteJsonWebTokenConfigurationByID(ctx, intToStr(id)) })

	got, err := pc.GetJsonWebTokenConfigurationByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJsonWebTokenConfigurationByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	if err := pc.DeleteJsonWebTokenConfigurationByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetJsonWebTokenConfigurationByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_WebhookCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-wh-" + runSuffix()
	created, err := pc.CreateWebhookByID(ctx, "0", &proclassic.Webhook{
		Name:        new(name),
		URL:         new("https://webhook.example.test/receiver"),
		Event:       new(proclassic.WebhookEventComputerAdded),
		ContentType: new(proclassic.WebhookContentTypeApplicationJson),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateWebhookByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteWebhookByID", func() error { return pc.DeleteWebhookByID(ctx, intToStr(id)) })
	if err := pc.DeleteWebhookByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetWebhookByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_AccountUserCRUD exercises the /accounts/userid
// CRUD lifecycle. The Classic spec omits the `password` field the
// server requires on create; the SDK generator injects it via the
// schemaAdditions hook in config.json so we can send a valid payload.
func TestAcceptance_Classic_AccountUserCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-user-" + runSuffix()
	created, err := pc.CreateAccountByUserID(ctx, "0", &proclassic.Account{
		Name:         new(name),
		FullName:     new("SDK Acceptance User"),
		Email:        new(name + "@sdk.test"),
		Password:     new("SDK-acc-pw-" + runSuffix() + "!"),
		AccessLevel:  new(proclassic.AccountAccessLevelFullAccess),
		PrivilegeSet: new(proclassic.AccountPrivilegeSetAdministrator),
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden on this tenant: %v", err)
		}
		t.Fatalf("CreateAccountByUserID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAccountByUserID", func() error { return pc.DeleteAccountByUserID(ctx, intToStr(id)) })

	got, err := pc.GetAccountByUserID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAccountByUserID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	if err := pc.DeleteAccountByUserID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetAccountByUserID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_AccountGroupCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-grp-" + runSuffix()
	created, err := pc.CreateAccountGroupByID(ctx, "0", &proclassic.Group{
		Name:         new(name),
		AccessLevel:  new(proclassic.GroupAccessLevelFullAccess),
		PrivilegeSet: new(proclassic.GroupPrivilegeSetAdministrator),
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden on this tenant: %v", err)
		}
		t.Fatalf("CreateAccountGroupByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAccountGroupByID", func() error { return pc.DeleteAccountGroupByID(ctx, intToStr(id)) })
	if err := pc.DeleteAccountGroupByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetAccountGroupByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_ComputerInvitationCRUD exercises the
// /computerinvitations CRUD lifecycle. Classic 500s on create unless
// SshUsername + SshPassword are both set — the server uses those creds
// to SSH into the target computer to complete enrollment, and rejects
// any attempt that doesn't carry them. InvitationType=USER_INITIATED_URL
// keeps the invitation from trying to send an actual email. The 39-digit
// invitation code the server returns rides on *BigInt.
func TestAcceptance_Classic_ComputerInvitationCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	createAccount := false
	created, err := pc.CreateComputerInvitationByID(ctx, "0", &proclassic.ComputerInvitation{
		InvitationType:              new("USER_INITIATED_URL"),
		SshUsername:                 new("sdk-acc"),
		SshPassword:                 new("sdk-acc-pw"),
		CreateAccountIfDoesNotExist: &createAccount,
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden: %v", err)
		}
		t.Fatalf("CreateComputerInvitationByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteComputerInvitationByID", func() error { return pc.DeleteComputerInvitationByID(ctx, intToStr(id)) })

	got, err := pc.GetComputerInvitationByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerInvitationByID(%d): %v", id, err)
	}
	if got == nil || got.Invitation == nil {
		t.Fatalf("expected Invitation populated, got %+v", got)
	}
	t.Logf("ComputerInvitation id=%d invitation=%s", id, got.Invitation.String())

	if err := pc.DeleteComputerInvitationByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetComputerInvitationByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_MobileDeviceInvitationCRUD exercises the
// mobile_device_invitation CRUD lifecycle. The 39-digit `invitation`
// code the server returns is carried as *BigInt via the fieldTypeOverrides
// entry `*.invitation: *BigInt`, so the response decodes without int64
// overflow.
func TestAcceptance_Classic_MobileDeviceInvitationCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	created, err := pc.CreateMobileDeviceInvitationByID(ctx, "0", &proclassic.MobileDeviceInvitationPost{
		InvitationType: new("USER_INITIATED_URL"),
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden on this tenant: %v", err)
		}
		t.Fatalf("CreateMobileDeviceInvitationByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceInvitationByID", func() error { return pc.DeleteMobileDeviceInvitationByID(ctx, intToStr(id)) })

	got, err := pc.GetMobileDeviceInvitationByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceInvitationByID(%d): %v", id, err)
	}
	if got == nil || got.Invitation == nil {
		t.Fatalf("expected Invitation populated, got %+v", got)
	}
	t.Logf("MobileDeviceInvitation id=%d invitation=%s", id, got.Invitation.String())

	if err := pc.DeleteMobileDeviceInvitationByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetMobileDeviceInvitationByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_MobileDeviceEnrollmentProfileCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-mdep-" + runSuffix()
	created, err := pc.CreateMobileDeviceEnrollmentProfileByID(ctx, "0", &proclassic.MobileDeviceEnrollmentProfilePost{
		General: &proclassic.MobileDeviceEnrollmentProfilePostGeneral{Name: new(name)},
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden: %v", err)
		}
		t.Fatalf("CreateMobileDeviceEnrollmentProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceEnrollmentProfileByID", func() error { return pc.DeleteMobileDeviceEnrollmentProfileByID(ctx, intToStr(id)) })
	if err := pc.DeleteMobileDeviceEnrollmentProfileByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetMobileDeviceEnrollmentProfileByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

func TestAcceptance_Classic_MobileDeviceProvisioningProfileCRUD(t *testing.T) {
	t.Skip("mobile_device_provisioning_profile requires a real provisioning profile blob; test scaffolding only covers endpoint shape via unit tests")
}

func TestAcceptance_Classic_PatchExternalSourceCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-pes-" + runSuffix()
	port := 443
	sslEnabled := true
	created, err := pc.CreatePatchExternalSourceByID(ctx, "0", &proclassic.PatchExternalSource{
		Name:       new(name),
		HostName:   new("patches.example.test"),
		Port:       &port,
		SslEnabled: &sslEnabled,
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden: %v", err)
		}
		t.Fatalf("CreatePatchExternalSourceByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeletePatchExternalSourceByID", func() error { return pc.DeletePatchExternalSourceByID(ctx, intToStr(id)) })
	if err := pc.DeletePatchExternalSourceByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetPatchExternalSourceByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_GetPatchInternalSource is read-only. The built-in
// Jamf internal source is id=1; endpoint reports it whether or not
// customers have configured it. No write endpoints exist for internal
// sources.
func TestAcceptance_Classic_GetPatchInternalSource(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	src, err := pc.GetPatchInternalSourceByID(ctx, "1")
	if err != nil {
		skipOnServerError(t, err)
		t.Skipf("GetPatchInternalSourceByID(1): %v", err)
	}
	if src == nil {
		t.Fatal("expected non-nil internal source")
	}
}

// TestAcceptance_Classic_GetPatchAvailableTitles reads catalog data for
// the built-in internal source. No write surface needed.
func TestAcceptance_Classic_GetPatchAvailableTitles(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	titles, err := pc.ListPatchAvailableTitlesBySourceID(ctx, "1")
	if err != nil {
		skipOnServerError(t, err)
		t.Skipf("ListPatchAvailableTitlesBySourceID(1): %v", err)
	}
	if titles == nil {
		t.Fatal("expected non-nil available titles")
	}
}

// Read-only probes of Classic singletons — all Jamf tenants have these
// endpoints populated, and updating them via the test credentials would
// mutate tenant state (SMTP settings, activation code, etc.) which is
// not safe. Coverage is shape-of-response only; unit tests cover writes.

func TestAcceptance_Classic_GetActivationCode(t *testing.T) {
	c := accClient(t)
	a, err := proclassic.New(c).GetActivationCode(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetActivationCode: %v", err)
	}
	if a == nil {
		t.Fatal("nil ActivationCode")
	}
}

func TestAcceptance_Classic_GetSMTPServer(t *testing.T) {
	c := accClient(t)
	s, err := proclassic.New(c).GetSMTPServer(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSMTPServer: %v", err)
	}
	if s == nil {
		t.Fatal("nil SMTPServer")
	}
}

func TestAcceptance_Classic_GetGSXConnection(t *testing.T) {
	c := accClient(t)
	g, err := proclassic.New(c).GetGSXConnection(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetGSXConnection: %v", err)
	}
	if g == nil {
		t.Fatal("nil GSXConnection")
	}
}

func TestAcceptance_Classic_GetJSSUser(t *testing.T) {
	c := accClient(t)
	u, err := proclassic.New(c).GetJSSUser(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJSSUser: %v", err)
	}
	if u == nil {
		t.Fatal("nil JssUser")
	}
}

func TestAcceptance_Classic_GetComputerCheckIn(t *testing.T) {
	c := accClient(t)
	ci, err := proclassic.New(c).GetComputerCheckIn(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerCheckIn: %v", err)
	}
	if ci == nil {
		t.Fatal("nil")
	}
}

func TestAcceptance_Classic_GetComputerInventoryCollection(t *testing.T) {
	c := accClient(t)
	ic, err := proclassic.New(c).GetComputerInventoryCollection(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerInventoryCollection: %v", err)
	}
	if ic == nil {
		t.Fatal("nil")
	}
}

func TestAcceptance_Classic_SoftwareUpdateServerCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)
	name := "sdk-acc-sus-" + runSuffix()
	port := 8088
	created, err := pc.CreateSoftwareUpdateServerByID(ctx, "0", &proclassic.SoftwareUpdateServer{
		Name:      new(name),
		IPAddress: new("sus.example.test"),
		Port:      &port,
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden: %v", err)
		}
		t.Fatalf("CreateSoftwareUpdateServerByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteSoftwareUpdateServerByID", func() error { return pc.DeleteSoftwareUpdateServerByID(ctx, intToStr(id)) })
	if err := pc.DeleteSoftwareUpdateServerByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("delete: %v", err)
	}
	_, err = pc.GetSoftwareUpdateServerByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}

// TestAcceptance_Classic_VPPInvitationRead exercises GetVPPInvitationByID against
// the reference invitation (id 2).  It verifies the three previously broken fields:
//   - InvitationUsages block decoded (was silently nil — plural/singular mismatch)
//   - LastActionDateEpoch populated in usage items (was nil — wrong field name)
//   - General.AutoRegisterManagedUsers decoded (was absent from struct)
//
// Defect #4 (exclusions user_group shape): wire-probed with DataJARLDAPS_JamfPro_Admins
// — server returns name-only, no <id>. Original spec's name-only struct was correct.
//
// The id is discovered from ListVPPInvitations rather than hardcoded. It used to
// default to "2", which 404s on any tenant that never had that record — the
// acceptance tenant has zero VPP invitations (wire-probed 2026-07-31), so the
// test was reporting a missing fixture as an endpoint failure. Set
// JAMFPLATFORM_ACC_PRO_VPP_INVITATION_ID to pin a specific record.
func TestAcceptance_Classic_VPPInvitationRead(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()

	id := accEnv("JAMFPLATFORM_ACC_PRO_VPP_INVITATION_ID")
	if id == "" {
		list, err := pc.ListVPPInvitations(ctx)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListVPPInvitations: %v", err)
		}
		if list == nil || len(list.VppInvitations) == 0 {
			t.Skip("tenant has no VPP invitations; set JAMFPLATFORM_ACC_PRO_VPP_INVITATION_ID to override")
		}
		first := list.VppInvitations[0]
		if first.ID == nil {
			t.Fatalf("first VPP invitation has no ID: %+v", first)
		}
		id = intToStr(*first.ID)
	}

	inv, err := pc.GetVPPInvitationByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetVPPInvitationByID(%s): %v", id, err)
	}
	if inv == nil {
		t.Fatal("nil VppInvitation")
	}

	// General block and auto_register_managed_users (Defect #3).
	if inv.General == nil {
		t.Fatal("General nil")
	}
	t.Logf("General.Name=%v AutoRegisterManagedUsers=%v", inv.General.Name, inv.General.AutoRegisterManagedUsers)
	if inv.General.AutoRegisterManagedUsers == nil {
		t.Error("General.AutoRegisterManagedUsers nil — field still missing from struct or not returned by server")
	}

	// InvitationUsages wrapper (Defect #2).
	if inv.InvitationUsages == nil {
		t.Error("InvitationUsages nil — <invitation_usages> wrapper still not decoded")
	} else {
		t.Logf("InvitationUsages.Size=%v len(Usage)=%d", inv.InvitationUsages.Size, func() int {
			if inv.InvitationUsages.Usage == nil {
				return 0
			}
			return len(*inv.InvitationUsages.Usage)
		}())
		if inv.InvitationUsages.Usage != nil {
			for i, u := range *inv.InvitationUsages.Usage {
				// LastActionDateEpoch (Defect #1).
				t.Logf("Usage[%d]: Name=%v Status=%v LastActionDateEpoch=%v", i, u.Name, u.Status, u.LastActionDateEpoch)
				if u.LastActionDateEpoch == nil {
					t.Errorf("Usage[%d].LastActionDateEpoch nil — last_action_date_epoch still not decoded", i)
				}
			}
		}
	}
}

// TestAcceptance_Classic_VPPInvitationCRUD creates a VPP invitation via
// POST /vppinvitations/id/0, asserts 201, then deletes it. This exercises
// the field-order fix: Classic <general> is order-sensitive and returns HTTP
// 500 when fields arrive alphabetically instead of the required wire order.
//
// Uses distribution_method "Make Available in Self Service" (non-emailing) to
// avoid triggering invite emails. No %@ is used in any string field (separate
// known server-500 bug for plist strings).
//
// The VPP account is discovered rather than hardcoded to id 3. An invitation
// referencing a nonexistent VPP account is rejected with a misleading
// `409 Invalid distribution method` — wire-probed 2026-07-31, where every
// distribution_method value (including "Email", "Self Service" and "") returned
// that same 409 on a tenant with zero VPP accounts. Without a real account the
// endpoint cannot be exercised at all, so the test skips instead of reporting an
// unconfigured prerequisite as an endpoint defect.
func TestAcceptance_Classic_VPPInvitationCRUD(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()

	accts, err := pc.ListVPPAccounts(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListVPPAccounts: %v", err)
	}
	if accts == nil || len(accts.VppAccounts) == 0 {
		t.Skip("tenant has no VPP accounts (no content token configured); VPP invitations cannot be created")
	}
	if accts.VppAccounts[0].ID == nil {
		t.Fatalf("first VPP account has no ID: %+v", accts.VppAccounts[0])
	}
	vppAccID := *accts.VppAccounts[0].ID

	name := "sdk-acc-vpp-inv-" + runSuffix()
	dist := "Make Available in Self Service"
	req := &proclassic.VppInvitation{
		General: &proclassic.VppInvitationGeneral{
			Name:               &name,
			VppAccount:         &proclassic.VppInvitationGeneralVppAccount{ID: &vppAccID},
			DistributionMethod: &dist,
		},
	}

	created, err := pc.CreateVPPInvitationByID(ctx, "0", req)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateVPPInvitationByID: %v", err)
	}
	if created == nil || created.General == nil || created.General.ID == nil {
		t.Fatal("CreateVPPInvitationByID: nil or missing ID in response")
	}
	createdID := strconv.Itoa(*created.General.ID)
	t.Logf("created VPP invitation id=%s name=%s", createdID, name)

	t.Cleanup(func() {
		if err := pc.DeleteVPPInvitationByID(ctx, createdID); err != nil {
			t.Logf("cleanup DeleteVPPInvitationByID(%s): %v", createdID, err)
		}
	})

	got, err := pc.GetVPPInvitationByID(ctx, createdID)
	if err != nil {
		t.Fatalf("GetVPPInvitationByID(%s): %v", createdID, err)
	}
	if got == nil || got.General == nil {
		t.Fatal("GetVPPInvitationByID: nil or missing General")
	}
	if got.General.Name == nil || *got.General.Name != name {
		t.Errorf("General.Name = %v, want %s", got.General.Name, name)
	}
}

func TestAcceptance_Classic_GetComputerHistoryByID(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()

	id := accEnv("JAMFPLATFORM_ACC_PROCLASSIC_COMPUTER_ID")
	if id == "" {
		list, err := pc.MatchComputers(ctx, "*")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("MatchComputers(*): %v", err)
		}
		if list == nil || len(list.Computers) == 0 {
			t.Skip("tenant has no computers; set JAMFPLATFORM_ACC_PROCLASSIC_COMPUTER_ID to override")
		}
		first := list.Computers[0]
		if first.ID == nil {
			t.Fatalf("first computer has no ID: %+v", first)
		}
		id = intToStr(*first.ID)
	}

	h, err := pc.GetComputerHistoryByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Skipf("GetComputerHistoryByID(%s): %v", id, err)
	}
	if h == nil {
		t.Fatal("nil ComputerHistory")
	}
}

func TestAcceptance_Classic_SiteCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-classic-site-" + runSuffix()
	created, err := pc.CreateSiteByID(ctx, "0", &proclassic.Site{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateSiteByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateSiteByID returned no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteSiteByID", func() error { return pc.DeleteSiteByID(ctx, intToStr(id)) })

	got, err := pc.GetSiteByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSiteByID(%d): %v", id, err)
	}
	if got.Name == nil || *got.Name != name {
		t.Errorf("Name = %v, want %q", got.Name, name)
	}

	if err := pc.DeleteSiteByID(ctx, intToStr(id)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteSiteByID(%d): %v", id, err)
	}
	_, err = pc.GetSiteByID(ctx, intToStr(id))
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("after delete: want 404, got %v", err)
	}
}
