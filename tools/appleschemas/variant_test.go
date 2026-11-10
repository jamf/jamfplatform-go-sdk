// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

func TestVariantPayloadType(t *testing.T) {
	cases := []struct{ file, declared, want string }{
		{"com.apple.MCX(EnergySaver).yaml", "com.apple.MCX", "com.apple.MCX.EnergySaver"},
		{"com.apple.MCX(FileVault2).yaml", "com.apple.MCX", "com.apple.MCX.FileVault2"},
		{"com.apple.MCX.TimeMachine.yaml", "com.apple.MCX.TimeMachine", "com.apple.MCX.TimeMachine"},
		{"com.apple.wifi.managed.yaml", "com.apple.wifi.managed", "com.apple.wifi.managed"},
		{"com.apple.MCX().yaml", "com.apple.MCX", "com.apple.MCX"},
		{"com.apple.extensiblesso(kerberos).yaml", "com.apple.extensiblesso", "com.apple.extensiblesso"},
		{"com.apple.other(Thing).yaml", "com.apple.MCX", "com.apple.MCX"},
	}
	for _, c := range cases {
		if got := variantPayloadType(c.file, c.declared); got != c.want {
			t.Errorf("variantPayloadType(%q, %q) = %q, want %q", c.file, c.declared, got, c.want)
		}
	}
}
