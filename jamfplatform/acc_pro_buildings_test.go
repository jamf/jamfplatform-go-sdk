// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Buildings CRUD + history + export + bulk-delete. Buildings are a
// core reference resource (building records are referenced by
// device records, prestages, smart groups etc.). All tests create
// and delete their own fixtures.

// createAccBuilding creates a named building fixture with cleanup
// registered, returning the id. Shared by history / bulk-delete tests.
func createAccBuilding(t *testing.T, p *pro.Client, suffix string) string {
	t.Helper()
	ctx := context.Background()
	created, err := p.CreateBuildingV1(ctx, &pro.Building{
		Name: "sdk-acc-building-" + suffix,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBuildingV1: %v", err)
	}
	id := created.ID
	cleanupDelete(t, "Building "+id, func() error { return p.DeleteBuildingV1(ctx, id) })
	return id
}

func TestAcceptance_Pro_ListBuildings(t *testing.T) {
	c := accClient(t)

	buildings, err := pro.New(c).ListBuildingsV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBuildingsV1 failed: %v", err)
	}
	t.Logf("Found %d buildings", len(buildings))
}

func TestAcceptance_Pro_ListBuildingsWithSort(t *testing.T) {
	c := accClient(t)

	buildings, err := pro.New(c).ListBuildingsV1(context.Background(), []string{"name:asc"}, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBuildingsV1 sorted failed: %v", err)
	}
	t.Logf("Found %d buildings (sorted by name asc)", len(buildings))
}

// TestAcceptance_Pro_BuildingCRUD covers the full create → get → update →
// delete → verify-gone flow for a single Building, with t.Cleanup insuring
// against leaks if any assertion fails mid-test.
func TestAcceptance_Pro_BuildingCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-building-" + runSuffix()
	city := "Cupertino"
	country := "United States"

	created, err := p.CreateBuildingV1(ctx, &pro.Building{
		Name:    name,
		City:    &city,
		Country: &country,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBuildingV1 failed: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateBuildingV1 returned no ID (href=%q)", created.Href)
	}
	cleanupDelete(t, "DeleteBuildingV1", func() error { return p.DeleteBuildingV1(ctx, created.ID) })
	t.Logf("Created building %s (%s)", created.ID, created.Href)

	got, err := p.GetBuildingV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBuildingV1(%s) failed: %v", created.ID, err)
	}
	if got.Name != name {
		t.Errorf("GetBuildingV1 Name = %q, want %q", got.Name, name)
	}
	if got.City == nil || *got.City != city {
		t.Errorf("GetBuildingV1 City = %v, want %q", got.City, city)
	}

	newCity := "Eau Claire"
	got.City = &newCity
	updated, err := p.UpdateBuildingV1(ctx, created.ID, got)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateBuildingV1(%s) failed: %v", created.ID, err)
	}
	if updated.City == nil || *updated.City != newCity {
		t.Errorf("UpdateBuildingV1 City = %v, want %q", updated.City, newCity)
	}

	if err := p.DeleteBuildingV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteBuildingV1(%s) failed: %v", created.ID, err)
	}

	_, err = p.GetBuildingV1(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetBuildingV1(%s) after delete should have failed, succeeded", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetBuildingV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

func TestAcceptance_Pro_ExportBuildings(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// Ensure at least one building exists so the export isn't empty-header-only.
	name := "sdk-acc-export-" + runSuffix()
	created, err := p.CreateBuildingV1(ctx, &pro.Building{Name: name})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBuildingV1: %v", err)
	}
	cleanupDelete(t, "DeleteBuildingV1", func() error { return p.DeleteBuildingV1(ctx, created.ID) })

	csv, err := p.ExportBuildingsV1(ctx, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportBuildingsV1: %v", err)
	}
	if len(csv) == 0 {
		t.Fatal("ExportBuildingsV1 returned empty body")
	}
	firstLine := string(csv)
	if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
		firstLine = firstLine[:nl]
	}
	if !strings.Contains(strings.ToLower(firstLine), "name") {
		t.Errorf("export header %q does not contain 'name'", firstLine)
	}
	t.Logf("Exported %d bytes; header: %s", len(csv), firstLine)
}

func TestAcceptance_Pro_BuildingHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	id := createAccBuilding(t, p, runSuffix())

	if _, err := p.CreateBuildingHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test building history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBuildingHistoryNoteV1: %v", err)
	}

	hist, err := p.ListBuildingHistoryV1(ctx, id, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBuildingHistoryV1: %v", err)
	}
	t.Logf("Building history: %d entries", len(hist))

	body, err := p.ExportBuildingHistoryV1(ctx, id, &pro.ExportParameters{}, nil, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportBuildingHistoryV1: %v", err)
	}
	t.Logf("Building history export: %d bytes", len(body))
}

func TestAcceptance_Pro_BuildingsDeleteMultipleV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	a := createAccBuilding(t, p, runSuffix()+"-a")
	b := createAccBuilding(t, p, runSuffix()+"-b")

	ids := []string{a, b}
	if err := p.DeleteMultipleBuildingsV1(ctx, &pro.Ids{IDs: &ids}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteMultipleBuildingsV1: %v", err)
	}
	t.Logf("DeleteMultipleBuildingsV1 succeeded for ids %v", ids)
}
