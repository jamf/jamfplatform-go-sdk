// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/aigovernance"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/blueprints"
)

// AI Governance is environment-scoped: X-Environment-Id is required: true and the
// gateway accepts the header for this product. Note the namespace is
// ai/governance/policies with slashes — every published spec says
// `ai-governance`, which the gateway 404s. See CLAUDE.md.
//
// Everything asserted here was probed against a live EU environment on
// 2026-08-30; where the server contradicts the spec the wire wins and the
// disagreement is named in a comment.

// BlueprintDeployment.lastDeployment is `nullable: true` + a single-member
// `allOf` — OpenAPI 3.0's only way to hang siblings off a $ref. That wrapper
// used to fall through to `any`, hiding DeploymentRun's fields behind a map
// type-assert. This is the tripwire: generated packages carry no hand-written
// tests, so a regression to `any` has to fail at compile time. `go vet -tags
// acceptance ./jamfplatform/` is what runs it.
var _ = func() *aigovernance.DeploymentRun {
	return aigovernance.BlueprintDeployment{}.LastDeployment
}

// aigovErrorCodes returns the `code` values from an API error's structured
// details. AI Governance populates these on every non-2xx (`POLICY_NOT_FOUND`,
// `SCHEMA_VALIDATION_FAILED`, …), which makes them worth asserting on rather
// than the status code alone — several distinct faults share a status.
func aigovErrorCodes(t *testing.T, err error) (int, []string, string) {
	t.Helper()
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIResponseError, got %T: %v", err, err)
	}
	var codes []string
	for _, d := range apiErr.Details() {
		codes = append(codes, d.Code)
	}
	return apiErr.StatusCode, codes, apiErr.Summary()
}

// requireAigovError asserts a call failed with a given status and, when named,
// a given error code.
func requireAigovError(t *testing.T, label string, err error, wantStatus int, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected %d %s, got success", label, wantStatus, wantCode)
	}
	status, codes, summary := aigovErrorCodes(t, err)
	if status != wantStatus {
		t.Errorf("%s: status = %d, want %d (%s)", label, status, wantStatus, summary)
	}
	if wantCode != "" && !slicesContains(codes, wantCode) {
		t.Errorf("%s: error codes = %v, want one to be %q (%s)", label, codes, wantCode, summary)
	}
	t.Logf("%s: %d %v — %s", label, status, codes, summary)
}

func slicesContains(hay []string, needle string) bool {
	return slices.Contains(hay, needle)
}

// versionLabel renders currentVersionNumber, which is a *int because a policy
// that has never been published carries an explicit null.
func versionLabel(n *int) string {
	if n == nil {
		return "unpublished"
	}
	return strconv.Itoa(*n)
}

func TestAcceptance_AiGovernanceReads(t *testing.T) {
	g := aigovernance.New(accEnvClient(t))
	ctx := context.Background()

	tools, err := g.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	t.Logf("%d tools (totalCount=%d)", len(tools.Results), tools.TotalCount)
	for _, tool := range tools.Results {
		t.Logf("  %s (%s) current=%s all=%v", tool.ID, tool.DisplayName, tool.SchemaVersion, tool.SchemaVersions)
	}
	if len(tools.Results) == 0 {
		t.Fatal("no tools returned — every policy needs a toolId, so an empty catalogue makes the write tests impossible")
	}
	// The tools list is not paginated server-side: page/page-size are ignored
	// and the whole catalogue comes back in one response (wire-verified —
	// page-size=1 still returned all three tools). That is why the op carries
	// no `pagination` key in config, and why totalCount must equal the slice
	// length rather than exceeding it.
	if tools.TotalCount != len(tools.Results) {
		t.Errorf("ListTools totalCount = %d but returned %d results — the tools list is unpaginated, so a mismatch means the response is being truncated",
			tools.TotalCount, len(tools.Results))
	}

	// GetTool / GetToolSchema against a real tool and one of its own versions,
	// rather than a hardcoded id: the catalogue is Jamf-managed and moves.
	first := tools.Results[0]
	tool, err := g.GetTool(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetTool(%s): %v", first.ID, err)
	}
	if tool.ID != first.ID || tool.DisplayName != first.DisplayName {
		t.Errorf("GetTool(%s) = {%s, %q}, list said {%s, %q}", first.ID, tool.ID, tool.DisplayName, first.ID, first.DisplayName)
	}

	// Every declared schema version must be fetchable — a version the catalogue
	// advertises but cannot serve would make a policy authored against it
	// unwritable, and the 422 would land on the caller rather than here.
	for _, v := range first.SchemaVersions {
		schema, err := g.GetToolSchema(ctx, first.ID, v)
		if err != nil {
			t.Errorf("GetToolSchema(%s, %s): %v", first.ID, v, err)
			continue
		}
		// VendorJsonSchema is json.RawMessage by design (the shape is the
		// vendor's, not Jamf's). Asserting it parses as an object is the only
		// structural check available, and it catches the field arriving as a
		// JSON string of a schema rather than the schema itself.
		var doc map[string]any
		if err := json.Unmarshal(schema.Schema, &doc); err != nil {
			t.Errorf("GetToolSchema(%s, %s): schema is not a JSON object: %v", first.ID, v, err)
			continue
		}
		t.Logf("GetToolSchema(%s, %s): %d bytes, %d top-level keys", first.ID, v, len(schema.Schema), len(doc))
	}

	policies, err := g.ListPolicies(ctx, nil, false)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	t.Logf("%d policies in this environment", len(policies))

	for _, p := range policies {
		t.Logf("  %s tool=%s status=%v drift=%v version=%s hasDraft=%v", p.Name, p.ToolID, p.Status, p.SchemaDrift, versionLabel(p.CurrentVersionNumber), p.HasDraft)

		// Archiving removes a policy from the list *and* makes GetPolicy 404
		// (see the lifecycle test), so nothing reachable here is ever ARCHIVED.
		// The enum's second value is unobservable through the API.
		if p.Status != aigovernance.PolicySummaryStatusActive {
			t.Errorf("ListPolicies returned %s with status %q — archived policies are excluded from the list, so only ACTIVE should appear", p.Name, p.Status)
		}

		detail, err := g.GetPolicy(ctx, p.ID)
		if err != nil {
			t.Errorf("GetPolicy(%s): %v", p.ID, err)
			continue
		}
		if detail.Name != p.Name {
			t.Errorf("GetPolicy(%s).Name = %q, list said %q", p.ID, detail.Name, p.Name)
		}
		// The list item deliberately omits settings; the detail must carry them.
		// An empty RawMessage here means the field was absent, which would make
		// every read-modify-write cycle send `null` back.
		if len(detail.Settings) == 0 {
			t.Errorf("GetPolicy(%s).Settings is empty — PolicyDetail declares settings required", p.ID)
		}

		versions, err := g.ListPolicyVersions(ctx, p.ID)
		if err != nil {
			t.Errorf("ListPolicyVersions(%s): %v", p.ID, err)
			continue
		}
		t.Logf("    %d versions", len(versions))
		// currentVersionNumber is the latest published version, so it must be
		// present in the version list and absent when the list is empty.
		switch {
		case p.CurrentVersionNumber == nil && len(versions) != 0:
			t.Errorf("%s: currentVersionNumber is null but %d versions exist", p.Name, len(versions))
		case p.CurrentVersionNumber != nil && len(versions) == 0:
			t.Errorf("%s: currentVersionNumber = %d but no versions exist", p.Name, *p.CurrentVersionNumber)
		}
		if len(versions) > 0 {
			n := strconv.Itoa(versions[0].VersionNumber)
			v, err := g.GetPolicyVersion(ctx, p.ID, n)
			if err != nil {
				t.Errorf("GetPolicyVersion(%s, %s): %v", p.ID, n, err)
			} else if v.PolicyID != p.ID || v.VersionNumber != versions[0].VersionNumber {
				t.Errorf("GetPolicyVersion(%s, %s) returned policyId=%s version=%d", p.ID, n, v.PolicyID, v.VersionNumber)
			}
		}

		// The deployment endpoint always answers 200, including for a policy
		// that has never been published — it returns an empty blueprints array
		// rather than a 404. An earlier revision of this test tolerated a 404
		// as "not published"; that branch was dead, and tolerating it would
		// have hidden the endpoint answering the wrong status.
		dep, err := g.GetPolicyDeployment(ctx, p.ID)
		if err != nil {
			t.Errorf("GetPolicyDeployment(%s): %v", p.ID, err)
			continue
		}
		for _, bd := range dep.Blueprints {
			// lastDeployment is documented as present for DEPLOYED and
			// OUT_OF_DATE and null for NOT_DEPLOYED. Asserting it is what
			// exercises the typed *DeploymentRun the generator now emits;
			// while the endpoint is broken (below) this loop never runs.
			hasRun := bd.LastDeployment != nil
			wantRun := bd.State != aigovernance.BlueprintDeploymentStateNotDeployed
			if hasRun != wantRun {
				t.Errorf("blueprint %s state=%s lastDeployment!=nil is %v, want %v", bd.BlueprintID, bd.State, hasRun, wantRun)
			}
			if hasRun {
				t.Logf("    blueprint %s state=%s lastDeployment={started=%s state=%s}",
					bd.BlueprintID, bd.State, bd.LastDeployment.Started, bd.LastDeployment.State)
			}
		}
	}

	// schemaDrift=true narrows to policies authored against a superseded vendor
	// schema. schemaDrift=false is *not* the complement — the param is a
	// switch, not a boolean filter, and false disables it (wire-verified: false
	// returns every policy, drifted ones included). So the only relation that
	// holds is subset.
	drifted, err := g.ListPolicies(ctx, nil, true)
	if err != nil {
		t.Fatalf("ListPolicies(schemaDrift=true): %v", err)
	}
	if len(drifted) > len(policies) {
		t.Errorf("schemaDrift=true returned %d policies, more than the unfiltered %d — the filter is not being applied", len(drifted), len(policies))
	}
	for _, p := range drifted {
		if !p.SchemaDrift {
			t.Errorf("schemaDrift=true returned %s whose schemaDrift is false — the filter is being ignored", p.Name)
		}
	}
	t.Logf("%d of %d policies have schema drift", len(drifted), len(policies))

	// sort is applied server-side over createdAt/updatedAt/name. Only the first
	// criterion is honoured (wire-verified: a bogus second criterion is
	// accepted where a bogus first is rejected), so a single key is all that
	// can be asserted.
	byName, err := g.ListPolicies(ctx, []string{"name:asc"}, false)
	if err != nil {
		t.Fatalf("ListPolicies(sort=name:asc): %v", err)
	}
	for i := 1; i < len(byName); i++ {
		if strings.Compare(byName[i-1].Name, byName[i].Name) > 0 {
			t.Errorf("sort=name:asc returned %q before %q", byName[i-1].Name, byName[i].Name)
		}
	}
}

// TestAcceptance_AiGovernanceReadRejections pins the not-found and validation
// answers. Nothing here can mutate anything — every request names an id that
// does not exist or a body the server refuses — so it runs ungated, and it is
// the only coverage the error codes get.
func TestAcceptance_AiGovernanceReadRejections(t *testing.T) {
	g := aigovernance.New(accEnvClient(t))
	ctx := context.Background()
	const missing = "00000000-0000-0000-0000-000000000000"

	t.Run("GetPolicy unknown id", func(t *testing.T) {
		_, err := g.GetPolicy(ctx, missing)
		requireAigovError(t, "GetPolicy", err, 404, "POLICY_NOT_FOUND")
	})

	t.Run("GetPolicy malformed id", func(t *testing.T) {
		// The path param is declared format: uuid, but the server answers 404
		// rather than 400 for a non-UUID — it looks the id up as an opaque
		// string. Worth pinning: a consumer cannot use 400 to detect a
		// malformed id.
		_, err := g.GetPolicy(ctx, "not-a-uuid")
		requireAigovError(t, "GetPolicy(not-a-uuid)", err, 404, "POLICY_NOT_FOUND")
	})

	t.Run("ListPolicyVersions unknown policy", func(t *testing.T) {
		_, err := g.ListPolicyVersions(ctx, missing)
		requireAigovError(t, "ListPolicyVersions", err, 404, "POLICY_NOT_FOUND")
	})

	t.Run("GetPolicyDeployment unknown policy", func(t *testing.T) {
		_, err := g.GetPolicyDeployment(ctx, missing)
		requireAigovError(t, "GetPolicyDeployment", err, 404, "POLICY_NOT_FOUND")
	})

	t.Run("GetTool unknown id", func(t *testing.T) {
		_, err := g.GetTool(ctx, "com.example.does-not-exist")
		requireAigovError(t, "GetTool", err, 404, "TOOL_ID_UNKNOWN")
	})

	t.Run("GetToolSchema unknown version", func(t *testing.T) {
		tools, err := g.ListTools(ctx)
		if err != nil || len(tools.Results) == 0 {
			t.Skipf("Skipping: no tool to probe (%v)", err)
		}
		// 422, though the operation declares only 200/401/403/404. Report
		// upstream; assert the wire.
		_, err = g.GetToolSchema(ctx, tools.Results[0].ID, "1999-01-01")
		requireAigovError(t, "GetToolSchema(bad version)", err, 422, "SCHEMA_VERSION_UNKNOWN")
	})

	t.Run("CreatePolicy empty body", func(t *testing.T) {
		// A missing required member is a 400 with one detail per field, while a
		// *semantic* failure (unknown tool, unknown schema version, settings
		// failing the vendor schema) is a 422. That split is the contract a
		// consumer branches on, so both halves are pinned.
		_, err := g.CreatePolicy(ctx, &aigovernance.CreatePolicyRequest{})
		requireAigovError(t, "CreatePolicy{}", err, 400, "VALIDATION_FAILED")
	})

	t.Run("CreatePolicy unknown tool", func(t *testing.T) {
		_, err := g.CreatePolicy(ctx, &aigovernance.CreatePolicyRequest{
			Name:          "sdk-acc-should-not-exist",
			ToolID:        "com.example.does-not-exist",
			SchemaVersion: "2026-01-01",
			Settings:      json.RawMessage(`{}`),
		})
		requireAigovError(t, "CreatePolicy(unknown tool)", err, 422, "TOOL_ID_UNKNOWN")
	})

	t.Run("CreatePolicy unknown schema version", func(t *testing.T) {
		tools, err := g.ListTools(ctx)
		if err != nil || len(tools.Results) == 0 {
			t.Skipf("Skipping: no tool to probe (%v)", err)
		}
		_, err = g.CreatePolicy(ctx, &aigovernance.CreatePolicyRequest{
			Name:          "sdk-acc-should-not-exist",
			ToolID:        tools.Results[0].ID,
			SchemaVersion: "1999-01-01",
			Settings:      json.RawMessage(`{}`),
		})
		requireAigovError(t, "CreatePolicy(unknown schemaVersion)", err, 422, "SCHEMA_VERSION_UNKNOWN")
	})

	t.Run("UpdatePolicy unknown policy", func(t *testing.T) {
		tools, err := g.ListTools(ctx)
		if err != nil || len(tools.Results) == 0 {
			t.Skipf("Skipping: no tool to probe (%v)", err)
		}
		// Body validation runs first (an illegal body against this same
		// missing id answers 400 VALIDATION_FAILED, wire-verified
		// 2026-09-09), so a legal body is what lets the request reach
		// resolution and 404 rather than stopping at 400.
		err = g.UpdatePolicy(ctx, missing, &aigovernance.UpdatePolicyRequest{
			SchemaVersion: tools.Results[0].SchemaVersion,
			Settings:      json.RawMessage(`{}`),
		}, "")
		requireAigovError(t, "UpdatePolicy(unknown policy)", err, 404, "POLICY_NOT_FOUND")
	})

	t.Run("PublishPolicy unknown policy", func(t *testing.T) {
		_, err := g.PublishPolicy(ctx, missing)
		requireAigovError(t, "PublishPolicy(unknown policy)", err, 404, "POLICY_NOT_FOUND")
	})

	t.Run("ArchivePolicy unknown policy", func(t *testing.T) {
		// Archive is not idempotent: a second archive of the same policy gives
		// the same 404 as one that never existed.
		err := g.ArchivePolicy(ctx, missing)
		requireAigovError(t, "ArchivePolicy(unknown policy)", err, 404, "POLICY_NOT_FOUND")
	})
}

// TestAcceptance_AiGovernancePolicyLifecycle covers create, read, update,
// publish and archive against a policy the test creates, so the environment's
// real policies are never touched.
//
// Publishing is inside this gate rather than behind one of its own. It used to
// have a separate opt-in on the grounds that it "deploys the policy to the
// environment's real devices"; that is wrong. Publishing only freezes the draft
// into an immutable version row. A policy reaches a device solely by being
// referenced from a blueprint's com.jamf.ai-governance component and that
// blueprint being deployed, which this suite never does. What the gate is
// actually protecting is the residue: archived policies are retained forever
// (they leave the list and 404 on read, but the row and its versions remain),
// so every run of this test permanently adds one.
func TestAcceptance_AiGovernancePolicyLifecycle(t *testing.T) {
	requireWriteOptIn(t, "JAMFPLATFORM_ACC_AIGOVERNANCE_WRITE_OK",
		"Creates and publishes a policy in a real environment. Publishing only creates a version row — a policy reaches devices only via a blueprint, which this suite never touches. The policy is archived on cleanup, but archived policies are retained permanently, so each run leaves one behind.")

	g := aigovernance.New(accEnvClient(t))
	ctx := context.Background()

	tools, err := g.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Results) == 0 || len(tools.Results[0].SchemaVersions) == 0 {
		t.Skip("Skipping: no tool with a schema version is available to author a policy against")
	}
	tool := tools.Results[0]
	schemaVersion := tool.SchemaVersion

	// Settings are validated server-side against the vendor's JSON Schema for
	// this schemaVersion, and the shape is tool-specific, so an empty object is
	// the only body that is valid for every tool. If a tool ever requires a
	// member, this fails with a 422 naming it, which is the right outcome — it
	// tells the next reader what to send rather than silently skipping.
	name := fmt.Sprintf("sdk-acc-policy-%d", rand.IntN(1_000_000))
	desc := "Created by the jamfplatform-go-sdk acceptance suite. Safe to delete."
	created, err := g.CreatePolicy(ctx, &aigovernance.CreatePolicyRequest{
		Name:          name,
		Description:   &desc,
		ToolID:        tool.ID,
		SchemaVersion: schemaVersion,
		Settings:      json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreatePolicy(tool=%s schemaVersion=%s): %v", tool.ID, schemaVersion, err)
	}
	if created.ID == "" {
		t.Fatal("CreatePolicy returned an empty ID")
	}
	// The archive subtest below archives the policy itself, so cleanup normally
	// finds it already gone. A 404 there is the success case, not a leak — but
	// any other error still has to surface, since an unarchived policy stays
	// visible to admins in a real environment.
	cleanupDelete(t, "policy "+name, func() error {
		err := g.ArchivePolicy(ctx, created.ID)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			return nil
		}
		return err
	})
	// href is populated on create and publish regardless of Content-Encoding:
	// this service emits it itself rather than relying on the gateway's
	// href-injection plugin, so the Security Cloud problem where asking for
	// gzip nulls href does not apply here. Go always asks for gzip, so an
	// empty href would mean that has changed.
	if created.Href == "" {
		t.Error("CreatePolicy returned an empty href — this service sends href itself, so an empty one means the gateway is now rewriting the body")
	}
	t.Logf("created policy %s id=%s href=%q", name, created.ID, created.Href)

	detail, err := g.GetPolicy(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPolicy(%s): %v", created.ID, err)
	}
	if detail.Name != name {
		t.Errorf("round-trip Name = %q, want %q", detail.Name, name)
	}
	if detail.ToolID != tool.ID {
		t.Errorf("round-trip ToolID = %q, want %q", detail.ToolID, tool.ID)
	}
	if detail.SchemaVersion != schemaVersion {
		t.Errorf("round-trip SchemaVersion = %q, want %q", detail.SchemaVersion, schemaVersion)
	}
	// Authoring against the tool's *current* schema version must not read back
	// as drift; that is what makes the schemaDrift filter meaningful.
	if detail.SchemaDrift {
		t.Errorf("policy authored against the current schemaVersion %s reports schemaDrift", schemaVersion)
	}
	// A never-published policy is all draft and no version.
	if !detail.HasDraft {
		t.Error("a freshly created policy reports hasDraft=false — the initial settings are a draft until published")
	}
	if detail.CurrentVersionNumber != nil {
		t.Errorf("a never-published policy has currentVersionNumber = %d, want null", *detail.CurrentVersionNumber)
	}
	// version (v2121) is the optimistic-lock counter, not the published
	// version: a freshly created policy is at 0 while currentVersionNumber is
	// still null. Wire-verified 2026-09-09 — the create's own GET answered
	// `ETag: "0"` with `"version": 0`. The spec says null means a legacy
	// document that predates versioning, so a policy this suite just created
	// must never be null.
	if detail.Version == nil {
		t.Error("a policy created just now has version = null, which the spec reserves for documents predating versioning")
	} else if *detail.Version != 0 {
		t.Errorf("a never-updated policy has version = %d, want 0", *detail.Version)
	}
	if versions, err := g.ListPolicyVersions(ctx, created.ID); err != nil {
		t.Errorf("ListPolicyVersions on a never-published policy: %v", err)
	} else if len(versions) != 0 {
		t.Errorf("a never-published policy has %d versions, want 0", len(versions))
	}

	t.Run("publish freezes the draft into a version", func(t *testing.T) {
		published, err := g.PublishPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("PublishPolicy(%s): %v", created.ID, err)
		}
		if published.VersionNumber != 1 {
			t.Errorf("first publish assigned versionNumber %d, want 1", published.VersionNumber)
		}
		if published.ID == "" || published.Href == "" {
			t.Errorf("PublishPolicy returned id=%q href=%q, both are required", published.ID, published.Href)
		}

		after, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy after publish: %v", err)
		}
		if after.HasDraft {
			t.Error("hasDraft is still true after publishing — publish is meant to consume the draft")
		}
		if after.CurrentVersionNumber == nil || *after.CurrentVersionNumber != published.VersionNumber {
			t.Errorf("currentVersionNumber = %v after publishing version %d", after.CurrentVersionNumber, published.VersionNumber)
		}

		versions, err := g.ListPolicyVersions(ctx, created.ID)
		if err != nil {
			t.Fatalf("ListPolicyVersions after publish: %v", err)
		}
		if len(versions) != 1 {
			t.Fatalf("after one publish there are %d versions, want 1", len(versions))
		}
		if versions[0].ID != published.ID {
			t.Errorf("version list carries id %s, publish returned %s", versions[0].ID, published.ID)
		}

		// Republishing with no intervening edit is refused. This is the one
		// 409 in the API and it is what makes publish safe to call from an
		// idempotent reconciler only after checking hasDraft.
		_, err = g.PublishPolicy(ctx, created.ID)
		requireAigovError(t, "PublishPolicy (no draft)", err, 409, "NO_DRAFT_TO_PUBLISH")
	})

	t.Run("update replaces settings wholesale", func(t *testing.T) {
		// PATCH is not a merge. Sending a settings object that omits a member
		// present in the stored draft *removes* that member — wire-verified,
		// and load-bearing for any consumer (the Terraform provider especially)
		// that reads, edits one key, and writes back. Nothing in the spec says
		// so, which is exactly why it is pinned here.
		//
		// The two shapes are chosen to be valid under every vendor schema: an
		// object with one unknown key, then an empty object. Unknown keys are
		// accepted (the vendor schemas do not set additionalProperties: false
		// at the root), so this does not depend on which tool was picked.
		if err := g.UpdatePolicy(ctx, created.ID, &aigovernance.UpdatePolicyRequest{
			SchemaVersion: schemaVersion,
			Settings:      json.RawMessage(`{"sdkAccProbeKey":"present"}`),
		}, ""); err != nil {
			t.Fatalf("UpdatePolicy(settings with a probe key): %v", err)
		}
		withKey, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if !strings.Contains(string(withKey.Settings), "sdkAccProbeKey") {
			t.Fatalf("settings round-trip lost the probe key: %s", withKey.Settings)
		}
		// Every PATCH increments version, whether or not it changes anything
		// (wire-verified 2026-09-09: an unconditional PATCH with the body
		// already stored still went 1 -> 2 -> 3). Publishing does not.
		firstVersion := withKey.Version
		if firstVersion == nil {
			t.Fatal("version is null after a PATCH — the optimistic-lock counter stopped being maintained")
		}

		if err := g.UpdatePolicy(ctx, created.ID, &aigovernance.UpdatePolicyRequest{
			SchemaVersion: schemaVersion,
			Settings:      json.RawMessage(`{}`),
		}, ""); err != nil {
			t.Fatalf("UpdatePolicy(empty settings): %v", err)
		}
		emptied, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if emptied.Version == nil {
			t.Error("version is null after a second PATCH")
		} else if *emptied.Version <= *firstVersion {
			t.Errorf("version did not advance across a PATCH: %d then %d", *firstVersion, *emptied.Version)
		}
		if strings.Contains(string(emptied.Settings), "sdkAccProbeKey") {
			t.Errorf("settings still carry the probe key after a PATCH that omitted it (%s) — PATCH has become a merge, and every consumer that assumed replace is now sending stale members",
				emptied.Settings)
		}

		// hasDraft is a *diff* against the published version's settings, not a
		// record that an update happened. Wire-verified: the policy was
		// published with `{}`, so patching the probe key in sets hasDraft and
		// patching back to `{}` clears it again — even though both PATCHes
		// returned 204 and both bumped the policy's write counter.
		//
		// This is the trap for a reconciler that patches and then publishes only
		// when hasDraft is true: a patch that lands on the published settings
		// leaves nothing to publish, and calling publish anyway is a 409
		// NO_DRAFT_TO_PUBLISH rather than a no-op.
		if emptied.HasDraft {
			t.Errorf("hasDraft is true after patching the settings back to the published version's %s — hasDraft is meant to mean the draft differs from what is published", emptied.Settings)
		}
		if err := g.UpdatePolicy(ctx, created.ID, &aigovernance.UpdatePolicyRequest{
			SchemaVersion: schemaVersion,
			Settings:      json.RawMessage(`{"sdkAccProbeKey":"again"}`),
		}, ""); err != nil {
			t.Fatalf("UpdatePolicy(divergent settings): %v", err)
		}
		divergent, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if !divergent.HasDraft {
			t.Error("hasDraft is false while the draft settings differ from the published version — publish-if-hasDraft would skip a real change")
		}
	})

	t.Run("If-Match makes the update conditional", func(t *testing.T) {
		// The optimistic-lock protocol v2121 declared, exercised through the
		// SDK. Wire-established 2026-09-09; three halves are load-bearing and
		// none of them is guessable from the spec:
		//
		//   - version is the ETag's value, so a caller needs no header
		//     plumbing to take part: strconv.FormatInt(*d.Version, 10) is a
		//     valid precondition. This is the assertion that keeps that true.
		//   - the server matches on the number, accepting the bare form, the
		//     quoted strong validator and the quoted weak one alike. A caller
		//     that quotes and one that does not both work, which is why the
		//     generated argument is a plain string rather than something that
		//     formats an int64 for you.
		//   - a stale value is 409 POLICY_VERSION_CONFLICT, and an omitted
		//     header is an unconditional update. Both are asserted, because
		//     the failure mode of getting this wrong is silent: an update the
		//     caller believes is conditional simply overwrites.
		before, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if before.Version == nil {
			t.Skip("policy has no version — conditional update is unavailable for documents predating versioning")
		}
		body := func() *aigovernance.UpdatePolicyRequest {
			return &aigovernance.UpdatePolicyRequest{SchemaVersion: schemaVersion, Settings: json.RawMessage(`{}`)}
		}

		// A version that cannot be current. Both quoted and bare, since the
		// two travel differently on the wire and only one was ever probed
		// before.
		for _, stale := range []string{
			strconv.FormatInt(*before.Version+1000, 10),
			`"` + strconv.FormatInt(*before.Version+1000, 10) + `"`,
		} {
			err := g.UpdatePolicy(ctx, created.ID, body(), stale)
			requireAigovError(t, "UpdatePolicy(If-Match: "+stale+")", err, 409, "POLICY_VERSION_CONFLICT")
		}
		if unchanged, err := g.GetPolicy(ctx, created.ID); err != nil {
			t.Fatalf("GetPolicy after the refused updates: %v", err)
		} else if unchanged.Version == nil || *unchanged.Version != *before.Version {
			t.Errorf("a refused conditional update still moved version: %v then %v", before.Version, unchanged.Version)
		}

		// Each accepted form advances the counter by one, so the next
		// iteration has to re-read it — which is the read-modify-write loop a
		// consumer writes.
		for _, form := range []struct{ label, format string }{
			{"bare", "%s"},
			{"quoted", `"%s"`},
			{"weak", `W/"%s"`},
		} {
			live, err := g.GetPolicy(ctx, created.ID)
			if err != nil {
				t.Fatalf("GetPolicy before the %s precondition: %v", form.label, err)
			}
			if live.Version == nil {
				t.Fatalf("version went null mid-test")
			}
			value := fmt.Sprintf(form.format, strconv.FormatInt(*live.Version, 10))
			if err := g.UpdatePolicy(ctx, created.ID, body(), value); err != nil {
				t.Fatalf("UpdatePolicy(If-Match: %s, %s form): %v", value, form.label, err)
			}
			after, err := g.GetPolicy(ctx, created.ID)
			if err != nil {
				t.Fatalf("GetPolicy after the %s precondition: %v", form.label, err)
			}
			if after.Version == nil || *after.Version != *live.Version+1 {
				t.Errorf("%s precondition: version went %v -> %v, want +1", form.label, live.Version, after.Version)
			}
		}

		// An omitted header is unconditional, which is what every other
		// UpdatePolicy call in this file relies on.
		live, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if err := g.UpdatePolicy(ctx, created.ID, body(), ""); err != nil {
			t.Fatalf("UpdatePolicy with no precondition: %v", err)
		}
		if after, err := g.GetPolicy(ctx, created.ID); err != nil {
			t.Fatalf("GetPolicy: %v", err)
		} else if after.Version == nil || *after.Version != *live.Version+1 {
			t.Errorf("unconditional update: version went %v -> %v, want +1", live.Version, after.Version)
		}
	})

	t.Run("update can rename without touching the name", func(t *testing.T) {
		// name and description are optional on update: omitting them must leave
		// the stored values alone, which is the only way to edit settings
		// without restating the identity.
		renamed := name + "-updated"
		if err := g.UpdatePolicy(ctx, created.ID, &aigovernance.UpdatePolicyRequest{
			Name:          &renamed,
			SchemaVersion: schemaVersion,
			Settings:      json.RawMessage(`{}`),
		}, ""); err != nil {
			t.Fatalf("UpdatePolicy(rename): %v", err)
		}
		after, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy after rename: %v", err)
		}
		if after.Name != renamed {
			t.Errorf("after rename, Name = %q, want %q", after.Name, renamed)
		}
		if after.Description == nil || *after.Description != desc {
			t.Errorf("omitting description on update changed it to %v, want %q", after.Description, desc)
		}

		if err := g.UpdatePolicy(ctx, created.ID, &aigovernance.UpdatePolicyRequest{
			SchemaVersion: schemaVersion,
			Settings:      json.RawMessage(`{}`),
		}, ""); err != nil {
			t.Fatalf("UpdatePolicy(no name): %v", err)
		}
		still, err := g.GetPolicy(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if still.Name != renamed {
			t.Errorf("omitting name on update changed it to %q, want %q", still.Name, renamed)
		}
	})

	t.Run("archive makes the policy unreachable but keeps its versions", func(t *testing.T) {
		// The spec calls archive a soft delete that "retains all published
		// versions for audit trail integrity". On the wire the parent becomes
		// unreachable — GET, PATCH, publish and a second archive all 404, and
		// the policy leaves the list — while the version sub-resources keep
		// answering 200. So ARCHIVED is a status no caller can ever read, and
		// an archived policy's audit trail is reachable only by an id the
		// caller must already hold.
		//
		// This is asserted rather than logged because it is the whole delete
		// contract: a consumer that expects status ARCHIVED on read, or expects
		// re-archiving to be idempotent, is wrong today and needs to find out
		// from a test rather than from production.
		versionsBefore, err := g.ListPolicyVersions(ctx, created.ID)
		if err != nil {
			t.Fatalf("ListPolicyVersions before archive: %v", err)
		}

		if err := g.ArchivePolicy(ctx, created.ID); err != nil {
			t.Fatalf("ArchivePolicy(%s): %v", created.ID, err)
		}

		_, err = g.GetPolicy(ctx, created.ID)
		requireAigovError(t, "GetPolicy after archive", err, 404, "POLICY_NOT_FOUND")

		err = g.ArchivePolicy(ctx, created.ID)
		requireAigovError(t, "ArchivePolicy twice", err, 404, "POLICY_NOT_FOUND")

		_, err = g.PublishPolicy(ctx, created.ID)
		requireAigovError(t, "PublishPolicy after archive", err, 404, "POLICY_NOT_FOUND")

		versionsAfter, err := g.ListPolicyVersions(ctx, created.ID)
		if err != nil {
			t.Errorf("ListPolicyVersions after archive: %v — the versions of an archived policy are documented as retained", err)
		} else if len(versionsAfter) != len(versionsBefore) {
			t.Errorf("archive changed the version count from %d to %d", len(versionsBefore), len(versionsAfter))
		}

		policies, err := g.ListPolicies(ctx, nil, false)
		if err != nil {
			t.Fatalf("ListPolicies after archive: %v", err)
		}
		for _, p := range policies {
			if p.ID == created.ID {
				t.Errorf("archived policy %s is still in the list with status %q", p.ID, p.Status)
			}
		}
	})
}

// TestAcceptance_AiGovernanceDeploymentReportsReferencingBlueprints pins a
// server defect: GET /v1/policies/{id}/deployment answers 200 with an empty
// blueprints array even for a policy a blueprint demonstrably references.
//
// Probed 2026-08-30 three ways — two pre-existing policies referenced by a
// DEPLOYED blueprint, and a blueprint created for the purpose referencing a
// freshly published policy. All three reported nothing. The blueprints service
// does resolve the link (referencing a nonexistent policy version is refused
// with 400 POLICY_VERSION_NOT_FOUND), so the reference exists and the AI
// governance side is not reading it.
//
// The test cross-references the two APIs off existing environment state rather
// than building a fixture, and FAILS when the endpoint starts working — that is
// the point. When it does, delete the inversion, keep the assertion, and drop
// the note from CLAUDE.md. Do not weaken it to a skip: a silently empty
// response is exactly what went unnoticed until it was probed by hand.
func TestAcceptance_AiGovernanceDeploymentReportsReferencingBlueprints(t *testing.T) {
	c := accEnvClient(t)
	g := aigovernance.New(c)
	bp := blueprints.New(c)
	ctx := context.Background()

	overviews, err := bp.ListBlueprints(ctx, nil, "")
	if err != nil {
		t.Skipf("Skipping: cannot list blueprints on this credential: %v", err)
	}

	// policyID -> blueprint IDs whose com.jamf.ai-governance component names it.
	refs := map[string][]string{}
	for _, o := range overviews {
		detail, err := bp.GetBlueprint(ctx, o.ID)
		if err != nil {
			t.Logf("GetBlueprint(%s): %v", o.ID, err)
			continue
		}
		for _, step := range detail.Steps {
			for _, comp := range step.Components {
				if comp.Identifier != "com.jamf.ai-governance" {
					continue
				}
				var cfg struct {
					Policies []struct {
						PolicyID      string `json:"policyId"`
						VersionNumber int    `json:"versionNumber"`
					} `json:"policies"`
				}
				if err := json.Unmarshal(comp.Configuration, &cfg); err != nil {
					t.Errorf("blueprint %s: com.jamf.ai-governance configuration does not parse: %v", o.ID, err)
					continue
				}
				for _, p := range cfg.Policies {
					refs[p.PolicyID] = append(refs[p.PolicyID], detail.ID)
				}
			}
		}
	}

	if len(refs) == 0 {
		t.Skip("Skipping: no blueprint in this environment references an AI governance policy, so there is nothing to cross-check")
	}

	for policyID, blueprintIDs := range refs {
		dep, err := g.GetPolicyDeployment(ctx, policyID)
		if err != nil {
			// A blueprint may reference a policy that has since been archived,
			// which 404s. That is not the defect under test.
			t.Logf("GetPolicyDeployment(%s): %v — referenced by %v; archived?", policyID, err, blueprintIDs)
			continue
		}
		if len(dep.Blueprints) > 0 {
			t.Errorf("GetPolicyDeployment(%s) now reports %d blueprints (referenced by %v) — the endpoint has been FIXED. Invert this test to assert the referencing blueprints appear, and update the AI Governance section of CLAUDE.md.",
				policyID, len(dep.Blueprints), blueprintIDs)
			continue
		}
		t.Logf("KNOWN DEFECT: policy %s is referenced by blueprint(s) %v, GetPolicyDeployment reports 0 blueprints", policyID, blueprintIDs)
	}
}

// TestAcceptance_AiGovernancePreviewHeader asserts the one preview claim no
// generated method can surface. GitOps v2192's info.description states that
// "every successful (2xx) response carries a `Jamf-Preview: true` header", and
// v2121 declared the header on all twelve operations — but the transport
// discards response headers on success, so a caller cannot see it and neither
// can a test written against a generated method. This one goes through
// Transport().HTTPClient(), which carries the OAuth bearer, and stamps the
// scope header itself from the exported Client.Scope accessor, since
// setScopeHeader runs inside Do rather than in a RoundTripper.
//
// It is asserted rather than logged because the header is the wire's own
// statement that these shapes are unstable; the day it stops arriving is the
// day the endpoints have graduated, and that should fail here and send the
// reader to the ai-governance row of CLAUDE.md's package table.
func TestAcceptance_AiGovernancePreviewHeader(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()

	kind, scopeID := c.Scope()
	tr := c.Transport()

	// Two operations, one a collection and one an item, so a header attached
	// to a single handler rather than to the product cannot pass this.
	for _, path := range []string{"/v1/tools", "/v1/policies"} {
		url := tr.BaseURL() + tr.APIPrefix("ai/governance/policies", "") + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("NewRequest(%s): %v", url, err)
		}
		if h := kind.ScopeHeader(); h != "" && scopeID != "" {
			req.Header.Set(h, scopeID)
		}

		resp, err := tr.HTTPClient().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200 (%s)", path, resp.StatusCode, truncateBody(body))
		}
		if got := resp.Header.Get("Jamf-Preview"); got != "true" {
			t.Errorf("GET %s: Jamf-Preview = %q, want \"true\". If the header has been withdrawn the endpoints may have graduated out of preview — check x-preview in the spec, then update the ai-governance row of CLAUDE.md's package table and the generated Preview godoc line.", path, got)
			continue
		}
		t.Logf("GET %s: 200 with Jamf-Preview: true", path)
	}
}

// truncateBody keeps a failure message readable when the body is a tool schema.
func truncateBody(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "… (truncated)"
}
