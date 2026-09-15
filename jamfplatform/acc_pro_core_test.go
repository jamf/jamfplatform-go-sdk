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

// --- categories ---------------------------------------------------------

func TestAcceptance_Pro_Core_ListCategoriesV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListCategoriesV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListCategoriesV1: %v", err)
	}
	t.Logf("Found %d categories", len(items))
}

func TestAcceptance_Pro_Core_CategoryCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-category-" + runSuffix()

	created, err := p.CreateCategoryV1(ctx, &pro.Category{Name: name, Priority: 5})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCategoryV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateCategoryV1 returned no ID (href=%q)", created.Href)
	}
	cleanupDelete(t, "DeleteCategoryV1", func() error { return p.DeleteCategoryV1(ctx, created.ID) })
	t.Logf("Created category %s", created.ID)

	got, err := p.GetCategoryV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCategoryV1(%s): %v", created.ID, err)
	}
	if got.Name != name {
		t.Errorf("GetCategoryV1 Name = %q, want %q", got.Name, name)
	}
	if got.Priority != 5 {
		t.Errorf("GetCategoryV1 Priority = %d, want 5", got.Priority)
	}

	got.Priority = 10
	updated, err := p.UpdateCategoryV1(ctx, created.ID, got)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateCategoryV1(%s): %v", created.ID, err)
	}
	if updated.Priority != 10 {
		t.Errorf("UpdateCategoryV1 Priority = %d, want 10", updated.Priority)
	}

	if err := p.DeleteCategoryV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteCategoryV1(%s): %v", created.ID, err)
	}

	_, err = p.GetCategoryV1(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetCategoryV1(%s) after delete should 404, succeeded", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetCategoryV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

// TestAcceptance_Pro_Core_DeleteMultipleCategoriesV1 creates two throwaway
// categories, then deletes both via the bulk endpoint and confirms they 404.
func TestAcceptance_Pro_Core_DeleteMultipleCategoriesV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	suffix := runSuffix()
	var ids []string
	for i, tag := range []string{"a", "b"} {
		resp, err := p.CreateCategoryV1(ctx, &pro.Category{
			Name:     "sdk-acc-cat-bulk-" + suffix + "-" + tag,
			Priority: 5 + i,
		})
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("CreateCategoryV1[%d]: %v", i, err)
		}
		ids = append(ids, resp.ID)
		id := resp.ID
		cleanupDelete(t, "DeleteCategoryV1(fallback)", func() error { return p.DeleteCategoryV1(ctx, id) })
	}

	if err := p.DeleteMultipleCategoriesV1(ctx, &pro.Ids{IDs: &ids}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteMultipleCategoriesV1: %v", err)
	}

	for _, id := range ids {
		if _, err := p.GetCategoryV1(ctx, id); err == nil {
			t.Errorf("GetCategoryV1(%s) after bulk delete should 404, succeeded", id)
		}
	}
}

// TestAcceptance_Pro_Core_CategoryHistoryV1 covers list + create-note against
// a freshly created category.
func TestAcceptance_Pro_Core_CategoryHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	created, err := p.CreateCategoryV1(ctx, &pro.Category{
		Name:     "sdk-acc-cat-hist-" + runSuffix(),
		Priority: 3,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCategoryV1: %v", err)
	}
	cleanupDelete(t, "DeleteCategoryV1", func() error { return p.DeleteCategoryV1(ctx, created.ID) })

	note, err := p.CreateCategoryHistoryNoteV1(ctx, created.ID, &pro.ObjectHistoryNote{
		Note: "sdk-acc test history entry",
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCategoryHistoryNoteV1: %v", err)
	}
	if note.Note == "" {
		t.Errorf("CreateCategoryHistoryNoteV1 returned empty note body")
	}

	hist, err := p.ListCategoryHistoryV1(ctx, created.ID, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListCategoryHistoryV1(%s): %v", created.ID, err)
	}
	t.Logf("Category %s history has %d entries", created.ID, len(hist))
}

// --- departments --------------------------------------------------------

func TestAcceptance_Pro_Core_ListDepartmentsV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListDepartmentsV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDepartmentsV1: %v", err)
	}
	t.Logf("Found %d departments", len(items))
}

func TestAcceptance_Pro_Core_DepartmentCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-department-" + runSuffix()

	created, err := p.CreateDepartmentV1(ctx, &pro.Department{Name: name})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDepartmentV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateDepartmentV1 returned no ID")
	}
	cleanupDelete(t, "DeleteDepartmentV1", func() error { return p.DeleteDepartmentV1(ctx, created.ID) })

	got, err := p.GetDepartmentV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDepartmentV1(%s): %v", created.ID, err)
	}
	if got.Name != name {
		t.Errorf("GetDepartmentV1 Name = %q, want %q", got.Name, name)
	}

	newName := name + "-updated"
	got.Name = newName
	updated, err := p.UpdateDepartmentV1(ctx, created.ID, got)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateDepartmentV1(%s): %v", created.ID, err)
	}
	if updated.Name != newName {
		t.Errorf("UpdateDepartmentV1 Name = %q, want %q", updated.Name, newName)
	}

	if err := p.DeleteDepartmentV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteDepartmentV1(%s): %v", created.ID, err)
	}

	_, err = p.GetDepartmentV1(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetDepartmentV1(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetDepartmentV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

func TestAcceptance_Pro_Core_DeleteMultipleDepartmentsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	suffix := runSuffix()
	var ids []string
	for _, tag := range []string{"a", "b"} {
		resp, err := p.CreateDepartmentV1(ctx, &pro.Department{
			Name: "sdk-acc-dept-bulk-" + suffix + "-" + tag,
		})
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("CreateDepartmentV1[%s]: %v", tag, err)
		}
		ids = append(ids, resp.ID)
		id := resp.ID
		cleanupDelete(t, "DeleteDepartmentV1(fallback)", func() error { return p.DeleteDepartmentV1(ctx, id) })
	}

	if err := p.DeleteMultipleDepartmentsV1(ctx, &pro.Ids{IDs: &ids}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteMultipleDepartmentsV1: %v", err)
	}

	for _, id := range ids {
		if _, err := p.GetDepartmentV1(ctx, id); err == nil {
			t.Errorf("GetDepartmentV1(%s) after bulk delete should 404, succeeded", id)
		}
	}
}

func TestAcceptance_Pro_Core_DepartmentHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	created, err := p.CreateDepartmentV1(ctx, &pro.Department{
		Name: "sdk-acc-dept-hist-" + runSuffix(),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDepartmentV1: %v", err)
	}
	cleanupDelete(t, "DeleteDepartmentV1", func() error { return p.DeleteDepartmentV1(ctx, created.ID) })

	// Department history POST returns HrefResponse (asymmetry vs category/script).
	resp, err := p.CreateDepartmentHistoryNoteV1(ctx, created.ID, &pro.ObjectHistoryNote{
		Note: "sdk-acc test history entry",
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDepartmentHistoryNoteV1: %v", err)
	}
	if resp.Href == "" {
		t.Errorf("CreateDepartmentHistoryNoteV1 returned empty Href")
	}

	hist, err := p.ListDepartmentHistoryV1(ctx, created.ID, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDepartmentHistoryV1(%s): %v", created.ID, err)
	}
	t.Logf("Department %s history has %d entries", created.ID, len(hist))
}

// --- sites --------------------------------------------------------------

// Sites are read-only via the Pro API on Jamf tenants (writes go through
// Classic). Only list + list-site-objects are exercised here.

func TestAcceptance_Pro_Core_ListSitesV1(t *testing.T) {
	c := accClient(t)

	sites, err := pro.New(c).ListSitesV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListSitesV1: %v", err)
	}
	t.Logf("Found %d sites", len(sites))
}

// TestAcceptance_Pro_Core_ListSiteObjectsV1 exercises the rawArray pagination
// path: response is a bare `[]SiteObject` with page/page-size query params.
// When the tenant has no sites, plumbing-only: probe against a bogus id and
// accept either empty result or 404.
func TestAcceptance_Pro_Core_ListSiteObjectsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	sites, err := p.ListSitesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListSitesV1: %v", err)
	}

	siteID := "1"
	if len(sites) > 0 {
		siteID = sites[0].ID
	}

	objs, err := p.ListSiteObjectsV1(ctx, siteID, nil, "")
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("ListSiteObjectsV1(%s): 404 (no such site) — plumbing OK", siteID)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("ListSiteObjectsV1(%s): %v", siteID, err)
	}
	t.Logf("Site %s has %d objects", siteID, len(objs))
}

// --- scripts ------------------------------------------------------------

func TestAcceptance_Pro_Core_ListScriptsV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListScriptsV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListScriptsV1: %v", err)
	}
	t.Logf("Found %d scripts", len(items))
}

func TestAcceptance_Pro_Core_ScriptCRUD(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-script-" + runSuffix()
	contents := "#!/bin/sh\necho hello from sdk acc\n"

	created, err := p.CreateScriptV1(ctx, &pro.Script{
		Name:           name,
		ScriptContents: new("#!/bin/sh\necho initial\n"),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateScriptV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateScriptV1 returned no ID")
	}
	cleanupDelete(t, "DeleteScriptV1", func() error { return p.DeleteScriptV1(ctx, created.ID) })

	got, err := p.GetScriptV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetScriptV1(%s): %v", created.ID, err)
	}
	if got.Name != name {
		t.Errorf("GetScriptV1 Name = %q, want %q", got.Name, name)
	}

	got.ScriptContents = &contents
	updated, err := p.UpdateScriptV1(ctx, created.ID, got)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateScriptV1(%s): %v", created.ID, err)
	}
	if updated.ScriptContents == nil || *updated.ScriptContents != contents {
		t.Errorf("UpdateScriptV1 ScriptContents = %v, want %q", updated.ScriptContents, contents)
	}

	body, err := p.DownloadScriptV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadScriptV1(%s): %v", created.ID, err)
	}
	if !strings.Contains(string(body), "hello from sdk acc") {
		t.Errorf("DownloadScriptV1 body = %q, want substring %q", body, contents)
	}

	if err := p.DeleteScriptV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteScriptV1(%s): %v", created.ID, err)
	}

	_, err = p.GetScriptV1(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetScriptV1(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetScriptV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

func TestAcceptance_Pro_Core_ScriptHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	created, err := p.CreateScriptV1(ctx, &pro.Script{
		Name:           "sdk-acc-script-hist-" + runSuffix(),
		ScriptContents: new("#!/bin/sh\necho hist\n"),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateScriptV1: %v", err)
	}
	cleanupDelete(t, "DeleteScriptV1", func() error { return p.DeleteScriptV1(ctx, created.ID) })

	note, err := p.CreateScriptHistoryNoteV1(ctx, created.ID, &pro.ObjectHistoryNote{
		Note: "sdk-acc test history entry",
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateScriptHistoryNoteV1: %v", err)
	}
	if note.Note == "" {
		t.Errorf("CreateScriptHistoryNoteV1 returned empty note body")
	}

	hist, err := p.ListScriptHistoryV1(ctx, created.ID, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListScriptHistoryV1(%s): %v", created.ID, err)
	}
	t.Logf("Script %s history has %d entries", created.ID, len(hist))
}

//go:fix inline
func ptr[T any](v T) *T { return new(v) }

func derefStrPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// --- dock-items ---------------------------------------------------------

func TestAcceptance_Pro_Core_DockItemCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-dockitem-" + runSuffix()
	created, err := p.CreateDockItemV1(ctx, &pro.DockItem{
		Name: name,
		Type: pro.DockItemTypeApp,
		Path: "file:///Applications/Safari.app/",
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDockItemV1: %v", err)
	}
	id := created.ID
	t.Logf("Created dock item id=%s name=%s", id, name)
	cleanupDelete(t, "DockItem "+id, func() error { return p.DeleteDockItemV1(ctx, id) })

	got, err := p.GetDockItemV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDockItemV1: %v", err)
	}
	if got.Name != name {
		t.Errorf("name round-trip mismatch: got %q, want %q", got.Name, name)
	}

	got.Name = name + "-upd"
	if _, err := p.UpdateDockItemV1(ctx, id, got); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateDockItemV1: %v", err)
	}
}

// --- ebooks -------------------------------------------------------------

func TestAcceptance_Pro_Core_EbooksV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	list, err := p.ListEbooksV1(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEbooksV1: %v", err)
	}
	t.Logf("Ebooks: %d", len(list))

	if _, err := p.GetEbookV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			t.Logf("GetEbookV1(-1): 404 — expected")
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetEbookV1: %v", err)
		}
	}
	if _, err := p.GetEbookScopeV1(ctx, "-1"); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			t.Logf("GetEbookScopeV1(-1): 404 — expected")
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetEbookScopeV1: %v", err)
		}
	}
}
