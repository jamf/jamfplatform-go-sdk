// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devicegroups"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devices"
)

func TestAcceptance_ListDeviceGroups(t *testing.T) {
	c := accClient(t)

	groups, err := devicegroups.New(c).ListDeviceGroups(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroups failed: %v", err)
	}
	t.Logf("Found %d device groups", len(groups))
}

func TestAcceptance_ListDeviceGroupsWithSort(t *testing.T) {
	c := accClient(t)

	groups, err := devicegroups.New(c).ListDeviceGroups(context.Background(), []string{"name:asc"}, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroups with sort failed: %v", err)
	}
	t.Logf("Found %d device groups (sorted)", len(groups))
}

func TestAcceptance_DeviceGroup_SmartGroupFixture(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accClient(t)

	group, err := devicegroups.New(c).GetDeviceGroup(context.Background(), groupID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceGroup failed: %v", err)
	}
	if group.GroupType != "SMART" {
		t.Errorf("expected SMART, got %q", group.GroupType)
	}
	if group.DeviceType != "COMPUTER" {
		t.Errorf("expected COMPUTER, got %q", group.DeviceType)
	}
	t.Logf("Fixture smart group ID: %s, members: %d", groupID, group.MemberCount)
}

func TestAcceptance_DeviceGroup_CreateAndDeleteStaticGroup(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	suffix := runSuffix()
	dg := devicegroups.New(c)

	name := "sdk-acc-static-group-" + suffix
	desc := "SDK acceptance test — safe to delete"
	emptyMembers := []string{}
	resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        name,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "STATIC",
		Members:     &emptyMembers,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup failed: %v", err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, resp.ID) })

	group, err := dg.GetDeviceGroup(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceGroup failed: %v", err)
	}
	if group.Name != name {
		t.Errorf("expected name %q, got %q", name, group.Name)
	}
	if group.GroupType != "STATIC" {
		t.Errorf("expected STATIC, got %q", group.GroupType)
	}
	t.Logf("Created static group ID: %s", resp.ID)
}

func TestAcceptance_DeviceGroup_UpdateGroup(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	suffix := runSuffix()
	dg := devicegroups.New(c)

	desc := "SDK acceptance test — safe to delete"
	emptyMembers := []string{}
	resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        "sdk-acc-update-original-" + suffix,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "STATIC",
		Members:     &emptyMembers,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup failed: %v", err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, resp.ID) })

	renamedName := "sdk-acc-update-renamed-" + suffix
	updatedDesc := "Updated description"
	err = dg.UpdateDeviceGroup(ctx, resp.ID, &devicegroups.DeviceGroupUpdateRepresentationV1{
		Name:        &renamedName,
		Description: &updatedDesc,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateDeviceGroup failed: %v", err)
	}

	group, err := dg.GetDeviceGroup(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceGroup after update failed: %v", err)
	}
	if group.Name != renamedName {
		t.Errorf("expected name %q, got %q", renamedName, group.Name)
	}
	t.Logf("Updated device group ID: %s", resp.ID)
}

func TestAcceptance_DeviceGroup_SmartGroupWithCriteria(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	suffix := runSuffix()
	dg := devicegroups.New(c)

	name := "sdk-acc-smart-criteria-" + suffix
	desc := "SDK acceptance test smart group — safe to delete"
	criteria := []devicegroups.DeviceGroupCriteriaRepresentationV1{
		{
			Order:          0,
			AttributeName:  "Serial Number",
			Operator:       "LIKE",
			AttributeValue: "",
			JoinType:       "AND",
		},
	}
	resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        name,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "SMART",
		Criteria:    &criteria,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup failed: %v", err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, resp.ID) })

	group, err := dg.GetDeviceGroup(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceGroup failed: %v", err)
	}
	if group.GroupType != "SMART" {
		t.Errorf("expected SMART, got %q", group.GroupType)
	}
	if group.Criteria == nil || len(*group.Criteria) != 1 {
		t.Errorf("expected 1 criterion, got %v", group.Criteria)
	}
	t.Logf("Created smart group ID: %s, members: %d", resp.ID, group.MemberCount)
}

func TestAcceptance_DeviceGroup_PartialUpdatePreservesCriteria(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	suffix := runSuffix()
	dg := devicegroups.New(c)

	name := "sdk-acc-partial-criteria-" + suffix
	desc := "SDK acceptance test — safe to delete"
	criteria := []devicegroups.DeviceGroupCriteriaRepresentationV1{
		{
			Order:          0,
			AttributeName:  "Serial Number",
			Operator:       "LIKE",
			AttributeValue: "",
			JoinType:       "AND",
		},
	}
	resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        name,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "SMART",
		Criteria:    &criteria,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup failed: %v", err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, resp.ID) })

	group, err := dg.GetDeviceGroup(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceGroup failed: %v", err)
	}
	if group.Criteria == nil || len(*group.Criteria) != 1 {
		t.Fatalf("expected 1 criterion after creation, got %v", group.Criteria)
	}

	// Update only the name — omit Criteria entirely.
	// Before the fix, this would serialize "criteria":[] and wipe them.
	renamedName := "sdk-acc-partial-criteria-renamed-" + suffix
	err = dg.UpdateDeviceGroup(ctx, resp.ID, &devicegroups.DeviceGroupUpdateRepresentationV1{
		Name: &renamedName,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateDeviceGroup (partial) failed: %v", err)
	}

	updated, err := dg.GetDeviceGroup(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceGroup after partial update failed: %v", err)
	}
	if updated.Name != renamedName {
		t.Errorf("expected name %q, got %q", renamedName, updated.Name)
	}
	if updated.Criteria == nil || len(*updated.Criteria) != 1 {
		t.Errorf("criteria were lost: expected 1 criterion, got %v", updated.Criteria)
	}
	t.Logf("Partial update preserved criteria on device group %s", resp.ID)
}

func TestAcceptance_DeviceGroup_ListMembers(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accClient(t)

	members, err := devicegroups.New(c).ListDeviceGroupMembers(context.Background(), groupID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroupMembers failed: %v", err)
	}
	t.Logf("Fixture group has %d members", len(members))

	// Membership is the busiest unwrap surface downstream —
	// terraform-provider-jamfplatform calls it from device_group Read and
	// Update, the data source, the list resource and the impact cache — so an
	// unnoticed shape flip here would have failed every plan touching a static
	// group. The method accepts either shape now; this is what dates a change.
	// "envelope" is what every passing run of this test has decoded, since the
	// struct decode it used until now accepted nothing else.
	assertListBodyShape(t, c, "device-groups", "v1", "/device-groups/"+groupID+"/members", "envelope")
}

func TestAcceptance_DeviceGroup_UpdateMembers(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accClient(t)
	ctx := context.Background()
	dg := devicegroups.New(c)

	// Use a member from the COMPUTER smart group fixture so the device type
	// is guaranteed to match the COMPUTER static group we create below.
	fixtureMembers, err := dg.ListDeviceGroupMembers(ctx, groupID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroupMembers (fixture) failed: %v", err)
	}
	if len(fixtureMembers) == 0 {
		t.Skip("Smart group fixture has no members — cannot test member updates")
	}
	deviceID := fixtureMembers[0]

	suffix := runSuffix()
	desc := "SDK acceptance test — safe to delete"
	emptyMembers := []string{}
	resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        "sdk-acc-members-" + suffix,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "STATIC",
		Members:     &emptyMembers,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup failed: %v", err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, resp.ID) })

	// Add a device
	addIDs := []string{deviceID}
	err = dg.UpdateDeviceGroupMembers(ctx, resp.ID, &devicegroups.DeviceGroupMemberPatchRepresentationV1{
		Added: &addIDs,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateDeviceGroupMembers (add) failed: %v", err)
	}

	members, err := dg.ListDeviceGroupMembers(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroupMembers failed: %v", err)
	}
	if len(members) != 1 || members[0] != deviceID {
		t.Errorf("expected [%s], got %v", deviceID, members)
	}

	// Remove the device
	removeIDs := []string{deviceID}
	err = dg.UpdateDeviceGroupMembers(ctx, resp.ID, &devicegroups.DeviceGroupMemberPatchRepresentationV1{
		Removed: &removeIDs,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateDeviceGroupMembers (remove) failed: %v", err)
	}

	members, err = dg.ListDeviceGroupMembers(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroupMembers after remove failed: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected empty members, got %v", members)
	}
	t.Logf("Added and removed device %s from group %s", deviceID, resp.ID)
}

func TestAcceptance_DeviceGroup_ListGroupsForDevice(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	d, err := devices.New(c).ListDevices(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDevices failed: %v", err)
	}
	if len(d) == 0 {
		t.Skip("No devices available")
	}

	groups, err := devicegroups.New(c).ListDeviceGroupsForDevice(ctx, d[0].ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceGroupsForDevice failed: %v", err)
	}
	t.Logf("Device %s belongs to %d groups", d[0].ID, len(groups))

	// The second unwrap surface in this package. Same reasoning as
	// TestAcceptance_DeviceGroup_ListMembers.
	assertListBodyShape(t, c, "device-groups", "v1", "/devices/"+d[0].ID+"/device-groups", "envelope")
	for _, g := range groups {
		t.Logf("  %s (%s)", g.GroupName, g.GroupID)
	}
}
