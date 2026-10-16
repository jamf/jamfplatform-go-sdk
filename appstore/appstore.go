// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Package appstore looks up an App Store app's public listing: its name, version, bundle
// identifier, store URL, description, icon and price. It is the metadata an MDM administrator
// otherwise copies by hand into a Mac or mobile device App Store app record.
//
// The upstream is Apple's unauthenticated content-metadata lookup for device management, not a
// Jamf service, so this package shares nothing with jamfplatform's transport: no credentials, no
// scope header, no retries. It is standard library only and carries no consumer's vocabulary, so a
// Terraform provider and a command-line tool can both build on it. Apple documents the endpoint at
// https://developer.apple.com/documentation/devicemanagement/getting-app-and-book-information-legacy
// and has published an authenticated successor that needs an Apple Developer token; Query and App
// name no endpoint parameter, so a token-based source can be added behind the same types.
//
// # Wire behaviour
//
// Probed 2026-10-16 (unauthenticated, US and GB storefronts). Each rule below shapes the code.
//
//   - The answer is always 200, for a bad id, a wrong storefront, the wrong platform or an
//     unauthenticated custom app alike. A miss is an empty results object, so Lookup turns it into
//     a *NotFoundError rather than returning an empty App. It is not transient and is not retried.
//   - The id parameter takes a comma list and bundleId is also a lookup key. A bundle identifier
//     is matched case-insensitively and the result is keyed by the request's spelling, while the
//     App carries Apple's spelling. A caller that stores the bundle identifier verbatim should
//     compare the two.
//   - platform=macappstore filters out iOS-only apps and returns the Mac flavour of a universal
//     app: a different icon, description, release notes, offer and requirements string. Every
//     other value returns the iPhone and iPad flavour. MinimumOSVersion and Kind do not switch
//     with the flavour (Pages' Mac flavour reports kind iosSoftware and its iOS minimum), so treat
//     both as properties of the listing rather than of the platform asked for.
//   - An unrecognised country code silently falls back to the US storefront. Lookup compares the
//     storefront that answered with the one requested and returns a *StorefrontMismatchError,
//     checked before emptiness because a real id under a bad code comes back populated.
//   - The language is negotiated (GB with fr answers en-gb), and URL gains the storefront path, so
//     the URL differs per country and language.
//   - Apple has merged some Mac apps into their iOS listing: the old Mac Pages id 409201541 is
//     empty everywhere and Pages is 361309726 on both platforms.
//   - The version is the first offer's version.display. volumestore adds a second education offer
//     carrying a quantity; offers with a quantity are skipped. Free means a price of zero.
//   - artwork was an object in every listing probed. Apple's documentation also shows an array
//     form, unprobed, so both decode. Its url is a template ending {w}x{h}bb.{f}; see ArtworkURL.
//     iconArtwork uses a different token, {w}x{h}{c}.{f}, and is deliberately not read.
//   - Responses carry cache-control: max-age=900. Bursts of 400 requests at 44 per second drew no
//     429 and no latency growth, but the limit is undocumented, so callers should cap their own
//     concurrency.
//   - Not probed: the itvt sToken cookie that exposes custom (B2B) apps.
package appstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// DefaultEndpoint is Apple's content-metadata lookup URL.
const DefaultEndpoint = "https://uclient-api.itunes.apple.com/WebObjects/MZStorePlatform.woa/wa/lookup"

// DefaultCountry is the storefront Lookup uses when a Query names none.
const DefaultCountry = "US"

// maxResponseBytes caps a lookup body. A single listing is about 12 KB; the cap only guards
// against a runaway response.
const maxResponseBytes = 4 << 20

// Platform selects which flavour of a listing Lookup asks for. Its values are the wire values.
type Platform string

const (
	// PlatformMac asks for the Mac flavour. An app with no Mac version is not found.
	PlatformMac Platform = "macappstore"
	// PlatformMobile asks for the iPhone and iPad flavour.
	PlatformMobile Platform = "enterprisestore"
)

// PlatformValues returns every Platform Lookup accepts.
func PlatformValues() []Platform {
	return []Platform{PlatformMac, PlatformMobile}
}

// Query names one app to look up. Exactly one of ID and BundleID must be set.
type Query struct {
	// ID is the numeric App Store id, the digits after "id" in a store URL.
	ID string
	// BundleID is the app's bundle identifier, matched case-insensitively.
	BundleID string
	// Platform is required.
	Platform Platform
	// Country is a two-letter storefront code, either case. Empty means DefaultCountry.
	Country string
	// Language is an optional language tag. Apple negotiates it against the storefront.
	Language string
}

// App is one listing as the storefront that answered describes it.
type App struct {
	ID       string
	BundleID string
	Name     string
	// Version is the first offer's display version. Empty when the listing has no offer.
	Version string
	// URL is the listing's store URL for the storefront and language that answered.
	URL         string
	Description string
	// ArtworkURLTemplate is the icon URL with Apple's {w}, {h}, {f} placeholders left in. Empty
	// when the listing carries no artwork.
	ArtworkURLTemplate string
	Free               bool
	Price              float64
	PriceFormatted     string
	Genres             []string
	// DeviceBasedLicensing reports whether licences for the app can be assigned to devices
	// rather than to users.
	DeviceBasedLicensing bool
	Developer            string
	ReleaseNotes         string
	// MinimumOSVersion does not follow Platform; see the package documentation.
	MinimumOSVersion string
	DeviceFamilies   []string
	// Kind does not follow Platform; see the package documentation.
	Kind string
	// Country is the storefront that answered, upper case.
	Country string
	// Language is the negotiated language tag.
	Language string
}

// artworkToken is the size-and-format placeholder at the end of an artwork URL template.
const artworkToken = "{w}x{h}bb.{f}"

// ErrNoArtwork is returned by ArtworkURL when the listing carries no artwork.
var ErrNoArtwork = errors.New("appstore: listing has no artwork")

// ErrArtworkTemplate is wrapped by ArtworkURL when the template lacks the {w}x{h}bb.{f} token,
// so a changed template fails instead of yielding a URL with the placeholders left in.
var ErrArtworkTemplate = errors.New("appstore: artwork URL template has no {w}x{h}bb.{f} token")

// ArtworkURL fills the icon template at the given pixel size and image format ("png", "jpg").
// Apple's image service resizes and transcodes on request: 512x512 and 1024x1024 PNG and 512x512
// JPEG were each served at exactly the size asked for (2026-10-16). Mac-flavour icons carry an
// alpha channel and iPhone-flavour icons are opaque, so the Platform a listing was looked up with
// decides the icon's shape.
func (a *App) ArtworkURL(width, height int, format string) (string, error) {
	if a == nil || a.ArtworkURLTemplate == "" {
		return "", ErrNoArtwork
	}
	if width <= 0 || height <= 0 || format == "" {
		return "", fmt.Errorf("appstore: artwork size %dx%d and format %q must be positive and non-empty", width, height, format)
	}
	if strings.Count(a.ArtworkURLTemplate, artworkToken) != 1 {
		return "", fmt.Errorf("%w: %s", ErrArtworkTemplate, a.ArtworkURLTemplate)
	}
	return strings.Replace(a.ArtworkURLTemplate, artworkToken, fmt.Sprintf("%dx%dbb.%s", width, height, format), 1), nil
}

// Logger receives each request and response. Its method set matches jamfplatform.Logger, so one
// value can serve both packages.
type Logger interface {
	LogRequest(ctx context.Context, method, url string, body []byte)
	LogResponse(ctx context.Context, statusCode int, headers http.Header, body []byte)
}

// Client performs lookups. The zero value is not usable; build one with New.
type Client struct {
	endpoint   string
	httpClient *http.Client
	logger     Logger
	userAgent  string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client. The default has no Timeout, so the context passed to
// Lookup is the request deadline, and it honours proxy environment variables.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) {
		if c != nil {
			cl.httpClient = c
		}
	}
}

// WithLogger sets a logger for each request and response.
func WithLogger(l Logger) Option {
	return func(cl *Client) { cl.logger = l }
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(cl *Client) { cl.userAgent = ua }
}

// WithEndpoint replaces DefaultEndpoint, for a test server or a proxy.
func WithEndpoint(endpoint string) Option {
	return func(cl *Client) {
		if endpoint != "" {
			cl.endpoint = endpoint
		}
	}
}

// New returns a Client.
func New(opts ...Option) *Client {
	transport, _ := http.DefaultTransport.(*http.Transport)
	c := &Client{endpoint: DefaultEndpoint, httpClient: &http.Client{}}
	if transport != nil {
		c.httpClient.Transport = transport.Clone()
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ErrInvalidQuery is wrapped by every error Lookup returns for a Query it refuses to send.
var ErrInvalidQuery = errors.New("appstore: invalid query")

// NotFoundError reports that the storefront answered with no listing for the query. It is a
// definitive answer, not a transient failure.
type NotFoundError struct {
	Query Query
}

// Error implements error.
func (e *NotFoundError) Error() string {
	key := "id " + e.Query.ID
	if e.Query.ID == "" {
		key = "bundle identifier " + e.Query.BundleID
	}
	return fmt.Sprintf("appstore: no %s app with %s in the %s storefront", platformLabel(e.Query.Platform), key, e.Query.Country)
}

// StorefrontMismatchError reports that a storefront other than the requested one answered, which
// is how Apple treats a country code it does not recognise.
type StorefrontMismatchError struct {
	Requested string
	Answered  string
}

// Error implements error.
func (e *StorefrontMismatchError) Error() string {
	return fmt.Sprintf("appstore: asked for the %s storefront and the %s storefront answered; %s is not a recognised country or region code", e.Requested, e.Answered, e.Requested)
}

// StatusError reports a non-200 response.
type StatusError struct {
	StatusCode int
	Body       []byte
}

// Error implements error.
func (e *StatusError) Error() string {
	return fmt.Sprintf("appstore: lookup answered HTTP %d", e.StatusCode)
}

var (
	digitsPattern  = regexp.MustCompile(`^[0-9]+$`)
	countryPattern = regexp.MustCompile(`^[A-Za-z]{2}$`)
)

// normalise validates q and fills its defaults, returning the query that will be sent.
func normalise(q Query) (Query, error) {
	q.ID = strings.TrimSpace(q.ID)
	q.BundleID = strings.TrimSpace(q.BundleID)
	q.Country = strings.ToUpper(strings.TrimSpace(q.Country))
	q.Language = strings.TrimSpace(q.Language)
	switch {
	case q.ID == "" && q.BundleID == "":
		return q, fmt.Errorf("%w: set an id or a bundle identifier", ErrInvalidQuery)
	case q.ID != "" && q.BundleID != "":
		return q, fmt.Errorf("%w: set an id or a bundle identifier, not both", ErrInvalidQuery)
	case q.ID != "" && !digitsPattern.MatchString(q.ID):
		return q, fmt.Errorf("%w: id %q is not numeric", ErrInvalidQuery, q.ID)
	case strings.Contains(q.BundleID, ","):
		return q, fmt.Errorf("%w: bundle identifier %q contains a comma", ErrInvalidQuery, q.BundleID)
	}
	if q.Platform != PlatformMac && q.Platform != PlatformMobile {
		return q, fmt.Errorf("%w: platform %q is not one of %v", ErrInvalidQuery, q.Platform, PlatformValues())
	}
	if q.Country == "" {
		q.Country = DefaultCountry
	}
	if !countryPattern.MatchString(q.Country) {
		return q, fmt.Errorf("%w: country %q is not a two-letter code", ErrInvalidQuery, q.Country)
	}
	return q, nil
}

// requestURL builds the lookup URL for a normalised query.
func (c *Client) requestURL(q Query) (string, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return "", fmt.Errorf("appstore: endpoint %q: %w", c.endpoint, err)
	}
	v := u.Query()
	v.Set("version", "2")
	v.Set("p", "mdm-lockup")
	v.Set("caller", "MDM")
	v.Set("cc", strings.ToLower(q.Country))
	v.Set("platform", string(q.Platform))
	if q.Language != "" {
		v.Set("l", q.Language)
	}
	if q.ID != "" {
		v.Set("id", q.ID)
	} else {
		v.Set("bundleId", q.BundleID)
	}
	u.RawQuery = v.Encode()
	return u.String(), nil
}

// Lookup returns the listing q names. A miss is a *NotFoundError, a storefront Apple substituted
// is a *StorefrontMismatchError, a non-200 answer is a *StatusError, and a refused query wraps
// ErrInvalidQuery.
func (c *Client) Lookup(ctx context.Context, q Query) (*App, error) {
	q, err := normalise(q)
	if err != nil {
		return nil, err
	}
	target, err := c.requestURL(q)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("appstore: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if c.logger != nil {
		c.logger.LogRequest(ctx, req.Method, target, nil)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("appstore: lookup: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("appstore: read response: %w", err)
	}
	if c.logger != nil {
		c.logger.LogResponse(ctx, resp.StatusCode, resp.Header, body)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("appstore: response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: body}
	}
	return decode(q, body)
}

// platformLabel names a Platform in an error message.
func platformLabel(p Platform) string {
	switch p {
	case PlatformMac:
		return "Mac"
	case PlatformMobile:
		return "iPhone or iPad"
	default:
		return string(p)
	}
}
