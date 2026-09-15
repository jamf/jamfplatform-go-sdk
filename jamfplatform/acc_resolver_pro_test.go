// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// ─── Test helpers ───────────────────────────────────────────────────────────

// requireNotFoundErr asserts err is an APIResponseError with status 404.
func requireNotFoundErr(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected not-found error, got nil", label)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("%s: expected APIResponseError(404), got %T: %v", label, err, err)
	}
}

// requireAmbiguousErr asserts err is an AmbiguousMatchError with ≥ 2 matches.
func requireAmbiguousErr(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected ambiguous match error, got nil", label)
	}
	var amErr *jamfplatform.AmbiguousMatchError
	if !errors.As(err, &amErr) {
		t.Fatalf("%s: expected *AmbiguousMatchError, got %T: %v", label, err, err)
	}
	if len(amErr.Matches) < 2 {
		t.Errorf("%s: expected ≥2 matches, got %d: %v", label, len(amErr.Matches), amErr.Matches)
	}
}

// tryCreateDuplicate attempts to create a second resource with the same name.
// If the server rejects duplicates (4xx), it returns ("", false).
// If creation succeeds, it returns (id, true) and registers a t.Cleanup delete.
func tryCreateDuplicate(t *testing.T, label string, createFn func() (string, error), deleteFn func(string) error) (string, bool) {
	t.Helper()
	id, err := createFn()
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("server rejects duplicate %s names (%d) — skipping ambiguous test: %s", label, apiErr.StatusCode, apiErr.Summary())
			return "", false
		}
		t.Fatalf("unexpected error creating duplicate %s: %v", label, err)
	}
	t.Cleanup(func() {
		if err := deleteFn(id); err != nil {
			t.Logf("cleanup duplicate %s %s: %v", label, id, err)
		}
	})
	return id, true
}

// ─── Buildings ──────────────────────────────────────────────────────────────

func TestAcceptance_ResolveBuildingV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-bldg-" + runSuffix()

	// Step 1: Not found
	_, err := c.ResolveBuildingV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create
	resp, err := c.CreateBuildingV1(ctx, &pro.Building{Name: name})
	if err != nil {
		t.Fatalf("CreateBuildingV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteBuildingV1(ctx, id1) })
	t.Logf("step 2: created %s", id1)

	// Step 3: Resolve ID
	gotID, err := c.ResolveBuildingV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveBuildingV1IDByName: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", name, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveBuildingV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveBuildingV1ByName: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}
	t.Log("step 4: resolve typed ✓")

	// Step 5: Attempt duplicate
	id2, dupCreated := tryCreateDuplicate(t, "building", func() (string, error) {
		r, e := c.CreateBuildingV1(ctx, &pro.Building{Name: name})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteBuildingV1(ctx, id) })

	// Step 6: Ambiguous
	if dupCreated {
		_, err = c.ResolveBuildingV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("step 6: ambiguous with IDs %s, %s ✓", id1, id2)

		// Delete duplicate so step 7 can verify single-then-gone
		if err := c.DeleteBuildingV1(ctx, id2); err != nil {
			t.Logf("early delete dup: %v", err)
		}
	}

	// Step 7: Delete original
	if err := c.DeleteBuildingV1(ctx, id1); err != nil {
		t.Fatalf("delete original: %v", err)
	}

	// Step 8: Not found after delete
	_, err = c.ResolveBuildingV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("step 8: not-found after delete ✓")
}

// ─── Categories ─────────────────────────────────────────────────────────────

func TestAcceptance_ResolveCategoryV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-cat-" + runSuffix()

	_, err := c.ResolveCategoryV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	resp, err := c.CreateCategoryV1(ctx, &pro.Category{Name: name, Priority: 9})
	if err != nil {
		t.Fatalf("CreateCategoryV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteCategoryV1(ctx, id1) })

	gotID, err := c.ResolveCategoryV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveCategoryV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "category", func() (string, error) {
		r, e := c.CreateCategoryV1(ctx, &pro.Category{Name: name, Priority: 9})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteCategoryV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveCategoryV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteCategoryV1(ctx, id2)
	}

	if err := c.DeleteCategoryV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = c.ResolveCategoryV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Departments ────────────────────────────────────────────────────────────

func TestAcceptance_ResolveDepartmentV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-dept-" + runSuffix()

	_, err := c.ResolveDepartmentV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	resp, err := c.CreateDepartmentV1(ctx, &pro.Department{Name: name})
	if err != nil {
		t.Fatalf("CreateDepartmentV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteDepartmentV1(ctx, id1) })

	gotID, err := c.ResolveDepartmentV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveDepartmentV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "department", func() (string, error) {
		r, e := c.CreateDepartmentV1(ctx, &pro.Department{Name: name})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteDepartmentV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveDepartmentV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteDepartmentV1(ctx, id2)
	}

	if err := c.DeleteDepartmentV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveDepartmentV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Scripts ────────────────────────────────────────────────────────────────

func TestAcceptance_ResolveScriptV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-script-" + runSuffix()
	contents := "#!/bin/bash\necho hello"

	_, err := c.ResolveScriptV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	resp, err := c.CreateScriptV1(ctx, &pro.Script{Name: name, ScriptContents: &contents})
	if err != nil {
		t.Fatalf("CreateScriptV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteScriptV1(ctx, id1) })

	gotID, err := c.ResolveScriptV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveScriptV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "script", func() (string, error) {
		r, e := c.CreateScriptV1(ctx, &pro.Script{Name: name, ScriptContents: &contents})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteScriptV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveScriptV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteScriptV1(ctx, id2)
	}

	if err := c.DeleteScriptV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveScriptV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Packages ───────────────────────────────────────────────────────────────
// No create endpoint (requires binary upload). Not-found only.

func TestAcceptance_ResolvePackageV1_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolvePackageV1IDByName(context.Background(), "sdk-does-not-exist-pkg-"+runSuffix())
	requireNotFoundErr(t, "ResolvePackageV1IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

// ─── Smart Computer Groups (V2) ────────────────────────────────────────────

// ─── Static Computer Groups (V2) ───────────────────────────────────────────

// ─── Smart Mobile Device Groups (V1) ───────────────────────────────────────

// ─── Static Mobile Device Groups (V1) ──────────────────────────────────────

// ─── Computer Extension Attributes (V1) ────────────────────────────────────

func TestAcceptance_ResolveComputerExtensionAttributeV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-cea-" + runSuffix()

	_, err := c.ResolveComputerExtensionAttributeV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	// manageExistingData is deliberately omitted. The server rejects it on
	// create ("This field should be blank for first time CEA creation") and on
	// any update whose inputType is not SCRIPT with enabled=false — a
	// precondition the spec does not encode. This test previously sent
	// "DELETE_EXISTING_DATA", which is not one of the two accepted values and
	// only passed because the server silently drops enum members it does not
	// recognise; the spec's own values would have produced a 400 here. Verified
	// on the wire 2026-08-03: RETAIN and DELETE round-trip once inputType is
	// SCRIPT and enabled is false.
	newCEA := func(n string) *pro.ComputerExtensionAttributes {
		return &pro.ComputerExtensionAttributes{
			Name: n, Enabled: new(true),
			DataType:             pro.ComputerExtensionAttributesDataTypeString,
			InputType:            pro.ComputerExtensionAttributesInputTypeText,
			InventoryDisplayType: pro.ComputerExtensionAttributesInventoryDisplayTypeGeneral,
			PopupMenuChoices:     &[]string{},
		}
	}
	resp, err := c.CreateComputerExtensionAttributeV1(ctx, newCEA(name))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id1 := resp.ID
	ids1 := []string{id1}
	t.Cleanup(func() { _ = c.DeleteMultipleComputerExtensionAttributesV1(ctx, &pro.Ids{IDs: &ids1}) })

	gotID, err := c.ResolveComputerExtensionAttributeV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveComputerExtensionAttributeV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "computer ext attr", func() (string, error) {
		r, e := c.CreateComputerExtensionAttributeV1(ctx, newCEA(name))
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error {
		ids := []string{id}
		return c.DeleteMultipleComputerExtensionAttributesV1(ctx, &pro.Ids{IDs: &ids})
	})

	if dupCreated {
		_, err = c.ResolveComputerExtensionAttributeV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		ids := []string{id2}
		_ = c.DeleteMultipleComputerExtensionAttributesV1(ctx, &pro.Ids{IDs: &ids})
	}

	if err := c.DeleteMultipleComputerExtensionAttributesV1(ctx, &pro.Ids{IDs: &ids1}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveComputerExtensionAttributeV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Mobile Device Extension Attributes (V1) ───────────────────────────────

func TestAcceptance_ResolveMobileDeviceExtensionAttributeV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-mdea-" + runSuffix()

	_, err := c.ResolveMobileDeviceExtensionAttributeV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	newMDEA := func(n string) *pro.MobileDeviceExtensionAttributes {
		return &pro.MobileDeviceExtensionAttributes{
			Name: n, DataType: pro.MobileDeviceExtensionAttributesDataTypeString, InputType: pro.MobileDeviceExtensionAttributesInputTypeText,
			InventoryDisplayType: pro.MobileDeviceExtensionAttributesInventoryDisplayTypeGeneral, PopupMenuChoices: &[]string{},
		}
	}
	resp, err := c.CreateMobileDeviceExtensionAttributeV1(ctx, newMDEA(name))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteMobileDeviceExtensionAttributeV1(ctx, id1) })

	gotID, err := c.ResolveMobileDeviceExtensionAttributeV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveMobileDeviceExtensionAttributeV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "mobile device ext attr", func() (string, error) {
		r, e := c.CreateMobileDeviceExtensionAttributeV1(ctx, newMDEA(name))
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteMobileDeviceExtensionAttributeV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveMobileDeviceExtensionAttributeV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteMobileDeviceExtensionAttributeV1(ctx, id2)
	}

	if err := c.DeleteMobileDeviceExtensionAttributeV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveMobileDeviceExtensionAttributeV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Platform Groups (V1) ──────────────────────────────────────────────────
// Synced from identity providers — no create endpoint. Read-only probe.

// ─── Computer Inventory (V3) ───────────────────────────────────────────────
// Computers are enrolled, not created via API. Read-only probe.

// ─── Computer Inventory (V4) ───────────────────────────────────────────────
// V4 is the undeprecated successor to V1/V2/V3 (issue #50). The V4 resolvers
// prove the RSQL `filter` the deprecated versions relied on still works on
// the new path — the migration blocker the issue called out was that
// /preview/computers, the only undeprecated alternative, takes no filter.

func TestAcceptance_ResolveComputerInventoryV4_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveComputerInventoryV4IDByName(context.Background(), "sdk-does-not-exist-ci-"+runSuffix())
	requireNotFoundErr(t, "ResolveComputerInventoryV4IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveComputerInventoryV4IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	computers, err := c.ListComputersInventoryV4(ctx, []string{"GENERAL"}, nil, "")
	if err != nil {
		t.Fatalf("ListComputersInventoryV4: %v", err)
	}
	if len(computers) == 0 {
		t.Skip("no computers — skipping")
	}
	// Pick a name that occurs exactly once. The resolver refuses an ambiguous
	// name by design, so a test that grabs computers[0] is asserting uniqueness
	// it never checked — and it fails on any tenant that has two machines with
	// the same name, which is normal: one instance probed on 2026-09-03 had
	// three computers all called "admin's Virtual Machine". That refusal is the
	// resolver working, so the test must supply a resolvable name rather than
	// treat the tenant's data as a bug.
	counts := map[string]int{}
	for _, comp := range computers {
		if comp.General != nil && comp.General.Name != "" {
			counts[comp.General.Name]++
		}
	}
	var unique *pro.ComputerInventoryV4
	for i, comp := range computers {
		if comp.General != nil && counts[comp.General.Name] == 1 {
			unique = &computers[i]
			break
		}
	}
	if unique == nil {
		t.Skipf("no uniquely-named computer among %d — every name is duplicated, so ResolveComputerInventoryV4IDByName cannot succeed by design", len(computers))
	}
	first := *unique
	gotID, err := c.ResolveComputerInventoryV4IDByName(ctx, first.General.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.General.Name, gotID)
}

func TestAcceptance_ResolveComputerInventoryV4IDBySerialNumber_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	computers, err := c.ListComputersInventoryV4(ctx, []string{"HARDWARE"}, nil, "")
	if err != nil {
		t.Fatalf("ListComputersInventoryV4: %v", err)
	}
	if len(computers) == 0 {
		t.Skip("no computers — skipping")
	}
	first := computers[0]
	if first.Hardware == nil || first.Hardware.SerialNumber == "" {
		t.Skip("first computer has no serial number — skipping")
	}
	gotID, err := c.ResolveComputerInventoryV4IDBySerialNumber(ctx, first.Hardware.SerialNumber)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved serial %q → %s ✓", first.Hardware.SerialNumber, gotID)
}

func TestAcceptance_ResolveComputerInventoryV4IDByUDID_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	computers, err := c.ListComputersInventoryV4(ctx, nil, nil, "")
	if err != nil {
		t.Fatalf("ListComputersInventoryV4: %v", err)
	}
	if len(computers) == 0 {
		t.Skip("no computers — skipping")
	}
	first := computers[0]
	if first.UDID == "" {
		t.Skip("first computer has no UDID — skipping")
	}
	gotID, err := c.ResolveComputerInventoryV4IDByUDID(ctx, first.UDID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved UDID %q → %s ✓", first.UDID, gotID)
}

// ─── Sites ─────────────────────────────────────────────────────────────────
// No create/delete endpoint. Read-only probe.

func TestAcceptance_ResolveSiteV1_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveSiteV1IDByName(context.Background(), "sdk-does-not-exist-site-"+runSuffix())
	requireNotFoundErr(t, "ResolveSiteV1IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveSiteV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	sites, err := c.ListSitesV1(ctx)
	if err != nil {
		t.Fatalf("ListSitesV1: %v", err)
	}
	if len(sites) == 0 {
		t.Skip("no sites — skipping")
	}
	first := sites[0]
	gotID, err := c.ResolveSiteV1IDByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.Name, gotID)
}

// ─── Computer Groups (combined V1) ─────────────────────────────────────────
// Combined smart+static list. Create via SmartComputerGroupV3 to test.

func TestAcceptance_ResolveComputerGroupV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-cg-" + runSuffix()

	_, err := c.ResolveComputerGroupV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	// Create a smart computer group — it should appear in the combined list.
	resp, err := c.CreateSmartComputerGroupV3(ctx, &pro.SmartComputerGroupV3{Name: name}, false)
	if err != nil {
		t.Fatalf("CreateSmartComputerGroupV3: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteSmartComputerGroupV3(ctx, id1) })

	// Group reads lag their own writes — see settleUntilFound.
	var gotID string
	settleUntilFound(t, "resolve ID", func() error {
		var err error
		gotID, err = c.ResolveComputerGroupV1IDByName(ctx, name)
		return err
	})
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("resolved %q → %s ✓", name, gotID)

	// Attempt duplicate via static group with same name
	emptyAssignmentsCG := []string{}
	id2, dupCreated := tryCreateDuplicate(t, "computer group (static)", func() (string, error) {
		r, e := c.CreateStaticComputerGroupV3(ctx, &pro.StaticComputerGroupAssignment{Name: name, Assignments: &emptyAssignmentsCG}, false)
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteStaticComputerGroupV3(ctx, id) })

	if dupCreated {
		_, err = c.ResolveComputerGroupV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteStaticComputerGroupV3(ctx, id2)
	}

	if err := c.DeleteSmartComputerGroupV3(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	settleUntilGone(t, "post-delete", func() error {
		_, err := c.ResolveComputerGroupV1IDByName(ctx, name)
		return err
	})
	t.Log("lifecycle complete ✓")
}

// ─── Mobile Device Groups (combined V1) ────────────────────────────────────
// Combined smart+static list. Create via SmartMobileDeviceGroupV1 to test.

// ─── Advanced Mobile Device Searches ────────────────────────────────────────

func TestAcceptance_ResolveAdvancedMobileDeviceSearchV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-amds-" + runSuffix()

	_, err := c.ResolveAdvancedMobileDeviceSearchV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	resp, err := c.CreateAdvancedMobileDeviceSearchV1(ctx, &pro.AdvancedSearch{Name: name})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteAdvancedMobileDeviceSearchV1(ctx, id1) })

	gotID, err := c.ResolveAdvancedMobileDeviceSearchV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveAdvancedMobileDeviceSearchV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "advanced mobile device search", func() (string, error) {
		r, e := c.CreateAdvancedMobileDeviceSearchV1(ctx, &pro.AdvancedSearch{Name: name})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteAdvancedMobileDeviceSearchV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveAdvancedMobileDeviceSearchV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteAdvancedMobileDeviceSearchV1(ctx, id2)
	}

	if err := c.DeleteAdvancedMobileDeviceSearchV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveAdvancedMobileDeviceSearchV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Static User Groups ────────────────────────────────────────────────────
// No create endpoint. Read-only probe.

func TestAcceptance_ResolveStaticUserGroupV1_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveStaticUserGroupV1IDByName(context.Background(), "sdk-does-not-exist-sug-"+runSuffix())
	requireNotFoundErr(t, "ResolveStaticUserGroupV1IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveStaticUserGroupV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	groups, err := c.ListStaticUserGroupsV1(ctx)
	if err != nil {
		t.Fatalf("ListStaticUserGroupsV1: %v", err)
	}
	if len(groups) == 0 {
		t.Skip("no static user groups — skipping")
	}
	first := groups[0]
	gotID, err := c.ResolveStaticUserGroupV1IDByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != strconv.Itoa(first.ID) {
		t.Errorf("resolved id = %q, want %d", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.Name, gotID)
}

// ─── Users ─────────────────────────────────────────────────────────────────

func TestAcceptance_ResolveUserV1_Lifecycle(t *testing.T) {
	t.Skip("Pro users POST+DELETE currently broken at the gateway (server 500) — known exception")
}

// ─── Computer Prestages ────────────────────────────────────────────────────
// Requires deviceEnrollmentProgramInstanceId — read-only probe.

func TestAcceptance_ResolveComputerPrestageV3_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveComputerPrestageV3IDByName(context.Background(), "sdk-does-not-exist-cprest-"+runSuffix())
	requireNotFoundErr(t, "ResolveComputerPrestageV3IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveComputerPrestageV3IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	prestages, err := c.ListComputerPrestagesV3(ctx, nil)
	if err != nil {
		t.Fatalf("ListComputerPrestagesV3: %v", err)
	}
	if len(prestages) == 0 {
		t.Skip("no computer prestages — skipping")
	}
	first := prestages[0]
	gotID, err := c.ResolveComputerPrestageV3IDByName(ctx, first.DisplayName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Logf("resolved %q → %s ✓", first.DisplayName, gotID)
}

// ─── Mobile Device Prestages ───────────────────────────────────────────────
// Requires deviceEnrollmentProgramInstanceId — read-only probe.

func TestAcceptance_ResolveMobileDevicePrestageV3_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveMobileDevicePrestageV3IDByName(context.Background(), "sdk-does-not-exist-mdprest-"+runSuffix())
	requireNotFoundErr(t, "ResolveMobileDevicePrestageV3IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveMobileDevicePrestageV3IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	prestages, err := c.ListMobileDevicePrestagesV3(ctx, nil)
	if err != nil {
		t.Fatalf("ListMobileDevicePrestagesV3: %v", err)
	}
	if len(prestages) == 0 {
		t.Skip("no mobile device prestages — skipping")
	}
	first := prestages[0]
	gotID, err := c.ResolveMobileDevicePrestageV3IDByName(ctx, first.DisplayName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Logf("resolved %q → %s ✓", first.DisplayName, gotID)
}

// ─── Patch Policies ────────────────────────────────────────────────────────
// No create endpoint. Read-only probe.

func TestAcceptance_ResolvePatchPolicyV2_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolvePatchPolicyV2IDByName(context.Background(), "sdk-does-not-exist-pp-"+runSuffix())
	requireNotFoundErr(t, "ResolvePatchPolicyV2IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolvePatchPolicyV2IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	policies, err := c.ListPatchPoliciesV2(ctx, nil, "")
	if err != nil {
		t.Fatalf("ListPatchPoliciesV2: %v", err)
	}
	if len(policies) == 0 {
		t.Skip("no patch policies — skipping")
	}
	first := policies[0]
	gotID, err := c.ResolvePatchPolicyV2IDByName(ctx, first.PolicyName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.PolicyName, gotID)
}

// ─── Distribution Points ───────────────────────────────────────────────────

func TestAcceptance_ResolveDistributionPointV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-dp-" + runSuffix()

	_, err := c.ResolveDistributionPointV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	newDP := func(n string) *pro.DistributionPoint {
		return &pro.DistributionPoint{
			Name: n, FileSharingConnectionType: pro.DistributionPointFileSharingConnectionTypeSmb, ServerName: "localhost",
			ShareName: new("share"), ReadWriteUsername: new("rw"), ReadWritePassword: new("rw"),
			ReadOnlyUsername: new("ro"), ReadOnlyPassword: new("ro"),
		}
	}
	resp, err := c.CreateDistributionPointV1(ctx, newDP(name))
	if err != nil {
		t.Fatalf("CreateDistributionPointV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteDistributionPointV1(ctx, id1) })

	gotID, err := c.ResolveDistributionPointV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("resolved %q → %s ✓", name, gotID)

	id2, dupCreated := tryCreateDuplicate(t, "distribution point", func() (string, error) {
		r, e := c.CreateDistributionPointV1(ctx, newDP(name))
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteDistributionPointV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveDistributionPointV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteDistributionPointV1(ctx, id2)
	}

	if err := c.DeleteDistributionPointV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveDistributionPointV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Ebooks ────────────────────────────────────────────────────────────────
// No create endpoint. Read-only probe.

func TestAcceptance_ResolveEbookV1_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveEbookV1IDByName(context.Background(), "sdk-does-not-exist-ebook-"+runSuffix())
	requireNotFoundErr(t, "ResolveEbookV1IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveEbookV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	ebooks, err := c.ListEbooksV1(ctx, nil)
	if err != nil {
		t.Fatalf("ListEbooksV1: %v", err)
	}
	if len(ebooks) == 0 {
		t.Skip("no ebooks — skipping")
	}
	first := ebooks[0]
	gotID, err := c.ResolveEbookV1IDByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.Name, gotID)
}

// ─── Supervision Identities ────────────────────────────────────────────────

func TestAcceptance_ResolveSupervisionIdentityV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-si-" + runSuffix()

	_, err := c.ResolveSupervisionIdentityV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	newSI := func(n string) *pro.SupervisionIdentityCreate {
		return &pro.SupervisionIdentityCreate{DisplayName: n, Password: "Sdk-Test-Pass-123!"}
	}
	resp, err := c.CreateSupervisionIdentityV1(ctx, newSI(name))
	if err != nil {
		t.Fatalf("CreateSupervisionIdentityV1: %v", err)
	}
	id1 := strconv.Itoa(resp.ID)
	t.Cleanup(func() { _ = c.DeleteSupervisionIdentityV1(ctx, id1) })

	gotID, err := c.ResolveSupervisionIdentityV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("resolved %q → %s ✓", name, gotID)

	got, err := c.ResolveSupervisionIdentityV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.DisplayName != name {
		t.Errorf("typed DisplayName = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "supervision identity", func() (string, error) {
		r, e := c.CreateSupervisionIdentityV1(ctx, newSI(name))
		if e != nil {
			return "", e
		}
		return strconv.Itoa(r.ID), nil
	}, func(id string) error { return c.DeleteSupervisionIdentityV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveSupervisionIdentityV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteSupervisionIdentityV1(ctx, id2)
	}

	if err := c.DeleteSupervisionIdentityV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveSupervisionIdentityV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Volume Purchasing Locations ───────────────────────────────────────────
// Requires VPP service token. Read-only probe.

func TestAcceptance_ResolveVolumePurchasingLocationV1_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveVolumePurchasingLocationV1IDByName(context.Background(), "sdk-does-not-exist-vpl-"+runSuffix())
	requireNotFoundErr(t, "ResolveVolumePurchasingLocationV1IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveVolumePurchasingLocationV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	vpls, err := c.ListVolumePurchasingLocationsV1(ctx, nil, "")
	if err != nil {
		t.Fatalf("ListVolumePurchasingLocationsV1: %v", err)
	}
	if len(vpls) == 0 {
		t.Skip("no volume purchasing locations — skipping")
	}
	first := vpls[0]
	gotID, err := c.ResolveVolumePurchasingLocationV1IDByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.Name, gotID)
}

// ─── Accounts ──────────────────────────────────────────────────────────────

func TestAcceptance_ResolveAccountV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-acct-" + runSuffix()

	_, err := c.ResolveAccountV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	accessLevel := "FullAccess"
	privilegeLevel := "ADMINISTRATOR"
	pass := "Sdk-Test-Pass-123!" + runSuffix()
	acctEmail := name + "@example.invalid"
	siteID := -1
	acctStatus := "Enabled"
	falseVal := false
	ldapServerID := -1
	distinguishedName := ""
	phone := "000-000-0000"
	newAcct := func(n string) *pro.UserAccount {
		realname := "SDK Res " + n
		return &pro.UserAccount{
			Username:                  &n,
			Realname:                  &realname,
			Email:                     &acctEmail,
			Phone:                     &phone,
			AccessLevel:               &accessLevel,
			PrivilegeLevel:            &privilegeLevel,
			PlainPassword:             &pass,
			SiteID:                    &siteID,
			LdapServerID:              &ldapServerID,
			DistinguishedName:         &distinguishedName,
			AccountStatus:             &acctStatus,
			ChangePasswordOnNextLogin: &falseVal,
		}
	}
	resp, err := c.CreateAccountV1(ctx, newAcct(name))
	if err != nil {
		t.Fatalf("CreateAccountV1: %v", err)
	}
	if resp.ID == nil {
		t.Fatal("CreateAccountV1 returned nil ID")
	}
	id1 := *resp.ID
	t.Cleanup(func() { _ = c.DeleteAccountV1(ctx, id1) })

	gotID, err := c.ResolveAccountV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("resolved %q → %s ✓", name, gotID)

	id2, dupCreated := tryCreateDuplicate(t, "account", func() (string, error) {
		r, e := c.CreateAccountV1(ctx, newAcct(name))
		if e != nil {
			return "", e
		}
		if r.ID == nil {
			return "", fmt.Errorf("nil ID from create")
		}
		return *r.ID, nil
	}, func(id string) error { return c.DeleteAccountV1(ctx, id) })

	if dupCreated {
		_, err = c.ResolveAccountV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteAccountV1(ctx, id2)
	}

	if err := c.DeleteAccountV1(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveAccountV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── Enrollment Customizations ─────────────────────────────────────────────

func TestAcceptance_ResolveEnrollmentCustomizationV2_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-ec-" + runSuffix()

	_, err := c.ResolveEnrollmentCustomizationV2IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	newEC := func(n string) *pro.EnrollmentCustomizationV2 {
		return &pro.EnrollmentCustomizationV2{
			DisplayName: n, Description: "SDK test", SiteID: "-1",
			EnrollmentCustomizationBrandingSettings: pro.EnrollmentCustomizationBrandingSettings{
				TextColor: "000000", ButtonColor: "007AFF", ButtonTextColor: "FFFFFF",
				BackgroundColor: "FFFFFF", IconURL: "",
			},
		}
	}
	resp, err := c.CreateEnrollmentCustomizationV2(ctx, newEC(name))
	if err != nil {
		t.Fatalf("CreateEnrollmentCustomizationV2: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteEnrollmentCustomizationV2(ctx, id1) })

	gotID, err := c.ResolveEnrollmentCustomizationV2IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("resolved %q → %s ✓", name, gotID)

	got, err := c.ResolveEnrollmentCustomizationV2ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.DisplayName != name {
		t.Errorf("typed DisplayName = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "enrollment customization", func() (string, error) {
		r, e := c.CreateEnrollmentCustomizationV2(ctx, newEC(name))
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteEnrollmentCustomizationV2(ctx, id) })

	if dupCreated {
		_, err = c.ResolveEnrollmentCustomizationV2IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteEnrollmentCustomizationV2(ctx, id2)
	}

	if err := c.DeleteEnrollmentCustomizationV2(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = c.ResolveEnrollmentCustomizationV2IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("lifecycle complete ✓")
}

// ─── AdvancedUserContentSearches ────────────────────────────────────────────

func TestAcceptance_ResolveAdvancedUserContentSearchV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-aucs-" + runSuffix()

	// Step 1: Not found
	_, err := c.ResolveAdvancedUserContentSearchV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create
	resp, err := c.CreateAdvancedUserContentSearchV1(ctx, &pro.AdvancedUserContentSearch{Name: name})
	if err != nil {
		t.Fatalf("CreateAdvancedUserContentSearchV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteAdvancedUserContentSearchV1(ctx, id1) })
	t.Logf("step 2: created %s", id1)

	// Step 3: Resolve ID
	gotID, err := c.ResolveAdvancedUserContentSearchV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveAdvancedUserContentSearchV1IDByName: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", name, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveAdvancedUserContentSearchV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveAdvancedUserContentSearchV1ByName: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}
	t.Log("step 4: resolve typed ✓")

	// Step 5: Attempt duplicate
	id2, dupCreated := tryCreateDuplicate(t, "advanced user content search", func() (string, error) {
		r, e := c.CreateAdvancedUserContentSearchV1(ctx, &pro.AdvancedUserContentSearch{Name: name})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteAdvancedUserContentSearchV1(ctx, id) })

	// Step 6: Ambiguous
	if dupCreated {
		_, err = c.ResolveAdvancedUserContentSearchV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("step 6: ambiguous with IDs %s, %s ✓", id1, id2)

		if err := c.DeleteAdvancedUserContentSearchV1(ctx, id2); err != nil {
			t.Logf("early delete dup: %v", err)
		}
	}

	// Step 7: Delete original
	if err := c.DeleteAdvancedUserContentSearchV1(ctx, id1); err != nil {
		t.Fatalf("delete original: %v", err)
	}

	// Step 8: Not found after delete
	_, err = c.ResolveAdvancedUserContentSearchV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("step 8: not-found after delete ✓")
}

// ─── InventoryPreloadRecords ────────────────────────────────────────────────

func TestAcceptance_ResolveInventoryPreloadRecordV2_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	suffix := runSuffix()
	if len(suffix) > 6 {
		suffix = suffix[:6]
	}
	serial := "SDKACCRIP" + suffix

	// Step 1: Not found
	_, err := c.ResolveInventoryPreloadRecordV2IDBySerialNumber(ctx, serial)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create
	resp, err := c.CreateInventoryPreloadRecordV2(ctx, &pro.InventoryPreloadRecordV2{SerialNumber: serial, DeviceType: pro.InventoryPreloadRecordV2DeviceTypeComputer})
	if err != nil {
		t.Fatalf("CreateInventoryPreloadRecordV2: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteInventoryPreloadRecordV2(ctx, id1) })
	t.Logf("step 2: created %s (serial=%s)", id1, serial)

	// Step 3: Resolve ID
	gotID, err := c.ResolveInventoryPreloadRecordV2IDBySerialNumber(ctx, serial)
	if err != nil {
		t.Fatalf("ResolveInventoryPreloadRecordV2IDBySerialNumber: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", serial, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveInventoryPreloadRecordV2BySerialNumber(ctx, serial)
	if err != nil {
		t.Fatalf("ResolveInventoryPreloadRecordV2BySerialNumber: %v", err)
	}
	if got == nil || got.SerialNumber != serial {
		t.Errorf("typed SerialNumber = %v, want %q", got, serial)
	}
	t.Log("step 4: resolve typed ✓")

	// Step 5: Attempt duplicate — inventory preload keyed by serial, so duplicate should be rejected
	id2, dupCreated := tryCreateDuplicate(t, "inventory preload record", func() (string, error) {
		r, e := c.CreateInventoryPreloadRecordV2(ctx, &pro.InventoryPreloadRecordV2{SerialNumber: serial, DeviceType: pro.InventoryPreloadRecordV2DeviceTypeComputer})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteInventoryPreloadRecordV2(ctx, id) })

	// Step 6: Ambiguous
	if dupCreated {
		_, err = c.ResolveInventoryPreloadRecordV2IDBySerialNumber(ctx, serial)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("step 6: ambiguous with IDs %s, %s ✓", id1, id2)

		if err := c.DeleteInventoryPreloadRecordV2(ctx, id2); err != nil {
			t.Logf("early delete dup: %v", err)
		}
	}

	// Step 7: Delete original
	if err := c.DeleteInventoryPreloadRecordV2(ctx, id1); err != nil {
		t.Fatalf("delete original: %v", err)
	}

	// Step 8: Not found after delete
	_, err = c.ResolveInventoryPreloadRecordV2IDBySerialNumber(ctx, serial)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("step 8: not-found after delete ✓")
}

// ─── EnrollmentAccessGroups ─────────────────────────────────────────────────

func TestAcceptance_ResolveEnrollmentAccessGroupV3_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-eag-" + runSuffix()

	// Step 1: Not found
	_, err := c.ResolveEnrollmentAccessGroupV3IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create — requires LDAP; may fail if not configured
	resp, err := c.CreateEnrollmentAccessGroupV3(ctx, &pro.EnrollmentAccessGroupPreview{Name: name, GroupID: "1", LdapServerID: "1"})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Skipf("create rejected (LDAP probably not configured): %s", apiErr.Summary())
		}
		t.Fatalf("CreateEnrollmentAccessGroupV3: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteEnrollmentAccessGroupV3(ctx, id1) })
	t.Logf("step 2: created %s", id1)

	// Step 3: Resolve ID
	gotID, err := c.ResolveEnrollmentAccessGroupV3IDByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveEnrollmentAccessGroupV3IDByName: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", name, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveEnrollmentAccessGroupV3ByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveEnrollmentAccessGroupV3ByName: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}
	t.Log("step 4: resolve typed ✓")

	// Step 5: Attempt duplicate
	id2, dupCreated := tryCreateDuplicate(t, "enrollment access group", func() (string, error) {
		r, e := c.CreateEnrollmentAccessGroupV3(ctx, &pro.EnrollmentAccessGroupPreview{Name: name, GroupID: "1", LdapServerID: "1"})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteEnrollmentAccessGroupV3(ctx, id) })

	// Step 6: Ambiguous
	if dupCreated {
		_, err = c.ResolveEnrollmentAccessGroupV3IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("step 6: ambiguous with IDs %s, %s ✓", id1, id2)

		if err := c.DeleteEnrollmentAccessGroupV3(ctx, id2); err != nil {
			t.Logf("early delete dup: %v", err)
		}
	}

	// Step 7: Delete original
	if err := c.DeleteEnrollmentAccessGroupV3(ctx, id1); err != nil {
		t.Fatalf("delete original: %v", err)
	}

	// Step 8: Not found after delete
	_, err = c.ResolveEnrollmentAccessGroupV3IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("step 8: not-found after delete ✓")
}

// ─── ReturnToServiceConfigurations ──────────────────────────────────────────

// ReturnToServiceConfiguration requires a valid wifiProfileId (config profile) for create.
// Read-only probe: verify not-found, then resolve any existing config if present.

func TestAcceptance_ResolveReturnToServiceConfigurationV1IDByName_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveReturnToServiceConfigurationV1IDByName(context.Background(), "sdk-does-not-exist-rts-"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found ✓")
}

func TestAcceptance_ResolveReturnToServiceConfigurationV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	result, err := c.ListReturnToServiceConfigurationsV1(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result == nil || len(result.Results) == 0 {
		t.Skip("no return-to-service configurations — skipping")
	}
	first := result.Results[0]
	gotID, err := c.ResolveReturnToServiceConfigurationV1IDByName(ctx, first.DisplayName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.DisplayName, gotID)
}

func TestAcceptance_ResolveReturnToServiceConfigurationV1ByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	result, err := c.ListReturnToServiceConfigurationsV1(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result == nil || len(result.Results) == 0 {
		t.Skip("no return-to-service configurations — skipping")
	}
	first := result.Results[0]
	got, err := c.ResolveReturnToServiceConfigurationV1ByName(ctx, first.DisplayName)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.DisplayName != first.DisplayName {
		t.Errorf("typed DisplayName = %v, want %q", got, first.DisplayName)
	}
	t.Logf("resolved typed %q → %s ✓", first.DisplayName, got.ID)
}

// ─── IOSBrandingConfigurations ──────────────────────────────────────────────

func TestAcceptance_ResolveIOSBrandingConfigurationV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-iosb-" + runSuffix()

	// Step 1: Not found
	_, err := c.ResolveIOSBrandingConfigurationV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create
	resp, err := c.CreateIOSBrandingConfigurationV1(ctx, &pro.IosBrandingConfiguration{
		BrandingName:              name,
		BrandingNameColorCode:     "000000",
		HeaderBackgroundColorCode: "ffffff",
		MenuIconColorCode:         "333333",
		StatusBarTextColor:        "DARK",
	})
	if err != nil {
		t.Fatalf("CreateIOSBrandingConfigurationV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteIOSBrandingConfigurationV1(ctx, id1) })
	t.Logf("step 2: created %s", id1)

	// Step 3: Resolve ID
	gotID, err := c.ResolveIOSBrandingConfigurationV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveIOSBrandingConfigurationV1IDByName: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", name, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveIOSBrandingConfigurationV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveIOSBrandingConfigurationV1ByName: %v", err)
	}
	if got == nil || got.BrandingName != name {
		t.Errorf("typed BrandingName = %v, want %q", got, name)
	}
	t.Log("step 4: resolve typed ✓")

	// Step 5: Attempt duplicate
	id2, dupCreated := tryCreateDuplicate(t, "iOS branding configuration", func() (string, error) {
		r, e := c.CreateIOSBrandingConfigurationV1(ctx, &pro.IosBrandingConfiguration{
			BrandingName:              name,
			BrandingNameColorCode:     "000000",
			HeaderBackgroundColorCode: "ffffff",
			MenuIconColorCode:         "333333",
			StatusBarTextColor:        "DARK",
		})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteIOSBrandingConfigurationV1(ctx, id) })

	// Step 6: Ambiguous
	if dupCreated {
		_, err = c.ResolveIOSBrandingConfigurationV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("step 6: ambiguous with IDs %s, %s ✓", id1, id2)

		if err := c.DeleteIOSBrandingConfigurationV1(ctx, id2); err != nil {
			t.Logf("early delete dup: %v", err)
		}
	}

	// Step 7: Delete original
	if err := c.DeleteIOSBrandingConfigurationV1(ctx, id1); err != nil {
		t.Fatalf("delete original: %v", err)
	}

	// Step 8: Not found after delete
	_, err = c.ResolveIOSBrandingConfigurationV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("step 8: not-found after delete ✓")
}

// ─── MacOSBrandingConfigurations ────────────────────────────────────────────

func TestAcceptance_ResolveMacOSBrandingConfigurationV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-macb-" + runSuffix()

	// Step 1: Not found
	_, err := c.ResolveMacOSBrandingConfigurationV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create
	resp, err := c.CreateMacOSBrandingConfigurationV1(ctx, &pro.MacOsBrandingConfiguration{BrandingName: &name})
	if err != nil {
		t.Fatalf("CreateMacOSBrandingConfigurationV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteMacOSBrandingConfigurationV1(ctx, id1) })
	t.Logf("step 2: created %s", id1)

	// Step 3: Resolve ID
	gotID, err := c.ResolveMacOSBrandingConfigurationV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveMacOSBrandingConfigurationV1IDByName: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", name, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveMacOSBrandingConfigurationV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveMacOSBrandingConfigurationV1ByName: %v", err)
	}
	if got == nil || got.BrandingName == nil || *got.BrandingName != name {
		t.Errorf("typed BrandingName = %v, want %q", got, name)
	}
	t.Log("step 4: resolve typed ✓")

	// Step 5: Attempt duplicate
	id2, dupCreated := tryCreateDuplicate(t, "macOS branding configuration", func() (string, error) {
		r, e := c.CreateMacOSBrandingConfigurationV1(ctx, &pro.MacOsBrandingConfiguration{BrandingName: &name})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteMacOSBrandingConfigurationV1(ctx, id) })

	// Step 6: Ambiguous
	if dupCreated {
		_, err = c.ResolveMacOSBrandingConfigurationV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("step 6: ambiguous with IDs %s, %s ✓", id1, id2)

		if err := c.DeleteMacOSBrandingConfigurationV1(ctx, id2); err != nil {
			t.Logf("early delete dup: %v", err)
		}
	}

	// Step 7: Delete original
	if err := c.DeleteMacOSBrandingConfigurationV1(ctx, id1); err != nil {
		t.Fatalf("delete original: %v", err)
	}

	// Step 8: Not found after delete
	_, err = c.ResolveMacOSBrandingConfigurationV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("step 8: not-found after delete ✓")
}

// ─── VolumePurchasingSubscriptions ──────────────────────────────────────────

func TestAcceptance_ResolveVolumePurchasingSubscriptionV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-vps-" + runSuffix()

	// Step 1: Not found
	_, err := c.ResolveVolumePurchasingSubscriptionV1IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create
	resp, err := c.CreateVolumePurchasingSubscriptionV1(ctx, &pro.VolumePurchasingSubscriptionBase{Name: name})
	if err != nil {
		t.Fatalf("CreateVolumePurchasingSubscriptionV1: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteVolumePurchasingSubscriptionV1(ctx, id1) })
	t.Logf("step 2: created %s", id1)

	// Step 3: Resolve ID
	gotID, err := c.ResolveVolumePurchasingSubscriptionV1IDByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveVolumePurchasingSubscriptionV1IDByName: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", name, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveVolumePurchasingSubscriptionV1ByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveVolumePurchasingSubscriptionV1ByName: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}
	t.Log("step 4: resolve typed ✓")

	// Step 5: Attempt duplicate
	id2, dupCreated := tryCreateDuplicate(t, "volume purchasing subscription", func() (string, error) {
		r, e := c.CreateVolumePurchasingSubscriptionV1(ctx, &pro.VolumePurchasingSubscriptionBase{Name: name})
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteVolumePurchasingSubscriptionV1(ctx, id) })

	// Step 6: Ambiguous
	if dupCreated {
		_, err = c.ResolveVolumePurchasingSubscriptionV1IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("step 6: ambiguous with IDs %s, %s ✓", id1, id2)

		if err := c.DeleteVolumePurchasingSubscriptionV1(ctx, id2); err != nil {
			t.Logf("early delete dup: %v", err)
		}
	}

	// Step 7: Delete original
	if err := c.DeleteVolumePurchasingSubscriptionV1(ctx, id1); err != nil {
		t.Fatalf("delete original: %v", err)
	}

	// Step 8: Not found after delete
	_, err = c.ResolveVolumePurchasingSubscriptionV1IDByName(ctx, name)
	requireNotFoundErr(t, "post-delete", err)
	t.Log("step 8: not-found after delete ✓")
}

// ─── CloudIdp (read-only) ───────────────────────────────────────────────────

func TestAcceptance_ResolveCloudIdpV1IDByName_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveCloudIdpV1IDByName(context.Background(), "sdk-does-not-exist-"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found ✓")
}

func TestAcceptance_ResolveCloudIdpV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListCloudIdpV1(ctx, nil)
	if err != nil {
		t.Fatalf("ListCloudIdpV1: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no cloud IdPs in tenant — skipping")
	}
	first := items[0]
	gotID, err := c.ResolveCloudIdpV1IDByName(ctx, first.DisplayName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.DisplayName, gotID)
}

func TestAcceptance_ResolveCloudIdpV1ByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListCloudIdpV1(ctx, nil)
	if err != nil {
		t.Fatalf("ListCloudIdpV1: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no cloud IdPs in tenant — skipping")
	}
	first := items[0]
	got, err := c.ResolveCloudIdpV1ByName(ctx, first.DisplayName)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.DisplayName != first.DisplayName {
		t.Errorf("typed DisplayName = %q, want %q", got.DisplayName, first.DisplayName)
	}
	t.Logf("resolved typed %q → %s ✓", first.DisplayName, got.ID)
}

// ─── DeviceEnrollments (read-only) ──────────────────────────────────────────

func TestAcceptance_ResolveDeviceEnrollmentV1IDByName_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveDeviceEnrollmentV1IDByName(context.Background(), "sdk-does-not-exist-"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found ✓")
}

func TestAcceptance_ResolveDeviceEnrollmentV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListDeviceEnrollmentsV1(ctx, nil)
	if err != nil {
		t.Fatalf("ListDeviceEnrollmentsV1: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no device enrollments in tenant — skipping")
	}
	first := items[0]
	var firstID string
	if first.ID != nil {
		firstID = *first.ID
	} else {
		t.Fatal("first device enrollment has nil ID")
	}
	gotID, err := c.ResolveDeviceEnrollmentV1IDByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != firstID {
		t.Errorf("resolved id = %q, want %q", gotID, firstID)
	}
	t.Logf("resolved %q → %s ✓", first.Name, gotID)
}

func TestAcceptance_ResolveDeviceEnrollmentV1ByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListDeviceEnrollmentsV1(ctx, nil)
	if err != nil {
		t.Fatalf("ListDeviceEnrollmentsV1: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no device enrollments in tenant — skipping")
	}
	first := items[0]
	got, err := c.ResolveDeviceEnrollmentV1ByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.Name != first.Name {
		t.Errorf("typed Name = %q, want %q", got.Name, first.Name)
	}
	var gotID string
	if got.ID != nil {
		gotID = *got.ID
	}
	t.Logf("resolved typed %q → %s ✓", first.Name, gotID)
}

// ─── PatchSoftwareTitleConfigurations (read-only) ───────────────────────────

func TestAcceptance_ResolvePatchSoftwareTitleConfigurationV3IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListPatchSoftwareTitleConfigurationsV3(ctx)
	if err != nil {
		t.Fatalf("ListPatchSoftwareTitleConfigurationsV3: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no patch software title configurations in tenant — skipping")
	}
	first := items[0]
	gotID, err := c.ResolvePatchSoftwareTitleConfigurationV3IDByName(ctx, first.DisplayName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.ID {
		t.Errorf("resolved id = %q, want %q", gotID, first.ID)
	}
	t.Logf("resolved %q → %s ✓", first.DisplayName, gotID)
}

// ─── PatchSoftwareTitleConfigurations V3 ────────────────────────────────────
// V3 is the undeprecated successor to V2 (issue #50). Unlike V2's read-only
// probe these use a test-owned fixture, so the resolve path is exercised even
// on a tenant with no configurations of its own.

func TestAcceptance_ResolvePatchSoftwareTitleConfigurationV3IDByName_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolvePatchSoftwareTitleConfigurationV3IDByName(context.Background(), "sdk-does-not-exist-"+runSuffix())
	requireNotFoundErr(t, "ResolvePatchSoftwareTitleConfigurationV3IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolvePatchSoftwareTitleConfigurationV3ByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	id := seedPatchSoftwareTitleFixture(t)
	cleanupDelete(t, "DeletePatchSoftwareTitleConfigurationV3 "+id, func() error {
		return c.DeletePatchSoftwareTitleConfigurationV3(ctx, id)
	})
	seeded, err := c.GetPatchSoftwareTitleConfigurationV3(ctx, id)
	if err != nil {
		t.Fatalf("GetPatchSoftwareTitleConfigurationV3(%s): %v", id, err)
	}

	gotID, err := c.ResolvePatchSoftwareTitleConfigurationV3IDByName(ctx, seeded.DisplayName)
	if err != nil {
		t.Fatalf("resolve id: %v", err)
	}
	if gotID != id {
		t.Errorf("resolved id = %q, want %q", gotID, id)
	}

	got, err := c.ResolvePatchSoftwareTitleConfigurationV3ByName(ctx, seeded.DisplayName)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.DisplayName != seeded.DisplayName {
		t.Errorf("typed DisplayName = %q, want %q", got.DisplayName, seeded.DisplayName)
	}
	t.Logf("resolved typed %q → %s ✓", seeded.DisplayName, got.ID)
}

// ─── ComputerInventoryV4 alternate resolvers (not-found) ────────────────────

func TestAcceptance_ResolveComputerInventoryV4IDBySerialNumber_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveComputerInventoryV4IDBySerialNumber(context.Background(), "SDKNOTEXIST"+runSuffix())
	requireNotFoundErr(t, "ResolveComputerInventoryV4IDBySerialNumber", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveComputerInventoryV4IDByUDID_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveComputerInventoryV4IDByUDID(context.Background(), "SDK-NOT-EXIST-"+runSuffix())
	requireNotFoundErr(t, "ResolveComputerInventoryV4IDByUDID", err)
	t.Log("not-found surfaced 404 ✓")
}

// ─── ComputerInventoryV3 alternate resolvers ───────────────────────────────

// ─── MobileDeviceDetailV2 resolvers ─────────────────────────────────────────
// Enrolled devices, not created via API. Read-only probe.
// The list returns a discriminated union (MobileDeviceResponse). Fields
// like displayName, serialNumber, and UDID are nested inside the variant
// (General/Hardware), but the RSQL filter and raw-JSON resolver access
// them at the top level of each result element.

// mobileDeviceFields extracts common identifiers from a MobileDeviceResponse union.
func mobileDeviceFields(m pro.MobileDeviceResponse) (displayName, serial, udid, mobileDeviceID string) {
	switch {
	case m.IOS != nil:
		mobileDeviceID = m.IOS.MobileDeviceID
		if m.IOS.General != nil {
			displayName = m.IOS.General.DisplayName
			udid = m.IOS.General.UDID
		}
		if m.IOS.Hardware != nil {
			serial = m.IOS.Hardware.SerialNumber
		}
	case m.TvOS != nil:
		mobileDeviceID = m.TvOS.MobileDeviceID
		if m.TvOS.General != nil {
			displayName = m.TvOS.General.DisplayName
			udid = m.TvOS.General.UDID
		}
		if m.TvOS.Hardware != nil {
			serial = m.TvOS.Hardware.SerialNumber
		}
	case m.WatchOS != nil:
		mobileDeviceID = m.WatchOS.MobileDeviceID
		if m.WatchOS.General != nil {
			displayName = m.WatchOS.General.DisplayName
			udid = m.WatchOS.General.UDID
		}
		if m.WatchOS.Hardware != nil {
			serial = m.WatchOS.Hardware.SerialNumber
		}
	}
	return
}

func TestAcceptance_ResolveMobileDeviceDetailV2IDByName_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveMobileDeviceDetailV2IDByName(context.Background(), "sdk-does-not-exist-mobile-"+runSuffix())
	requireNotFoundErr(t, "ResolveMobileDeviceDetailV2IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveMobileDeviceDetailV2IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	mobiles, err := c.ListMobileDevicesDetailV2(ctx, []string{"GENERAL"}, nil, "")
	if err != nil {
		t.Fatalf("ListMobileDevicesDetailV2: %v", err)
	}
	if len(mobiles) == 0 {
		t.Skip("no mobile devices — skipping")
	}
	// Find a device whose displayName is unique (not duplicated across devices).
	// Also prefer pure ASCII names since Unicode chars may break RSQL filtering.
	nameCounts := make(map[string]int)
	nameToID := make(map[string]string)
	for _, m := range mobiles {
		dn, _, _, id := mobileDeviceFields(m)
		if dn == "" || id == "" {
			continue
		}
		nameCounts[dn]++
		nameToID[dn] = id
	}
	var displayName, mobileDeviceID string
	for dn, count := range nameCounts {
		if count == 1 {
			displayName, mobileDeviceID = dn, nameToID[dn]
			break
		}
	}
	if displayName == "" {
		t.Skip("no mobile device with unique displayName — skipping")
	}
	gotID, err := c.ResolveMobileDeviceDetailV2IDByName(ctx, displayName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != mobileDeviceID {
		t.Errorf("resolved id = %q, want %q", gotID, mobileDeviceID)
	}
	t.Logf("resolved %q → %s ✓", displayName, gotID)
}

func TestAcceptance_ResolveMobileDeviceDetailV2IDBySerialNumber_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveMobileDeviceDetailV2IDBySerialNumber(context.Background(), "SDKNOTEXIST"+runSuffix())
	requireNotFoundErr(t, "ResolveMobileDeviceDetailV2IDBySerialNumber", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveMobileDeviceDetailV2IDBySerialNumber_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	mobiles, err := c.ListMobileDevicesDetailV2(ctx, []string{"HARDWARE"}, nil, "")
	if err != nil {
		t.Fatalf("ListMobileDevicesDetailV2: %v", err)
	}
	if len(mobiles) == 0 {
		t.Skip("no mobile devices — skipping")
	}
	var serial, mobileDeviceID string
	for _, m := range mobiles {
		_, s, _, id := mobileDeviceFields(m)
		if s != "" && id != "" {
			serial, mobileDeviceID = s, id
			break
		}
	}
	if serial == "" {
		t.Skip("no mobile device with serial number — skipping")
	}
	gotID, err := c.ResolveMobileDeviceDetailV2IDBySerialNumber(ctx, serial)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != mobileDeviceID {
		t.Errorf("resolved id = %q, want %q", gotID, mobileDeviceID)
	}
	t.Logf("resolved serial %q → %s ✓", serial, gotID)
}

func TestAcceptance_ResolveMobileDeviceDetailV2IDByUDID_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveMobileDeviceDetailV2IDByUDID(context.Background(), "00000000-0000-0000-0000-000000000000")
	requireNotFoundErr(t, "ResolveMobileDeviceDetailV2IDByUDID", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveMobileDeviceDetailV2IDByUDID_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	mobiles, err := c.ListMobileDevicesDetailV2(ctx, []string{"GENERAL"}, nil, "")
	if err != nil {
		t.Fatalf("ListMobileDevicesDetailV2: %v", err)
	}
	if len(mobiles) == 0 {
		t.Skip("no mobile devices — skipping")
	}
	var udid, mobileDeviceID string
	for _, m := range mobiles {
		_, _, u, id := mobileDeviceFields(m)
		if u != "" && id != "" {
			udid, mobileDeviceID = u, id
			break
		}
	}
	if udid == "" {
		t.Skip("no mobile device with UDID — skipping")
	}
	gotID, err := c.ResolveMobileDeviceDetailV2IDByUDID(ctx, udid)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != mobileDeviceID {
		t.Errorf("resolved id = %q, want %q", gotID, mobileDeviceID)
	}
	t.Logf("resolved UDID %q → %s ✓", udid, gotID)
}

// ─── JamfConnectConfigProfile (read-only) ───────────────────────────────────

func TestAcceptance_ResolveJamfConnectConfigProfileV1IDByName_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveJamfConnectConfigProfileV1IDByName(context.Background(), "sdk-does-not-exist-"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found ✓")
}

// uniqueJamfConnectProfile returns a profile whose name is held by exactly one
// record, skipping the test when no such profile exists.
//
// Jamf Connect config profile names are not unique. On the acceptance tenant all
// four profiles are named "App Installers - Custom values for Jamf Connect"
// (ids 1780, 1782, 1783, 1787 — wire-probed 2026-07-31), because App Installers
// creates one identically-named profile per deployment. Resolving items[0]'s name
// therefore returned a correct *AmbiguousMatchError, which the test read as a
// resolver failure. Name-based resolution is only meaningful for a name with a
// single holder.
func uniqueJamfConnectProfile(t *testing.T, items []pro.LinkedConnectProfile) pro.LinkedConnectProfile {
	t.Helper()
	counts := map[string]int{}
	for _, it := range items {
		if it.ProfileName != nil {
			counts[*it.ProfileName]++
		}
	}
	for _, it := range items {
		if it.ProfileName != nil && it.ProfileID != nil && counts[*it.ProfileName] == 1 {
			return it
		}
	}
	t.Skipf("no uniquely-named Jamf Connect config profile in tenant (%d profiles, %d distinct names) — name resolution is ambiguous by construction", len(items), len(counts))
	return pro.LinkedConnectProfile{}
}

func TestAcceptance_ResolveJamfConnectConfigProfileV1IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListJamfConnectConfigProfilesV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfConnectConfigProfilesV1: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no Jamf Connect config profiles in tenant — skipping")
	}
	first := uniqueJamfConnectProfile(t, items)
	gotID, err := c.ResolveJamfConnectConfigProfileV1IDByName(ctx, *first.ProfileName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantID := strconv.Itoa(*first.ProfileID)
	if gotID != wantID {
		t.Errorf("resolved id = %q, want %q", gotID, wantID)
	}
	t.Logf("resolved %q → %s ✓", *first.ProfileName, gotID)
}

func TestAcceptance_ResolveJamfConnectConfigProfileV1ByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListJamfConnectConfigProfilesV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfConnectConfigProfilesV1: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no Jamf Connect config profiles in tenant — skipping")
	}
	first := uniqueJamfConnectProfile(t, items)
	got, err := c.ResolveJamfConnectConfigProfileV1ByName(ctx, *first.ProfileName)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.ProfileName == nil || *got.ProfileName != *first.ProfileName {
		t.Errorf("typed ProfileName = %v, want %q", got.ProfileName, *first.ProfileName)
	}
	t.Logf("resolved typed %q ✓", *first.ProfileName)
}

// ─── EnrollmentLanguage (read-only — languages are tenant-managed) ──────────

func TestAcceptance_ResolveEnrollmentLanguageV3IDByName_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveEnrollmentLanguageV3IDByName(context.Background(), "sdk-does-not-exist-"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found ✓")
}

func TestAcceptance_ResolveEnrollmentLanguageV3IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListEnrollmentLanguagesV3(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentLanguagesV3: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no enrollment languages in tenant — skipping")
	}
	first := items[0]
	if first.Name == nil || first.LanguageCode == nil {
		t.Skip("first language has nil name or languageCode — skipping")
	}
	gotID, err := c.ResolveEnrollmentLanguageV3IDByName(ctx, *first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != *first.LanguageCode {
		t.Errorf("resolved id = %q, want %q", gotID, *first.LanguageCode)
	}
	t.Logf("resolved %q → %s ✓", *first.Name, gotID)
}

func TestAcceptance_ResolveEnrollmentLanguageV3ByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	items, err := c.ListEnrollmentLanguagesV3(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentLanguagesV3: %v", err)
	}
	if len(items) == 0 {
		t.Skip("no enrollment languages in tenant — skipping")
	}
	first := items[0]
	if first.Name == nil {
		t.Skip("first language has nil name — skipping")
	}
	got, err := c.ResolveEnrollmentLanguageV3ByName(ctx, *first.Name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.Name == nil || *got.Name != *first.Name {
		t.Errorf("typed Name = %v, want %q", got.Name, *first.Name)
	}
	t.Logf("resolved typed %q → %s ✓", *first.Name, *got.LanguageCode)
}

// ─── AppRequestFormInputField (CRUD lifecycle) ──────────────────────────────

func TestAcceptance_ResolveAppRequestFormInputFieldV1_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()

	title := "sdk-acc-res-appfield-" + runSuffix()

	// Step 1: Not found
	_, err := c.ResolveAppRequestFormInputFieldV1IDByName(ctx, title)
	requireNotFoundErr(t, "pre-create", err)
	t.Log("step 1: not-found ✓")

	// Step 2: Create
	created, err := c.CreateAppRequestFormInputFieldV1(ctx, &pro.AppRequestFormInputField{
		Title:    title,
		Priority: 1,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAppRequestFormInputFieldV1: %v", err)
	}
	if created.ID == nil {
		t.Fatal("created field has nil ID")
	}
	id := strconv.Itoa(*created.ID)
	t.Cleanup(func() { _ = c.DeleteAppRequestFormInputFieldV1(ctx, id) })
	t.Logf("step 2: created %s", id)

	// Step 3: Resolve ID
	gotID, err := c.ResolveAppRequestFormInputFieldV1IDByName(ctx, title)
	if err != nil {
		t.Fatalf("ResolveAppRequestFormInputFieldV1IDByName: %v", err)
	}
	if gotID != id {
		t.Errorf("resolve ID = %q, want %q", gotID, id)
	}
	t.Logf("step 3: resolve ID %q → %s ✓", title, gotID)

	// Step 4: Resolve typed
	got, err := c.ResolveAppRequestFormInputFieldV1ByName(ctx, title)
	if err != nil {
		t.Fatalf("ResolveAppRequestFormInputFieldV1ByName: %v", err)
	}
	if got == nil || got.Title != title {
		t.Errorf("typed Title = %v, want %q", got, title)
	}
	t.Logf("step 4: resolve typed %q ✓", title)
}

// ─── Smart Computer Groups (V3) ────────────────────────────────────────────
// New in Jamf Pro 11.28.0 — same lifecycle as V2, exercising the v3 endpoint
// family that the backfill cloned from the V2 sibling.

func TestAcceptance_ResolveSmartComputerGroupV3_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-scg-v3-" + runSuffix()

	_, err := c.ResolveSmartComputerGroupV3IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	resp, err := c.CreateSmartComputerGroupV3(ctx, &pro.SmartComputerGroupV3{Name: name}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteSmartComputerGroupV3(ctx, id1) })

	gotID, err := c.ResolveSmartComputerGroupV3IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveSmartComputerGroupV3ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "smart computer group v3", func() (string, error) {
		r, e := c.CreateSmartComputerGroupV3(ctx, &pro.SmartComputerGroupV3{Name: name}, false)
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteSmartComputerGroupV3(ctx, id) })

	if dupCreated {
		_, err = c.ResolveSmartComputerGroupV3IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteSmartComputerGroupV3(ctx, id2)
	}

	if err := c.DeleteSmartComputerGroupV3(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Group reads lag their own writes — see settleUntilGone.
	settleUntilGone(t, "post-delete", func() error {
		_, err := c.ResolveSmartComputerGroupV3IDByName(ctx, name)
		return err
	})
	t.Log("lifecycle complete ✓")
}

// ─── Static Computer Groups (V3) ───────────────────────────────────────────

func TestAcceptance_ResolveStaticComputerGroupV3_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-stcg-v3-" + runSuffix()

	_, err := c.ResolveStaticComputerGroupV3IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	emptyAssignments := []string{}
	resp, err := c.CreateStaticComputerGroupV3(ctx, &pro.StaticComputerGroupAssignment{Name: name, Assignments: &emptyAssignments}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteStaticComputerGroupV3(ctx, id1) })

	gotID, err := c.ResolveStaticComputerGroupV3IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveStaticComputerGroupV3ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil || got.Name != name {
		t.Errorf("typed Name = %v, want %q", got, name)
	}

	id2, dupCreated := tryCreateDuplicate(t, "static computer group v3", func() (string, error) {
		r, e := c.CreateStaticComputerGroupV3(ctx, &pro.StaticComputerGroupAssignment{Name: name, Assignments: &emptyAssignments}, false)
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteStaticComputerGroupV3(ctx, id) })

	if dupCreated {
		_, err = c.ResolveStaticComputerGroupV3IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteStaticComputerGroupV3(ctx, id2)
	}

	if err := c.DeleteStaticComputerGroupV3(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Group reads lag their own writes — see settleUntilGone.
	settleUntilGone(t, "post-delete", func() error {
		_, err := c.ResolveStaticComputerGroupV3IDByName(ctx, name)
		return err
	})
	t.Log("lifecycle complete ✓")
}

// ─── Smart Mobile Device Groups (V2) ───────────────────────────────────────

func TestAcceptance_ResolveSmartMobileDeviceGroupV2_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-smdg-v2-" + runSuffix()

	_, err := c.ResolveSmartMobileDeviceGroupV2IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	resp, err := c.CreateSmartMobileDeviceGroupV2(ctx, &pro.SmartGroupAssignmentV2{GroupName: name, SiteID: new("-1")}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteSmartMobileDeviceGroupV2(ctx, id1) })

	gotID, err := c.ResolveSmartMobileDeviceGroupV2IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveSmartMobileDeviceGroupV2ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatalf("resolve typed: nil result")
	}
	t.Logf("resolve typed: groupName field on V2 schema = %+v", got)

	id2, dupCreated := tryCreateDuplicate(t, "smart mobile device group v2", func() (string, error) {
		r, e := c.CreateSmartMobileDeviceGroupV2(ctx, &pro.SmartGroupAssignmentV2{GroupName: name, SiteID: new("-1")}, false)
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteSmartMobileDeviceGroupV2(ctx, id) })

	if dupCreated {
		_, err = c.ResolveSmartMobileDeviceGroupV2IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteSmartMobileDeviceGroupV2(ctx, id2)
	}

	if err := c.DeleteSmartMobileDeviceGroupV2(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Group reads lag their own writes — see settleUntilGone.
	settleUntilGone(t, "post-delete", func() error {
		_, err := c.ResolveSmartMobileDeviceGroupV2IDByName(ctx, name)
		return err
	})
	t.Log("lifecycle complete ✓")
}

// ─── Static Mobile Device Groups (V2) ──────────────────────────────────────

func TestAcceptance_ResolveStaticMobileDeviceGroupV2_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-stmdg-v2-" + runSuffix()

	_, err := c.ResolveStaticMobileDeviceGroupV2IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	emptyAssignments := []pro.Assignment{}
	resp, err := c.CreateStaticMobileDeviceGroupV2(ctx, &pro.StaticGroupAssignment{GroupName: name, SiteID: new("-1"), Assignments: &emptyAssignments}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteStaticMobileDeviceGroupV2(ctx, id1) })

	gotID, err := c.ResolveStaticMobileDeviceGroupV2IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	if gotID != id1 {
		t.Errorf("resolve ID = %q, want %q", gotID, id1)
	}

	got, err := c.ResolveStaticMobileDeviceGroupV2ByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatalf("resolve typed: nil result")
	}

	if err := c.DeleteStaticMobileDeviceGroupV2(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Group reads lag their own writes — see settleUntilGone.
	settleUntilGone(t, "post-delete", func() error {
		_, err := c.ResolveStaticMobileDeviceGroupV2IDByName(ctx, name)
		return err
	})
	t.Log("lifecycle complete ✓")
}

// ─── Mobile Device Groups (combined V2) ────────────────────────────────────
// Combined smart+static list. Create via SmartMobileDeviceGroupV2 to test.

func TestAcceptance_ResolveMobileDeviceGroupV2_Lifecycle(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	name := "sdk-acc-res-mdg-v2-" + runSuffix()

	_, err := c.ResolveMobileDeviceGroupV2IDByName(ctx, name)
	requireNotFoundErr(t, "pre-create", err)

	resp, err := c.CreateSmartMobileDeviceGroupV2(ctx, &pro.SmartGroupAssignmentV2{GroupName: name, SiteID: new("-1")}, false)
	if err != nil {
		t.Fatalf("CreateSmartMobileDeviceGroupV2: %v", err)
	}
	id1 := resp.ID
	t.Cleanup(func() { _ = c.DeleteSmartMobileDeviceGroupV2(ctx, id1) })

	gotID, err := c.ResolveMobileDeviceGroupV2IDByName(ctx, name)
	if err != nil {
		t.Fatalf("resolve ID: %v", err)
	}
	// MobileDeviceGroup.ID is int, resolver returns string
	t.Logf("resolved %q → %s ✓", name, gotID)

	emptyAssignmentsMDG := []pro.Assignment{}
	id2, dupCreated := tryCreateDuplicate(t, "mobile device group v2 (static)", func() (string, error) {
		r, e := c.CreateStaticMobileDeviceGroupV2(ctx, &pro.StaticGroupAssignment{GroupName: name, SiteID: new("-1"), Assignments: &emptyAssignmentsMDG}, false)
		if e != nil {
			return "", e
		}
		return r.ID, nil
	}, func(id string) error { return c.DeleteStaticMobileDeviceGroupV2(ctx, id) })

	if dupCreated {
		_, err = c.ResolveMobileDeviceGroupV2IDByName(ctx, name)
		requireAmbiguousErr(t, "ambiguous", err)
		t.Logf("ambiguous with IDs %s, %s ✓", id1, id2)
		_ = c.DeleteStaticMobileDeviceGroupV2(ctx, id2)
	}

	if err := c.DeleteSmartMobileDeviceGroupV2(ctx, id1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Group reads lag their own writes — see settleUntilGone.
	settleUntilGone(t, "post-delete", func() error {
		_, err := c.ResolveMobileDeviceGroupV2IDByName(ctx, name)
		return err
	})
	t.Log("lifecycle complete ✓")
}

// ─── Platform Groups (V2) ──────────────────────────────────────────────────
// Synced from identity providers — no create endpoint. Read-only probe.

func TestAcceptance_ResolveGroupV2_NotFound(t *testing.T) {
	c := pro.New(accClient(t))
	_, err := c.ResolveGroupV2IDByName(context.Background(), "sdk-does-not-exist-grp-v2-"+runSuffix())
	requireNotFoundErr(t, "ResolveGroupV2IDByName", err)
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveGroupV2IDByName_Existing(t *testing.T) {
	c := pro.New(accClient(t))
	ctx := context.Background()
	groups, err := c.ListGroupsV2(ctx, nil, "")
	if err != nil {
		t.Fatalf("ListGroupsV2: %v", err)
	}
	if len(groups) == 0 {
		t.Skip("no platform groups — skipping")
	}
	first := groups[0]
	gotID, err := c.ResolveGroupV2IDByName(ctx, first.GroupName)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.GroupPlatformID {
		t.Errorf("resolved id = %q, want %q", gotID, first.GroupPlatformID)
	}
	t.Logf("resolved %q → %s ✓", first.GroupName, gotID)
}
