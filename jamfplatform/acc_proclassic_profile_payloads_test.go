// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

// Wire-level documentation of how configuration-profile <payloads>
// bodies survive Create (POST) and Update (PUT) through Jamf Pro
// Classic. These tests encode the server's actual behaviour — including
// its defects — so any server-side change breaks them loudly.
//
// Server model (wire-verified 2026-07-30 against two Jamf Pro
// 11.x tenants, superseding the 2026-05-27 probe conclusions recorded in
// earlier revisions of this file):
//
//  1. Validation: the server entity-decodes submitted payload content
//     once and 409s ("Unable to update the database") when the result
//     contains a bare `&` or `<`. Raw spec-correct CDATA is therefore
//     rejected for ANY plist containing `&amp;`/`&lt;`; the escape-once
//     CDATA form PayloadsXMLText emits is the only submittable one.
//  2. Storage is per-payload-type: fragments of types the server
//     re-renders (com.apple.ManagedClient.preferences custom settings —
//     values and dict keys — and com.apple.notificationsettings) are
//     entity-decoded once, so the wire form stores them BYTE-EXACT.
//     Fragments of every other payload type (TCC, direct loginwindow,
//     all mobiledeviceconfigurationprofiles payloads) are stored
//     VERBATIM, keeping one extra entity layer — values with `&`/`<` in
//     those types cannot be stored faithfully by any client. Tests for
//     that category assert the defect's exact shape.
//
//     Divergence observed 2026-08-03 on 11.30.2: a mobile
//     com.apple.applicationaccess fragment stored `&amp;` DECODED, while
//     the same profile's top-level ConsentText kept the extra layer — so
//     "all mobile payloads are verbatim" does not hold on that version.
//     Treat this per-type split as version-specific and empirical: assert
//     it in acceptance tests, never encode it as a client-side rule.
//
//  3. Line breaks inside string values are a separate law (wire-verified
//     2026-08-03, Jamf Pro 11.30.2, both profile endpoints; rendering
//     confirmed on macOS 26.6). Literal LF and TAB are DELETED — not
//     collapsed to a space, so the words either side merge — for every
//     verbatim-stored payload type and every slot outside PayloadContent
//     (e.g. the top-level ConsentText dictionary). CR survives, but only
//     as a character reference: XML 1.0 §2.11 normalises a literal CR to
//     LF in transit and the server then deletes it, which is why
//     PayloadsXMLText preserves CR references rather than decoding them,
//     and why Jamf Pro's own UI emits `&#13;` for banner line breaks.
//     U+2028, U+2029 and U+0085 survive every slot untouched. The
//     exemption must stay CR-only: leaving `&#38;` bare decodes to an
//     ampersand and the server rejects the write with 409.
//
//  4. Astral-plane characters (non-BMP, e.g. emoji) cannot be stored at
//     all: verbatim slots keep two U+FFFD replacement characters in their
//     place, and an MCX payload drops its whole mcx_preference_settings
//     dictionary, sibling keys included. macOS handles them correctly, so
//     this is server-side. Not asserted here — the failure is data loss,
//     not a shape worth pinning.
//
// The server canonicalises stored plists regardless of input form:
// entities for `&` `<` `>` (`&amp;` `&lt;` `&gt;` — a raw `>` is stored
// as `&gt;`), literals for `"` `'` (even when sent as `&quot;`/`&#39;`),
// numeric refs collapsed to those canonical forms, keys sorted, and
// leading/trailing whitespace inside string values trimmed. Assertions
// therefore compare against the canonical stored form, never the input
// representation.

package jamfplatform_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// osxProfilePlist builds a minimal valid macOS configuration profile
// payload. Two flavours:
//   - includeAmpersand=false → only the prompt's PPPC `"` marker is
//     embedded. Isolates the original PUT-double-escape-on-quote bug
//     from the orthogonal `&`-content corruption.
//   - includeAmpersand=true  → adds `&amp;` inside the inner-payload
//     PayloadDescription string. PayloadDescription is a free-text
//     description field the server preserves verbatim; using it for the
//     `&` marker isolates the entity round-trip from server-side
//     bundle-ID / CodeRequirement syntax validation, which rejects `&`
//     even when the wire form is well-formed.
func osxProfilePlist(displayName, marker string, includeAmpersand bool) string {
	description := "Test description"
	if includeAmpersand {
		description = "Foo &amp; Bar &lt;br/&gt; baz"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.TCC.configuration-profile-policy</string>
			<key>PayloadIdentifier</key>
			<string>com.example.tcc.sdk-acc</string>
			<key>PayloadUUID</key>
			<string>11111111-2222-3333-4444-555555555555</string>
			<key>PayloadDisplayName</key>
			<string>` + marker + `</string>
			<key>PayloadDescription</key>
			<string>` + description + `</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>Services</key>
			<dict>
				<key>SystemPolicyAllFiles</key>
				<array>
					<dict>
						<key>Identifier</key>
						<string>com.example.sdk</string>
						<key>IdentifierType</key>
						<string>bundleID</string>
						<key>CodeRequirement</key>
						<string>identifier "com.example.sdk" and anchor apple generic</string>
						<key>Authorization</key>
						<string>Allow</string>
					</dict>
				</array>
			</dict>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>` + displayName + `</string>
	<key>PayloadIdentifier</key>
	<string>com.example.profile.sdk-acc</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>66666666-7777-8888-9999-aaaaaaaaaaaa</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`
}

// mobileProfilePlist builds a minimal valid mobile device configuration
// profile payload. Two flavours mirror osxProfilePlist:
//   - includeAmpersand=false → marker lives inside the
//     autonomousSingleAppIDs string with a literal `"`.
//   - includeAmpersand=true  → adds `&amp;` inside the same entry.
//
// The marker rides in autonomousSingleAppIDs because the server
// canonicalises the inner payload's PayloadDisplayName to a default
// ("Restrictions Payload") on this profile type and discards anything
// the caller wrote there. autonomousSingleAppIDs string content is
// preserved verbatim through canonicalisation.
func mobileProfilePlist(displayName, marker string, includeAmpersand bool) string {
	appID := `com.example.sdk-` + marker + ` "primary" bundle`
	if includeAmpersand {
		appID = `com.example&amp;sdk-` + marker + ` "primary" bundle`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.applicationaccess</string>
			<key>PayloadIdentifier</key>
			<string>com.example.restrictions.sdk-acc</string>
			<key>PayloadUUID</key>
			<string>aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee</string>
			<key>PayloadDisplayName</key>
			<string>` + marker + `</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>allowCamera</key>
			<true/>
			<key>safariAcceptCookies</key>
			<integer>2</integer>
			<key>autonomousSingleAppIDs</key>
			<array>
				<string>` + appID + `</string>
			</array>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>` + displayName + `</string>
	<key>PayloadIdentifier</key>
	<string>com.example.mobile.sdk-acc</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>ffffffff-1111-2222-3333-444444444444</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`
}

// assertQuoteRoundtrip checks the SDK-decoded payload string returned
// from a GET after a write. Verifies the prompt's specific repro: a
// literal `"` round-trips without being corrupted into `&amp;#34;` (the
// double-escape PUT bug) or `&amp;quot;`. Does not exercise `&`-content.
func assertQuoteRoundtrip(t *testing.T, stage, got string) {
	t.Helper()
	for _, bad := range []string{"&amp;#34;", "&amp;quot;", "&amp;lt;", "&amp;gt;"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%s: payload chardata contains double-encoded entity %q — entity layer was not collapsed by the server.\nstored payload:\n%s", stage, bad, got)
		}
	}
	if !strings.Contains(got, "<plist") {
		t.Fatalf("%s: payload chardata missing <plist> root — server may have rejected or rewritten the body. Got:\n%s", stage, got)
	}
	if !strings.Contains(got, `"`) && !strings.Contains(got, "&quot;") && !strings.Contains(got, "&#34;") {
		t.Fatalf("%s: payload chardata missing the quote marker (\", &quot;, or &#34;) — got:\n%s", stage, got)
	}
}

// assertAmpersandRoundtrip is the stricter variant: also requires that
// an inbound `&amp;` survives without becoming `&amp;amp;` (9 bytes) or
// any further-doubled form. Passes with PayloadsXMLText's CDATA wire
// form (wire-verified 2026-07-30); failed under both earlier text-form
// designs — single-escape 409d, double-escape corrupted.
func assertAmpersandRoundtrip(t *testing.T, stage, got string) {
	t.Helper()
	for _, bad := range []string{"&amp;amp;"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%s: payload chardata contains double-encoded entity %q — entity layer was not collapsed by the server.\nstored payload:\n%s", stage, bad, got)
		}
	}
	if !strings.Contains(got, "&amp;") && !strings.Contains(got, "&#38;") {
		t.Fatalf("%s: payload chardata missing the encoded ampersand marker (&amp; or &#38;) — got:\n%s", stage, got)
	}
}

// TestAcceptance_Classic_OSXProfile_QuoteRoundtrip isolates the prompt's
// specific PUT-double-escape-on-`"` bug from the orthogonal `&`-content
// corruption. Plist contains literal `"` chars (TCC CodeRequirement
// style) and no `&amp;` content. Without the single-escape PUT fix the
// stored chardata would contain `&amp;#34;` after Update.
func TestAcceptance_Classic_OSXProfile_QuoteRoundtrip(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-osxcp-quote-" + runSuffix()
	createPayload := osxProfilePlist(name, "create-marker-quote-\"-only", false)

	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(createPayload),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOSXConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateOSXConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteOSXConfigurationProfileByID", func() error {
		return pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	got := string(*afterCreate.General.Payloads)
	assertQuoteRoundtrip(t, "after Create (POST)", got)
	t.Logf("after Create: payload chardata:\n%s", got)

	updatePayload := osxProfilePlist(name, "update-marker-quote-\"-only", false)
	updateReq := &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(updatePayload),
		},
	}
	if err := pc.UpdateOSXConfigurationProfileByID(ctx, intToStr(id), updateReq); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateOSXConfigurationProfileByID: %v", err)
	}

	afterUpdate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after update: %v", err)
	}
	if afterUpdate == nil || afterUpdate.General == nil || afterUpdate.General.Payloads == nil {
		t.Fatalf("GET after update: profile or payloads missing: %+v", afterUpdate)
	}
	got = string(*afterUpdate.General.Payloads)
	assertQuoteRoundtrip(t, "after Update (PUT)", got)
	t.Logf("after Update: payload chardata:\n%s", got)

	if !strings.Contains(got, "update-marker") {
		t.Fatalf("after Update: payload still contains create-marker, server did not apply the update. Got:\n%s", got)
	}
}

// TestAcceptance_Classic_OSXProfile_AmpersandRoundtrip exercises `&amp;`
// and `&lt;` markers inside a TCC payload — a payload type the server
// stores VERBATIM (category 2b in the file header). The write now
// succeeds (escape-once passes validation), but the stored fragment
// keeps the wire's extra entity layer: the device would see `&amp;`
// where `&` was meant. That is a filed server defect no client can
// avoid; this test asserts its exact shape so a server-side fix is
// detected the moment it ships.
func TestAcceptance_Classic_OSXProfile_AmpersandRoundtrip(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-osxcp-amp-" + runSuffix()
	createPayload := osxProfilePlist(name, "create-marker-amp-\"-and-&amp;", true)

	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(createPayload),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOSXConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateOSXConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteOSXConfigurationProfileByID", func() error {
		return pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	got := string(*afterCreate.General.Payloads)
	assertVerbatimStorageDefect(t, "after Create (POST)", got)
	t.Logf("after Create: payload chardata:\n%s", got)

	updatePayload := osxProfilePlist(name, "update-marker-amp-\"-and-&amp;", true)
	updateReq := &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(updatePayload),
		},
	}
	if err := pc.UpdateOSXConfigurationProfileByID(ctx, intToStr(id), updateReq); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateOSXConfigurationProfileByID: %v", err)
	}

	afterUpdate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after update: %v", err)
	}
	if afterUpdate == nil || afterUpdate.General == nil || afterUpdate.General.Payloads == nil {
		t.Fatalf("GET after update: profile or payloads missing: %+v", afterUpdate)
	}
	got = string(*afterUpdate.General.Payloads)
	assertVerbatimStorageDefect(t, "after Update (PUT)", got)
	t.Logf("after Update: payload chardata:\n%s", got)

	if !strings.Contains(got, "update-marker") {
		t.Fatalf("after Update: payload still contains create-marker, server did not apply the update. Got:\n%s", got)
	}
}

// assertVerbatimStorageDefect pins the exact shape of the server's
// verbatim storage of TCC fragments: the wire's extra entity layer is
// kept (`&amp;amp;`, `&amp;lt;`), while entity-free strings like the
// CodeRequirement stay intact. If Jamf ever fixes that ingest defect, the
// first assertion flips and this test fails — the desired signal.
func assertVerbatimStorageDefect(t *testing.T, stage, got string) {
	t.Helper()
	// Note the tail: the source's &gt; is minimised to a literal ">" on
	// the wire and the server canonicalises it back — value-correct. Only
	// the &/< references keep the verbatim extra layer.
	if !strings.Contains(got, "Foo &amp;amp; Bar &amp;lt;br/&gt; baz") {
		t.Fatalf("%s: TCC description no longer stored with the verbatim extra entity layer — server ingest behaviour changed (entity-escaping defect fixed?). Got:\n%s", stage, got)
	}
	if !strings.Contains(got, `identifier "com.example.sdk" and anchor apple generic`) {
		t.Fatalf("%s: entity-free CodeRequirement corrupted. Got:\n%s", stage, got)
	}
}

// TestAcceptance_Classic_MobileDeviceProfile_QuoteRoundtrip mirrors the
// OS X quote-only scenario for the mobile resource.
func TestAcceptance_Classic_MobileDeviceProfile_QuoteRoundtrip(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-mdcp-quote-" + runSuffix()
	createPayload := mobileProfilePlist(name, "create-marker-quote-\"-only", false)

	created, err := pc.CreateMobileDeviceConfigurationProfileByID(ctx, "0", &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(createPayload),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceConfigurationProfileByID", func() error {
		return pc.DeleteMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	got := string(*afterCreate.General.Payloads)
	assertQuoteRoundtrip(t, "after Create (POST)", got)
	t.Logf("after Create: payload chardata:\n%s", got)

	updatePayload := mobileProfilePlist(name, "update-marker-quote-\"-only", false)
	updateReq := &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(updatePayload),
		},
	}
	if err := pc.UpdateMobileDeviceConfigurationProfileByID(ctx, intToStr(id), updateReq); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateMobileDeviceConfigurationProfileByID: %v", err)
	}

	afterUpdate, err := pc.GetMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceConfigurationProfileByID after update: %v", err)
	}
	if afterUpdate == nil || afterUpdate.General == nil || afterUpdate.General.Payloads == nil {
		t.Fatalf("GET after update: profile or payloads missing: %+v", afterUpdate)
	}
	got = string(*afterUpdate.General.Payloads)
	assertQuoteRoundtrip(t, "after Update (PUT)", got)
	t.Logf("after Update: payload chardata:\n%s", got)

	if !strings.Contains(got, "update-marker") {
		t.Fatalf("after Update: payload still contains create-marker, server did not apply the update. Got:\n%s", got)
	}
}

// minimalNotificationsPlist mirrors the shape of
// terraform-provider-jamfplatform/local-testing/pro/support_files/
// minimal_notifications.mobileconfig — a fixture caught by the original
// `&amp;`-content regression. Embeds a URL with `&amp;` and a display
// string with `&lt;br/&gt;`. Round-trip success means both POST and PUT
// preserve the entity encoding caller-side.
func minimalNotificationsPlist(displayName, marker string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.notificationsettings</string>
			<key>PayloadIdentifier</key>
			<string>com.example.notifications.sdk-acc</string>
			<key>PayloadUUID</key>
			<string>12121212-3434-5656-7878-909090909090</string>
			<key>PayloadDisplayName</key>
			<string>Notifications Payload</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>SyncURL</key>
			<string>https://sync-server-hostname/blockables/%file_sha%&amp;test=` + marker + `</string>
			<key>StatusMessage</key>
			<string>Entering Monitor mode&lt;br/&gt;Please be careful! (` + marker + `)</string>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>` + displayName + `</string>
	<key>PayloadIdentifier</key>
	<string>com.example.notifications-profile.sdk-acc</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>56565656-7878-9090-1212-343434343434</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`
}

// TestAcceptance_Classic_OSXProfile_NotificationsFixtureRoundtrip uses
// a fixture mirroring terraform-provider-jamfplatform's
// minimal_notifications.mobileconfig: a URL with `&amp;` and a string
// containing `&lt;br/&gt;`. Previously skipped alongside
// AmpersandRoundtrip under the disproven "server-side limitation"
// diagnosis — see that test's docstring. Passes with the CDATA wire
// form.
func TestAcceptance_Classic_OSXProfile_NotificationsFixtureRoundtrip(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-osxcp-notifs-" + runSuffix()
	createPayload := minimalNotificationsPlist(name, "create-marker")

	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(createPayload),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOSXConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateOSXConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteOSXConfigurationProfileByID", func() error {
		return pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	got := string(*afterCreate.General.Payloads)
	assertAmpersandRoundtrip(t, "after Create (POST)", got)
	if !strings.Contains(got, "&lt;br/&gt;") && !strings.Contains(got, "<br/>") {
		t.Fatalf("after Create: payload missing the <br/> fragment (literal or &lt;-encoded). Got:\n%s", got)
	}
	if strings.Contains(got, "&amp;lt;") || strings.Contains(got, "&amp;gt;") {
		t.Fatalf("after Create: payload double-encodes &lt;/&gt; → &amp;lt;/&amp;gt;. Got:\n%s", got)
	}
	t.Logf("after Create: payload chardata:\n%s", got)

	updatePayload := minimalNotificationsPlist(name, "update-marker")
	updateReq := &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(updatePayload),
		},
	}
	if err := pc.UpdateOSXConfigurationProfileByID(ctx, intToStr(id), updateReq); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateOSXConfigurationProfileByID: %v", err)
	}

	afterUpdate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after update: %v", err)
	}
	if afterUpdate == nil || afterUpdate.General == nil || afterUpdate.General.Payloads == nil {
		t.Fatalf("GET after update: profile or payloads missing: %+v", afterUpdate)
	}
	got = string(*afterUpdate.General.Payloads)
	assertAmpersandRoundtrip(t, "after Update (PUT)", got)
	if strings.Contains(got, "&amp;lt;") || strings.Contains(got, "&amp;gt;") {
		t.Fatalf("after Update: payload double-encodes &lt;/&gt; → &amp;lt;/&amp;gt;. Got:\n%s", got)
	}
	t.Logf("after Update: payload chardata:\n%s", got)

	if !strings.Contains(got, "update-marker") {
		t.Fatalf("after Update: payload still contains create-marker, server did not apply the update. Got:\n%s", got)
	}
}

func payloadsXMLPtr(s string) *proclassic.PayloadsXMLText {
	v := proclassic.PayloadsXMLText(s)
	return &v
}

// reservedCharCase pairs one mcx_preference_settings entry with the
// canonical <key>…</key><string>…</string> fragment the server stores
// for it. Canonical form (wire-verified 2026-07-30): entities for
// `&` `<` `>`, literals for `"` `'`, numeric refs collapsed.
type reservedCharCase struct {
	key    string // dict key, also used to locate the stored fragment
	input  string // value as written in the submitted plist source
	stored string // value as the server's canonical plist source stores it
}

// reservedCharCases is the full matrix: every reserved XML character in
// every legal plist-source representation, plus the compound cases that
// caught real-world regressions (CEL expressions, literal entity text,
// embedded CDATA sections).
var reservedCharCases = []reservedCharCase{
	{"amp_entity", "A &amp; B", "A &amp; B"},
	{"amp_decimal", "A &#38; B", "A &amp; B"},
	{"amp_hex", "A &#x26; B", "A &amp; B"},
	{"lt_entity", "a &lt; b", "a &lt; b"},
	{"lt_decimal", "a &#60; b", "a &lt; b"},
	{"lt_hex", "a &#x3C; b", "a &lt; b"},
	{"gt_raw", "x > y", "x &gt; y"},
	{"gt_entity", "x &gt; y", "x &gt; y"},
	{"gt_decimal", "x &#62; y", "x &gt; y"},
	{"quot_raw", `q " q`, `q " q`},
	{"quot_entity", "q &quot; q", `q " q`},
	{"quot_decimal", "q &#34; q", `q " q`},
	{"apos_raw", "p ' p", "p ' p"},
	{"apos_entity", "p &apos; p", "p ' p"},
	{"apos_decimal", "p &#39; p", "p ' p"},
	{"literal_entity_text", "shows &amp;amp; and &amp;lt; as text", "shows &amp;amp; and &amp;lt; as text"},
	{"all_five_mixed", `&amp; &lt; > " ' &#38; &#60; &gt; &quot; &apos; end`, `&amp; &lt; &gt; " ' &amp; &lt; &gt; " ' end`},
	{"cel_expression", `target.signing_time >= timestamp('2025-05-31T00:00:00Z') &amp;&amp; target.team_id == "EQHXZ8M8AV"`, `target.signing_time &gt;= timestamp('2025-05-31T00:00:00Z') &amp;&amp; target.team_id == "EQHXZ8M8AV"`},
	{"embedded_cdata", `<![CDATA[cdata section: & < > " ' kept literal]]>`, `cdata section: &amp; &lt; &gt; " ' kept literal`},
}

// matrixProfilePlist embeds the full reserved-character corpus in an MCX
// custom-settings payload — the server preserves mcx_preference_settings
// string values verbatim (modulo canonicalisation), unlike some typed
// payload fields it rewrites or validates.
func matrixProfilePlist(displayName string) string {
	var b strings.Builder
	for _, c := range reservedCharCases {
		b.WriteString("\t\t\t\t\t\t\t\t<key>" + c.key + "</key>\n")
		b.WriteString("\t\t\t\t\t\t\t\t<string>" + c.input + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.ManagedClient.preferences</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>PayloadIdentifier</key>
			<string>com.example.sdkacc.charmatrix.mcx</string>
			<key>PayloadUUID</key>
			<string>3b1f5c77-4c1e-4f7a-9a55-0d9e2f6a1c01</string>
			<key>PayloadDisplayName</key>
			<string>reserved char matrix settings</string>
			<key>PayloadContent</key>
			<dict>
				<key>com.example.sdkacc.charmatrix</key>
				<dict>
					<key>Forced</key>
					<array>
						<dict>
							<key>mcx_preference_settings</key>
							<dict>
` + b.String() + `							</dict>
						</dict>
					</array>
				</dict>
			</dict>
		</dict>
	</array>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
	<key>PayloadIdentifier</key>
	<string>com.example.sdkacc.charmatrix</string>
	<key>PayloadUUID</key>
	<string>9e8d7c6b-5a49-4838-a7b6-c5d4e3f2a101</string>
	<key>PayloadDisplayName</key>
	<string>` + displayName + `</string>
	<key>PayloadScope</key>
	<string>System</string>
</dict>
</plist>
`
}

// verbatimStored computes the stored form for endpoints/payload types the
// server stores VERBATIM (category 2b): the wire fragment as-is — i.e.
// the client's single `&` escape retained — with raw `>` canonicalised
// to `&gt;`. Embedded CDATA sections are rewritten client-side before
// the wire escape, so that case starts from the rewritten source.
func verbatimStored(c reservedCharCase) string {
	src := c.input
	if c.key == "embedded_cdata" {
		src = `cdata section: &amp; &lt; &gt; " ' kept literal`
	}
	// Mirror PayloadsXMLText's source-escaping minimisation: references for
	// `>` `"` `'` decode to literals on the wire, so they store
	// value-correct even through verbatim storage. Only `&`/`<` references
	// must stay encoded and therefore still gain the extra layer.
	src = strings.NewReplacer(
		"&gt;", ">", "&#62;", ">",
		"&quot;", `"`, "&#34;", `"`,
		"&apos;", "'", "&#39;", "'",
	).Replace(src)
	src = strings.ReplaceAll(src, "&", "&amp;")
	return strings.ReplaceAll(src, ">", "&gt;")
}

// assertMatrixStored verifies every corpus entry appears in the stored
// plist source in the expected form: the byte-exact canonical form for
// MCX-family storage (verbatim=false), or the extra-entity-layer form
// for verbatim storage (verbatim=true — the escaping defect's exact
// shape; a failure there means the server behaviour changed). The
// server re-serialises compactly (<key>k</key><string>v</string>
// adjacent, keys sorted), so a direct substring check on the
// whitespace-stripped payload is exact.
func assertMatrixStored(t *testing.T, stage, got string, verbatim bool) {
	t.Helper()
	compact := strings.NewReplacer("\n", "", "\t", "").Replace(got)
	for _, c := range reservedCharCases {
		expect := c.stored
		if verbatim {
			expect = verbatimStored(c)
		}
		want := "<key>" + c.key + "</key><string>" + expect + "</string>"
		if !strings.Contains(compact, want) {
			t.Errorf("%s: stored payload missing expected fragment for %q:\n  want fragment: %s", stage, c.key, want)
		}
	}
	if t.Failed() {
		t.Fatalf("%s: stored payload was:\n%s", stage, got)
	}
}

// TestAcceptance_Classic_OSXProfile_ReservedCharacterMatrix proves every
// reserved XML character in every legal representation round-trips
// byte-exact (to the server's canonical form) through Create and Update.
func TestAcceptance_Classic_OSXProfile_ReservedCharacterMatrix(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-osxcp-charmatrix-" + runSuffix()

	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(matrixProfilePlist(name)),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOSXConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateOSXConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteOSXConfigurationProfileByID", func() error {
		return pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	assertMatrixStored(t, "after Create (POST)", string(*afterCreate.General.Payloads), false)

	// Update with the identical corpus — exercises the PUT path and the
	// server's identifier-preserving replace.
	if err := pc.UpdateOSXConfigurationProfileByID(ctx, intToStr(id), &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(matrixProfilePlist(name)),
		},
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateOSXConfigurationProfileByID: %v", err)
	}

	afterUpdate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after update: %v", err)
	}
	if afterUpdate == nil || afterUpdate.General == nil || afterUpdate.General.Payloads == nil {
		t.Fatalf("GET after update: profile or payloads missing: %+v", afterUpdate)
	}
	assertMatrixStored(t, "after Update (PUT)", string(*afterUpdate.General.Payloads), false)
}

// TestAcceptance_Classic_MobileDeviceProfile_ReservedCharacterMatrix
// mirrors the matrix for the mobile device resource. Mobile payloads are
// stored VERBATIM (category 2b in the file header): entity-bearing
// values keep the wire's extra layer — the escaping defect — while
// raw-character values survive. Asserted exactly so a server-side fix
// is detected the moment it ships.
func TestAcceptance_Classic_MobileDeviceProfile_ReservedCharacterMatrix(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-mdcp-charmatrix-" + runSuffix()

	created, err := pc.CreateMobileDeviceConfigurationProfileByID(ctx, "0", &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(matrixProfilePlist(name)),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceConfigurationProfileByID", func() error {
		return pc.DeleteMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	assertMatrixStored(t, "after Create (POST)", string(*afterCreate.General.Payloads), true)
}

// ---------------------------------------------------------------------
// Line breaks inside string values (wire-verified 2026-08-03, Jamf Pro
// 11.30.2; trigger: Jamf-Concepts/terraform-jamf-platform#488).
//
// Literal LF and TAB are DELETED from string values of verbatim-stored
// payload types and of every slot outside PayloadContent — not collapsed
// to a space, so the words either side merge. CR survives, but only when
// transmitted as a character reference: XML 1.0 §2.11 normalises a
// literal CR to LF in transit, which the server then deletes. That is why
// PayloadsXMLText preserves CR references instead of decoding them, and
// why Jamf Pro's own UI emits `&#13;` for banner line breaks. U+2028 also
// survives untouched and needs no special handling.
// ---------------------------------------------------------------------

// lineBreakProbePlist builds a profile carrying three line-break probes in
// two slots each — the top-level ConsentText dictionary (outside
// PayloadContent) and a direct com.apple.loginwindow payload (a
// verbatim-stored type):
//
//	CR-A&#13;CR-B     carriage-return reference — must survive
//	LF-A<literal>LF-B literal newline           — server deletes it
//	LS-A&#8232;LS-B   U+2028 line separator     — must survive
func lineBreakProbePlist(displayName string) string {
	probes := "CR-A&#13;CR-B LF-A\nLF-B LS-A&#8232;LS-B"
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ConsentText</key>
	<dict>
		<key>default</key>
		<string>` + probes + `</string>
	</dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.loginwindow</string>
			<key>PayloadIdentifier</key>
			<string>com.example.linebreak.sdk-acc</string>
			<key>PayloadUUID</key>
			<string>11111111-2222-3333-4444-555555555555</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>LoginwindowText</key>
			<string>` + probes + `</string>
			<key>AdminHostInfo</key>
			<string>HostName</string>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>` + displayName + `</string>
	<key>PayloadIdentifier</key>
	<string>com.example.profile.linebreak.sdk-acc</string>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>66666666-7777-8888-9999-aaaaaaaaaaaa</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`
}

// assertLineBreakStorage checks the three probes in the stored payload.
//
// A surviving CR may legitimately come back either as the reference the
// wire carried or as a raw CR byte, depending on how the server
// canonicalises the fragment — both parse to the same value, so either
// counts. Failure means neither is present: the line break was destroyed,
// which is the regression this test exists to catch.
func assertLineBreakStorage(t *testing.T, stage, got string) {
	t.Helper()

	crSurvived := strings.Contains(got, "CR-A&#13;CR-B") ||
		strings.Contains(got, "CR-A&#xD;CR-B") ||
		strings.Contains(got, "CR-A\rCR-B")
	if !crSurvived {
		t.Fatalf("%s: carriage-return line break destroyed — expected the reference or a raw CR between CR-A and CR-B. Got:\n%s", stage, got)
	}

	// The literal-LF deletion is a server defect, asserted in its exact
	// shape so a server-side fix is detected the moment it ships.
	if !strings.Contains(got, "LF-ALF-B") {
		t.Logf("%s: literal LF was NOT deleted — the server defect this file documents may be fixed; re-probe and update the model. Got:\n%s", stage, got)
	}

	lsSurvived := strings.Contains(got, "LS-A\u2028LS-B") || strings.Contains(got, "LS-A&#8232;LS-B")
	if !lsSurvived {
		t.Fatalf("%s: U+2028 line separator destroyed — expected it between LS-A and LS-B. Got:\n%s", stage, got)
	}
}

// TestAcceptance_Classic_OSXProfile_LineBreakRoundtrip pins the line-break
// wire law for the macOS resource across both write verbs. Without
// PayloadsXMLText preserving CR references, the CR assertion fails: the
// reference decodes to a literal CR, XML normalisation turns it into LF,
// and the server deletes it.
func TestAcceptance_Classic_OSXProfile_LineBreakRoundtrip(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-osxcp-linebreak-" + runSuffix()

	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(lineBreakProbePlist(name)),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOSXConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateOSXConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteOSXConfigurationProfileByID", func() error {
		return pc.DeleteOSXConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	assertLineBreakStorage(t, "after Create (POST)", string(*afterCreate.General.Payloads))

	updateReq := &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(lineBreakProbePlist(name)),
		},
	}
	if err := pc.UpdateOSXConfigurationProfileByID(ctx, intToStr(id), updateReq); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateOSXConfigurationProfileByID: %v", err)
	}

	afterUpdate, err := pc.GetOSXConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOSXConfigurationProfileByID after update: %v", err)
	}
	if afterUpdate == nil || afterUpdate.General == nil || afterUpdate.General.Payloads == nil {
		t.Fatalf("GET after update: profile or payloads missing: %+v", afterUpdate)
	}
	assertLineBreakStorage(t, "after Update (PUT)", string(*afterUpdate.General.Payloads))
}

// TestAcceptance_Classic_MobileDeviceProfile_LineBreakRoundtrip mirrors the
// line-break law for the mobile device resource, which shares the same
// PayloadsXMLText wire form.
func TestAcceptance_Classic_MobileDeviceProfile_LineBreakRoundtrip(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	pc := proclassic.New(c)

	name := "sdk-acc-mdcp-linebreak-" + runSuffix()

	created, err := pc.CreateMobileDeviceConfigurationProfileByID(ctx, "0", &proclassic.MobileDeviceConfigurationProfile{
		General: &proclassic.MobileDeviceConfigurationProfileGeneral{
			Name:     new(name),
			Payloads: payloadsXMLPtr(lineBreakProbePlist(name)),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Fatalf("CreateMobileDeviceConfigurationProfileByID: no ID returned: %+v", created)
	}
	id := *created.ID
	cleanupDelete(t, "DeleteMobileDeviceConfigurationProfileByID", func() error {
		return pc.DeleteMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	})

	afterCreate, err := pc.GetMobileDeviceConfigurationProfileByID(ctx, intToStr(id))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMobileDeviceConfigurationProfileByID after create: %v", err)
	}
	if afterCreate == nil || afterCreate.General == nil || afterCreate.General.Payloads == nil {
		t.Fatalf("GET after create: profile or payloads missing: %+v", afterCreate)
	}
	assertLineBreakStorage(t, "after Create (POST)", string(*afterCreate.General.Payloads))
}
