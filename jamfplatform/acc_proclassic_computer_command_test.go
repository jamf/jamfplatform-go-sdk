// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

// Classic computer commands. A queued MDM command cannot be deleted, so every
// test that queues one sits behind a write opt-in and acts only on the
// computers named in JAMFPLATFORM_ACC_CLASSIC_COMPUTER_IDS. The refusal tests
// name a nonexistent computer, queue nothing, and need no opt-in.
//
// Only EnableRemoteDesktop, DisableRemoteDesktop and UnlockUserAccount are
// ever queued: they are the three the server accepts, and none of them erases,
// locks or unmanages anything.

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

const computerCommandsWriteOptIn = "JAMFPLATFORM_ACC_CLASSIC_COMPUTER_COMMANDS_WRITE_OK"

// computerIDsEnv names the computers the command tests may act on. The tests
// never pick a computer themselves: the first one a tenant lists can be anyone's.
const computerIDsEnv = "JAMFPLATFORM_ACC_CLASSIC_COMPUTER_IDS"

// bogusComputerID is a computer that does not exist. Resolved before a known
// command is queued, so a known command naming it queues nothing. The largest
// Classic ID, as the mobile command tests use, and requireComputerAbsent checks
// it before anything is sent: the refusal tests name destructive commands with
// no opt-in, and an ID that existed would queue them.
const bogusComputerID = 2147483647

// requireComputerAbsent fails the test unless id is absent from the tenant.
func requireComputerAbsent(t *testing.T, cl *proclassic.Client, id int) {
	t.Helper()
	list, err := cl.MatchComputers(context.Background(), "*")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("MatchComputers(*): %v", err)
	}
	for _, c := range list.Computers {
		if c.ID != nil && *c.ID == id {
			t.Fatalf("computer %d exists on this tenant; refusing to send commands to it", id)
		}
	}
}

// namedComputerIDs returns the first n computers named in computerIDsEnv,
// skipping when fewer are named and failing when one is not on the tenant.
func namedComputerIDs(t *testing.T, cl *proclassic.Client, n int) []int {
	t.Helper()
	var ids []int
	for f := range strings.SplitSeq(os.Getenv(computerIDsEnv), ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		id, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("%s: %q is not a computer ID", computerIDsEnv, f)
		}
		ids = append(ids, id)
	}
	if len(ids) < n {
		t.Skipf("needs %d computer IDs in %s, found %d", n, computerIDsEnv, len(ids))
	}
	ids = ids[:n]

	list, err := cl.MatchComputers(context.Background(), "*")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("MatchComputers(*): %v", err)
	}
	present := map[int]bool{}
	for _, c := range list.Computers {
		if c.ID != nil {
			present[*c.ID] = true
		}
	}
	for _, id := range ids {
		if !present[id] {
			t.Fatalf("%s names computer %d, which is not on this tenant", computerIDsEnv, id)
		}
	}
	return ids
}

// postComputers builds the request's computer list: one <computer> per ID.
func postComputers(ids ...int) *proclassic.ComputerCommandPostComputers {
	items := make([]proclassic.ComputerCommandPostComputersComputerItem, len(ids))
	for i, id := range ids {
		items[i].ID = new(id)
	}
	return &proclassic.ComputerCommandPostComputers{Computer: &items}
}

func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// issuedByComputer maps computer id to the command echoed for it, failing the
// test unless the response carries exactly the named computers, each once and
// each with a UUID.
func issuedByComputer(t *testing.T, got *proclassic.ComputerCommandIssued, command string, ids ...int) map[int]string {
	t.Helper()
	if got == nil || got.Command == nil {
		t.Fatalf("no commands in the response: %+v", got)
	}
	byComputer := map[int]string{}
	for _, c := range *got.Command {
		if c.Name == nil || !strings.EqualFold(*c.Name, command) {
			t.Errorf("command name = %v, want %s", c.Name, command)
		}
		if c.ComputerID == nil || c.CommandUUID == nil || *c.CommandUUID == "" {
			t.Fatalf("command without computer_id or command_uuid: %+v", c)
		}
		byComputer[*c.ComputerID] = *c.CommandUUID
	}
	if len(*got.Command) != len(ids) || len(byComputer) != len(ids) {
		t.Fatalf("response carries %d commands for %d distinct computers, want %d", len(*got.Command), len(byComputer), len(ids))
	}
	for _, id := range ids {
		if byComputer[id] == "" {
			t.Errorf("no command for computer %d in %v", id, byComputer)
		}
	}
	return byComputer
}

func enableRemoteDesktop(ids ...int) *proclassic.ComputerCommandPost {
	return &proclassic.ComputerCommandPost{
		General:   &proclassic.ComputerCommandPostGeneral{Command: new("EnableRemoteDesktop")},
		Computers: postComputers(ids...),
	}
}

func wantAPIStatus(t *testing.T, err error, status int, bodyContains string) {
	t.Helper()
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil || !apiErr.HasStatus(status) {
		t.Fatalf("err = %v, want HTTP %d", err, status)
	}
	if !strings.Contains(apiErr.Body, bodyContains) {
		t.Errorf("body = %q, want it to contain %q", apiErr.Body, bodyContains)
	}
}

// TestAcceptance_Classic_ComputerCommandCreateVariants queues a command by URL
// ID and by body, and checks the flat response. The spec
// declares no response body and config pointed it at computer_command, which
// decodes to an empty struct, so a caller got no UUID. Unlike mobile device
// commands, which answer one UUID for every device, the server answers one
// command element per computer, each with its own UUID.
func TestAcceptance_Classic_ComputerCommandCreateVariants(t *testing.T) {
	requireWriteOptIn(t, computerCommandsWriteOptIn,
		"EnableRemoteDesktop and DisableRemoteDesktop are queued against real computers and cannot be deleted.")
	cl := proclassic.New(accClient(t))
	ids := namedComputerIDs(t, cl, 2)
	ctx := context.Background()

	t.Run("ByID", func(t *testing.T) {
		got, err := cl.IssueComputerCommandByID(ctx, "EnableRemoteDesktop", strconv.Itoa(ids[0]), nil)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("IssueComputerCommandByID: %v", err)
		}
		issuedByComputer(t, got, "EnableRemoteDesktop", ids[0])
	})

	// The server takes a comma-separated list in the URL (curl with a literal
	// comma queues one command per computer), but the generated method applies
	// url.PathEscape, which sends %2C, and the server does not decode it. So the
	// list form cannot be reached through the SDK: pass several computers in the
	// body instead. Fails the day the SDK sends the comma literally.
	t.Run("ByIDList_comma_is_percent_encoded_and_refused", func(t *testing.T) {
		_, err := cl.IssueComputerCommandByID(ctx, "DisableRemoteDesktop", joinIDs(ids), nil)
		wantAPIStatus(t, err, 400, "Unable to match computer")
	})

	t.Run("ByBody_several_computers", func(t *testing.T) {
		req := enableRemoteDesktop(ids...)
		got, err := cl.CreateComputerCommandByCommand(ctx, "EnableRemoteDesktop", req)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("CreateComputerCommandByCommand: %v", err)
		}
		uuids := issuedByComputer(t, got, "EnableRemoteDesktop", ids...)
		if uuids[ids[0]] == uuids[ids[1]] {
			t.Errorf("both computers share UUID %s", uuids[ids[0]])
		}
	})

	// A body naming computers wins over the IDs in the URL: with the URL
	// naming one computer and the body the other, only the body's is queued.
	t.Run("body_wins_over_URL", func(t *testing.T) {
		got, err := cl.IssueComputerCommandByID(ctx, "EnableRemoteDesktop", strconv.Itoa(ids[0]), enableRemoteDesktop(ids[1]))
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("IssueComputerCommandByID: %v", err)
		}
		issuedByComputer(t, got, "EnableRemoteDesktop", ids[1])
	})

	// The same command pending for the same computer is deduplicated: the
	// repeat answers the earlier UUID rather than queueing another.
	t.Run("identical_pending_command_shares_UUID", func(t *testing.T) {
		first, err := cl.IssueComputerCommandByID(ctx, "EnableRemoteDesktop", strconv.Itoa(ids[0]), nil)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("first: %v", err)
		}
		second, err := cl.IssueComputerCommandByID(ctx, "EnableRemoteDesktop", strconv.Itoa(ids[0]), nil)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("second: %v", err)
		}
		a := issuedByComputer(t, first, "EnableRemoteDesktop", ids[0])[ids[0]]
		b := issuedByComputer(t, second, "EnableRemoteDesktop", ids[0])[ids[0]]
		if a != b {
			t.Errorf("repeat UUID = %s, want the pending %s", b, a)
		}
	})
}

// TestAcceptance_Classic_ComputerCommandUnlockUserAccountNeedsUserName pins
// user_name, which the spec declares on the request but the generator dropped
// (post-symmetry replaced the request's general with the response's). The server
// refuses UnlockUserAccount without it, and takes it in the body only: the
// URL-ID form with a body that names no computers is refused too.
func TestAcceptance_Classic_ComputerCommandUnlockUserAccountNeedsUserName(t *testing.T) {
	requireWriteOptIn(t, computerCommandsWriteOptIn,
		"The accepted request queues UnlockUserAccount against a real computer and cannot be deleted.")
	cl := proclassic.New(accClient(t))
	id := namedComputerIDs(t, cl, 1)[0]
	ctx := context.Background()

	unlock := func(user *string) *proclassic.ComputerCommandPost {
		return &proclassic.ComputerCommandPost{
			General:   &proclassic.ComputerCommandPostGeneral{Command: new("UnlockUserAccount"), UserName: user},
			Computers: postComputers(id),
		}
	}

	_, err := cl.CreateComputerCommandByCommand(ctx, "UnlockUserAccount", unlock(nil))
	wantAPIStatus(t, err, 400, "user_name required for UnlockUserAccount")

	got, err := cl.CreateComputerCommandByCommand(ctx, "UnlockUserAccount", unlock(new("administrator")))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("with user_name: %v", err)
	}
	issuedByComputer(t, got, "UnlockUserAccount", id)

	// user_name without a computer list, through the URL-ID form.
	_, err = cl.IssueComputerCommandByID(ctx, "UnlockUserAccount", strconv.Itoa(id), &proclassic.ComputerCommandPost{
		General: &proclassic.ComputerCommandPostGeneral{Command: new("UnlockUserAccount"), UserName: new("administrator")},
	})
	wantAPIStatus(t, err, 400, "Invalid command or device list")
}

// TestAcceptance_Classic_ComputerCommandRefusals pins what the server does with
// a command it will not queue. None of these requests queues anything, so no
// opt-in is needed and the computer ID is one that does not exist.
//
//   - A known command naming a missing computer is 400 "Unable to match computer".
//   - An unknown command name is a deterministic 500, and the command is
//     checked first: the missing computer is never looked at.
//   - One missing ID in a list fails the whole request.
//
// The second makes a missing computer a safe probe of the command vocabulary.
func TestAcceptance_Classic_ComputerCommandRefusals(t *testing.T) {
	cl := proclassic.New(accClient(t))
	ctx := context.Background()
	requireComputerAbsent(t, cl, bogusComputerID)
	missing := strconv.Itoa(bogusComputerID)

	// control: the routed, recognised form answers from the service layer
	_, err := cl.IssueComputerCommandByID(ctx, "EnableRemoteDesktop", missing, nil)
	wantAPIStatus(t, err, 400, "Unable to match computer")

	recognised := []string{
		"EnableRemoteDesktop", "DisableRemoteDesktop", "UnlockUserAccount",
		"BlankPush", "RestartDevice", "ShutDownDevice", "DeviceLock", "EraseDevice",
		"SettingsEnableBluetooth", "SettingsDisableBluetooth", "ScheduleOSUpdate",
		"DeleteUser", "UnmanageDevice",
	}
	for _, name := range recognised {
		t.Run("recognised_"+name, func(t *testing.T) {
			_, err := cl.IssueComputerCommandByID(ctx, name, missing, nil)
			wantAPIStatus(t, err, 400, "Unable to match computer")
		})
	}

	// The server issues several of these itself (the list is full of them), but
	// it refuses them through this endpoint. Asserted, not skipped: a permanent
	// 5xx that starts working is what this test is for.
	unrecognised := []string{
		"ProfileList", "CertificateList", "DeviceInformation", "InstalledApplicationList",
		"SecurityInfo", "UpdateInventory", "ClearPasscode", "InstallProfile",
		"EnableRemoteDesktop (macOS 10.14.4 and later)", "NotACommand",
	}
	for _, name := range unrecognised {
		t.Run("unrecognised_"+name, func(t *testing.T) {
			_, err := cl.IssueComputerCommandByID(ctx, name, missing, nil)
			apiErr := jamfplatform.AsAPIError(err)
			if apiErr == nil || !apiErr.HasStatus(500) {
				t.Fatalf("err = %v, want 500 INTERNAL_SERVER_ERROR", err)
			}
		})
	}

	t.Run("body_form_with_missing_computer", func(t *testing.T) {
		_, err := cl.CreateComputerCommandByCommand(ctx, "EnableRemoteDesktop", enableRemoteDesktop(bogusComputerID))
		wantAPIStatus(t, err, 400, "Unable to match computer")
	})
}

// TestAcceptance_Classic_ComputerCommandAllOrNothing pins that one unknown ID
// in a list fails the whole request and queues nothing, for the computer named
// alongside it too.
func TestAcceptance_Classic_ComputerCommandAllOrNothing(t *testing.T) {
	requireWriteOptIn(t, computerCommandsWriteOptIn,
		"A real computer ID is named beside the missing one, and the test checks that no command was queued for it.")
	cl := proclassic.New(accClient(t))
	id := namedComputerIDs(t, cl, 1)[0]
	requireComputerAbsent(t, cl, bogusComputerID)
	ctx := context.Background()

	_, err := cl.IssueComputerCommandByID(ctx, "DisableRemoteDesktop", joinIDs([]int{id, bogusComputerID}), nil)
	wantAPIStatus(t, err, 400, "Unable to match computer")
	_, err = cl.CreateComputerCommandByCommand(ctx, "DisableRemoteDesktop", &proclassic.ComputerCommandPost{
		General:   &proclassic.ComputerCommandPostGeneral{Command: new("DisableRemoteDesktop")},
		Computers: postComputers(id, bogusComputerID),
	})
	wantAPIStatus(t, err, 400, "Unable to match computer")
}

// TestAcceptance_Classic_ComputerCommandNotQueuedOnFixtures pins the commands
// the server recognises and then declines to queue on this tenant's computers:
// 400 "No command was queued for computer N". Whether that is the computer's
// capability or the command's name cannot be told apart from here (the fixtures
// are simulated), so it is recorded as an observation. If one of these starts
// answering 201 the vocabulary in the method godoc is out of date.
func TestAcceptance_Classic_ComputerCommandNotQueuedOnFixtures(t *testing.T) {
	requireWriteOptIn(t, computerCommandsWriteOptIn,
		"The requests name a real computer, which the server must resolve before declining to queue the command.")
	cl := proclassic.New(accClient(t))
	id := namedComputerIDs(t, cl, 1)[0]
	ctx := context.Background()

	// Only commands that cannot do harm if a real device ever queued them.
	for _, name := range []string{"BlankPush", "SettingsEnableBluetooth", "SettingsDisableBluetooth"} {
		t.Run(name, func(t *testing.T) {
			_, err := cl.IssueComputerCommandByID(ctx, name, strconv.Itoa(id), nil)
			wantAPIStatus(t, err, 400, "No command was queued for computer "+strconv.Itoa(id))
		})
	}
}

// TestAcceptance_Classic_ComputerCommandReads pins the read shapes. The spec
// types all six reads as one computer_command (general + computers). On the
// wire the by-UUID read matches it, the list and the name filter answer a
// <computer_commands> list of flat elements, and the status read answers a
// {uuid, command, status} body no schema declares. Decoding the spec's shapes
// returned a nil error and an empty struct for the name filter, one element of
// 601 for the list, and no status at all, so every assertion is on a field that
// was silently lost.
func TestAcceptance_Classic_ComputerCommandReads(t *testing.T) {
	requireWriteOptIn(t, computerCommandsWriteOptIn,
		"A command is queued first so there is one to find, and it cannot be deleted.")
	cl := proclassic.New(accClient(t))
	ids := namedComputerIDs(t, cl, 2)
	ctx := context.Background()

	// Two computers so the list proves it carries more than its first element.
	issued, err := cl.CreateComputerCommandByCommand(ctx, "UnlockUserAccount", &proclassic.ComputerCommandPost{
		General:   &proclassic.ComputerCommandPostGeneral{Command: new("UnlockUserAccount"), UserName: new("administrator")},
		Computers: postComputers(ids...),
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerCommandByCommand: %v", err)
	}
	uuids := issuedByComputer(t, issued, "UnlockUserAccount", ids...)
	uuid := uuids[ids[0]]

	t.Run("ByUUID", func(t *testing.T) {
		var got *proclassic.ComputerCommand
		settleUntilFound(t, "command by UUID", func() error {
			var err error
			got, err = cl.GetComputerCommandByUUID(ctx, uuid)
			return err
		})
		if got.General == nil || got.General.UUID == nil || *got.General.UUID != uuid {
			t.Fatalf("general = %+v, want uuid %s", got.General, uuid)
		}
		if got.General.Command == nil || *got.General.Command != "UnlockUserAccount" {
			t.Errorf("command = %v, want UnlockUserAccount", got.General.Command)
		}
		if got.Computers == nil || got.Computers.Computer == nil {
			t.Fatalf("computers = %+v, want one computer", got.Computers)
		}
		comp := got.Computers.Computer
		if comp.ID == nil || *comp.ID != ids[0] {
			t.Errorf("computer id = %v, want %d", comp.ID, ids[0])
		}
		if comp.ManagementID == nil || *comp.ManagementID == "" {
			t.Error("computer management_id is empty; the spec omits it and a schemaPatch adds it")
		}
		if comp.UDID == nil || *comp.UDID == "" || comp.SerialNumber == nil || *comp.SerialNumber == "" {
			t.Errorf("computer udid/serial_number empty: %+v", comp)
		}
	})

	t.Run("ByName", func(t *testing.T) {
		var got *proclassic.ComputerCommandsListed
		settleUntilFound(t, "commands by name", func() error {
			var err error
			got, err = cl.GetComputerCommandByName(ctx, "UnlockUserAccount")
			if err == nil && listedComputerCommand(got, uuid) == nil {
				t.Logf("command %s not listed yet", uuid)
				return &jamfplatform.APIResponseError{StatusCode: 404}
			}
			return err
		})
		if got.Size == nil || *got.Size < 2 {
			t.Errorf("size = %v, want at least the two commands just queued", got.Size)
		}
		for _, id := range ids {
			if listedComputerCommand(got, uuids[id]) == nil {
				t.Errorf("command %s for computer %d missing from the by-name list", uuids[id], id)
			}
		}
		for _, c := range *got.ComputerCommand {
			if c.Command == nil || *c.Command != "UnlockUserAccount" {
				t.Errorf("by-name list carries command %v", c.Command)
			}
		}
		want := listedComputerCommand(got, uuid)
		if want.Computers == nil || want.Computers.Computer == nil || len(*want.Computers.Computer) != 1 {
			t.Fatalf("computers = %+v, want one", want.Computers)
		}
		comp := (*want.Computers.Computer)[0]
		if comp.ID == nil || *comp.ID != ids[0] || comp.ManagementID == nil || *comp.ManagementID == "" {
			t.Errorf("listed computer = %+v, want id %d with a management_id", comp, ids[0])
		}
	})

	t.Run("List", func(t *testing.T) {
		var got *proclassic.ComputerCommandsListed
		settleUntilFound(t, "command list", func() error {
			var err error
			got, err = cl.ListComputerCommands(ctx)
			if err == nil && listedComputerCommand(got, uuid) == nil {
				return &jamfplatform.APIResponseError{StatusCode: 404}
			}
			return err
		})
		if got.ComputerCommand == nil || len(*got.ComputerCommand) < 2 {
			t.Fatalf("list decoded %d elements; the spec's bare-array type kept only the first", len(deref(got.ComputerCommand)))
		}
		if got.Size == nil || *got.Size != len(*got.ComputerCommand) {
			t.Errorf("size = %v, decoded %d elements", got.Size, len(*got.ComputerCommand))
		}
		if listedComputerCommand(got, uuids[ids[1]]) == nil {
			t.Errorf("command %s for computer %d missing from the list", uuids[ids[1]], ids[1])
		}
	})

	t.Run("Status", func(t *testing.T) {
		got, err := cl.GetComputerCommandsByStatus(ctx, uuid)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("GetComputerCommandsByStatus: %v", err)
		}
		if got.UUID == nil || *got.UUID != uuid || got.Command == nil || *got.Command != "UnlockUserAccount" {
			t.Errorf("status body = %+v, want uuid %s and command UnlockUserAccount", got, uuid)
		}
		// Pending until a device acknowledges; the fixtures never do.
		if got.Status == nil || *got.Status != "Pending" {
			t.Errorf("status = %v, want Pending", got.Status)
		}
	})
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

// listedComputerCommand returns the list element carrying uuid, or nil.
func listedComputerCommand(l *proclassic.ComputerCommandsListed, uuid string) *proclassic.ComputerCommandSummary {
	if l == nil || l.ComputerCommand == nil {
		return nil
	}
	i := slices.IndexFunc(*l.ComputerCommand, func(c proclassic.ComputerCommandSummary) bool {
		return c.UUID != nil && *c.UUID == uuid
	})
	if i < 0 {
		return nil
	}
	return &(*l.ComputerCommand)[i]
}

// TestAcceptance_Classic_ComputerCommandStatusUnknownUUID pins that an unknown
// UUID is not an error: the status endpoint answers 200 and Canceled, so a
// caller polling a mistyped UUID is told the command was cancelled. The by-UUID
// read of the same UUID answers 500 rather than 404. Read-only.
func TestAcceptance_Classic_ComputerCommandStatusUnknownUUID(t *testing.T) {
	cl := proclassic.New(accClient(t))
	ctx := context.Background()
	const unknown = "00000000-0000-0000-0000-000000000000"

	got, err := cl.GetComputerCommandsByStatus(ctx, unknown)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerCommandsByStatus(unknown): %v", err)
	}
	if got.UUID == nil || *got.UUID != unknown || got.Status == nil || *got.Status != "Canceled" {
		t.Errorf("unknown UUID status = %+v, want uuid echoed and Canceled", got)
	}

	_, err = cl.GetComputerCommandByUUID(ctx, unknown)
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil || !apiErr.HasStatus(500) {
		t.Errorf("GetComputerCommandByUUID(unknown) err = %v, want 500; a 404 means the server fixed it", err)
	}
}

// TestAcceptance_Classic_ComputerCommandsByCommandEmptyBody pins a server
// fault: GET /computercommands/command/{command} answers 200 with a body that
// is only the XML declaration, for commands the tenant holds hundreds of and for
// a nonsense name alike. The method can never return data. It fails the day the
// server returns a body, which is the cue to type it (it is the flat list shape
// the by-name read answers) and drop the godoc warning.
func TestAcceptance_Classic_ComputerCommandsByCommandEmptyBody(t *testing.T) {
	cl := proclassic.New(accClient(t))
	ctx := context.Background()

	// control: the by-name read holds the same commands and answers them
	named, err := cl.GetComputerCommandByName(ctx, "DeviceInformation")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("control GetComputerCommandByName: %v", err)
	}
	if named.ComputerCommand == nil || len(*named.ComputerCommand) == 0 {
		t.Skip("the tenant holds no DeviceInformation command to contrast with")
	}

	for _, name := range []string{"DeviceInformation", "EnableRemoteDesktop", "NotACommand"} {
		got, err := cl.GetComputerCommandsByCommand(ctx, name)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("GetComputerCommandsByCommand(%s): %v", name, err)
		}
		if got == nil {
			t.Fatalf("GetComputerCommandsByCommand(%s) returned nil with a nil error", name)
		}
		if got.XMLName.Local != "" || got.General != nil || got.Computers != nil {
			t.Errorf("GetComputerCommandsByCommand(%s) decoded %+v; the server now answers a body", name, got)
		}
	}
}
