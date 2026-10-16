// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package appstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fixture reads a captured response body from testdata. Every fixture is a real response from the
// 2026-10-16 probe, saved verbatim.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

// recorded is one request the test server saw.
type recorded struct {
	query     url.Values
	userAgent string
	accept    string
}

// server answers every request with body and records what it was asked.
func server(t *testing.T, status int, body []byte) (*httptest.Server, *[]recorded) {
	t.Helper()
	var mu sync.Mutex
	var seen []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, recorded{query: r.URL.Query(), userAgent: r.UserAgent(), accept: r.Header.Get("Accept")})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestLookup_MacByID(t *testing.T) {
	srv, seen := server(t, http.StatusOK, fixture(t, "xcode-mac"))
	app, err := New(WithEndpoint(srv.URL), WithUserAgent("test-agent/1")).Lookup(context.Background(), Query{ID: "497799835", Platform: PlatformMac})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if app.BundleID != "com.apple.dt.Xcode" || app.Name != "Xcode" || app.ID != "497799835" {
		t.Errorf("identity = %q %q %q", app.ID, app.BundleID, app.Name)
	}
	if app.Version != "27.1" {
		t.Errorf("Version = %q, want 27.1", app.Version)
	}
	if !app.Free || app.Price != 0 {
		t.Errorf("Free = %v Price = %v, want free", app.Free, app.Price)
	}
	if app.URL != "https://apps.apple.com/us/app/xcode/id497799835?mt=12" {
		t.Errorf("URL = %q", app.URL)
	}
	if app.Country != "US" || app.Language != "en-us" {
		t.Errorf("storefront = %q %q", app.Country, app.Language)
	}
	if app.Kind != "desktopApp" || app.Developer != "Apple" {
		t.Errorf("Kind = %q Developer = %q", app.Kind, app.Developer)
	}
	if len(app.Genres) != 1 || app.Genres[0] != "Developer Tools" {
		t.Errorf("Genres = %v", app.Genres)
	}
	if !app.DeviceBasedLicensing {
		t.Error("DeviceBasedLicensing = false, want true")
	}

	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	got := (*seen)[0]
	want := map[string]string{"version": "2", "p": "mdm-lockup", "caller": "MDM", "cc": "us", "platform": "macappstore", "id": "497799835"}
	for k, v := range want {
		if got.query.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, got.query.Get(k), v)
		}
	}
	for _, absent := range []string{"bundleId", "l"} {
		if got.query.Has(absent) {
			t.Errorf("query carries %s, want it absent", absent)
		}
	}
	if got.userAgent != "test-agent/1" || got.accept != "application/json" {
		t.Errorf("headers: UA %q Accept %q", got.userAgent, got.accept)
	}
}

func TestLookup_MobileByBundleIDReturnsApplesSpelling(t *testing.T) {
	srv, seen := server(t, http.StatusOK, fixture(t, "bundleid-case-variant"))
	app, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{BundleID: "COM.APPLE.PAGES", Platform: PlatformMobile, Country: "us"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if app.BundleID != "com.apple.Pages" || app.ID != "361309726" {
		t.Errorf("got %q / %q, want Apple's spelling com.apple.Pages / 361309726", app.BundleID, app.ID)
	}
	q := (*seen)[0].query
	if q.Get("bundleId") != "COM.APPLE.PAGES" || q.Get("platform") != "enterprisestore" || q.Has("id") {
		t.Errorf("query = %v", q)
	}
}

func TestLookup_MacAndMobileFlavoursDiffer(t *testing.T) {
	mac, _ := server(t, http.StatusOK, fixture(t, "pages-mac"))
	ios, _ := server(t, http.StatusOK, fixture(t, "pages-ios"))
	m, err := New(WithEndpoint(mac.URL)).Lookup(context.Background(), Query{ID: "361309726", Platform: PlatformMac})
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	i, err := New(WithEndpoint(ios.URL)).Lookup(context.Background(), Query{ID: "361309726", Platform: PlatformMobile})
	if err != nil {
		t.Fatalf("ios: %v", err)
	}
	if m.ArtworkURLTemplate == i.ArtworkURLTemplate || m.Description == i.Description {
		t.Error("the Mac and iOS flavours of Pages decoded to the same icon or description")
	}
	if m.Kind != "iosSoftware" {
		t.Errorf("Mac flavour Kind = %q; the fixture records iosSoftware, which is why Kind is documented as not following Platform", m.Kind)
	}
}

func TestLookup_PaidApp(t *testing.T) {
	srv, _ := server(t, http.StatusOK, fixture(t, "things3-mac-paid"))
	app, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{ID: "904280696", Platform: PlatformMac})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if app.Free || app.Price != 49.99 || app.PriceFormatted != "$49.99" {
		t.Errorf("Free = %v Price = %v %q", app.Free, app.Price, app.PriceFormatted)
	}
}

func TestLookup_VolumeOfferIsSkipped(t *testing.T) {
	srv, _ := server(t, http.StatusOK, fixture(t, "volumestore-two-offer"))
	app, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{ID: "361309726", Platform: PlatformMobile})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if app.Version != "15.4" || !app.Free {
		t.Errorf("Version = %q Free = %v", app.Version, app.Free)
	}
}

func TestLookup_EmptyResultsIsNotFound(t *testing.T) {
	srv, _ := server(t, http.StatusOK, fixture(t, "garbage-id"))
	_, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{ID: "1", Platform: PlatformMac})
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
	if nf.Query.ID != "1" || nf.Query.Country != "US" || nf.Query.Platform != PlatformMac {
		t.Errorf("NotFoundError.Query = %+v", nf.Query)
	}
	if !strings.Contains(err.Error(), "Mac") {
		t.Errorf("message %q does not name the platform", err)
	}
}

func TestLookup_StorefrontFallbackIsAMismatch(t *testing.T) {
	srv, _ := server(t, http.StatusOK, fixture(t, "cc-xx-fallback"))
	_, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{ID: "361309726", Platform: PlatformMobile, Country: "xx"})
	var mm *StorefrontMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("err = %v, want *StorefrontMismatchError (the fixture is populated, so this must win over not-found)", err)
	}
	if mm.Requested != "XX" || mm.Answered != "US" {
		t.Errorf("mismatch = %+v", mm)
	}
}

func TestLookup_NegotiatedLanguageAndStorefront(t *testing.T) {
	srv, seen := server(t, http.StatusOK, fixture(t, "pages-gb-lang-fr"))
	app, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{ID: "361309726", Platform: PlatformMobile, Country: "GB", Language: "fr"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if app.Country != "GB" || app.Language != "en-gb" || !strings.Contains(app.URL, "/gb/") {
		t.Errorf("Country %q Language %q URL %q", app.Country, app.Language, app.URL)
	}
	if q := (*seen)[0].query; q.Get("cc") != "gb" || q.Get("l") != "fr" {
		t.Errorf("query = %v", q)
	}
}

func TestLookup_StatusError(t *testing.T) {
	srv, _ := server(t, http.StatusServiceUnavailable, []byte("busy"))
	_, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{ID: "1", Platform: PlatformMac})
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusServiceUnavailable || string(se.Body) != "busy" {
		t.Fatalf("err = %v, want *StatusError 503", err)
	}
}

func TestLookup_MalformedBody(t *testing.T) {
	srv, _ := server(t, http.StatusOK, []byte("<html>blocked</html>"))
	_, err := New(WithEndpoint(srv.URL)).Lookup(context.Background(), Query{ID: "1", Platform: PlatformMac})
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("err = %v, want a decode error", err)
	}
}

func TestLookup_InvalidQueriesSendNothing(t *testing.T) {
	srv, seen := server(t, http.StatusOK, fixture(t, "garbage-id"))
	c := New(WithEndpoint(srv.URL))
	cases := map[string]Query{
		"neither key":       {Platform: PlatformMac},
		"both keys":         {ID: "1", BundleID: "com.example", Platform: PlatformMac},
		"non-numeric id":    {ID: "abc", Platform: PlatformMac},
		"comma list id":     {ID: "1,2", Platform: PlatformMac},
		"comma bundle id":   {BundleID: "a,b", Platform: PlatformMac},
		"missing platform":  {ID: "1"},
		"unknown platform":  {ID: "1", Platform: "volumestore"},
		"three-letter code": {ID: "1", Platform: PlatformMac, Country: "USA"},
	}
	for name, q := range cases {
		if _, err := c.Lookup(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("%s: err = %v, want ErrInvalidQuery", name, err)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("%d requests sent for refused queries, want 0", len(*seen))
	}
}

// logRecorder is a Logger that records what it was given.
type logRecorder struct {
	requests  []string
	responses []int
	bodyBytes int
}

func (l *logRecorder) LogRequest(_ context.Context, method, url string, _ []byte) {
	l.requests = append(l.requests, method+" "+url)
}

func (l *logRecorder) LogResponse(_ context.Context, statusCode int, _ http.Header, body []byte) {
	l.responses = append(l.responses, statusCode)
	l.bodyBytes = len(body)
}

func TestLookup_Logs(t *testing.T) {
	body := fixture(t, "xcode-mac")
	srv, _ := server(t, http.StatusOK, body)
	log := &logRecorder{}
	if _, err := New(WithEndpoint(srv.URL), WithLogger(log)).Lookup(context.Background(), Query{ID: "497799835", Platform: PlatformMac}); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(log.requests) != 1 || !strings.HasPrefix(log.requests[0], "GET "+srv.URL) || !strings.Contains(log.requests[0], "id=497799835") {
		t.Errorf("requests logged = %v", log.requests)
	}
	if len(log.responses) != 1 || log.responses[0] != http.StatusOK || log.bodyBytes != len(body) {
		t.Errorf("responses logged = %v (%d bytes)", log.responses, log.bodyBytes)
	}
}

func TestLookup_HonoursContext(t *testing.T) {
	srv, _ := server(t, http.StatusOK, fixture(t, "xcode-mac"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(WithEndpoint(srv.URL)).Lookup(ctx, Query{ID: "1", Platform: PlatformMac}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestNew_DefaultsAndHTTPClient(t *testing.T) {
	c := New()
	if c.endpoint != DefaultEndpoint {
		t.Errorf("endpoint = %q", c.endpoint)
	}
	if c.httpClient.Timeout != 0 {
		t.Errorf("default Timeout = %v, want 0 so the caller's context is the deadline", c.httpClient.Timeout)
	}
	custom := &http.Client{}
	if New(WithHTTPClient(custom)).httpClient != custom {
		t.Error("WithHTTPClient did not replace the client")
	}
	if New(WithHTTPClient(nil)).httpClient == nil {
		t.Error("WithHTTPClient(nil) left no client")
	}
}

func TestPlatformValues(t *testing.T) {
	got := PlatformValues()
	if len(got) != 2 || got[0] != PlatformMac || got[1] != PlatformMobile {
		t.Errorf("PlatformValues = %v", got)
	}
}
