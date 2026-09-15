// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

// Bulk list acc tests. Each List<Resource> call probes the endpoint
// shape end-to-end against a real tenant. Content-level assertions
// live in the CRUD tests. These tests skip on 403 BAD_PERMISSIONS
// (role gap) or 5xx (server-side bug) so they remain green across
// tenants with different privilege sets.

import (
	"context"
	"errors"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

func TestAcceptance_Classic_ListAccounts(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListAccounts(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListAccounts forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListAccounts: %v", err)
	}
}

func TestAcceptance_Classic_ListAdvancedComputerSearches(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListAdvancedComputerSearches(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListAdvancedComputerSearches forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListAdvancedComputerSearches: %v", err)
	}
}

func TestAcceptance_Classic_ListAdvancedMobileDeviceSearches(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListAdvancedMobileDeviceSearches(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListAdvancedMobileDeviceSearches forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListAdvancedMobileDeviceSearches: %v", err)
	}
}

func TestAcceptance_Classic_ListAdvancedUserSearches(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListAdvancedUserSearches(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListAdvancedUserSearches forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListAdvancedUserSearches: %v", err)
	}
}

func TestAcceptance_Classic_ListAllowedFileExtensions(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListAllowedFileExtensions(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListAllowedFileExtensions forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListAllowedFileExtensions: %v", err)
	}
}

func TestAcceptance_Classic_ListBuildings(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListBuildings(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListBuildings forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListBuildings: %v", err)
	}
}

func TestAcceptance_Classic_ListCategories(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListCategories(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListCategories forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListCategories: %v", err)
	}
}

func TestAcceptance_Classic_ListClasses(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListClasses(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListClasses forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListClasses: %v", err)
	}
}

func TestAcceptance_Classic_ListClassicPackages(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListClassicPackages(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListClassicPackages forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListClassicPackages: %v", err)
	}
}

func TestAcceptance_Classic_ListComputerCommands(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListComputerCommands(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListComputerCommands forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListComputerCommands: %v", err)
	}
}

func TestAcceptance_Classic_ListComputerExtensionAttributes(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListComputerExtensionAttributes(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListComputerExtensionAttributes forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListComputerExtensionAttributes: %v", err)
	}
}

func TestAcceptance_Classic_ListComputerGroups(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListComputerGroups(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListComputerGroups forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListComputerGroups: %v", err)
	}
}

func TestAcceptance_Classic_ListComputerInvitations(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListComputerInvitations(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListComputerInvitations forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListComputerInvitations: %v", err)
	}
}

func TestAcceptance_Classic_ListComputerReports(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListComputerReports(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListComputerReports forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListComputerReports: %v", err)
	}
}

func TestAcceptance_Classic_ListDepartments(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListDepartments(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListDepartments forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListDepartments: %v", err)
	}
}

func TestAcceptance_Classic_ListDirectoryBindings(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListDirectoryBindings(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListDirectoryBindings forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListDirectoryBindings: %v", err)
	}
}

func TestAcceptance_Classic_ListDiskEncryptionConfigurations(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListDiskEncryptionConfigurations(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListDiskEncryptionConfigurations forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListDiskEncryptionConfigurations: %v", err)
	}
}

func TestAcceptance_Classic_ListDistributionPoints(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListDistributionPoints(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListDistributionPoints forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListDistributionPoints: %v", err)
	}
}

func TestAcceptance_Classic_ListDockItems(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListDockItems(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListDockItems forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListDockItems: %v", err)
	}
}

func TestAcceptance_Classic_ListEbooks(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListEbooks(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListEbooks forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListEbooks: %v", err)
	}
}

func TestAcceptance_Classic_ListHealthcareListenerRules(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListHealthcareListenerRules(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListHealthcareListenerRules forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListHealthcareListenerRules: %v", err)
	}
}

func TestAcceptance_Classic_ListHealthcareListeners(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListHealthcareListeners(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListHealthcareListeners forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListHealthcareListeners: %v", err)
	}
}

func TestAcceptance_Classic_ListIBeacons(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListIBeacons(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListIBeacons forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListIBeacons: %v", err)
	}
}

func TestAcceptance_Classic_ListInfrastructureManagers(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListInfrastructureManagers(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListInfrastructureManagers forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListInfrastructureManagers: %v", err)
	}
}

func TestAcceptance_Classic_ListJsonWebTokenConfigurations(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListJsonWebTokenConfigurations(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListJsonWebTokenConfigurations forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListJsonWebTokenConfigurations: %v", err)
	}
}

func TestAcceptance_Classic_ListLDAPServers(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListLDAPServers(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListLDAPServers forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListLDAPServers: %v", err)
	}
}

func TestAcceptance_Classic_ListLicensedSoftware(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListLicensedSoftware(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListLicensedSoftware forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListLicensedSoftware: %v", err)
	}
}

func TestAcceptance_Classic_ListMacApplications(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMacApplications(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMacApplications forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMacApplications: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceApplications(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceApplications(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceApplications forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceApplications: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceCommands(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceCommands(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceCommands forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceCommands: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceConfigurationProfiles(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceConfigurationProfiles(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceConfigurationProfiles forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceConfigurationProfiles: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceEnrollmentProfiles(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceEnrollmentProfiles(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceEnrollmentProfiles forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceEnrollmentProfiles: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceExtensionAttributes(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceExtensionAttributes(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceExtensionAttributes forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceExtensionAttributes: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceGroups(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceGroups(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceGroups forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceGroups: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceInvitations(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceInvitations(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceInvitations forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceInvitations: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDeviceProvisioningProfiles(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDeviceProvisioningProfiles(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDeviceProvisioningProfiles forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDeviceProvisioningProfiles: %v", err)
	}
}

func TestAcceptance_Classic_ListMobileDevices(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListMobileDevices(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListMobileDevices forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListMobileDevices: %v", err)
	}
}

func TestAcceptance_Classic_ListNetworkSegments(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListNetworkSegments(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListNetworkSegments forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListNetworkSegments: %v", err)
	}
}

func TestAcceptance_Classic_ListOSXConfigurationProfiles(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListOSXConfigurationProfiles(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListOSXConfigurationProfiles forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListOSXConfigurationProfiles: %v", err)
	}
}

// ListPatchAvailableTitlesBySourceID: // covered by TestAcceptance_Classic_GetPatchAvailableTitles
func TestAcceptance_Classic_ListPatchExternalSources(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListPatchExternalSources(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListPatchExternalSources forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListPatchExternalSources: %v", err)
	}
}

func TestAcceptance_Classic_ListPatchInternalSources(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListPatchInternalSources(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListPatchInternalSources forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListPatchInternalSources: %v", err)
	}
}

func TestAcceptance_Classic_ListPolicies(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListPolicies(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListPolicies forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListPolicies: %v", err)
	}
}

func TestAcceptance_Classic_ListPrinters(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListPrinters(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListPrinters forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListPrinters: %v", err)
	}
}

func TestAcceptance_Classic_ListRemovableMacAddresses(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListRemovableMacAddresses(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListRemovableMacAddresses forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListRemovableMacAddresses: %v", err)
	}
}

func TestAcceptance_Classic_ListRestrictedSoftware(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListRestrictedSoftware(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListRestrictedSoftware forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListRestrictedSoftware: %v", err)
	}
}

func TestAcceptance_Classic_ListScripts(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListScripts(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListScripts forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListScripts: %v", err)
	}
}

func TestAcceptance_Classic_ListSites(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListSites(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListSites forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListSites: %v", err)
	}
}

func TestAcceptance_Classic_ListSoftwareUpdateServers(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListSoftwareUpdateServers(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListSoftwareUpdateServers forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListSoftwareUpdateServers: %v", err)
	}
}

func TestAcceptance_Classic_ListUserExtensionAttributes(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListUserExtensionAttributes(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListUserExtensionAttributes forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListUserExtensionAttributes: %v", err)
	}
}

func TestAcceptance_Classic_ListUserGroups(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListUserGroups(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListUserGroups forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListUserGroups: %v", err)
	}
}

func TestAcceptance_Classic_ListUsers(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListUsers(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListUsers forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListUsers: %v", err)
	}
}

func TestAcceptance_Classic_ListVPPAccounts(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListVPPAccounts(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListVPPAccounts forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListVPPAccounts: %v", err)
	}
}

func TestAcceptance_Classic_ListVPPAssignments(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListVPPAssignments(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListVPPAssignments forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListVPPAssignments: %v", err)
	}
}

func TestAcceptance_Classic_ListVPPInvitations(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListVPPInvitations(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListVPPInvitations forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListVPPInvitations: %v", err)
	}
}

func TestAcceptance_Classic_ListWebhooks(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListWebhooks(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListWebhooks forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListWebhooks: %v", err)
	}
}

func TestAcceptance_Classic_ListSavedSearches(t *testing.T) {
	c := accClient(t)
	if _, err := proclassic.New(c).ListSavedSearches(context.Background()); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			t.Skipf("ListSavedSearches forbidden on this tenant/credentials: %v", err)
		}
		t.Fatalf("ListSavedSearches: %v", err)
	}
}
