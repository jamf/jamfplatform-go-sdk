// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package appstore

import (
	"errors"
	"testing"
)

func TestParseStoreURL(t *testing.T) {
	cases := []struct {
		in, id, country string
	}{
		{"https://apps.apple.com/us/app/pages-create-documents/id361309726", "361309726", "US"},
		{"https://apps.apple.com/gb/app/pages-create-documents/id361309726", "361309726", "GB"},
		{"https://apps.apple.com/us/app/xcode/id497799835?mt=12", "497799835", "US"},
		{"https://apps.apple.com/app/imovie/id408981434", "408981434", ""},
		{"https://apps.apple.com/app/id408981434", "408981434", ""},
		{"https://itunes.apple.com/us/app/textwrangler/id404010395?mt=12&uo=4", "404010395", "US"},
		{"  https://APPS.apple.com/de/app/x/id1?l=en  ", "1", "DE"},
	}
	for _, tc := range cases {
		got, err := ParseStoreURL(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.ID != tc.id || got.Country != tc.country {
			t.Errorf("%q: got %+v, want id %q country %q", tc.in, got, tc.id, tc.country)
		}
	}
}

func TestParseStoreURL_Rejects(t *testing.T) {
	for _, in := range []string{
		"",
		"361309726",
		"apps.apple.com/us/app/x/id1",
		"https://example.com/us/app/x/id1",
		"https://apps.apple.com/us/app/pages",
		"https://apps.apple.com/us/app/x/idabc",
		"ftp://apps.apple.com/us/app/x/id1",
	} {
		if _, err := ParseStoreURL(in); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("%q: err = %v, want ErrInvalidQuery", in, err)
		}
	}
}
