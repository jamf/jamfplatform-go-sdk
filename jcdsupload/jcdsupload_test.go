// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jcdsupload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// fakeAPI serves the initiate and renew calls from fixed responses and counts them.
type fakeAPI struct {
	mu        sync.Mutex
	initiate  *pro.Credentials
	renew     *pro.Credentials
	initErr   error
	renewErr  error
	initCalls int
	renewCall int
}

func (f *fakeAPI) InitiateJCDSUploadV1(context.Context) (*pro.Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initCalls++
	return f.initiate, f.initErr
}

func (f *fakeAPI) RenewJCDSCredentialsV1(context.Context) (*pro.Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewCall++
	return f.renew, f.renewErr
}

func (f *fakeAPI) renews() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renewCall
}

// s3Request is what the fake S3 saw of one request.
type s3Request struct {
	method    string
	path      string
	query     string
	accessKey string
	token     string
	bytes     int64
}

// fakeS3 answers the five operations a transfer manager upload uses, path style, and records
// each request. failPart makes UploadPart answer a non-retryable 403.
type fakeS3 struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	reqs     []s3Request
	failPart bool
}

var credentialRE = regexp.MustCompile(`Credential=([^/]+)/`)

func newFakeS3(t *testing.T) *fakeS3 {
	f := &fakeS3{t: t}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeS3) handle(w http.ResponseWriter, r *http.Request) {
	n, _ := io.Copy(io.Discard, r.Body)
	// An aws-chunked body carries framing and the checksum trailer; the object length is the
	// decoded one.
	if d := r.Header.Get("X-Amz-Decoded-Content-Length"); d != "" {
		n, _ = strconv.ParseInt(d, 10, 64)
	}
	rec := s3Request{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		token:  r.Header.Get("X-Amz-Security-Token"),
		bytes:  n,
	}
	if m := credentialRE.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
		rec.accessKey = m[1]
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, rec)
	f.mu.Unlock()

	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		_, _ = fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == http.MethodPut && q.Has("partNumber"):
		if f.failPart {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
			return
		}
		w.Header().Set("ETag", `"part-`+q.Get("partNumber")+`"`)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		_, _ = fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><CompleteMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		w.Header().Set("ETag", `"single"`)
	default:
		f.t.Errorf("unexpected S3 request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *fakeS3) requests() []s3Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]s3Request(nil), f.reqs...)
}

// options points Upload at the fake, trusting its test certificate.
func (f *fakeS3) options(extra ...Option) []Option {
	return append([]Option{
		WithHTTPClient(f.srv.Client()),
		func(c *config) {
			c.endpoint = f.srv.URL
			c.usePathStyle = true
		},
	}, extra...)
}

func creds(accessKey string, expires time.Time) *pro.Credentials {
	return &pro.Credentials{
		AccessKeyID:     accessKey,
		SecretAccessKey: "secret-" + accessKey,
		SessionToken:    "token-" + accessKey,
		Region:          "eu-west-2",
		BucketName:      "bucket",
		Path:            "tmp/11111111-2222-3333-4444-555555555555/",
		UUID:            "11111111-2222-3333-4444-555555555555",
		Expiration:      expires.Unix(),
	}
}

func TestUploadSinglePart(t *testing.T) {
	s3 := newFakeS3(t)
	api := &fakeAPI{initiate: creds("AKIDINIT", time.Now().Add(time.Hour))}
	body := bytes.Repeat([]byte("x"), 1<<20)

	if err := Upload(t.Context(), api, "app.pkg", bytes.NewReader(body), s3.options()...); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	reqs := s3.requests()
	if len(reqs) != 1 {
		t.Fatalf("want one PutObject, got %d requests: %+v", len(reqs), reqs)
	}
	got := reqs[0]
	if got.method != http.MethodPut || got.path != "/bucket/tmp/11111111-2222-3333-4444-555555555555/app.pkg" {
		t.Errorf("key: got %s %s", got.method, got.path)
	}
	if got.bytes != int64(len(body)) {
		t.Errorf("bytes: got %d, want %d", got.bytes, len(body))
	}
	if got.accessKey != "AKIDINIT" || got.token != "token-AKIDINIT" {
		t.Errorf("signed with %q / %q, want the initiate credentials", got.accessKey, got.token)
	}
	if api.initCalls != 1 || api.renews() != 0 {
		t.Errorf("initiate %d, renew %d; want 1 and 0", api.initCalls, api.renews())
	}
}

func TestUploadMultipart(t *testing.T) {
	s3 := newFakeS3(t)
	api := &fakeAPI{initiate: creds("AKIDINIT", time.Now().Add(time.Hour))}
	const size = 3*MinPartSize + 123
	body := bytes.NewReader(make([]byte, size))

	err := Upload(t.Context(), api, "big.pkg", body, s3.options(WithPartSize(MinPartSize), WithConcurrency(2))...)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	var parts, total int64
	var created, completed bool
	for _, r := range s3.requests() {
		switch {
		case r.method == http.MethodPost && strings.Contains(r.query, "uploads"):
			created = true
		case r.method == http.MethodPut && strings.Contains(r.query, "partNumber"):
			parts++
			total += r.bytes
		case r.method == http.MethodPost && strings.Contains(r.query, "uploadId"):
			completed = true
		}
	}
	if !created || !completed {
		t.Errorf("multipart lifecycle: created=%v completed=%v", created, completed)
	}
	if parts != 4 || total != size {
		t.Errorf("parts %d, bytes %d; want 4 parts and %d bytes", parts, total, int64(size))
	}
}

func TestUploadRenewsCredentialsNearExpiry(t *testing.T) {
	s3 := newFakeS3(t)
	// The initiate credentials are already inside the renew margin, so the second request onward
	// is signed with the renewed ones.
	api := &fakeAPI{
		initiate: creds("AKIDINIT", time.Now().Add(time.Minute)),
		renew: func() *pro.Credentials {
			c := creds("AKIDNEW", time.Now().Add(time.Hour))
			// The renewal response leaves the location fields empty; Upload must not need them.
			c.Region, c.BucketName, c.Path, c.UUID = "", "", "", "00000000-0000-0000-0000-000000000000"
			return c
		}(),
	}
	body := bytes.NewReader(make([]byte, 2*MinPartSize+1))

	err := Upload(t.Context(), api, "big.pkg", body,
		s3.options(WithPartSize(MinPartSize), WithConcurrency(1), WithRenewMargin(10*time.Minute))...)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if api.renews() == 0 {
		t.Fatal("RenewJCDSCredentialsV1 was never called")
	}
	var renewed bool
	for _, r := range s3.requests() {
		if r.path != "/bucket/tmp/11111111-2222-3333-4444-555555555555/big.pkg" {
			t.Errorf("request went to %q, want the initiate path", r.path)
		}
		if r.accessKey == "AKIDNEW" {
			renewed = true
			if r.token != "token-AKIDNEW" {
				t.Errorf("renewed request carried token %q", r.token)
			}
		}
	}
	if !renewed {
		t.Error("no request was signed with the renewed credentials")
	}
}

func TestUploadRenewFailureFailsTheUpload(t *testing.T) {
	s3 := newFakeS3(t)
	api := &fakeAPI{
		initiate: creds("AKIDINIT", time.Now().Add(time.Minute)),
		renewErr: errors.New("renew refused"),
	}
	body := bytes.NewReader(make([]byte, 2*MinPartSize+1))

	err := Upload(t.Context(), api, "big.pkg", body,
		s3.options(WithPartSize(MinPartSize), WithConcurrency(1), WithRenewMargin(10*time.Minute))...)
	if err == nil || !strings.Contains(err.Error(), "renew refused") {
		t.Fatalf("want the renew error, got %v", err)
	}
}

func TestUploadRejectsAnExpiredRenewal(t *testing.T) {
	p := newRenewingProvider(&fakeAPI{renew: creds("AKIDOLD", time.Now().Add(-time.Minute))}, nil)
	if _, err := p.Retrieve(t.Context()); err == nil || !strings.Contains(err.Error(), "already expired") {
		t.Fatalf("want an already-expired error, got %v", err)
	}
	p = newRenewingProvider(&fakeAPI{renew: &pro.Credentials{Expiration: time.Now().Add(time.Hour).Unix()}}, nil)
	if _, err := p.Retrieve(t.Context()); err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("want a no-credentials error, got %v", err)
	}
}

func TestProviderFirstRetrieveIsTheInitiateCredentials(t *testing.T) {
	api := &fakeAPI{renew: creds("AKIDNEW", time.Now().Add(time.Hour))}
	p := newRenewingProvider(api, creds("AKIDINIT", time.Now().Add(time.Hour)))

	first, err := p.Retrieve(t.Context())
	if err != nil || first.AccessKeyID != "AKIDINIT" || !first.CanExpire {
		t.Fatalf("first Retrieve: %+v, %v", first, err)
	}
	if api.renews() != 0 {
		t.Fatalf("the first Retrieve renewed")
	}
	second, err := p.Retrieve(t.Context())
	if err != nil || second.AccessKeyID != "AKIDNEW" {
		t.Fatalf("second Retrieve: %+v, %v", second, err)
	}
}

// A stream that is neither seekable nor sized still uploads; the part size just cannot adapt to
// its length. Wrapping it with SizedReader gives Upload the length to size parts from.
func TestUploadStreamsNonSeekableBodies(t *testing.T) {
	const size = 2*MinPartSize + 17
	for name, body := range map[string]func() io.Reader{
		"unsized": func() io.Reader { return io.MultiReader(bytes.NewReader(make([]byte, size))) },
		"sized": func() io.Reader {
			return jamfplatform.SizedReader(io.MultiReader(bytes.NewReader(make([]byte, size))), size)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s3 := newFakeS3(t)
			api := &fakeAPI{initiate: creds("AKIDINIT", time.Now().Add(time.Hour))}

			if err := Upload(t.Context(), api, "s.pkg", body(), s3.options(WithPartSize(MinPartSize), WithConcurrency(2))...); err != nil {
				t.Fatalf("Upload: %v", err)
			}
			var total int64
			for _, r := range s3.requests() {
				if r.method == http.MethodPut && strings.Contains(r.query, "partNumber") {
					total += r.bytes
				}
			}
			if total != size {
				t.Errorf("uploaded %d bytes, want %d", total, int64(size))
			}
		})
	}
}

func TestUploadAbortsAFailedMultipart(t *testing.T) {
	s3 := newFakeS3(t)
	s3.failPart = true
	api := &fakeAPI{initiate: creds("AKIDINIT", time.Now().Add(time.Hour))}
	body := bytes.NewReader(make([]byte, 2*MinPartSize+1))

	err := Upload(t.Context(), api, "big.pkg", body, s3.options(WithPartSize(MinPartSize), WithConcurrency(1))...)
	if err == nil {
		t.Fatal("want an error from a refused part")
	}
	var aborted bool
	for _, r := range s3.requests() {
		if r.method == http.MethodDelete && strings.Contains(r.query, "uploadId") {
			aborted = true
		}
	}
	if !aborted {
		t.Error("the multipart upload was not aborted")
	}
}

func TestUploadInitiateFailureSendsNothing(t *testing.T) {
	s3 := newFakeS3(t)
	api := &fakeAPI{initErr: errors.New("403 BAD_PERMISSIONS")}

	err := Upload(t.Context(), api, "a.pkg", strings.NewReader("x"), s3.options()...)
	if err == nil || !strings.Contains(err.Error(), "BAD_PERMISSIONS") {
		t.Fatalf("want the initiate error, got %v", err)
	}
	if n := len(s3.requests()); n != 0 {
		t.Errorf("%d S3 requests after a failed initiate", n)
	}
}

func TestUploadRefusesIncompleteInitiate(t *testing.T) {
	for name, mutate := range map[string]func(*pro.Credentials){
		"no bucket":     func(c *pro.Credentials) { c.BucketName = "" },
		"no region":     func(c *pro.Credentials) { c.Region = "" },
		"no path":       func(c *pro.Credentials) { c.Path = "" },
		"no token":      func(c *pro.Credentials) { c.SessionToken = "" },
		"path no slash": func(c *pro.Credentials) { c.Path = "tmp/abc" },
		"no access key": func(c *pro.Credentials) { c.AccessKeyID = "" },
		"no secret":     func(c *pro.Credentials) { c.SecretAccessKey = "" },
	} {
		t.Run(name, func(t *testing.T) {
			s3 := newFakeS3(t)
			c := creds("AKIDINIT", time.Now().Add(time.Hour))
			mutate(c)
			err := Upload(t.Context(), &fakeAPI{initiate: c}, "a.pkg", strings.NewReader("x"), s3.options()...)
			if err == nil {
				t.Fatal("want an error")
			}
			if n := len(s3.requests()); n != 0 {
				t.Errorf("%d S3 requests", n)
			}
		})
	}
	if err := Upload(t.Context(), &fakeAPI{}, "a.pkg", strings.NewReader("x")); err == nil {
		t.Error("a nil initiate response must be refused")
	}
}

func TestUploadValidatesArguments(t *testing.T) {
	api := &fakeAPI{initiate: creds("AKIDINIT", time.Now().Add(time.Hour))}
	body := strings.NewReader("x")

	for _, name := range []string{"", "a/b.pkg", `a\b.pkg`, ".", ".."} {
		if err := Upload(t.Context(), api, name, body); err == nil {
			t.Errorf("file name %q was accepted", name)
		}
	}
	if err := Upload(t.Context(), nil, "a.pkg", body); err == nil {
		t.Error("nil API was accepted")
	}
	if err := Upload(t.Context(), api, "a.pkg", nil); err == nil {
		t.Error("nil body was accepted")
	}
	if err := Upload(t.Context(), api, "a.pkg", body, WithPartSize(MinPartSize-1)); err == nil {
		t.Error("a part size under S3's minimum was accepted")
	}
	if err := Upload(t.Context(), api, "a.pkg", body, WithConcurrency(0)); err == nil {
		t.Error("zero concurrency was accepted")
	}
	if api.initCalls != 0 {
		t.Errorf("initiate was called %d times for invalid arguments", api.initCalls)
	}
}

func TestFitPartSize(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		name       string
		want, size int64
		expect     int64
	}{
		{"unknown size keeps the default", DefaultPartSize, 0, DefaultPartSize},
		{"small file keeps the default", DefaultPartSize, 2 << 30, DefaultPartSize},
		{"80 GB fits at the default boundary", DefaultPartSize, 10000 * DefaultPartSize, DefaultPartSize},
		{"one byte over grows by a MiB", DefaultPartSize, 10000*DefaultPartSize + 1, DefaultPartSize + mib},
		{"1 TiB", DefaultPartSize, 1 << 40, 105 * mib},
		{"a caller's larger size is kept", 64 * mib, 2 << 30, 64 * mib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fitPartSize(tc.want, tc.size)
			if got != tc.expect {
				t.Fatalf("fitPartSize(%d, %d) = %d, want %d", tc.want, tc.size, got, tc.expect)
			}
			if tc.size > 0 && (tc.size+got-1)/got > maxParts {
				t.Errorf("%d bytes at %d needs more than %d parts", tc.size, got, maxParts)
			}
		})
	}
}
