// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package appstore

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// StoreURL is what a store URL says about an app.
type StoreURL struct {
	// ID is the numeric App Store id.
	ID string
	// Country is the storefront code from the URL path, upper case. Empty when the URL names none,
	// as https://apps.apple.com/app/id361309726 does.
	Country string
}

var idSegmentPattern = regexp.MustCompile(`^id([0-9]+)$`)

// storeHosts are the hosts a store URL may use.
var storeHosts = map[string]bool{
	"apps.apple.com":   true,
	"itunes.apple.com": true,
}

// ParseStoreURL reads the id and storefront out of an App Store URL such as
// https://apps.apple.com/gb/app/pages-create-documents/id361309726. The query string (?mt=12,
// ?l=fr) is ignored. It wraps ErrInvalidQuery when the URL is not an App Store app URL.
func ParseStoreURL(raw string) (StoreURL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return StoreURL{}, fmt.Errorf("%w: store URL %q: %v", ErrInvalidQuery, raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return StoreURL{}, fmt.Errorf("%w: store URL %q has no http or https scheme", ErrInvalidQuery, raw)
	}
	if !storeHosts[strings.ToLower(u.Hostname())] {
		return StoreURL{}, fmt.Errorf("%w: store URL %q is not on apps.apple.com", ErrInvalidQuery, raw)
	}
	segments := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	var out StoreURL
	for i, segment := range segments {
		if m := idSegmentPattern.FindStringSubmatch(segment); m != nil {
			out.ID = m[1]
		}
		if i == 0 && countryPattern.MatchString(segment) {
			out.Country = strings.ToUpper(segment)
		}
	}
	if out.ID == "" {
		return StoreURL{}, fmt.Errorf("%w: store URL %q carries no id<digits> segment", ErrInvalidQuery, raw)
	}
	return out, nil
}
