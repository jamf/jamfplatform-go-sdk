// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestMobileDeviceConfigurationProfileSelfServiceCategories_MarshalEmitsDisplayIn
// pins the one property the write requires. The spec declared
// self_service.self_service_categories[].category as a $ref to the shared
// `category` schema ({id, name, priority}), so the generated element type was
// proclassic.Category and had no display_in field at all — a caller could send
// nothing the server would store.
//
// Wire law (Jamf Pro 11.31.1, 2026-09-07, feature_on_main_page toggled in the
// same request as the control): a <category> inside <self_service_categories>
// persists only when it carries <display_in>true</display_in>. <id> alone,
// <id> + <name>, <id> + <feature_in>, and <display_in>false</display_in> are
// all silently discarded — display_in=false is a deletion gesture, not a
// stored value.
func TestMobileDeviceConfigurationProfileSelfServiceCategories_MarshalEmitsDisplayIn(t *testing.T) {
	in := proclassic.MobileDeviceConfigurationProfile{
		SelfService: &proclassic.MobileDeviceConfigurationProfileSelfService{
			FeatureOnMainPage: new(true),
			SelfServiceCategories: &proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategories{
				Category: &[]proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{
					{ID: new(64), Name: new("All Desktops"), DisplayIn: new(true)},
					{ID: new(46), DisplayIn: new(true)},
				},
			},
		},
	}

	buf, err := xml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)

	// The element the server reads must be nested inside <category>, not a
	// sibling of it — a display_in emitted one level up is ignored.
	for _, want := range []string{
		"<self_service_categories>",
		"<category><display_in>true</display_in><id>64</id><name>All Desktops</name></category>",
		"<category><display_in>true</display_in><id>46</id></category>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected substring %q absent in: %s", want, got)
		}
	}
	if n := strings.Count(got, "<display_in>true</display_in>"); n != 2 {
		t.Errorf("<display_in>true</display_in> count = %d, want 2 (payload: %s)", n, got)
	}

	// feature_in is deliberately not on the type: the mobile profile's Self
	// Service tab has no per-category "feature in" control (only the single
	// feature_on_main_page checkbox), the wire discards a <category> carrying
	// it without <display_in>, and the sibling mobile_device_application spec
	// declares display_in with no feature_in at the same position. This guard
	// fails if the field is ever added back without that evidence changing.
	if strings.Contains(got, "<feature_in>") {
		t.Errorf("<feature_in> emitted for a mobile profile category: %s", got)
	}
}

// TestMobileDeviceConfigurationProfileSelfServiceCategories_DecodeWireFixture
// decodes the shape the GET actually echoes. display_in is write-only — the
// read surface returns only <id> and <name> — so this fixture deliberately
// carries no <display_in> and the assertions cannot check it. That asymmetry
// is the reason no client can drift-detect the field.
func TestMobileDeviceConfigurationProfileSelfServiceCategories_DecodeWireFixture(t *testing.T) {
	const wire = `<configuration_profile>
  <general>
    <id>817</id>
    <name>sdk-probe-mdcp-ssc</name>
    <deployment_method>Make Available in Self Service</deployment_method>
  </general>
  <self_service>
    <self_service_description/>
    <security><removal_disallowed>Never</removal_disallowed></security>
    <self_service_icon/>
    <feature_on_main_page>false</feature_on_main_page>
    <self_service_categories>
      <category><id>64</id><name>All Desktops</name></category>
      <category><id>46</id><name>All Laptops</name></category>
    </self_service_categories>
  </self_service>
</configuration_profile>`

	var got proclassic.MobileDeviceConfigurationProfile
	if err := xml.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ss := got.SelfService
	if ss == nil || ss.SelfServiceCategories == nil || ss.SelfServiceCategories.Category == nil {
		t.Fatalf("SelfServiceCategories.Category nil: %+v", ss)
	}
	cats := *ss.SelfServiceCategories.Category
	if len(cats) != 2 {
		t.Fatalf("category count = %d, want 2 (multi-category lost)", len(cats))
	}
	if cats[0].ID == nil || *cats[0].ID != 64 || cats[0].Name == nil || *cats[0].Name != "All Desktops" {
		t.Errorf("category[0] mismatch: %+v %+v", cats[0].ID, cats[0].Name)
	}
	if cats[1].ID == nil || *cats[1].ID != 46 || cats[1].Name == nil || *cats[1].Name != "All Laptops" {
		t.Errorf("category[1] mismatch: %+v %+v", cats[1].ID, cats[1].Name)
	}
	if cats[0].DisplayIn != nil {
		t.Errorf("DisplayIn non-nil from a read body that carries no <display_in>: %+v", cats[0].DisplayIn)
	}
}

// TestMobileDeviceConfigurationProfileSelfServiceCategories_RoundTrip locks the
// Marshal -> Unmarshal cycle, which is what a provider does between plan and
// apply. display_in survives the client-side round trip even though the server
// never returns it.
func TestMobileDeviceConfigurationProfileSelfServiceCategories_RoundTrip(t *testing.T) {
	in := proclassic.MobileDeviceConfigurationProfile{
		SelfService: &proclassic.MobileDeviceConfigurationProfileSelfService{
			SelfServiceCategories: &proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategories{
				Category: &[]proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{
					{ID: new(64), Name: new("All Desktops"), DisplayIn: new(true)},
					{ID: new(46), Name: new("All Laptops"), DisplayIn: new(false)},
				},
			},
		},
	}

	buf, err := xml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out proclassic.MobileDeviceConfigurationProfile
	if err := xml.Unmarshal(buf, &out); err != nil {
		t.Fatalf("unmarshal: %v\npayload: %s", err, buf)
	}

	ss := out.SelfService
	if ss == nil || ss.SelfServiceCategories == nil || ss.SelfServiceCategories.Category == nil {
		t.Fatalf("categories lost: %+v", ss)
	}
	cats := *ss.SelfServiceCategories.Category
	if len(cats) != 2 {
		t.Fatalf("category count = %d, want 2", len(cats))
	}
	if cats[0].DisplayIn == nil || !*cats[0].DisplayIn {
		t.Errorf("category[0].DisplayIn lost: %+v", cats[0].DisplayIn)
	}
	// false must survive as an explicit false rather than collapsing to nil:
	// on the wire it is the gesture that removes the category, so a client
	// that cannot express it cannot unassign one.
	if cats[1].DisplayIn == nil || *cats[1].DisplayIn {
		t.Errorf("category[1].DisplayIn=false lost: %+v", cats[1].DisplayIn)
	}
}
