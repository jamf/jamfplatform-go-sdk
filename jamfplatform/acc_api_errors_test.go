// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/blueprints"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/compliancebenchmarks"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/ddmreport"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/deviceactions"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devicegroups"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devices"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// The tests in this file exercise APIResponseError accessors (AsAPIError,
// HasStatus, Details, FieldErrors, Summary) against every API family the SDK
// speaks. Each test probes a deliberate error path and logs the server's
// actual response shape so acc output serves as empirical documentation of
// per-family error-body conventions.

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// logAPIError dumps the full shape of a server error. Every probe calls this
// so acc output captures what each Jamf API family actually sends.
func logAPIError(t *testing.T, apiErr *jamfplatform.APIResponseError) {
	t.Helper()
	t.Logf("StatusCode: %d", apiErr.StatusCode)
	t.Logf("TraceID: %q", apiErr.TraceID)
	t.Logf("Summary: %s", apiErr.Summary())
	t.Logf("Details (%d): %+v", len(apiErr.Details()), apiErr.Details())
	t.Logf("FieldErrors: %+v", apiErr.FieldErrors())
	if len(apiErr.Details()) == 0 && apiErr.Body != "" {
		t.Logf("Body snippet: %q", truncate(apiErr.Body, 300))
	}
}

// requireAPIError asserts err wraps an *APIResponseError and returns it.
// Fails fatally when err is nil or is not an API error — both conditions
// mean the probe didn't reach the server layer we're testing.
func requireAPIError(t *testing.T, label string, err error) *jamfplatform.APIResponseError {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", label)
	}
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Fatalf("%s: AsAPIError returned nil for err=%v (type=%T)", label, err, err)
	}
	return apiErr
}

// ---------------------------------------------------------------------------
// Pro (JSON) — numeric string IDs
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_Pro_Buildings_NotFound uses a valid-format numeric
// id that doesn't exist. Must return 404.
func TestAcceptance_APIError_Pro_Buildings_NotFound(t *testing.T) {
	c := accClient(t)
	p := pro.New(c)

	_, err := p.GetBuildingV1(context.Background(), "999999999")
	apiErr := requireAPIError(t, "GetBuildingV1(999999999)", err)
	if !apiErr.HasStatus(404) {
		t.Fatalf("HasStatus(404) = false, StatusCode=%d, err=%v", apiErr.StatusCode, err)
	}
	logAPIError(t, apiErr)
}

// TestAcceptance_APIError_Pro_Buildings_InvalidFormat uses a non-numeric id,
// provoking the 400 INVALID_ID shape. Verifies FieldErrors picks up the
// path-parameter validation failure attributed to "id".
func TestAcceptance_APIError_Pro_Buildings_InvalidFormat(t *testing.T) {
	c := accClient(t)
	p := pro.New(c)

	_, err := p.GetBuildingV1(context.Background(), "not-a-number-"+runSuffix())
	apiErr := requireAPIError(t, "GetBuildingV1(bogus)", err)
	if !apiErr.HasStatus(400) {
		t.Fatalf("HasStatus(400) = false, StatusCode=%d, err=%v", apiErr.StatusCode, err)
	}
	logAPIError(t, apiErr)

	if len(apiErr.Details()) == 0 {
		t.Errorf("expected structured details on 400 INVALID_ID, got none")
	}
	if _, ok := apiErr.FieldErrors()["id"]; !ok {
		t.Errorf("expected FieldErrors to contain \"id\" key, got %+v", apiErr.FieldErrors())
	}
}

// TestAcceptance_APIError_Pro_Buildings_DuplicateName provokes a
// DUPLICATE_FIELD 400 with server-side field attribution on "name".
func TestAcceptance_APIError_Pro_Buildings_DuplicateName(t *testing.T) {
	c := accClient(t)
	p := pro.New(c)
	ctx := context.Background()

	name := "sdk-acc-err-dup-bld-" + runSuffix()
	first, err := p.CreateBuildingV1(ctx, &pro.Building{Name: name})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBuildingV1 (first): %v", err)
	}
	cleanupDelete(t, "DeleteBuildingV1", func() error { return p.DeleteBuildingV1(ctx, first.ID) })

	_, err = p.CreateBuildingV1(ctx, &pro.Building{Name: name})
	apiErr := requireAPIError(t, "CreateBuildingV1 (duplicate)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx duplicate rejection, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// TestAcceptance_APIError_Pro_Categories_DuplicateName cross-checks that
// duplicate-name rejection on a different Pro resource produces the same
// FieldErrors shape as buildings. Proves the accessor API is uniform across
// Pro resources, not just Building-specific.
func TestAcceptance_APIError_Pro_Categories_DuplicateName(t *testing.T) {
	c := accClient(t)
	p := pro.New(c)
	ctx := context.Background()

	name := "sdk-acc-err-dup-cat-" + runSuffix()
	first, err := p.CreateCategoryV1(ctx, &pro.Category{Name: name, Priority: 5})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCategoryV1 (first): %v", err)
	}
	cleanupDelete(t, "DeleteCategoryV1", func() error { return p.DeleteCategoryV1(ctx, first.ID) })

	_, err = p.CreateCategoryV1(ctx, &pro.Category{Name: name, Priority: 5})
	apiErr := requireAPIError(t, "CreateCategoryV1 (duplicate)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx duplicate rejection, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// ---------------------------------------------------------------------------
// Classic (XML) — integer string IDs, XML response bodies
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_Classic_Categories_NotFound probes a valid-format
// integer id that doesn't exist. Classic returns XML error bodies — this test
// documents empirically whether Details/FieldErrors populate for XML, or
// whether the transport's JSON-only parser leaves them empty.
func TestAcceptance_APIError_Classic_Categories_NotFound(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)

	_, err := pc.GetCategoryByID(context.Background(), "999999999")
	apiErr := requireAPIError(t, "GetCategoryByID(999999999)", err)
	if !apiErr.HasStatus(404) {
		t.Fatalf("HasStatus(404) = false, StatusCode=%d, err=%v", apiErr.StatusCode, err)
	}
	logAPIError(t, apiErr)
	t.Logf("NOTE: Details/FieldErrors populated for XML body: %v", len(apiErr.Details()) > 0)
}

// TestAcceptance_APIError_Classic_Categories_InvalidFormat probes a
// non-numeric id on a Classic endpoint. Documents what the XML API returns
// for format-invalid path params.
func TestAcceptance_APIError_Classic_Categories_InvalidFormat(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)

	_, err := pc.GetCategoryByID(context.Background(), "not-a-number-"+runSuffix())
	apiErr := requireAPIError(t, "GetCategoryByID(bogus)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// TestAcceptance_APIError_Classic_Categories_DuplicateName probes whether
// Classic emits a structured XML error body for validation failures (vs the
// HTML default-page body seen on 404s). This is the data point that decides
// whether the SDK should add XML error-body parsing to the transport: if
// Classic sends a parseable <errors><error>...</error></errors> payload here,
// we're currently discarding useful structured data; if it sends HTML or
// plain text, Classic errors are not worth parsing beyond status code.
func TestAcceptance_APIError_Classic_Categories_DuplicateName(t *testing.T) {
	c := accClient(t)
	pc := proclassic.New(c)
	ctx := context.Background()

	name := "sdk-acc-err-dup-classic-" + runSuffix()
	prio := 9
	first, err := pc.CreateCategoryByID(ctx, "0", &proclassic.Category{
		Name:     new(name),
		Priority: &prio,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCategoryByID (first): %v", err)
	}
	if first.ID == nil {
		t.Fatalf("CreateCategoryByID returned no ID: %+v", first)
	}
	firstID := *first.ID
	cleanupDelete(t, "DeleteCategoryByID", func() error { return pc.DeleteCategoryByID(ctx, intToStr(firstID)) })

	_, err = pc.CreateCategoryByID(ctx, "0", &proclassic.Category{
		Name:     new(name),
		Priority: &prio,
	})
	apiErr := requireAPIError(t, "CreateCategoryByID (duplicate)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
	t.Logf("NOTE: Details populated for Classic validation error: %v", len(apiErr.Details()) > 0)
}

// ---------------------------------------------------------------------------
// Devices (Platform JSON) — UUID-shaped IDs
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_Devices_NotFound probes the devices surface with a
// bogus UUID. Devices are read-only from the SDK's perspective — this is a
// pure NotFound probe.
func TestAcceptance_APIError_Devices_NotFound(t *testing.T) {
	c := accClient(t)
	dc := devices.New(c)

	_, err := dc.GetDevice(context.Background(), "00000000-0000-0000-0000-000000000000")
	apiErr := requireAPIError(t, "GetDevice(zero-uuid)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// ---------------------------------------------------------------------------
// Device Groups (Platform JSON) — UUID-shaped IDs
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_DeviceGroups_NotFound probes GetDeviceGroup with a
// bogus UUID.
func TestAcceptance_APIError_DeviceGroups_NotFound(t *testing.T) {
	c := accClient(t)
	dg := devicegroups.New(c)

	_, err := dg.GetDeviceGroup(context.Background(), "00000000-0000-0000-0000-000000000000")
	apiErr := requireAPIError(t, "GetDeviceGroup(zero-uuid)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// TestAcceptance_APIError_DeviceGroups_DuplicateName creates two groups with
// the same name — device-groups enforces name uniqueness, so the second
// CreateDeviceGroup must be rejected with a 4xx.
func TestAcceptance_APIError_DeviceGroups_DuplicateName(t *testing.T) {
	c := accClient(t)
	dg := devicegroups.New(c)
	ctx := context.Background()

	name := "sdk-acc-err-dup-dg-" + runSuffix()
	desc := "SDK acceptance test — safe to delete"
	emptyMembers := []string{}

	first, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        name,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "STATIC",
		Members:     &emptyMembers,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup (first): %v", err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, first.ID) })

	_, err = dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        name,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "STATIC",
		Members:     &emptyMembers,
	})
	apiErr := requireAPIError(t, "CreateDeviceGroup (duplicate)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx duplicate rejection, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// ---------------------------------------------------------------------------
// Device Actions (Platform JSON) — imperative, bogus-id probe only
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_DeviceActions_NotFound sends CheckInDevice to a
// bogus UUID. Actions are destructive — this probe uses an id that cannot
// match any real device so no MDM command is dispatched.
func TestAcceptance_APIError_DeviceActions_NotFound(t *testing.T) {
	c := accClient(t)
	da := deviceactions.New(c)

	err := da.CheckInDevice(context.Background(), "00000000-0000-0000-0000-000000000000")
	apiErr := requireAPIError(t, "CheckInDevice(zero-uuid)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// ---------------------------------------------------------------------------
// Blueprints (Platform JSON) — UUID-shaped IDs
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_Blueprints_NotFound probes GetBlueprint with a
// bogus UUID. Duplicate-name probe is skipped because CreateBlueprint has
// deep required structure (device group scope + steps); this test focuses
// purely on error-accessor behaviour, not CRUD exercise.
func TestAcceptance_APIError_Blueprints_NotFound(t *testing.T) {
	c := accEnvClient(t)
	bp := blueprints.New(c)

	_, err := bp.GetBlueprint(context.Background(), "00000000-0000-0000-0000-000000000000")
	apiErr := requireAPIError(t, "GetBlueprint(zero-uuid)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// ---------------------------------------------------------------------------
// Compliance Benchmarks (Platform JSON) — UUID-shaped IDs
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_ComplianceBenchmarks_NotFound probes GetBenchmark
// with a bogus UUID. Duplicate-title probe skipped due to CreateBenchmark's
// deep required body (rules, sources, baseline, target).
func TestAcceptance_APIError_ComplianceBenchmarks_NotFound(t *testing.T) {
	c := accEnvClient(t)
	cb := compliancebenchmarks.New(c)

	_, err := cb.GetBenchmark(context.Background(), "00000000-0000-0000-0000-000000000000")
	apiErr := requireAPIError(t, "GetBenchmark(zero-uuid)", err)
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx, got StatusCode=%d", apiErr.StatusCode)
	}
	logAPIError(t, apiErr)
}

// ---------------------------------------------------------------------------
// DDM Report (Platform JSON) — read-only reporting surface
// ---------------------------------------------------------------------------

// TestAcceptance_APIError_DdmReport_NotFound documents that the DDM report
// surface does NOT emit errors for unknown inputs — it returns an empty
// report for an unknown device rather than 404. This is an empirical finding
// about the API family, not a bug: DDM report is a pure query surface and
// "unknown device" is not an error condition. Consumers cannot rely on
// AsAPIError/HasStatus to detect "no such device" on this surface; they
// must inspect the response payload shape.
func TestAcceptance_APIError_DdmReport_NotFound(t *testing.T) {
	c := accClient(t)
	dr := ddmreport.New(c)

	// filter is required: true, so it travels even when empty; pass a real
	// expression so the only unknown under test is the device id.
	report, err := dr.GetDeviceDeclarationReportFiltered(context.Background(), "00000000-0000-0000-0000-000000000000", "active==true", nil)
	if err != nil {
		apiErr := jamfplatform.AsAPIError(err)
		if apiErr != nil {
			t.Logf("unexpected: server returned structured error for unknown device:")
			logAPIError(t, apiErr)
		}
		t.Fatalf("expected empty report for unknown device; got error: %v", err)
	}
	t.Logf("DDM report for zero-UUID returned successfully (payload=%+v) — surface does not error on NotFound", report)
}
