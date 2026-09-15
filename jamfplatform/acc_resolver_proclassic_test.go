// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// assertResolvedID confirms that the ID the Resolve<X>IDByName call returned
// matches the fixture's numeric id. Shared between every direct-mode test
// below so the comparison and diagnostic phrasing stay consistent.
func assertResolvedID(t *testing.T, resolver string, got string, want int) {
	t.Helper()
	if got != strconv.Itoa(want) {
		t.Errorf("%s = %q, want %q", resolver, got, strconv.Itoa(want))
	}
}

// assertResolvedTyped confirms that the Resolve<X>ByName call returned a
// non-nil typed value whose top-level *int ID matches the fixture. The ID
// accessor is passed in because the types themselves vary.
func assertResolvedTyped[T any](t *testing.T, resolver string, got *T, id func(*T) *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s returned nil", resolver)
		return
	}
	gotID := id(got)
	if gotID == nil {
		t.Fatalf("%s returned result with nil ID", resolver)
		return
	}
	if *gotID != want {
		t.Errorf("%s ID = %d, want %d", resolver, *gotID, want)
	}
}

// assertResolverNotFound verifies that an unknown name surfaces as a
// *APIResponseError with status 404 — the shape Classic's /name/{name}
// endpoint produces and the contract every direct-mode resolver should
// preserve when unwrapped.
func assertResolverNotFound(t *testing.T, resolver string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected not-found error, got nil", resolver)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("%s: want *APIResponseError, got %T: %v", resolver, err, err)
	}
	if !apiErr.HasStatus(http.StatusNotFound) {
		t.Errorf("%s: want status 404, got %d: %v", resolver, apiErr.StatusCode, err)
	}
}

func TestAcceptance_Classic_ResolveBuildingByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-bld-" + runSuffix()
	created, err := pc.CreateBuildingByID(ctx, "0", &proclassic.Building{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBuildingByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteBuildingByID", func() error { return pc.DeleteBuildingByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveBuildingIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveBuildingIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveBuildingIDByName", gotID, id)

	gotTyped, err := pc.ResolveBuildingByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveBuildingByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveBuildingByName", gotTyped, func(b *proclassic.Building) *int { return b.ID }, id)

	_, err = pc.ResolveBuildingIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveBuildingIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveCategoryByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-cat-" + runSuffix()
	prio := 5
	created, err := pc.CreateCategoryByID(ctx, "0", &proclassic.Category{Name: new(name), Priority: &prio})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCategoryByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteCategoryByID", func() error { return pc.DeleteCategoryByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveCategoryIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveCategoryIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveCategoryIDByName", gotID, id)

	gotTyped, err := pc.ResolveCategoryByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveCategoryByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveCategoryByName", gotTyped, func(c *proclassic.Category) *int { return c.ID }, id)
}

func TestAcceptance_Classic_ResolveClassByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-cls-" + runSuffix()
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

	gotID, err := pc.ResolveClassIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveClassIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveClassIDByName", gotID, id)

	gotTyped, err := pc.ResolveClassByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveClassByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveClassByName", gotTyped, func(c *proclassic.Class) *int { return c.ID }, id)
}

func TestAcceptance_Classic_ResolveDepartmentByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-dpt-" + runSuffix()
	created, err := pc.CreateDepartmentByID(ctx, "0", &proclassic.Department{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDepartmentByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteDepartmentByID", func() error { return pc.DeleteDepartmentByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveDepartmentIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDepartmentIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveDepartmentIDByName", gotID, id)

	gotTyped, err := pc.ResolveDepartmentByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDepartmentByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveDepartmentByName", gotTyped, func(d *proclassic.Department) *int { return d.ID }, id)
}

func TestAcceptance_Classic_ResolveSiteByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-site-" + runSuffix()
	created, err := pc.CreateSiteByID(ctx, "0", &proclassic.Site{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateSiteByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteSiteByID", func() error { return pc.DeleteSiteByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveSiteIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveSiteIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveSiteIDByName", gotID, id)

	gotTyped, err := pc.ResolveSiteByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveSiteByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveSiteByName", gotTyped, func(s *proclassic.Site) *int { return s.ID }, id)
}

func TestAcceptance_Classic_ResolveScriptByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-scr-" + runSuffix()
	created, err := pc.CreateScriptByID(ctx, "0", &proclassic.Script{
		Name:           new(name),
		ScriptContents: new("#!/bin/sh\necho hello\n"),
		Priority:       new(proclassic.ScriptPriorityAfter),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateScriptByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteScriptByID", func() error { return pc.DeleteScriptByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveScriptIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveScriptIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveScriptIDByName", gotID, id)

	gotTyped, err := pc.ResolveScriptByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveScriptByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveScriptByName", gotTyped, func(s *proclassic.Script) *int { return s.ID }, id)
}

func TestAcceptance_Classic_ResolveDirectoryBindingByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-dirbind-" + runSuffix()
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

	gotID, err := pc.ResolveDirectoryBindingIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDirectoryBindingIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveDirectoryBindingIDByName", gotID, id)

	gotTyped, err := pc.ResolveDirectoryBindingByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDirectoryBindingByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveDirectoryBindingByName", gotTyped, func(d *proclassic.DirectoryBinding) *int { return d.ID }, id)
}

func TestAcceptance_Classic_ResolveDockItemByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-dock-" + runSuffix()
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

	gotID, err := pc.ResolveDockItemIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDockItemIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveDockItemIDByName", gotID, id)

	gotTyped, err := pc.ResolveDockItemByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDockItemByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveDockItemByName", gotTyped, func(d *proclassic.DockItem) *int { return d.ID }, id)
}

func TestAcceptance_Classic_ResolveIBeaconByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-ibcn-" + runSuffix()
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

	gotID, err := pc.ResolveIBeaconIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveIBeaconIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveIBeaconIDByName", gotID, id)

	gotTyped, err := pc.ResolveIBeaconByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveIBeaconByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveIBeaconByName", gotTyped, func(i *proclassic.Ibeacon) *int { return i.ID }, id)
}

func TestAcceptance_Classic_ResolveLicensedSoftwareByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-licsw-" + runSuffix()
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

	gotID, err := pc.ResolveLicensedSoftwareIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveLicensedSoftwareIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveLicensedSoftwareIDByName", gotID, id)

	gotTyped, err := pc.ResolveLicensedSoftwareByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveLicensedSoftwareByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveLicensedSoftwareByName", gotTyped, func(l *proclassic.LicensedSoftware) *int {
		if l.General == nil {
			return nil
		}
		return l.General.ID
	}, id)
}

func TestAcceptance_Classic_ResolveRestrictedSoftwareByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-restsw-" + runSuffix()
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

	gotID, err := pc.ResolveRestrictedSoftwareIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveRestrictedSoftwareIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveRestrictedSoftwareIDByName", gotID, id)

	gotTyped, err := pc.ResolveRestrictedSoftwareByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveRestrictedSoftwareByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveRestrictedSoftwareByName", gotTyped, func(r *proclassic.RestrictedSoftware) *int {
		if r.General == nil {
			return nil
		}
		return r.General.ID
	}, id)
}

func TestAcceptance_Classic_ResolvePrinterByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-prt-" + runSuffix()
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

	gotID, err := pc.ResolvePrinterIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolvePrinterIDByName: %v", err)
	}
	assertResolvedID(t, "ResolvePrinterIDByName", gotID, id)

	gotTyped, err := pc.ResolvePrinterByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolvePrinterByName: %v", err)
	}
	assertResolvedTyped(t, "ResolvePrinterByName", gotTyped, func(p *proclassic.Printer) *int { return p.ID }, id)
}

func TestAcceptance_Classic_ResolveAdvancedComputerSearchByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-acs-" + runSuffix()
	created, err := pc.CreateAdvancedComputerSearchByID(ctx, "0", &proclassic.AdvancedComputerSearch{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAdvancedComputerSearchByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAdvancedComputerSearchByID", func() error { return pc.DeleteAdvancedComputerSearchByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveAdvancedComputerSearchIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAdvancedComputerSearchIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveAdvancedComputerSearchIDByName", gotID, id)

	gotTyped, err := pc.ResolveAdvancedComputerSearchByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAdvancedComputerSearchByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveAdvancedComputerSearchByName", gotTyped, func(a *proclassic.AdvancedComputerSearch) *int { return a.ID }, id)
}

func TestAcceptance_Classic_ResolveAdvancedMobileDeviceSearchByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-amds-" + runSuffix()
	created, err := pc.CreateAdvancedMobileDeviceSearchByID(ctx, "0", &proclassic.AdvancedMobileDeviceSearch{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAdvancedMobileDeviceSearchByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAdvancedMobileDeviceSearchByID", func() error { return pc.DeleteAdvancedMobileDeviceSearchByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveAdvancedMobileDeviceSearchIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAdvancedMobileDeviceSearchIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveAdvancedMobileDeviceSearchIDByName", gotID, id)

	gotTyped, err := pc.ResolveAdvancedMobileDeviceSearchByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAdvancedMobileDeviceSearchByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveAdvancedMobileDeviceSearchByName", gotTyped, func(a *proclassic.AdvancedMobileDeviceSearch) *int { return a.ID }, id)
}

func TestAcceptance_Classic_ResolveAdvancedUserSearchByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-aus-" + runSuffix()
	created, err := pc.CreateAdvancedUserSearchByID(ctx, "0", &proclassic.AdvancedUserSearch{Name: new(name)})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAdvancedUserSearchByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteAdvancedUserSearchByID", func() error { return pc.DeleteAdvancedUserSearchByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveAdvancedUserSearchIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAdvancedUserSearchIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveAdvancedUserSearchIDByName", gotID, id)

	gotTyped, err := pc.ResolveAdvancedUserSearchByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAdvancedUserSearchByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveAdvancedUserSearchByName", gotTyped, func(a *proclassic.AdvancedUserSearch) *int { return a.ID }, id)
}

func TestAcceptance_Classic_ResolveComputerGroupByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-cg-" + runSuffix()
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

	// Group reads lag their own writes — see settleUntilFound.
	var gotID string
	settleUntilFound(t, "ResolveComputerGroupIDByName", func() error {
		var err error
		gotID, err = pc.ResolveComputerGroupIDByName(ctx, name)
		return err
	})
	assertResolvedID(t, "ResolveComputerGroupIDByName", gotID, id)

	gotTyped, err := pc.ResolveComputerGroupByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveComputerGroupByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveComputerGroupByName", gotTyped, func(g *proclassic.ComputerGroup) *int { return g.ID }, id)
}

func TestAcceptance_Classic_ResolveMobileDeviceGroupByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-mdg-" + runSuffix()
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

	gotID, err := pc.ResolveMobileDeviceGroupIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceGroupIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveMobileDeviceGroupIDByName", gotID, id)

	gotTyped, err := pc.ResolveMobileDeviceGroupByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceGroupByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveMobileDeviceGroupByName", gotTyped, func(g *proclassic.MobileDeviceGroup) *int { return g.ID }, id)
}

func TestAcceptance_Classic_ResolveUserGroupByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-ug-" + runSuffix()
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

	gotID, err := pc.ResolveUserGroupIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveUserGroupIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveUserGroupIDByName", gotID, id)

	gotTyped, err := pc.ResolveUserGroupByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveUserGroupByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveUserGroupByName", gotTyped, func(g *proclassic.UserGroup) *int { return g.ID }, id)
}

func TestAcceptance_Classic_ResolveComputerExtensionAttributeByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-cea-" + runSuffix()
	created, err := pc.CreateComputerExtensionAttributeByID(ctx, "0", &proclassic.ComputerExtensionAttribute{
		Name:     new(name),
		DataType: new(proclassic.ComputerExtensionAttributeDataTypeString),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerExtensionAttributeByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteComputerExtensionAttributeByID", func() error { return pc.DeleteComputerExtensionAttributeByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveComputerExtensionAttributeIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveComputerExtensionAttributeIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveComputerExtensionAttributeIDByName", gotID, id)

	gotTyped, err := pc.ResolveComputerExtensionAttributeByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveComputerExtensionAttributeByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveComputerExtensionAttributeByName", gotTyped, func(e *proclassic.ComputerExtensionAttribute) *int { return e.ID }, id)
}

func TestAcceptance_Classic_ResolveMobileDeviceExtensionAttributeByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-mdea-" + runSuffix()
	// Note: Classic spec has the typo `date_type` (should be `data_type`) —
	// preserved here because generator tracks the spec verbatim.
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

	gotID, err := pc.ResolveMobileDeviceExtensionAttributeIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceExtensionAttributeIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveMobileDeviceExtensionAttributeIDByName", gotID, id)

	gotTyped, err := pc.ResolveMobileDeviceExtensionAttributeByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceExtensionAttributeByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveMobileDeviceExtensionAttributeByName", gotTyped, func(e *proclassic.MobileDeviceExtensionAttribute) *int { return e.ID }, id)
}

func TestAcceptance_Classic_ResolveUserExtensionAttributeByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-uea-" + runSuffix()
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

	gotID, err := pc.ResolveUserExtensionAttributeIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveUserExtensionAttributeIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveUserExtensionAttributeIDByName", gotID, id)

	gotTyped, err := pc.ResolveUserExtensionAttributeByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveUserExtensionAttributeByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveUserExtensionAttributeByName", gotTyped, func(e *proclassic.UserExtensionAttribute) *int { return e.ID }, id)
}

// TestAcceptance_Classic_ResolveEbookByName creates an ebook, resolves it,
// then queues the Classic tenant's known two-step cleanup (by-id, by-name)
// to work around the server's 400-echo quirk documented in the underlying
// EbookCRUD test. The resolver itself is unaffected by that quirk — the
// GET by-name path is well-behaved.
func TestAcceptance_Classic_ResolveEbookByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-ebk-" + runSuffix()
	created, err := pc.CreateEbookByID(ctx, "0", &proclassic.EbookPost{
		General: &proclassic.EbookPostGeneral{Name: new(name)},
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

	gotID, err := pc.ResolveEbookIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveEbookIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveEbookIDByName", gotID, id)

	gotTyped, err := pc.ResolveEbookByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveEbookByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveEbookByName", gotTyped, func(e *proclassic.Ebook) *int {
		if e.General == nil {
			return nil
		}
		return e.General.ID
	}, id)
}

func TestAcceptance_Classic_ResolveNetworkSegmentByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-ns-" + runSuffix()
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

	gotID, err := pc.ResolveNetworkSegmentIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveNetworkSegmentIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveNetworkSegmentIDByName", gotID, id)

	gotTyped, err := pc.ResolveNetworkSegmentByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveNetworkSegmentByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveNetworkSegmentByName", gotTyped, func(n *proclassic.NetworkSegment) *int { return n.ID }, id)
}

func TestAcceptance_Classic_ResolveMacApplicationByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-macapp-" + runSuffix()
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

	gotID, err := pc.ResolveMacApplicationIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMacApplicationIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveMacApplicationIDByName", gotID, id)

	gotTyped, err := pc.ResolveMacApplicationByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMacApplicationByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveMacApplicationByName", gotTyped, func(m *proclassic.MacApplication) *int {
		if m.General == nil {
			return nil
		}
		return m.General.ID
	}, id)
}

// TestAcceptance_Classic_ResolveMobileDeviceApplicationByName follows the
// same "delete is async-best-effort" caveat documented in the CRUD test —
// create succeeds and the resolver reads cleanly; delete is queued via
// t.Cleanup and may race indexing. The resolver contract is unaffected.
func TestAcceptance_Classic_ResolveMobileDeviceApplicationByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-mdapp-" + runSuffix()
	bundle := "com.example.sdk-" + runSuffix()
	created, err := pc.CreateMobileDeviceApplicationByID(ctx, "0", &proclassic.MobileDeviceApplication{
		General: &proclassic.MobileDeviceApplicationGeneral{
			Name:     new(name),
			BundleID: new(bundle),
			Version:  new("1.0.0"),
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

	gotID, err := pc.ResolveMobileDeviceApplicationIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceApplicationIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveMobileDeviceApplicationIDByName", gotID, id)

	gotTyped, err := pc.ResolveMobileDeviceApplicationByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceApplicationByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveMobileDeviceApplicationByName", gotTyped, func(m *proclassic.MobileDeviceApplication) *int {
		if m.General == nil {
			return nil
		}
		return m.General.ID
	}, id)
}

func TestAcceptance_Classic_ResolveWebhookByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-wh-" + runSuffix()
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

	gotID, err := pc.ResolveWebhookIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveWebhookIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveWebhookIDByName", gotID, id)

	gotTyped, err := pc.ResolveWebhookByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveWebhookByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveWebhookByName", gotTyped, func(w *proclassic.Webhook) *int { return w.ID }, id)
}

func TestAcceptance_Classic_ResolveRemovableMacAddressByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	suffix := runSuffix()
	// MAC-looking name, but the "name" is just a string column on this
	// endpoint — no format enforcement. Keep the form readable.
	name := "AA:BB:CC:DD:EE:" + suffix[len(suffix)-2:]
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

	gotID, err := pc.ResolveRemovableMacAddressIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveRemovableMacAddressIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveRemovableMacAddressIDByName", gotID, id)

	gotTyped, err := pc.ResolveRemovableMacAddressByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveRemovableMacAddressByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveRemovableMacAddressByName", gotTyped, func(r *proclassic.RemovableMacAddress) *int { return r.ID }, id)
}

func TestAcceptance_Classic_ResolvePolicyByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-pol-" + runSuffix()
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

	gotID, err := pc.ResolvePolicyIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolvePolicyIDByName: %v", err)
	}
	assertResolvedID(t, "ResolvePolicyIDByName", gotID, id)

	gotTyped, err := pc.ResolvePolicyByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolvePolicyByName: %v", err)
	}
	assertResolvedTyped(t, "ResolvePolicyByName", gotTyped, func(p *proclassic.Policy) *int {
		if p.General == nil {
			return nil
		}
		return p.General.ID
	}, id)
}

func TestAcceptance_Classic_ResolveDiskEncryptionConfigurationByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-dec-" + runSuffix()
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
	cleanupDelete(t, "DeleteDiskEncryptionConfigurationByID", func() error {
		return pc.DeleteDiskEncryptionConfigurationByID(ctx, intToStr(id))
	})

	gotID, err := pc.ResolveDiskEncryptionConfigurationIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDiskEncryptionConfigurationIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveDiskEncryptionConfigurationIDByName", gotID, id)

	gotTyped, err := pc.ResolveDiskEncryptionConfigurationByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDiskEncryptionConfigurationByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveDiskEncryptionConfigurationByName", gotTyped, func(d *proclassic.DiskEncryptionConfiguration) *int { return d.ID }, id)

	_, err = pc.ResolveDiskEncryptionConfigurationIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveDiskEncryptionConfigurationIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveLDAPServerByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-ldap-" + runSuffix()
	port := 389
	created, err := pc.CreateLDAPServerByID(ctx, "0", &proclassic.LdapServerPost{
		Connection: &proclassic.LdapServerPostConnection{
			Name:               new(name),
			Hostname:           new("ldap.example.test"),
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

	gotID, err := pc.ResolveLDAPServerIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveLDAPServerIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveLDAPServerIDByName", gotID, id)

	gotTyped, err := pc.ResolveLDAPServerByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveLDAPServerByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveLDAPServerByName", gotTyped, func(l *proclassic.LdapServer) *int {
		if l.Connection == nil {
			return nil
		}
		return l.Connection.ID
	}, id)

	_, err = pc.ResolveLDAPServerIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveLDAPServerIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveSoftwareUpdateServerByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-sus-" + runSuffix()
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
			t.Skipf("forbidden on this tenant: %v", err)
		}
		t.Fatalf("CreateSoftwareUpdateServerByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteSoftwareUpdateServerByID", func() error {
		return pc.DeleteSoftwareUpdateServerByID(ctx, intToStr(id))
	})

	gotID, err := pc.ResolveSoftwareUpdateServerIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveSoftwareUpdateServerIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveSoftwareUpdateServerIDByName", gotID, id)

	gotTyped, err := pc.ResolveSoftwareUpdateServerByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveSoftwareUpdateServerByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveSoftwareUpdateServerByName", gotTyped, func(s *proclassic.SoftwareUpdateServer) *int { return s.ID }, id)

	_, err = pc.ResolveSoftwareUpdateServerIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveSoftwareUpdateServerIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveOSXConfigurationProfileByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-osxcp-" + runSuffix()
	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{Name: new(name)},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOSXConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteOSXConfigurationProfileByID", func() error {
		return pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id))
	})

	gotID, err := pc.ResolveOSXConfigurationProfileIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveOSXConfigurationProfileIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveOSXConfigurationProfileIDByName", gotID, id)

	gotTyped, err := pc.ResolveOSXConfigurationProfileByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveOSXConfigurationProfileByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveOSXConfigurationProfileByName", gotTyped, func(p *proclassic.OsXConfigurationProfile) *int {
		if p.General == nil {
			return nil
		}
		return p.General.ID
	}, id)

	_, err = pc.ResolveOSXConfigurationProfileIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveOSXConfigurationProfileIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveMobileDeviceConfigurationProfileByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-mdcp-" + runSuffix()
	created, err := pc.CreateMobileDeviceConfigurationProfileByID(ctx, "0", &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{Name: new(name)},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceConfigurationProfileByID", func() error {
		return pc.DeleteMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	})

	gotID, err := pc.ResolveMobileDeviceConfigurationProfileIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceConfigurationProfileIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveMobileDeviceConfigurationProfileIDByName", gotID, id)

	gotTyped, err := pc.ResolveMobileDeviceConfigurationProfileByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceConfigurationProfileByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveMobileDeviceConfigurationProfileByName", gotTyped, func(p *proclassic.MobileDeviceConfigurationProfile) *int {
		if p.General == nil {
			return nil
		}
		return p.General.ID
	}, id)

	_, err = pc.ResolveMobileDeviceConfigurationProfileIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveMobileDeviceConfigurationProfileIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveDistributionPointByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-dp-" + runSuffix()
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
	cleanupDelete(t, "DeleteDistributionPointByID", func() error {
		return pc.DeleteDistributionPointByID(ctx, intToStr(id))
	})

	gotID, err := pc.ResolveDistributionPointIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDistributionPointIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveDistributionPointIDByName", gotID, id)

	gotTyped, err := pc.ResolveDistributionPointByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDistributionPointByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveDistributionPointByName", gotTyped, func(d *proclassic.DistributionPoint) *int { return d.ID }, id)

	_, err = pc.ResolveDistributionPointIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveDistributionPointIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveMobileDeviceEnrollmentProfileByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-mdep-" + runSuffix()
	created, err := pc.CreateMobileDeviceEnrollmentProfileByID(ctx, "0", &proclassic.MobileDeviceEnrollmentProfilePost{
		General: &proclassic.MobileDeviceEnrollmentProfilePostGeneral{Name: new(name)},
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("forbidden on this tenant: %v", err)
		}
		t.Fatalf("CreateMobileDeviceEnrollmentProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceEnrollmentProfileByID", func() error {
		return pc.DeleteMobileDeviceEnrollmentProfileByID(ctx, intToStr(id))
	})

	gotID, err := pc.ResolveMobileDeviceEnrollmentProfileIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceEnrollmentProfileIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveMobileDeviceEnrollmentProfileIDByName", gotID, id)

	gotTyped, err := pc.ResolveMobileDeviceEnrollmentProfileByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveMobileDeviceEnrollmentProfileByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveMobileDeviceEnrollmentProfileByName", gotTyped, func(p *proclassic.MobileDeviceEnrollmentProfile) *int {
		if p.General == nil {
			return nil
		}
		return p.General.ID
	}, id)

	_, err = pc.ResolveMobileDeviceEnrollmentProfileIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveMobileDeviceEnrollmentProfileIDByName(unknown)", err)
}

// TestAcceptance_Classic_ResolveMobileDeviceProvisioningProfileByName probes
// the resolver wiring with a not-found probe only — creating a real
// provisioning profile requires an actual Apple-signed .mobileprovision blob.
func TestAcceptance_Classic_ResolveMobileDeviceProvisioningProfileByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	_, err := pc.ResolveMobileDeviceProvisioningProfileIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveMobileDeviceProvisioningProfileIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveClassicPackageByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-pkg-" + runSuffix()
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

	gotID, err := pc.ResolveClassicPackageIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveClassicPackageIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveClassicPackageIDByName", gotID, id)

	gotTyped, err := pc.ResolveClassicPackageByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveClassicPackageByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveClassicPackageByName", gotTyped, func(p *proclassic.Package) *int { return p.ID }, id)

	_, err = pc.ResolveClassicPackageIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveClassicPackageIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolvePatchExternalSourceByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-pes-" + runSuffix()
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
			t.Skipf("forbidden on this tenant: %v", err)
		}
		t.Fatalf("CreatePatchExternalSourceByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeletePatchExternalSourceByID", func() error {
		return pc.DeletePatchExternalSourceByID(ctx, intToStr(id))
	})

	gotID, err := pc.ResolvePatchExternalSourceIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolvePatchExternalSourceIDByName: %v", err)
	}
	assertResolvedID(t, "ResolvePatchExternalSourceIDByName", gotID, id)

	gotTyped, err := pc.ResolvePatchExternalSourceByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolvePatchExternalSourceByName: %v", err)
	}
	assertResolvedTyped(t, "ResolvePatchExternalSourceByName", gotTyped, func(p *proclassic.PatchExternalSource) *int { return p.ID }, id)

	_, err = pc.ResolvePatchExternalSourceIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolvePatchExternalSourceIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveAccountGroupByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-grp-" + runSuffix()
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

	gotID, err := pc.ResolveAccountGroupIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAccountGroupIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveAccountGroupIDByName", gotID, id)

	gotTyped, err := pc.ResolveAccountGroupByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveAccountGroupByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveAccountGroupByName", gotTyped, func(g *proclassic.Group) *int { return g.ID }, id)

	_, err = pc.ResolveAccountGroupIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveAccountGroupIDByName(unknown)", err)
}

func TestAcceptance_Classic_ResolveUserByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-rsv-usr-" + runSuffix()
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
		t.Fatalf("no ID: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteUserByID", func() error { return pc.DeleteUserByID(ctx, intToStr(id)) })

	gotID, err := pc.ResolveUserIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveUserIDByName: %v", err)
	}
	assertResolvedID(t, "ResolveUserIDByName", gotID, id)

	gotTyped, err := pc.ResolveUserByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveUserByName: %v", err)
	}
	assertResolvedTyped(t, "ResolveUserByName", gotTyped, func(u *proclassic.User) *int { return u.ID }, id)

	_, err = pc.ResolveUserIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolveUserIDByName(unknown)", err)
}

// TestAcceptance_Classic_ResolvePatchInternalSourceByName probes the resolver
// with a not-found lookup only — PatchInternalSource is read-only (list+get);
// no create/delete available to build a fixture.
func TestAcceptance_Classic_ResolvePatchInternalSourceByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	_, err := pc.ResolvePatchInternalSourceIDByName(ctx, "sdk-acc-nonexistent-"+runSuffix())
	assertResolverNotFound(t, "ResolvePatchInternalSourceIDByName(unknown)", err)
}
