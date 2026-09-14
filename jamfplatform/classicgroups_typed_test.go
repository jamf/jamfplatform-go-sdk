// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// Locks in that proclassic.UserGroup, proclassic.MobileDeviceGroup, and
// proclassic.ComputerGroup round-trip their `<criteria>` / `<users>` /
// `<mobile_devices>` / `<computers>` wrappers without data loss. The Classic
// spec models these as arrays-of-{size,item}; the actual wire is a single
// wrapper element containing a size sibling and N repeated child entries.
// flattenClassicSizeWrappers in tools/generate/schema.go rewrites that
// pattern to a wrapper-object so every child decodes into a real slice.
// Without that transform Go's encoding/xml collapses every child but the
// last into a single non-slice field — a silent data-loss bug previously
// shared by all three group types.

const userGroupStaticXML = `<user_group>
  <id>3</id>
  <name>Excluded Users</name>
  <is_smart>false</is_smart>
  <is_notify_on_change>false</is_notify_on_change>
  <site><id>-1</id><name>NONE</name></site>
  <criteria><size>0</size></criteria>
  <users>
    <size>1</size>
    <user>
      <id>9</id>
      <username>alice@example.invalid</username>
      <full_name>Alice Example</full_name>
      <phone_number></phone_number>
      <email_address>alice@example.invalid</email_address>
    </user>
  </users>
</user_group>`

const userGroupSmartXML = `<user_group>
  <id>2</id>
  <name>All Managed Apple IDs - example.invalid - VPP Invitation Associated</name>
  <is_smart>true</is_smart>
  <is_notify_on_change>false</is_notify_on_change>
  <site><id>-1</id><name>NONE</name></site>
  <criteria>
    <size>2</size>
    <criterion>
      <name>User Group</name>
      <priority>0</priority>
      <and_or>and</and_or>
      <search_type>member of</search_type>
      <value>All Managed Apple IDs - example.invalid</value>
      <opening_paren>false</opening_paren>
      <closing_paren>false</closing_paren>
    </criterion>
    <criterion>
      <name>VPP Invitation Status</name>
      <priority>1</priority>
      <and_or>and</and_or>
      <search_type>is</search_type>
      <value>Associated</value>
      <opening_paren>false</opening_paren>
      <closing_paren>false</closing_paren>
    </criterion>
  </criteria>
  <users>
    <size>2</size>
    <user>
      <id>6</id>
      <username>bob.appleid@example.invalid</username>
      <full_name></full_name>
      <phone_number></phone_number>
      <email_address></email_address>
    </user>
    <user>
      <id>7</id>
      <username>carol.appleid@example.invalid</username>
      <full_name></full_name>
      <phone_number></phone_number>
      <email_address></email_address>
    </user>
  </users>
</user_group>`

func TestUserGroup_DecodeStatic(t *testing.T) {
	var ug proclassic.UserGroup
	if err := xml.Unmarshal([]byte(userGroupStaticXML), &ug); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ug.ID == nil || *ug.ID != 3 {
		t.Errorf("ID = %v, want 3", ug.ID)
	}
	if ug.IsSmart == nil || *ug.IsSmart {
		t.Errorf("IsSmart = %v, want false", ug.IsSmart)
	}
	if ug.Criteria == nil {
		t.Fatal("Criteria = nil")
	}
	if ug.Criteria.Size == nil || *ug.Criteria.Size != 0 {
		t.Errorf("Criteria.Size = %v, want 0", ug.Criteria.Size)
	}
	if ug.Criteria.Criterion != nil && len(*ug.Criteria.Criterion) != 0 {
		t.Errorf("Criteria.Criterion = %v, want nil/empty", *ug.Criteria.Criterion)
	}
	if ug.Users == nil || ug.Users.User == nil || len(*ug.Users.User) != 1 {
		t.Fatalf("Users.User len = %v, want 1", ug.Users)
	}
	u := (*ug.Users.User)[0]
	if u.Username == nil || *u.Username != "alice@example.invalid" {
		t.Errorf("Users.User[0].Username = %v, want alice@example.invalid", u.Username)
	}
	if u.FullName == nil || *u.FullName != "Alice Example" {
		t.Errorf("Users.User[0].FullName = %v, want Alice Example", u.FullName)
	}
}

func TestUserGroup_DecodeSmart(t *testing.T) {
	var ug proclassic.UserGroup
	if err := xml.Unmarshal([]byte(userGroupSmartXML), &ug); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ug.IsSmart == nil || !*ug.IsSmart {
		t.Errorf("IsSmart = %v, want true", ug.IsSmart)
	}
	if ug.Criteria == nil || ug.Criteria.Criterion == nil {
		t.Fatal("Criteria.Criterion = nil")
	}
	crits := *ug.Criteria.Criterion
	if len(crits) != 2 {
		t.Fatalf("len(Criteria.Criterion) = %d, want 2", len(crits))
	}
	if crits[0].Name == nil || *crits[0].Name != "User Group" {
		t.Errorf("Criteria.Criterion[0].Name = %v, want User Group", crits[0].Name)
	}
	if crits[0].SearchType == nil || *crits[0].SearchType != "member of" {
		t.Errorf("Criteria.Criterion[0].SearchType = %v, want member of", crits[0].SearchType)
	}
	if crits[1].Value == nil || *crits[1].Value != "Associated" {
		t.Errorf("Criteria.Criterion[1].Value = %v, want Associated", crits[1].Value)
	}
	if ug.Users == nil || ug.Users.User == nil {
		t.Fatal("Users.User = nil")
	}
	users := *ug.Users.User
	if len(users) != 2 {
		t.Fatalf("len(Users.User) = %d, want 2", len(users))
	}
	if users[0].Username == nil || *users[0].Username != "bob.appleid@example.invalid" {
		t.Errorf("Users.User[0].Username = %v", users[0].Username)
	}
	if users[1].Username == nil || *users[1].Username != "carol.appleid@example.invalid" {
		t.Errorf("Users.User[1].Username = %v", users[1].Username)
	}
}

func TestUserGroup_MarshalRoundTrip(t *testing.T) {
	ptrStr := func(s string) *string { return &s }
	ptrInt := func(i int) *int { return &i }
	ptrBool := func(b bool) *bool { return &b }
	name := "Group A"
	ug := proclassic.UserGroup{
		Name:    &name,
		IsSmart: ptrBool(true),
		Criteria: &proclassic.UserGroupCriteria{
			Criterion: &[]proclassic.Criterion{
				{
					Name:       ptrStr("Email Address"),
					Priority:   ptrInt(0),
					AndOr:      ptrStr("and"),
					SearchType: ptrStr("like"),
					Value:      ptrStr("company.com"),
				},
				{
					Name:       ptrStr("Phone Number"),
					Priority:   ptrInt(1),
					AndOr:      ptrStr("or"),
					SearchType: ptrStr("like"),
					Value:      ptrStr("555"),
				},
			},
		},
		Users: &proclassic.UserGroupUsers{
			User: &[]proclassic.UserGroupUsersUserItem{
				{
					ID:       ptrInt(42),
					Username: ptrStr("alice"),
				},
				{
					ID:       ptrInt(43),
					Username: ptrStr("bob"),
				},
			},
		},
	}
	out, err := xml.Marshal(&ug)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "<user_group>") {
		t.Errorf("root element = %s, want <user_group>", s[:min(len(s), 32)])
	}
	for _, want := range []string{
		"<name>Group A</name>",
		"<is_smart>true</is_smart>",
		"<criteria>",
		"<criterion>",
		"<name>Email Address</name>",
		"<name>Phone Number</name>",
		"<users>",
		"<user>",
		"<username>alice</username>",
		"<username>bob</username>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("marshal output missing %q\nfull: %s", want, s)
		}
	}

	var decoded proclassic.UserGroup
	if err := xml.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if decoded.Criteria == nil || decoded.Criteria.Criterion == nil || len(*decoded.Criteria.Criterion) != 2 {
		t.Fatalf("re-decoded Criterion count wrong: %+v", decoded.Criteria)
	}
	if decoded.Users == nil || decoded.Users.User == nil || len(*decoded.Users.User) != 2 {
		t.Fatalf("re-decoded User count wrong: %+v", decoded.Users)
	}
}

const mobileDeviceGroupSmartXML = `<mobile_device_group>
  <id>10</id>
  <name>Multi-criteria iPads</name>
  <is_smart>true</is_smart>
  <site><id>-1</id><name>NONE</name></site>
  <criteria>
    <size>2</size>
    <criterion>
      <name>Supervised</name>
      <priority>0</priority>
      <and_or>and</and_or>
      <search_type>is</search_type>
      <value>true</value>
      <opening_paren>false</opening_paren>
      <closing_paren>false</closing_paren>
    </criterion>
    <criterion>
      <name>Model Identifier</name>
      <priority>1</priority>
      <and_or>and</and_or>
      <search_type>like</search_type>
      <value>iPad</value>
      <opening_paren>false</opening_paren>
      <closing_paren>false</closing_paren>
    </criterion>
  </criteria>
  <mobile_devices>
    <mobile_device>
      <id>1</id>
      <name>iPad-1</name>
      <mac_address>02:00:00:00:00:01</mac_address>
      <udid>00000000-0000-0000-0000-000000000001</udid>
      <wifi_mac_address>02:00:00:00:00:01</wifi_mac_address>
      <serial_number>SERIAL0000001</serial_number>
    </mobile_device>
    <mobile_device>
      <id>2</id>
      <name>iPad-2</name>
      <udid>0000000000000000000000000000000000000002</udid>
      <serial_number>SERIAL0000002</serial_number>
    </mobile_device>
  </mobile_devices>
</mobile_device_group>`

func TestMobileDeviceGroup_DecodeSmart(t *testing.T) {
	var g proclassic.MobileDeviceGroup
	if err := xml.Unmarshal([]byte(mobileDeviceGroupSmartXML), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if g.Criteria == nil || g.Criteria.Criterion == nil {
		t.Fatal("Criteria.Criterion = nil")
	}
	crits := *g.Criteria.Criterion
	if len(crits) != 2 {
		t.Fatalf("len(Criteria.Criterion) = %d, want 2", len(crits))
	}
	if crits[0].Name == nil || *crits[0].Name != "Supervised" {
		t.Errorf("Criteria.Criterion[0].Name = %v", crits[0].Name)
	}
	if crits[1].Value == nil || *crits[1].Value != "iPad" {
		t.Errorf("Criteria.Criterion[1].Value = %v", crits[1].Value)
	}
	if g.MobileDevices == nil || g.MobileDevices.MobileDevice == nil {
		t.Fatal("MobileDevices.MobileDevice = nil")
	}
	mds := *g.MobileDevices.MobileDevice
	if len(mds) != 2 {
		t.Fatalf("len(MobileDevices.MobileDevice) = %d, want 2", len(mds))
	}
	if mds[0].UDID == nil || *mds[0].UDID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("MobileDevices.MobileDevice[0].UDID = %v", mds[0].UDID)
	}
	if mds[1].SerialNumber == nil || *mds[1].SerialNumber != "SERIAL0000002" {
		t.Errorf("MobileDevices.MobileDevice[1].SerialNumber = %v", mds[1].SerialNumber)
	}
}

func TestMobileDeviceGroup_MarshalRoundTrip(t *testing.T) {
	ptrStr := func(s string) *string { return &s }
	ptrInt := func(i int) *int { return &i }
	ptrBool := func(b bool) *bool { return &b }
	name := "Static Group"
	g := proclassic.MobileDeviceGroup{
		Name:    &name,
		IsSmart: ptrBool(false),
		MobileDevices: &proclassic.MobileDeviceGroupMobileDevices{
			MobileDevice: &[]proclassic.MobileDeviceGroupMobileDevicesMobileDeviceItem{
				{ID: ptrInt(42), Name: ptrStr("iPhone-42"), SerialNumber: ptrStr("SERIAL42")},
				{ID: ptrInt(43), Name: ptrStr("iPhone-43"), SerialNumber: ptrStr("SERIAL43")},
			},
		},
	}
	out, err := xml.Marshal(&g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "<mobile_device_group>") {
		t.Errorf("root element = %s, want <mobile_device_group>", s[:min(len(s), 40)])
	}
	for _, want := range []string{
		"<mobile_devices>",
		"<mobile_device>",
		"<serial_number>SERIAL42</serial_number>",
		"<serial_number>SERIAL43</serial_number>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("marshal output missing %q\nfull: %s", want, s)
		}
	}

	var decoded proclassic.MobileDeviceGroup
	if err := xml.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if decoded.MobileDevices == nil || decoded.MobileDevices.MobileDevice == nil || len(*decoded.MobileDevices.MobileDevice) != 2 {
		t.Fatalf("re-decoded MobileDevice count wrong: %+v", decoded.MobileDevices)
	}
}

// Verifies the list-endpoint wrapper decodes a list response without data
// loss. The fixture reproduces a real tenant's wire shape with synthetic
// values throughout — no real user, device or tenant identifier appears here. Pre-fix the generated type was `ComputerGroups [][]Item` (Classic
// spec quirk: `computer_groups.items.computer_group: type:array` instead of
// `type:object`), which Go's xml.Unmarshal cannot bind against the flat
// repeated-child wire. Trimmed to four entries — full payload is hundreds.
func TestComputerGroups_ListDecode(t *testing.T) {
	const xmlBody = `<?xml version="1.0" encoding="UTF-8"?>
<computer_groups>
  <size>4</size>
  <computer_group>
    <id>29</id>
    <name>Active Directory Not Bound</name>
    <is_smart>true</is_smart>
  </computer_group>
  <computer_group>
    <id>6947</id>
    <name>Example App 5.1.0 Is Installed</name>
    <is_smart>true</is_smart>
  </computer_group>
  <computer_group>
    <id>8</id>
    <name>Excluded Devices</name>
    <is_smart>false</is_smart>
  </computer_group>
  <computer_group>
    <id>10</id>
    <name>Updated Static Group</name>
    <is_smart>false</is_smart>
  </computer_group>
</computer_groups>`
	var list proclassic.ComputerGroups
	if err := xml.Unmarshal([]byte(xmlBody), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Size == nil || *list.Size != 4 {
		t.Errorf("Size = %v, want 4", list.Size)
	}
	if len(list.ComputerGroups) != 4 {
		t.Fatalf("len(ComputerGroups) = %d, want 4", len(list.ComputerGroups))
	}
	first := list.ComputerGroups[0]
	if first.ID == nil || *first.ID != 29 {
		t.Errorf("ComputerGroups[0].ID = %v, want 29", first.ID)
	}
	if first.Name == nil || *first.Name != "Active Directory Not Bound" {
		t.Errorf("ComputerGroups[0].Name = %v", first.Name)
	}
	if first.IsSmart == nil || !*first.IsSmart {
		t.Errorf("ComputerGroups[0].IsSmart = %v, want true", first.IsSmart)
	}
	last := list.ComputerGroups[3]
	if last.IsSmart == nil || *last.IsSmart {
		t.Errorf("ComputerGroups[3].IsSmart = %v, want false", last.IsSmart)
	}
}

// Locks in that the previously-broken ComputerGroup round-trip is now correct.
// Pre-fix: encoding/xml dropped every criterion except the last. Verified
// empirically before introducing flattenClassicSizeWrappers.
func TestComputerGroup_DecodeSmartCriteria(t *testing.T) {
	const xmlBody = `<computer_group>
  <id>5</id>
  <name>Smart Macs</name>
  <is_smart>true</is_smart>
  <criteria>
    <size>2</size>
    <criterion>
      <name>Operating System</name>
      <priority>0</priority>
      <and_or>and</and_or>
      <search_type>like</search_type>
      <value>14</value>
    </criterion>
    <criterion>
      <name>Building</name>
      <priority>1</priority>
      <and_or>and</and_or>
      <search_type>is</search_type>
      <value>HQ</value>
    </criterion>
  </criteria>
</computer_group>`
	var g proclassic.ComputerGroup
	if err := xml.Unmarshal([]byte(xmlBody), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if g.Criteria == nil || g.Criteria.Criterion == nil {
		t.Fatal("Criteria.Criterion = nil")
	}
	crits := *g.Criteria.Criterion
	if len(crits) != 2 {
		t.Fatalf("len(Criteria.Criterion) = %d, want 2 (pre-fix: 1)", len(crits))
	}
	if crits[0].Name == nil || *crits[0].Name != "Operating System" {
		t.Errorf("Criteria.Criterion[0].Name = %v", crits[0].Name)
	}
	if crits[1].Name == nil || *crits[1].Name != "Building" {
		t.Errorf("Criteria.Criterion[1].Name = %v", crits[1].Name)
	}
}
