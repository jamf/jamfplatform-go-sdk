// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

// The Wallpaper mobile device command. Both tests need a supervised device and
// queue a real MDM command against it, so they sit behind a write opt-in. The
// wire facts they pin are not in the spec's schema: the failure messages name
// wallpaper_setting singular, and the server accepts any integer there.

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// wallpaperPNG is a 1x1 PNG, base64-encoded as wallpaper_content requires.
const wallpaperPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

const wallpaperWriteOptIn = "JAMFPLATFORM_ACC_CLASSIC_MOBILE_COMMANDS_WRITE_OK"

// firstSupervisedMobileDeviceID returns the ID of a device the Wallpaper command
// accepts, skipping when the tenant has none.
func firstSupervisedMobileDeviceID(t *testing.T, cl *proclassic.Client) int {
	t.Helper()
	devices, err := cl.ListMobileDevices(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListMobileDevices: %v", err)
	}
	for _, d := range devices.MobileDevices {
		if d.ID != nil && d.Supervised != nil && *d.Supervised {
			return *d.ID
		}
	}
	t.Skip("no supervised mobile device on this tenant")
	return 0
}

// assertIssuedCommand checks the flat create response every mobile device
// command endpoint shares (<uuid>, <command>, then the device with
// management_id), which the spec's mobile_device_command nests under
// <general> instead, so it decodes into the config-created
// mobile_device_command_issued type.
func assertIssuedCommand(t *testing.T, got *proclassic.MobileDeviceCommandIssued, command string, deviceID int) {
	t.Helper()
	if got == nil || got.UUID == nil || *got.UUID == "" {
		t.Fatalf("no command UUID in the response: %+v", got)
	}
	if got.Command == nil || *got.Command != command {
		t.Errorf("command = %v, want %s", got.Command, command)
	}
	if got.MobileDevices == nil || got.MobileDevices.MobileDevice == nil || len(*got.MobileDevices.MobileDevice) != 1 {
		t.Fatalf("device echo = %+v, want one device", got.MobileDevices)
	}
	dev := (*got.MobileDevices.MobileDevice)[0]
	if dev.ID == nil || *dev.ID != deviceID {
		t.Errorf("device id = %v, want %d", dev.ID, deviceID)
	}
	if dev.ManagementID == nil || *dev.ManagementID == "" {
		t.Error("device management_id is empty")
	}
}

// postDevices builds the request's device list: one <mobile_device> per ID.
func postDevices(ids ...int) *proclassic.MobileDeviceCommandPostMobileDevices {
	devices := make([]proclassic.MobileDeviceCommandPostMobileDevicesMobileDeviceItem, len(ids))
	for i, id := range ids {
		devices[i].ID = new(id)
	}
	return &proclassic.MobileDeviceCommandPostMobileDevices{MobileDevice: &devices}
}

func wallpaperCommand(deviceID int, general proclassic.MobileDeviceCommandPostGeneral) *proclassic.MobileDeviceCommandPost {
	general.Command = new("Wallpaper")
	return &proclassic.MobileDeviceCommandPost{
		General:       &general,
		MobileDevices: postDevices(deviceID),
	}
}

// TestAcceptance_Classic_MobileDeviceCommandWallpaper sends Wallpaper with
// wallpaper_content and expects a 201 echoing the device. The generated request
// type once dropped every request-only field of the command's general block,
// so this fails to compile if wallpaper_setting or wallpaper_content go missing.
func TestAcceptance_Classic_MobileDeviceCommandWallpaper(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"The Wallpaper command is queued against a real mobile device and cannot be deleted.")
	c := accClient(t)
	cl := proclassic.New(c)
	id := firstSupervisedMobileDeviceID(t, cl)

	got, err := cl.IssueMobileDeviceCommand(context.Background(), wallpaperCommand(id, proclassic.MobileDeviceCommandPostGeneral{
		WallpaperSetting: new(proclassic.MobileDeviceCommandPostGeneralWallpaperSetting1),
		WallpaperContent: new(wallpaperPNG),
	}))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("IssueMobileDeviceCommand(Wallpaper): %v", err)
	}
	assertIssuedCommand(t, got, "Wallpaper", id)

	// The UUID is the handle a caller polls with; the queued command must
	// resolve through it.
	var read *proclassic.MobileDeviceCommand
	settleUntilFound(t, "queued Wallpaper command", func() error {
		var err error
		read, err = cl.GetMobileDeviceCommandByUUID(context.Background(), *got.UUID)
		return err
	})
	if read == nil {
		t.Fatal("GetMobileDeviceCommandByUUID returned nothing")
	}
}

// TestAcceptance_Classic_MobileDeviceCommandWallpaperValidation pins the
// server's field checks, which run after the device is resolved: setting is
// checked first, then the id-or-content pair. Neither request queues anything.
func TestAcceptance_Classic_MobileDeviceCommandWallpaperValidation(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"A device must be resolved before the fields are checked, so this needs a real mobile device.")
	c := accClient(t)
	cl := proclassic.New(c)
	id := firstSupervisedMobileDeviceID(t, cl)

	cases := []struct {
		name    string
		general proclassic.MobileDeviceCommandPostGeneral
		want    string
	}{
		{"no parameters", proclassic.MobileDeviceCommandPostGeneral{}, "wallpaper_setting must be provided"},
		{"setting without image", proclassic.MobileDeviceCommandPostGeneral{
			WallpaperSetting: new(1),
		}, "wallpaper_id or wallpaper_content must be provided"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cl.IssueMobileDeviceCommand(context.Background(), wallpaperCommand(id, tc.general))
			if err == nil {
				t.Fatal("expected a 400, got success")
			}
			skipOnServerError(t, err)
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// updateInventoryCommand is the request body for the by-body endpoints. The
// device is the only thing the server reads from it beyond the command.
func updateInventoryCommand(deviceID int) *proclassic.MobileDeviceCommandPost {
	return &proclassic.MobileDeviceCommandPost{
		General:       &proclassic.MobileDeviceCommandPostGeneral{Command: new("UpdateInventory")},
		MobileDevices: postDevices(deviceID),
	}
}

// TestAcceptance_Classic_MobileDeviceCommandCreateVariants issues
// UpdateInventory through each endpoint that routes and asserts all three
// answer with the same flat body the generated type decodes. Identical
// pending commands are deduplicated server-side, so the UUID may repeat
// across variants and is not compared.
func TestAcceptance_Classic_MobileDeviceCommandCreateVariants(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"UpdateInventory is queued against a real mobile device and cannot be deleted.")
	c := accClient(t)
	cl := proclassic.New(c)
	id := firstSupervisedMobileDeviceID(t, cl)
	ctx := context.Background()

	t.Run("ByCommandID", func(t *testing.T) {
		got, err := cl.CreateMobileDeviceCommandByCommandID(ctx, "UpdateInventory", strconv.Itoa(id), nil)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("CreateMobileDeviceCommandByCommandID: %v", err)
		}
		assertIssuedCommand(t, got, "UpdateInventory", id)
	})
	t.Run("ByCommand", func(t *testing.T) {
		got, err := cl.CreateMobileDeviceCommandByCommand(ctx, "UpdateInventory", updateInventoryCommand(id))
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("CreateMobileDeviceCommandByCommand: %v", err)
		}
		assertIssuedCommand(t, got, "UpdateInventory", id)
	})
	t.Run("ByName", func(t *testing.T) {
		got, err := cl.CreateMobileDeviceCommandByName(ctx, "UpdateInventory", updateInventoryCommand(id))
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("CreateMobileDeviceCommandByName: %v", err)
		}
		assertIssuedCommand(t, got, "UpdateInventory", id)
	})
}

// TestAcceptance_Classic_MobileDeviceCommandParameterFormsUnrouted pins the
// two parameterised endpoints, which the gateway refuses with the same
// 403 BAD_PERMISSIONS a nonsense path in the namespace gets (control below),
// so no command has ever been seen to succeed through them. The 4-segment form
// routes for DeviceName alone, and the service then answers 400 "Invalid
// command". Fails the day either form starts answering, which is the cue to
// assert a real command through it.
func TestAcceptance_Classic_MobileDeviceCommandParameterFormsUnrouted(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"The control request resolves a real mobile device.")
	c := accClient(t)
	cl := proclassic.New(c)
	id := firstSupervisedMobileDeviceID(t, cl)
	ctx := context.Background()
	devID := strconv.Itoa(id)

	want403 := func(t *testing.T, err error) {
		t.Helper()
		var apiErr *jamfplatform.APIResponseError
		if !errors.As(err, &apiErr) || !apiErr.HasStatus(403) {
			t.Fatalf("err = %v, want 403 BAD_PERMISSIONS from the gateway", err)
		}
	}
	t.Run("WithParameterByIDList", func(t *testing.T) {
		_, err := cl.CreateMobileDeviceCommandWithParameterByIDList(ctx, "UpdateInventory", "Foo", devID, nil)
		want403(t, err)
	})
	t.Run("WithParameterVersionByIDList", func(t *testing.T) {
		_, err := cl.CreateMobileDeviceCommandWithParameterVersionByIDList(ctx, "UpdateInventory", "Foo", "1", devID, nil)
		want403(t, err)
	})
	t.Run("control_routed_form_succeeds", func(t *testing.T) {
		got, err := cl.CreateMobileDeviceCommandByCommandID(ctx, "UpdateInventory", devID, nil)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("control: %v", err)
		}
		assertIssuedCommand(t, got, "UpdateInventory", id)
	})
}

// issueUpdateInventory queues UpdateInventory for the device and returns the
// UUID and the device's management_id from the create response.
func issueUpdateInventory(t *testing.T, cl *proclassic.Client, id int) (uuid, managementID string) {
	t.Helper()
	got, err := cl.CreateMobileDeviceCommandByCommandID(context.Background(), "UpdateInventory", strconv.Itoa(id), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMobileDeviceCommandByCommandID: %v", err)
	}
	assertIssuedCommand(t, got, "UpdateInventory", id)
	return *got.UUID, *(*got.MobileDevices.MobileDevice)[0].ManagementID
}

// listedCommand returns the list element carrying uuid, or nil.
func listedCommand(l *proclassic.MobileDeviceCommandsListed, uuid string) *proclassic.MobileDeviceCommandSummary {
	if l == nil || l.MobileDeviceCommand == nil {
		return nil
	}
	for i, c := range *l.MobileDeviceCommand {
		if c.UUID != nil && *c.UUID == uuid {
			return &(*l.MobileDeviceCommand)[i]
		}
	}
	return nil
}

// TestAcceptance_Classic_MobileDeviceCommandReads pins the four read shapes.
// The spec declares the list as a bare array of nested items and the two
// filter endpoints as a single command; the server answers all three with a
// <mobile_device_commands> list of flat elements. Decoding the spec's shapes
// returned a nil error and an empty struct (and one element of 453 for the
// list), so every assertion here is on a field that was silently lost.
func TestAcceptance_Classic_MobileDeviceCommandReads(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"A command is queued first so there is one to find, and it cannot be deleted.")
	c := accClient(t)
	cl := proclassic.New(c)
	id := firstSupervisedMobileDeviceID(t, cl)
	ctx := context.Background()
	uuid, managementID := issueUpdateInventory(t, cl, id)

	t.Run("ByUUID", func(t *testing.T) {
		var got *proclassic.MobileDeviceCommand
		settleUntilFound(t, "command by UUID", func() error {
			var err error
			got, err = cl.GetMobileDeviceCommandByUUID(ctx, uuid)
			return err
		})
		if got.General == nil || got.General.UUID == nil || *got.General.UUID != uuid {
			t.Fatalf("general = %+v, want uuid %s", got.General, uuid)
		}
		if got.General.Command == nil || *got.General.Command != "UpdateInventory" {
			t.Errorf("command = %v", got.General.Command)
		}
		if got.MobileDevices == nil || got.MobileDevices.MobileDevice == nil || got.MobileDevices.MobileDevice.ID == nil || *got.MobileDevices.MobileDevice.ID != id {
			t.Fatalf("devices = %+v, want device %d", got.MobileDevices, id)
		}
		if mid := got.MobileDevices.MobileDevice.ManagementID; mid == nil || *mid != managementID {
			t.Errorf("management_id = %v, want %s (dropped before the type declared it)", mid, managementID)
		}
	})

	checkListed := func(t *testing.T, l *proclassic.MobileDeviceCommandsListed) {
		t.Helper()
		item := listedCommand(l, uuid)
		if item == nil {
			t.Fatalf("command %s not among the listed commands", uuid)
		}
		if item.Command == nil || *item.Command != "UpdateInventory" {
			t.Errorf("command = %v", item.Command)
		}
		if item.MobileDevices == nil || item.MobileDevices.MobileDevice == nil || len(*item.MobileDevices.MobileDevice) != 1 {
			t.Fatalf("devices = %+v", item.MobileDevices)
		}
		d := (*item.MobileDevices.MobileDevice)[0]
		if d.ID == nil || *d.ID != id || d.ManagementID == nil || *d.ManagementID != managementID || d.UDID == nil || *d.UDID == "" {
			t.Errorf("device = %+v, want id %d management_id %s", d, id, managementID)
		}
	}
	t.Run("ByCommand", func(t *testing.T) {
		var l *proclassic.MobileDeviceCommandsListed
		settleUntilFound(t, "command in the by-command list", func() error {
			var err error
			l, err = cl.GetMobileDeviceCommandByCommand(ctx, "UpdateInventory")
			if err == nil && listedCommand(l, uuid) == nil {
				err = errors.New("not listed yet")
			}
			return err
		})
		checkListed(t, l)
	})
	t.Run("ByName", func(t *testing.T) {
		l, err := cl.GetMobileDeviceCommandByName(ctx, "UpdateInventory")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("GetMobileDeviceCommandByName: %v", err)
		}
		checkListed(t, l)
	})
	t.Run("List", func(t *testing.T) {
		l, err := cl.ListMobileDeviceCommands(ctx)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListMobileDeviceCommands: %v", err)
		}
		if l.MobileDeviceCommand == nil || len(*l.MobileDeviceCommand) < 2 {
			t.Fatalf("decoded %v commands, want every element of the list", l.MobileDeviceCommand)
		}
		if l.Size == nil {
			t.Error("size is empty")
		}
		for _, c := range *l.MobileDeviceCommand {
			if c.UUID == nil || *c.UUID == "" || c.Command == nil {
				t.Fatalf("element without uuid or command: %+v", c)
			}
		}
	})
}

// TestAcceptance_Classic_MobileDeviceCommandLostModeFields sends the
// request-only fields the generated type used to drop. The server does not
// echo them, and an identical pending command is deduplicated into the first
// one's UUID, so a field's arrival is provable only through a validation
// refusal: EnableLostMode with neither message nor phone is a 400, and either
// one alone is a 201. Everything else here asserts the command name the
// server records.
func TestAcceptance_Classic_MobileDeviceCommandLostModeFields(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"EnableLostMode puts a real mobile device into Lost Mode, and nothing in the API turns it off.")
	c := accClient(t)
	cl := proclassic.New(c)
	id := firstSupervisedMobileDeviceID(t, cl)
	ctx := context.Background()

	issue := func(g proclassic.MobileDeviceCommandPostGeneral) (*proclassic.MobileDeviceCommandIssued, error) {
		return cl.IssueMobileDeviceCommand(ctx, &proclassic.MobileDeviceCommandPost{
			General:       &g,
			MobileDevices: postDevices(id),
		})
	}

	t.Run("EnableLostMode_refused_without_message_or_phone", func(t *testing.T) {
		_, err := issue(proclassic.MobileDeviceCommandPostGeneral{Command: new("EnableLostMode")})
		if err == nil {
			t.Fatal("expected a 400, got success")
		}
		skipOnServerError(t, err)
		if !strings.Contains(err.Error(), "Either phone number or Lost Mode message must be entered") {
			t.Errorf("err = %v", err)
		}
	})
	for name, g := range map[string]proclassic.MobileDeviceCommandPostGeneral{
		"EnableLostMode_message": {Command: new("EnableLostMode"), LostModeMessage: new("Test fixture")},
		"EnableLostMode_phone": {
			Command: new("EnableLostMode"), LostModePhone: new("555-0100"),
			LostModeFootnote: new("fixture"), AlwaysEnforceLostMode: new(false), LostModeWithSound: new(true),
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := issue(g)
			if err != nil {
				skipOnServerError(t, err)
				t.Fatalf("issue: %v", err)
			}
			assertIssuedCommand(t, got, "EnableLostMode", id)
		})
	}
	t.Run("PlayLostModeSound_recorded_under_another_name", func(t *testing.T) {
		got, err := issue(proclassic.MobileDeviceCommandPostGeneral{Command: new("PlayLostModeSound")})
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("issue: %v", err)
		}
		assertIssuedCommand(t, got, "PLAY_LOST_MODE_SOUND", id)
	})
	t.Run("RefreshCellularPlans_not_in_the_specs_command_list", func(t *testing.T) {
		got, err := issue(proclassic.MobileDeviceCommandPostGeneral{
			Command: new("RefreshCellularPlans"), ESimServerURL: new("https://support.apple.com/en-us/HT209096"),
		})
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("issue: %v", err)
		}
		assertIssuedCommand(t, got, "RefreshCellularPlans", id)
	})
	t.Run("PasscodeLockGracePeriod_refused_off_Shared_iPad", func(t *testing.T) {
		_, err := issue(proclassic.MobileDeviceCommandPostGeneral{
			Command: new("PasscodeLockGracePeriod"), PasscodeLockGracePeriod: new(60),
		})
		if err == nil {
			t.Skip("the device accepted PasscodeLockGracePeriod; it is now a Shared iPad, assert the field")
		}
		skipOnServerError(t, err)
		if !strings.Contains(err.Error(), "does not support PasscodeLockGracePeriod") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("commands_the_server_does_not_know", func(t *testing.T) {
		for _, cmd := range []string{"RestartDevice", "ShutDownDevice", "ClearPasscode", "DisableLostMode"} {
			_, err := issue(proclassic.MobileDeviceCommandPostGeneral{Command: new(cmd)})
			if err == nil {
				t.Errorf("%s was accepted; it answered 400 Invalid command, update the methodNotes", cmd)
				continue
			}
			skipOnServerError(t, err)
			if !strings.Contains(err.Error(), "Invalid command") {
				t.Errorf("%s: err = %v", cmd, err)
			}
		}
	})
}

// pngBytes returns a size x size RGBA PNG gradient, small enough to upload
// cheaply and large enough for the server's downscale to show.
func pngBytes(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / size), G: uint8(y * 255 / size), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestAcceptance_Classic_MobileDeviceCommandWallpaperIcon sends Wallpaper by
// wallpaper_id, which names a Jamf Pro icon. Unlike wallpaper_content the ID is
// validated, and the icon upload resizes what it is given.
//
// An uploaded icon cannot be deleted and every upload mints a new ID, so the
// test uploads once per run at most.
func TestAcceptance_Classic_MobileDeviceCommandWallpaperIcon(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"Wallpaper is queued against a real mobile device, and an uploaded icon cannot be deleted.")
	c := accClient(t)
	cl := proclassic.New(c)
	id := firstSupervisedMobileDeviceID(t, cl)
	ctx := context.Background()

	icon, err := pro.New(c).UploadIconV1(ctx, "sdk-acc-wallpaper.png", bytes.NewReader(pngBytes(t, 1024)))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadIconV1: %v", err)
	}

	t.Run("upload_is_downscaled_to_512", func(t *testing.T) {
		resp, err := http.Get(icon.URL)
		if err != nil {
			t.Fatalf("GET %s: %v", icon.URL, err)
		}
		defer func() { _ = resp.Body.Close() }()
		cfg, err := png.DecodeConfig(resp.Body)
		if err != nil {
			t.Fatalf("stored icon is not a PNG: %v", err)
		}
		if cfg.Width != 512 || cfg.Height != 512 {
			t.Errorf("stored icon is %dx%d, want the server's 512x512 downscale of the 1024x1024 upload", cfg.Width, cfg.Height)
		}
	})
	t.Run("real_icon_is_accepted", func(t *testing.T) {
		got, err := cl.IssueMobileDeviceCommand(ctx, wallpaperCommand(id, proclassic.MobileDeviceCommandPostGeneral{
			WallpaperSetting: new(proclassic.MobileDeviceCommandPostGeneralWallpaperSetting3),
			WallpaperID:      new(icon.ID),
		}))
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("IssueMobileDeviceCommand: %v", err)
		}
		assertIssuedCommand(t, got, "Wallpaper", id)
	})
	t.Run("nonexistent_icon_is_refused", func(t *testing.T) {
		for _, bad := range []int{0, -1, 2147483647} {
			_, err := cl.IssueMobileDeviceCommand(ctx, wallpaperCommand(id, proclassic.MobileDeviceCommandPostGeneral{
				WallpaperSetting: new(proclassic.MobileDeviceCommandPostGeneralWallpaperSetting3),
				WallpaperID:      new(bad),
			}))
			if err == nil {
				t.Errorf("wallpaper_id %d was accepted", bad)
				continue
			}
			skipOnServerError(t, err)
			if !strings.Contains(err.Error(), "Problem with mobile_device_command") {
				t.Errorf("wallpaper_id %d: err = %v", bad, err)
			}
		}
	})
	t.Run("content_is_not_validated", func(t *testing.T) {
		got, err := cl.IssueMobileDeviceCommand(ctx, wallpaperCommand(id, proclassic.MobileDeviceCommandPostGeneral{
			WallpaperSetting: new(proclassic.MobileDeviceCommandPostGeneralWallpaperSetting3),
			WallpaperContent: new("***not base64, not an image***"),
		}))
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("the server now validates wallpaper_content (%v); assert the refusal", err)
		}
		assertIssuedCommand(t, got, "Wallpaper", id)
	})
}

// supervisedMobileDeviceIDs returns up to n supervised devices, skipping when
// the tenant has fewer.
func supervisedMobileDeviceIDs(t *testing.T, cl *proclassic.Client, n int) []int {
	t.Helper()
	devices, err := cl.ListMobileDevices(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListMobileDevices: %v", err)
	}
	var ids []int
	for _, d := range devices.MobileDevices {
		if d.ID != nil && d.Supervised != nil && *d.Supervised {
			ids = append(ids, *d.ID)
			if len(ids) == n {
				return ids
			}
		}
	}
	t.Skipf("needs %d supervised mobile devices, found %d", n, len(ids))
	return nil
}

// TestAcceptance_Classic_MobileDeviceCommandMultipleDevices sends Wallpaper to
// several devices in one request. The spec declares a single <mobile_device>;
// the server takes any number in the body, applies the command to all of them
// or none, and — when a body is present — ignores the IDs in the URL, so the
// ID-list path cannot carry Wallpaper's parameters.
func TestAcceptance_Classic_MobileDeviceCommandMultipleDevices(t *testing.T) {
	requireWriteOptIn(t, wallpaperWriteOptIn,
		"Wallpaper is queued against several real mobile devices and cannot be deleted.")
	c := accClient(t)
	cl := proclassic.New(c)
	ids := supervisedMobileDeviceIDs(t, cl, 3)
	ctx := context.Background()
	general := proclassic.MobileDeviceCommandPostGeneral{
		Command:          new("Wallpaper"),
		WallpaperSetting: new(proclassic.MobileDeviceCommandPostGeneralWallpaperSetting2),
		WallpaperContent: new(wallpaperPNG),
	}

	t.Run("all_devices_are_targeted", func(t *testing.T) {
		got, err := cl.IssueMobileDeviceCommand(ctx, &proclassic.MobileDeviceCommandPost{
			General: &general, MobileDevices: postDevices(ids...),
		})
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("IssueMobileDeviceCommand: %v", err)
		}
		if got.UUID == nil || got.MobileDevices == nil || got.MobileDevices.MobileDevice == nil {
			t.Fatalf("response = %+v", got)
		}
		var echoed []int
		for _, d := range *got.MobileDevices.MobileDevice {
			if d.ID != nil {
				echoed = append(echoed, *d.ID)
			}
		}
		if !slices.Equal(echoed, ids) {
			t.Errorf("echoed devices = %v, want %v", echoed, ids)
		}
	})
	t.Run("one_unknown_device_fails_the_request", func(t *testing.T) {
		_, err := cl.IssueMobileDeviceCommand(ctx, &proclassic.MobileDeviceCommandPost{
			General: &general, MobileDevices: postDevices(ids[0], 2147483647),
		})
		if err == nil {
			t.Fatal("expected a 400, got success")
		}
		skipOnServerError(t, err)
		if !strings.Contains(err.Error(), "Unable to match mobile_device") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("body_devices_win_over_the_url", func(t *testing.T) {
		got, err := cl.CreateMobileDeviceCommandByCommandID(ctx, "Wallpaper", strconv.Itoa(ids[2]), &proclassic.MobileDeviceCommandPost{
			General: &general, MobileDevices: postDevices(ids[0]),
		})
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("CreateMobileDeviceCommandByCommandID: %v", err)
		}
		assertIssuedCommand(t, got, "Wallpaper", ids[0])
	})
}
