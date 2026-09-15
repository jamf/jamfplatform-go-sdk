// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

// Wire law for mobile_device_configuration_profile.self_service.
// self_service_categories, established on Jamf Pro 11.31.1 (2026-09-07,
// EU tenant, raw XML with feature_on_main_page toggled in the same request
// as the control so every write is proven to have landed):
//
//	sent                                          stored
//	<id> alone                                    discarded
//	<id> + <name>                                 discarded
//	<id> + <feature_in>true                       discarded
//	<id> + <display_in>true                       persisted
//	<id> + <display_in>true + <feature_in>false   persisted
//	<id> + <display_in>false                      discarded
//
// So <display_in>true</display_in> is what makes the server store the
// category at all, and display_in=false is a deletion gesture rather than a
// stored value.
//
// All five sibling Classic resources carrying a self_service_categories block
// were probed in the same session, and the law above is identical on every one
// of them — so it is a property of the Self Service category relation, not a
// quirk of this resource. What IS a quirk of this resource is the read:
//
//	resource                             display_in echoed?  feature_in stored?
//	mobile_device_configuration_profile  NO                  no
//	mobile_device_application            yes                 no
//	os_x_configuration_profile           yes                 yes (default false)
//	policy                               yes                 yes (default false)
//	ebook                                yes                 yes (default false)
//	mac_application                      yes                 yes (default false)
//
// This is the only one of the six that hides display_in from the read, which is
// why the write-only note lives on this type's godoc and is explicitly scoped
// there — a consumer generalising from os_x_configuration_profile would be
// wrong. It is also why feature_in is absent from this type and present on the
// four macOS/ebook ones: both mobile resources store no feature_in at all, and
// mobile_device_application's spec declares none either.
//
// The published spec $refed the shared `category` schema
// ({id, name, priority}) at this position, so the generated element type could
// not express display_in and every write from this SDK was a silent no-op —
// which is what left terraform-provider-jamfplatform's
// jamfplatform_pro_mobile_device_configuration_profile.self_service.categories
// non-functional (provider issue #393).
package jamfplatform_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestAcceptance_Classic_MobileDeviceConfigurationProfileSelfServiceCategories
// is the pair of assertions that proves the wire law rather than the shape: a
// category written WITH display_in=true reads back, and the same category
// written WITHOUT display_in does not. Either one alone would pass against a
// server that ignored the field entirely, or against one that stored every
// category regardless.
//
// Note the read surface echoes only <id> and <name> — display_in is
// write-only — so neither assertion can check display_in itself. Presence in
// the read body is the only observable consequence of having sent it.
func TestAcceptance_Classic_MobileDeviceConfigurationProfileSelfServiceCategories(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	cats, err := pc.ListCategories(ctx)
	skipIfNoFixture(t, "categories", err)
	var catID int
	var catName string
	for _, cat := range cats.Categories {
		if cat.ID == nil || cat.Name == nil || *cat.ID <= 0 {
			continue
		}
		// Avoid %-wrapped template placeholders, which other Classic paths
		// 500 on, so a failure here is never about the fixture's name.
		if !strings.ContainsAny(*cat.Name, "%&?#") {
			catID, catName = *cat.ID, *cat.Name
			break
		}
	}
	if catID == 0 {
		t.Skip("no plainly-named category on tenant")
	}

	name := "sdk-acc-mdcp-ssc-" + runSuffix()
	created, err := pc.CreateMobileDeviceConfigurationProfileByID(ctx, "0", &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{
			Name:             new(name),
			DeploymentMethod: new("Make Available in Self Service"),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := intToStr(*created.ID)
	cleanupDelete(t, "DeleteMobileDeviceConfigurationProfileByID "+id, func() error {
		return pc.DeleteMobileDeviceConfigurationProfileByID(context.Background(), id)
	})

	// readCategoryIDs returns the category IDs the server echoes, plus whether
	// the control field it was told to set came back — a write that did not
	// land must not be read as "the server discarded the category".
	readCategoryIDs := func(wantControl bool) []int {
		t.Helper()
		got, err := pc.GetMobileDeviceConfigurationProfileByIDSubset(ctx, id, "SelfService")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("GetMobileDeviceConfigurationProfileByIDSubset(SelfService): %v", err)
		}
		ss := got.SelfService
		if ss == nil {
			t.Fatalf("SelfService nil in read body: %+v", got)
		}
		if ss.FeatureOnMainPage == nil || *ss.FeatureOnMainPage != wantControl {
			t.Fatalf("control feature_on_main_page = %v, want %v — the write did not land, so the category result proves nothing",
				ss.FeatureOnMainPage, wantControl)
		}
		var ids []int
		if ss.SelfServiceCategories != nil && ss.SelfServiceCategories.Category != nil {
			for _, cat := range *ss.SelfServiceCategories.Category {
				if cat.ID != nil {
					ids = append(ids, *cat.ID)
				}
				// display_in is write-only: assert the server does not start
				// echoing it, so the day it does is a visible change rather
				// than a silently-ignored capability.
				if cat.DisplayIn != nil {
					t.Errorf("display_in echoed on read (%v) — the write-only claim in the type's godoc is stale", *cat.DisplayIn)
				}
			}
		}
		return ids
	}

	write := func(control bool, cat proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem) {
		t.Helper()
		err := pc.UpdateMobileDeviceConfigurationProfileByID(ctx, id, &proclassic.MobileDeviceConfigurationProfile{
			SelfService: &proclassic.MobileDeviceConfigurationProfileSelfService{
				FeatureOnMainPage: new(control),
				SelfServiceCategories: &proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategories{
					Category: &[]proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{cat},
				},
			},
		})
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("UpdateMobileDeviceConfigurationProfileByID(display_in=%v): %v", cat.DisplayIn, err)
		}
	}

	// 1. WITH display_in=true — must read back. This is the assertion the
	//    $ref'd type made impossible to write at all.
	write(true, proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{
		ID: new(catID), Name: new(catName), DisplayIn: new(true),
	})
	if ids := readCategoryIDs(true); len(ids) != 1 || ids[0] != catID {
		t.Fatalf("category %d (%q) written with display_in=true did not persist: read back %v", catID, catName, ids)
	}

	// 2. WITHOUT display_in — must NOT read back. Sent from a clean slate
	//    (step 1's category is cleared first) so the absence is the server
	//    discarding this write rather than the previous one never having
	//    landed.
	write(false, proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{
		ID: new(catID), DisplayIn: new(false),
	})
	if ids := readCategoryIDs(false); len(ids) != 0 {
		t.Fatalf("display_in=false is a deletion gesture on this server; want no categories, read back %v", ids)
	}
	write(true, proclassic.MobileDeviceConfigurationProfileSelfServiceSelfServiceCategoriesCategoryItem{
		ID: new(catID), Name: new(catName),
	})
	if ids := readCategoryIDs(true); len(ids) != 0 {
		t.Fatalf("category written WITHOUT display_in persisted: read back %v — the wire law recorded in this file's comment has changed and the type's godoc is now wrong", ids)
	}
}
