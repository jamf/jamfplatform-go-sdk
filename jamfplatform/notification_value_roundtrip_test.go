// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestNotificationValue_MarshalXML_MethodFirst pins the wire-element order
// for the two <notification> tags Classic emits inside <self_service>.
// Sending <bool> before <method> causes the server-side parser to silently
// drop the bool (next GET returns <notification>false</notification>). The
// admin UI writes <method> first; the SDK must do the same. Wire-probed on
// the Pro tenant tenant 2026-05-24.
func TestNotificationValue_MarshalXML_MethodFirst(t *testing.T) {
	n := proclassic.NotificationValue{
		Enabled: new(true),
		Method:  new("Self Service"),
	}
	payload := struct {
		XMLName xml.Name                      `xml:"wrap"`
		N       *proclassic.NotificationValue `xml:"notification"`
	}{N: &n}

	buf, err := xml.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	const want = "<notification>Self Service</notification><notification>true</notification>"
	if !strings.Contains(got, want) {
		t.Fatalf("expected method-first order.\n  want: %s\n  got:  %s", want, got)
	}
	if idxBool, idxMethod := strings.Index(got, ">true<"), strings.Index(got, ">Self Service<"); idxMethod < 0 || idxBool < 0 || idxMethod > idxBool {
		t.Fatalf("method must precede bool in: %s", got)
	}
}

// TestNotificationValue_RoundTrip confirms Unmarshal still recovers both
// pieces regardless of the order they appear on the wire — the bug is
// purely on Marshal; decode tolerates either order.
func TestNotificationValue_RoundTrip(t *testing.T) {
	cases := []string{
		`<wrap><notification>Self Service</notification><notification>true</notification></wrap>`,
		`<wrap><notification>true</notification><notification>Self Service</notification></wrap>`,
	}
	for _, wire := range cases {
		t.Run(wire, func(t *testing.T) {
			var out struct {
				N *proclassic.NotificationValue `xml:"notification"`
			}
			if err := xml.Unmarshal([]byte(wire), &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if out.N == nil || out.N.Enabled == nil || !*out.N.Enabled {
				t.Errorf("Enabled lost: %+v", out.N)
			}
			if out.N == nil || out.N.Method == nil || *out.N.Method != "Self Service" {
				t.Errorf("Method lost: %+v", out.N)
			}
		})
	}
}
