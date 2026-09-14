// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestOsXConfigurationProfileSelfService_DecodeWireFixture verifies the SDK
// decodes the Self Service wire shape Classic actually emits — including the
// fields previously missing from the spec: <self_service_display_name>,
// <security><removal_disallowed>, and the multi-<category> wrapper that the
// spec modelled as a single nested object. Fixture mirrors the captured
// /tmp/ss_probes/09b_multi_category.xml + 13_combined.xml shape.
func TestOsXConfigurationProfileSelfService_DecodeWireFixture(t *testing.T) {
	const wire = `<os_x_configuration_profile>
  <general>
    <id>6100</id>
    <name>tf-acc-ss-probe</name>
    <distribution_method>Make Available in Self Service</distribution_method>
    <level>System</level>
  </general>
  <self_service>
    <self_service_display_name>SS Display Name</self_service_display_name>
    <install_button_text>Get</install_button_text>
    <self_service_description>Long description text here</self_service_description>
    <force_users_to_view_description>true</force_users_to_view_description>
    <security>
      <removal_disallowed>Never</removal_disallowed>
    </security>
    <feature_on_main_page>true</feature_on_main_page>
    <self_service_categories>
      <category>
        <id>64</id>
        <name>All Desktops</name>
        <display_in>true</display_in>
        <feature_in>false</feature_in>
      </category>
      <category>
        <id>46</id>
        <name>All Laptops</name>
        <display_in>true</display_in>
        <feature_in>true</feature_in>
      </category>
    </self_service_categories>
    <notification_subject>S</notification_subject>
    <notification_message>M</notification_message>
  </self_service>
</os_x_configuration_profile>`

	var got proclassic.OsXConfigurationProfile
	if err := xml.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ss := got.SelfService
	if ss == nil {
		t.Fatal("SelfService nil")
	}
	if ss.SelfServiceDisplayName == nil || *ss.SelfServiceDisplayName != "SS Display Name" {
		t.Errorf("SelfServiceDisplayName lost: %+v", ss.SelfServiceDisplayName)
	}
	if ss.Security == nil || ss.Security.RemovalDisallowed == nil || *ss.Security.RemovalDisallowed != "Never" {
		t.Errorf("Security.RemovalDisallowed lost: %+v", ss.Security)
	}
	if ss.SelfServiceCategories == nil || ss.SelfServiceCategories.Category == nil {
		t.Fatal("SelfServiceCategories.Category nil")
	}
	cats := *ss.SelfServiceCategories.Category
	if len(cats) != 2 {
		t.Fatalf("category count = %d, want 2 (multi-category lost)", len(cats))
	}
	if cats[0].ID == nil || *cats[0].ID != 64 || cats[0].Name == nil || *cats[0].Name != "All Desktops" {
		t.Errorf("category[0] mismatch: %+v %+v", cats[0].ID, cats[0].Name)
	}
	if cats[1].ID == nil || *cats[1].ID != 46 || cats[1].FeatureIn == nil || !*cats[1].FeatureIn {
		t.Errorf("category[1] mismatch: %+v feature_in=%+v", cats[1].ID, cats[1].FeatureIn)
	}
}

// TestOsXConfigurationProfileSelfService_MarshalEmits exercises the write
// path: building a SelfService with display_name, security, and two
// categories must emit the corresponding wire elements (terraform-provider-
// jamfplatform's macOS configuration profile resource depends on this).
func TestOsXConfigurationProfileSelfService_MarshalEmits(t *testing.T) {
	in := proclassic.OsXConfigurationProfile{
		SelfService: &proclassic.OsXConfigurationProfileSelfService{
			SelfServiceDisplayName:      new("SS Display Name"),
			ForceUsersToViewDescription: new(true),
			Security: &proclassic.OsXConfigurationProfileSelfServiceSecurity{
				RemovalDisallowed: new("Never"),
			},
			SelfServiceCategories: &proclassic.OsXConfigurationProfileSelfServiceSelfServiceCategories{
				Category: &[]proclassic.OsXConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{
					{ID: new(64), Name: new("All Desktops"), DisplayIn: new(true), FeatureIn: new(false)},
					{ID: new(46), Name: new("All Laptops"), DisplayIn: new(true), FeatureIn: new(true)},
				},
			},
		},
	}

	buf, err := xml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)

	expects := []string{
		"<self_service_display_name>SS Display Name</self_service_display_name>",
		"<security><removal_disallowed>Never</removal_disallowed></security>",
		"<self_service_categories>",
		"<id>64</id>",
		"<name>All Desktops</name>",
		"<id>46</id>",
		"<name>All Laptops</name>",
	}
	if got, want := strings.Count(got, "<category>"), 2; got != want {
		t.Errorf("<category> count = %d, want %d", got, want)
	}
	for _, e := range expects {
		if !strings.Contains(got, e) {
			t.Errorf("expected substring %q absent in: %s", e, got)
		}
	}
}

// TestOsXConfigurationProfileSelfService_RoundTrip locks the full Marshal →
// Unmarshal cycle for the new fields. Equivalent of the
// PROFILE_ROUNDTRIP_REPORT.md done-criterion: marshal(unmarshal(wire))
// reproduces <self_service_display_name>, <security>, and multi <category>.
func TestOsXConfigurationProfileSelfService_RoundTrip(t *testing.T) {
	in := proclassic.OsXConfigurationProfile{
		SelfService: &proclassic.OsXConfigurationProfileSelfService{
			SelfServiceDisplayName: new("RT"),
			Security: &proclassic.OsXConfigurationProfileSelfServiceSecurity{
				RemovalDisallowed: new("With Authorization"),
				Password:          new("hunter2"),
			},
			SelfServiceCategories: &proclassic.OsXConfigurationProfileSelfServiceSelfServiceCategories{
				Category: &[]proclassic.OsXConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{
					{ID: new(1), Name: new("A")},
					{ID: new(2), Name: new("B")},
				},
			},
		},
	}

	buf, err := xml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out proclassic.OsXConfigurationProfile
	if err := xml.Unmarshal(buf, &out); err != nil {
		t.Fatalf("unmarshal: %v\npayload: %s", err, buf)
	}

	ss := out.SelfService
	if ss == nil || ss.SelfServiceDisplayName == nil || *ss.SelfServiceDisplayName != "RT" {
		t.Errorf("display_name lost: %+v", ss)
	}
	if ss.Security == nil || ss.Security.RemovalDisallowed == nil || *ss.Security.RemovalDisallowed != "With Authorization" {
		t.Errorf("security.removal_disallowed lost: %+v", ss.Security)
	}
	if ss.Security == nil || ss.Security.Password == nil || *ss.Security.Password != "hunter2" {
		t.Errorf("security.password lost: %+v", ss.Security)
	}
	if ss.SelfServiceCategories == nil || ss.SelfServiceCategories.Category == nil || len(*ss.SelfServiceCategories.Category) != 2 {
		t.Errorf("categories lost (want 2): %+v", ss.SelfServiceCategories)
	}
}

// TestOsXConfigurationProfileScope_DecodeWireFixture locks in the scope
// wire-shape corrections audited against profile id=11 on a live tenant:
//   - <jss_users><user> (not <users>) at top level
//   - <jss_user_groups><user_group> (not <jss_user_group>) at top level
//   - <exclusions><jss_users><user> (not <jss_user>)
//   - <exclusions><jss_user_groups><user_group> (not <jss_user_group>)
//   - <limitations><network_segments><network_segment> carries <uid>
//
// Without the generator overrides each of these silently dropped data on read
// or sent the wrong element on write.
func TestOsXConfigurationProfileScope_DecodeWireFixture(t *testing.T) {
	const wire = `<os_x_configuration_profile>
  <scope>
    <jss_users>
      <user><id>10</id><name>username</name></user>
    </jss_users>
    <jss_user_groups>
      <user_group><id>451</id><name>zz-probe-ug</name></user_group>
    </jss_user_groups>
    <limitations>
      <network_segments>
        <network_segment>
          <id>1</id>
          <uid>43_1</uid>
          <name>JSPP</name>
        </network_segment>
      </network_segments>
    </limitations>
    <exclusions>
      <jss_users>
        <user><id>10</id><name>username</name></user>
        <user><id>16</id><name>profile.user@example.invalid</name></user>
      </jss_users>
      <jss_user_groups>
        <user_group><id>1</id><name>All Managed Apple IDs</name></user_group>
      </jss_user_groups>
    </exclusions>
  </scope>
</os_x_configuration_profile>`

	var got proclassic.OsXConfigurationProfile
	if err := xml.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sc := got.Scope
	if sc == nil {
		t.Fatal("Scope nil")
	}

	if sc.JssUsers == nil || sc.JssUsers.User == nil || len(*sc.JssUsers.User) != 1 {
		t.Fatalf("scope.JssUsers.User lost: %+v", sc.JssUsers)
	}
	if (*sc.JssUsers.User)[0].ID == nil || *(*sc.JssUsers.User)[0].ID != 10 {
		t.Errorf("scope.JssUsers.User[0].ID mismatch")
	}

	if sc.JssUserGroups == nil || sc.JssUserGroups.UserGroup == nil || len(*sc.JssUserGroups.UserGroup) != 1 {
		t.Fatalf("scope.JssUserGroups.UserGroup lost: %+v", sc.JssUserGroups)
	}
	if (*sc.JssUserGroups.UserGroup)[0].ID == nil || *(*sc.JssUserGroups.UserGroup)[0].ID != 451 {
		t.Errorf("scope.JssUserGroups.UserGroup[0].ID mismatch")
	}

	if sc.Limitations == nil || sc.Limitations.NetworkSegments == nil || sc.Limitations.NetworkSegments.NetworkSegment == nil {
		t.Fatalf("limitations.NetworkSegments lost: %+v", sc.Limitations)
	}
	ns := (*sc.Limitations.NetworkSegments.NetworkSegment)[0]
	if ns.Uid == nil || *ns.Uid != "43_1" {
		t.Errorf("limitations.network_segment.uid lost: %+v", ns)
	}
	if ns.ID == nil || *ns.ID != 1 || ns.Name == nil || *ns.Name != "JSPP" {
		t.Errorf("limitations.network_segment id/name mismatch: %+v", ns)
	}

	if sc.Exclusions == nil || sc.Exclusions.JssUsers == nil || sc.Exclusions.JssUsers.User == nil || len(*sc.Exclusions.JssUsers.User) != 2 {
		t.Fatalf("exclusions.JssUsers.User lost (want 2): %+v", sc.Exclusions)
	}
	if sc.Exclusions.JssUserGroups == nil || sc.Exclusions.JssUserGroups.UserGroup == nil || len(*sc.Exclusions.JssUserGroups.UserGroup) != 1 {
		t.Fatalf("exclusions.JssUserGroups.UserGroup lost: %+v", sc.Exclusions.JssUserGroups)
	}
	if (*sc.Exclusions.JssUserGroups.UserGroup)[0].ID == nil || *(*sc.Exclusions.JssUserGroups.UserGroup)[0].ID != 1 {
		t.Errorf("exclusions.user_group[0].ID mismatch")
	}
}

// TestOsXConfigurationProfileScope_MarshalEmits verifies the corrected wire
// element names are produced on write. Important for the provider — server
// silently ignores anything emitted under the wrong tag.
func TestOsXConfigurationProfileScope_MarshalEmits(t *testing.T) {
	in := proclassic.OsXConfigurationProfile{
		Scope: &proclassic.OsXConfigurationProfileScope{
			JssUsers: &proclassic.OsXConfigurationProfileScopeJssUsers{
				User: &[]proclassic.IDName{{ID: new(10), Name: new("u")}},
			},
			JssUserGroups: &proclassic.OsXConfigurationProfileScopeJssUserGroups{
				UserGroup: &[]proclassic.IDName{{ID: new(451), Name: new("g")}},
			},
			Limitations: &proclassic.OsXConfigurationProfileScopeLimitations{
				NetworkSegments: &proclassic.OsXConfigurationProfileScopeLimitationsNetworkSegments{
					NetworkSegment: &[]proclassic.OsXConfigurationProfileScopeLimitationsNetworkSegmentsNetworkSegmentItem{
						{ID: new(1), Uid: new("43_1"), Name: new("JSPP")},
					},
				},
			},
			Exclusions: &proclassic.OsXConfigurationProfileScopeExclusions{
				JssUsers: &proclassic.OsXConfigurationProfileScopeExclusionsJssUsers{
					User: &[]proclassic.IDName{{ID: new(10), Name: new("u")}, {ID: new(16), Name: new("n")}},
				},
				JssUserGroups: &proclassic.OsXConfigurationProfileScopeExclusionsJssUserGroups{
					UserGroup: &[]proclassic.IDName{{ID: new(1), Name: new("g")}},
				},
			},
		},
	}

	buf, err := xml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)

	expects := []string{
		"<jss_users><user>",
		"<jss_user_groups><user_group><id>451</id>",
		"<exclusions>",
		"<jss_user_groups><user_group>",
		"<network_segment>",
		"<uid>43_1</uid>",
	}
	for _, e := range expects {
		if !strings.Contains(got, e) {
			t.Errorf("expected substring %q absent in: %s", e, got)
		}
	}
	for _, bad := range []string{"<jss_user>", "<jss_user_group>"} {
		if strings.Contains(got, bad) {
			t.Errorf("unwanted legacy element %q present: %s", bad, got)
		}
	}
}
