// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package appstore

import (
	"encoding/json"
	"fmt"
	"strings"
)

// wireResponse is the lookup envelope.
type wireResponse struct {
	Meta struct {
		Language struct {
			Tag string `json:"tag"`
		} `json:"language"`
		Storefront struct {
			CC string `json:"cc"`
		} `json:"storefront"`
	} `json:"meta"`
	Results map[string]wireApp `json:"results"`
}

// wireApp is one listing, decoded only as far as App needs.
type wireApp struct {
	ID          string          `json:"id"`
	BundleID    string          `json:"bundleId"`
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	ArtistName  string          `json:"artistName"`
	Artwork     json.RawMessage `json:"artwork"`
	IconArtwork json.RawMessage `json:"iconArtwork"`
	Description struct {
		Standard string `json:"standard"`
	} `json:"description"`
	DeviceFamilies                   []string    `json:"deviceFamilies"`
	GenreNames                       []string    `json:"genreNames"`
	IsVppDeviceBasedLicensingEnabled bool        `json:"isVppDeviceBasedLicensingEnabled"`
	Kind                             string      `json:"kind"`
	MinimumOSVersion                 string      `json:"minimumOSVersion"`
	Offers                           []wireOffer `json:"offers"`
	WhatsNew                         string      `json:"whatsNew"`
}

// wireOffer is one purchase offer. Quantity is present only on volume (education) offers.
type wireOffer struct {
	Price          float64 `json:"price"`
	PriceFormatted string  `json:"priceFormatted"`
	Type           string  `json:"type"`
	Quantity       *int    `json:"quantity"`
	Version        struct {
		Display string `json:"display"`
	} `json:"version"`
}

// wireArtwork is one artwork entry.
type wireArtwork struct {
	URL string `json:"url"`
}

// decode turns a 200 body into an App for the normalised query q.
func decode(q Query, body []byte) (*App, error) {
	var resp wireResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("appstore: decode response: %w", err)
	}
	answered := strings.ToUpper(resp.Meta.Storefront.CC)
	if answered != "" && answered != q.Country {
		return nil, &StorefrontMismatchError{Requested: q.Country, Answered: answered}
	}
	w, ok := pick(q, resp.Results)
	if !ok {
		return nil, &NotFoundError{Query: q}
	}
	artwork, err := artworkTemplate(w.Artwork)
	if err != nil {
		return nil, err
	}
	iconArtwork, err := artworkTemplate(w.IconArtwork)
	if err != nil {
		return nil, err
	}
	app := &App{
		ID:                     w.ID,
		BundleID:               w.BundleID,
		Name:                   w.Name,
		URL:                    w.URL,
		Description:            w.Description.Standard,
		ArtworkURLTemplate:     artwork,
		IconArtworkURLTemplate: iconArtwork,
		Genres:                 w.GenreNames,
		DeviceBasedLicensing:   w.IsVppDeviceBasedLicensingEnabled,
		Developer:              w.ArtistName,
		ReleaseNotes:           w.WhatsNew,
		MinimumOSVersion:       w.MinimumOSVersion,
		DeviceFamilies:         w.DeviceFamilies,
		Kind:                   w.Kind,
		Country:                answered,
		Language:               resp.Meta.Language.Tag,
	}
	if offer, ok := primaryOffer(w.Offers); ok {
		app.Version = offer.Version.Display
		app.Price = offer.Price
		app.PriceFormatted = offer.PriceFormatted
		app.Free = offer.Price == 0
	}
	if app.Country == "" {
		app.Country = q.Country
	}
	return app, nil
}

// pick returns the listing for q. The results object is keyed by the requested id or by the
// requested spelling of the bundle identifier; a lone result under another key is accepted only
// when its own id or bundle identifier matches the query.
func pick(q Query, results map[string]wireApp) (wireApp, bool) {
	key := q.ID
	if key == "" {
		key = q.BundleID
	}
	if w, ok := results[key]; ok {
		return w, true
	}
	for _, w := range results {
		if q.ID != "" && w.ID == q.ID {
			return w, true
		}
		if q.BundleID != "" && strings.EqualFold(w.BundleID, q.BundleID) {
			return w, true
		}
	}
	return wireApp{}, false
}

// primaryOffer returns the first offer that is not a volume offer, falling back to the first offer.
func primaryOffer(offers []wireOffer) (wireOffer, bool) {
	for _, o := range offers {
		if o.Quantity == nil {
			return o, true
		}
	}
	if len(offers) > 0 {
		return offers[0], true
	}
	return wireOffer{}, false
}

// artworkTemplate reads the artwork URL template from either the object or the array form.
func artworkTemplate(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return "", nil
	case strings.HasPrefix(trimmed, "["):
		var list []wireArtwork
		if err := json.Unmarshal(raw, &list); err != nil {
			return "", fmt.Errorf("appstore: decode artwork: %w", err)
		}
		for _, a := range list {
			if a.URL != "" {
				return a.URL, nil
			}
		}
		return "", nil
	default:
		var one wireArtwork
		if err := json.Unmarshal(raw, &one); err != nil {
			return "", fmt.Errorf("appstore: decode artwork: %w", err)
		}
		return one.URL, nil
	}
}
