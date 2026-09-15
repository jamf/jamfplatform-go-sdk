// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestPolicyPost_OmissionSemantics locks in the wire shape contract the
// upcoming terraform-provider-jamfplatform Pro policy resource depends on.
// The provider distinguishes "not in plan" (field omitted on the wire) from
// "explicitly cleared" (empty parent or false-valued field emitted on the
// wire) — if the generator's omitempty handling drifts, the provider's input
// builders start sending unintended state to the server.
func TestPolicyPost_OmissionSemantics(t *testing.T) {
	cases := []struct {
		name    string
		in      proclassic.PolicyPost
		expect  string
		expects []string
		absent  []string
	}{
		{
			name:   "nameOnly_omitsScope",
			in:     proclassic.PolicyPost{General: &proclassic.PolicyPostGeneral{Name: new("x")}},
			expect: "<name>x</name>",
			absent: []string{"<scope", "<reboot", "<self_service", "<scripts", "<package_configuration"},
		},
		{
			name:   "emptyScope_emitsEmptyParent",
			in:     proclassic.PolicyPost{Scope: &proclassic.PolicyPostScope{}},
			expect: "<scope></scope>",
		},
		{
			name: "scopeBuilding_emitsFull",
			in: proclassic.PolicyPost{
				Scope: &proclassic.PolicyPostScope{
					Buildings: &proclassic.PolicyScopeBuildings{
						Building: &[]proclassic.IDName{{ID: new(1), Name: new("A")}},
					},
				},
			},
			expect: "<buildings><building><id>1</id><name>A</name></building></buildings>",
		},
		{
			name:   "allComputersFalse_isNotOmitted",
			in:     proclassic.PolicyPost{Scope: &proclassic.PolicyPostScope{AllComputers: new(false)}},
			expect: "<all_computers>false</all_computers>",
		},
		{
			name:   "rebootNil_isAbsent",
			in:     proclassic.PolicyPost{General: &proclassic.PolicyPostGeneral{Name: new("x")}, Reboot: nil},
			absent: []string{"<reboot"},
		},
		{
			name: "rebootPopulated_roundTrips",
			in: proclassic.PolicyPost{
				Reboot: &proclassic.PolicyPostReboot{
					Message:                     new("restart"),
					MinutesUntilReboot:          new(10),
					StartRebootTimerImmediately: new(true),
					FileVault2Reboot:            new(false),
				},
			},
			expects: []string{"<reboot>", "<message>restart</message>", "<minutes_until_reboot>10</minutes_until_reboot>", "<start_reboot_timer_immediately>true</start_reboot_timer_immediately>", "<file_vault_2_reboot>false</file_vault_2_reboot>"},
		},
		{
			name: "scopeJssUsers_emitsParentWithChildList",
			in: proclassic.PolicyPost{
				Scope: &proclassic.PolicyPostScope{
					JssUsers: &proclassic.PolicyScopeJssUsers{
						User: &[]proclassic.IDName{{ID: new(10), Name: new("u")}},
					},
				},
			},
			expect: "<jss_users><user><id>10</id><name>u</name></user></jss_users>",
		},
		{
			name: "scopeExclusionsJssUserGroups_emits",
			in: proclassic.PolicyPost{
				Scope: &proclassic.PolicyPostScope{
					Exclusions: &proclassic.PolicyScopeExclusions{
						JssUserGroups: &proclassic.PolicyScopeExclusionsJssUserGroups{
							UserGroup: &[]proclassic.IDName{{ID: new(1), Name: new("All")}},
						},
					},
				},
			},
			expect: "<exclusions><jss_user_groups><user_group><id>1</id><name>All</name></user_group></jss_user_groups></exclusions>",
		},
		{
			name: "selfServiceNotificationFields_emit",
			in: proclassic.PolicyPost{
				SelfService: &proclassic.PolicyPostSelfService{
					Notification:        &proclassic.NotificationValue{Enabled: new(true)},
					NotificationType:    new("Self Service"),
					NotificationSubject: new("SDK Builder"),
					NotificationMessage: new("test"),
				},
			},
			expects: []string{"<notification>true</notification>", "<notification_type>Self Service</notification_type>", "<notification_subject>SDK Builder</notification_subject>", "<notification_message>test</notification_message>"},
		},
		{
			name: "printersParentStruct_emits",
			in: proclassic.PolicyPost{
				Printers: &proclassic.PolicyPostPrinters{
					Size:                 new(1),
					LeaveExistingDefault: new(false),
					Printer: &[]proclassic.PolicyPrintersPrinterItem{
						{ID: new(67), Name: new("Printer"), Action: new("install"), MakeDefault: new(true)},
					},
				},
			},
			expects: []string{"<printers>", "<size>1</size>", "<leave_existing_default>false</leave_existing_default>", "<printer>", "<id>67</id>", "<name>Printer</name>", "<action>install</action>", "<make_default>true</make_default>"},
		},
		{
			name: "noExecuteOnDay_isList",
			in: proclassic.PolicyPost{
				General: &proclassic.PolicyPostGeneral{
					DateTimeLimitations: &proclassic.PolicyGeneralDateTimeLimitations{
						NoExecuteOn: &proclassic.PolicyGeneralDateTimeLimitationsNoExecuteOn{
							Day: &[]string{"Sun", "Mon", "Tue"},
						},
					},
				},
			},
			expect: "<no_execute_on><day>Sun</day><day>Mon</day><day>Tue</day></no_execute_on>",
		},
		{
			name: "accountMaintenanceHintAndSecureToken_emit",
			in: proclassic.PolicyPost{
				AccountMaintenance: &proclassic.PolicyPostAccountMaintenance{
					Accounts: &proclassic.PolicyAccountMaintenanceAccounts{
						Account: &[]proclassic.PolicyAccountMaintenanceAccountsAccountItem{
							{
								Username:           new("admin"),
								Hint:               new("pet"),
								SecureTokenAllowed: new(true),
							},
						},
					},
				},
			},
			expect: "<hint>pet</hint><secure_token_allowed>true</secure_token_allowed>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := xml.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := string(buf)
			if tc.expect != "" && !strings.Contains(got, tc.expect) {
				t.Errorf("expect substring not found.\n  want: %s\n  got:  %s", tc.expect, got)
			}
			for _, e := range tc.expects {
				if !strings.Contains(got, e) {
					t.Errorf("expected substring %q not found in: %s", e, got)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("expected substring %q to be absent, got: %s", a, got)
				}
			}
		})
	}
}

// TestPolicyScopeLimitToUsers_MultipleGroups locks in the wire shape for
// `<limit_to_users><user_groups>` with N>1 user groups. The earlier spec
// shape modelled `user_groups` as `array<{user_group}>` which encoding/xml
// emits as one `<user_groups>` wrapper per slice element (malformed for
// N>=2; the server discards all but the last on decode). Server-correct
// shape is a single `<user_groups>` wrapper with repeated `<user_group>`
// children — mirroring sibling `<limitations><user_groups>`.
func TestPolicyScopeLimitToUsers_MultipleGroups(t *testing.T) {
	limit := &proclassic.PolicyScopeLimitToUsers{
		UserGroups: &proclassic.PolicyScopeLimitToUsersUserGroups{
			UserGroup: &[]string{"GroupA", "GroupB"},
		},
	}
	buf, err := xml.Marshal(limit)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "<limit_to_users><user_groups><user_group>GroupA</user_group><user_group>GroupB</user_group></user_groups></limit_to_users>"
	if string(buf) != want {
		t.Errorf("marshal mismatch.\n  want: %s\n  got:  %s", want, buf)
	}

	var got proclassic.PolicyScopeLimitToUsers
	if err := xml.Unmarshal([]byte(want), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.UserGroups == nil || got.UserGroups.UserGroup == nil {
		t.Fatalf("UserGroups.UserGroup nil after decode: %+v", got)
	}
	if g := *got.UserGroups.UserGroup; len(g) != 2 || g[0] != "GroupA" || g[1] != "GroupB" {
		t.Errorf("decode lost children: %v", g)
	}
}

// TestPolicyTagMismatchFixes_Decode catches the silent-decode failures the
// brief flagged: previous spec tags `re-install_button_text` and
// `allow_user_to_defer` never decoded because the wire elements are
// `reinstall_button_text` and `allow_users_to_defer`. Distinguish
// "field present and recognised" (correct) from "field present and dropped"
// (silent decode failure) by populating only the new wire shape and
// verifying the Go field reads back non-nil.
func TestPolicyTagMismatchFixes_Decode(t *testing.T) {
	xmlIn := `<policy>
		<self_service><reinstall_button_text>Reinstall</reinstall_button_text></self_service>
		<user_interaction><allow_users_to_defer>true</allow_users_to_defer></user_interaction>
	</policy>`

	var p proclassic.Policy
	if err := xml.Unmarshal([]byte(xmlIn), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.SelfService == nil || p.SelfService.ReinstallButtonText == nil || *p.SelfService.ReinstallButtonText != "Reinstall" {
		t.Errorf("ReinstallButtonText not decoded: %+v", p.SelfService)
	}
	if p.UserInteraction == nil || p.UserInteraction.AllowUsersToDefer == nil || *p.UserInteraction.AllowUsersToDefer != true {
		t.Errorf("AllowUsersToDefer not decoded: %+v", p.UserInteraction)
	}
}

// TestPolicyPost_RoundTrip ensures every block reachable via PolicyPost
// marshalled by xml.Marshal decodes back into a Policy without losing
// fields. Locks in the brief's "Post symmetry" contract — a write payload
// must round-trip into the read shape so the server's view of the resource
// matches what the client sent.
func TestPolicyPost_RoundTrip(t *testing.T) {
	in := proclassic.PolicyPost{
		General: &proclassic.PolicyPostGeneral{Name: new("p")},
		Scope: &proclassic.PolicyPostScope{
			AllComputers: new(true),
			Buildings: &proclassic.PolicyScopeBuildings{
				Building: &[]proclassic.IDName{{ID: new(1), Name: new("Main")}},
			},
			JssUsers: &proclassic.PolicyScopeJssUsers{
				User: &[]proclassic.IDName{{ID: new(10), Name: new("user")}},
			},
		},
		Reboot: &proclassic.PolicyPostReboot{
			Message:            new("restart"),
			MinutesUntilReboot: new(10),
		},
		Printers: &proclassic.PolicyPostPrinters{
			Size: new(1),
			Printer: &[]proclassic.PolicyPrintersPrinterItem{
				{ID: new(67), Name: new("Printer"), Action: new("install")},
			},
		},
	}

	buf, err := xml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out proclassic.Policy
	if err := xml.Unmarshal(buf, &out); err != nil {
		t.Fatalf("unmarshal: %v\npayload: %s", err, buf)
	}
	if out.General == nil || out.General.Name == nil || *out.General.Name != "p" {
		t.Errorf("General.Name lost: %+v", out.General)
	}
	if out.Scope == nil || out.Scope.AllComputers == nil || !*out.Scope.AllComputers {
		t.Errorf("Scope.AllComputers lost: %+v", out.Scope)
	}
	if out.Scope == nil || out.Scope.JssUsers == nil || out.Scope.JssUsers.User == nil || len(*out.Scope.JssUsers.User) != 1 {
		t.Errorf("Scope.JssUsers lost: %+v", out.Scope)
	}
	if out.Reboot == nil || out.Reboot.MinutesUntilReboot == nil || *out.Reboot.MinutesUntilReboot != 10 {
		t.Errorf("Reboot lost: %+v", out.Reboot)
	}
	if out.Printers == nil || out.Printers.Printer == nil || len(*out.Printers.Printer) != 1 {
		t.Errorf("Printers lost: %+v", out.Printers)
	}
}
