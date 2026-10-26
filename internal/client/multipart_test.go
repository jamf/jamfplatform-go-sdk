// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoMultipart_FileUpload(t *testing.T) {
	c, srv, mux := newTestClient(t)

	const fieldName = "file"
	const filename = "icon.png"
	const contents = "\x89PNG\r\n\x1a\n-fake-png-body"

	var seenName, seenFilename, seenBody string
	mux.HandleFunc("/api/icons", func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("Content-Type = %q, want multipart/form-data", ct)
		}
		_, params, err := strings.Cut(ct, "boundary=")
		if !err {
			t.Fatalf("no boundary in Content-Type: %q", ct)
		}
		mr := multipart.NewReader(r.Body, params)
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("reading part: %v", err)
			}
			seenName = p.FormName()
			seenFilename = p.FileName()
			buf, _ := io.ReadAll(p)
			seenBody = string(buf)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"icon-42"}`))
	})

	var result struct{ ID string }
	err := c.DoMultipart(context.Background(), http.MethodPost, "/api/icons", []MultipartField{
		{Name: fieldName, Filename: filename, Content: bytes.NewBufferString(contents)},
	}, http.StatusCreated, &result)
	if err != nil {
		t.Fatalf("DoMultipart: %v", err)
	}
	if seenName != fieldName {
		t.Errorf("field name = %q, want %q", seenName, fieldName)
	}
	if seenFilename != filename {
		t.Errorf("filename = %q, want %q", seenFilename, filename)
	}
	if seenBody != contents {
		t.Errorf("body = %q, want %q", seenBody, contents)
	}
	if result.ID != "icon-42" {
		t.Errorf("result.ID = %q, want icon-42", result.ID)
	}
	_ = srv
}

// Every multipart request declares an exact Content-Length when every file
// part's size is knowable — a seekable reader or an *os.File — and is sent
// chunked only for a plain reader. All three go over HTTP/1.1.
func TestDoMultipart_ContentLengthFollowsReader(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "up-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("A", 4096)
	if _, err := file.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })

	cases := map[string]struct {
		mk      func() io.Reader
		chunked bool
	}{
		"bytes.Reader (seekable)": {func() io.Reader { return bytes.NewReader([]byte(payload)) }, false},
		"*os.File":                {func() io.Reader { _, _ = file.Seek(0, io.SeekStart); return file }, false},
		"plain io.Reader":         {func() io.Reader { return unseekableReader{r: strings.NewReader(payload)} }, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, _, mux := newTestClient(t)
			var gotCL int64
			var gotTE []string
			var gotBody int64
			var gotProto string
			mux.HandleFunc("/api/up", func(w http.ResponseWriter, r *http.Request) {
				gotCL, gotTE, gotProto = r.ContentLength, r.TransferEncoding, r.Proto
				gotBody, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusCreated)
			})

			err := c.DoMultipart(context.Background(), http.MethodPost, "/api/up", []MultipartField{
				{Name: "file", Filename: "x.bin", Content: tc.mk()},
				{Name: "note", Value: "v"},
			}, http.StatusCreated, nil)
			if err != nil {
				t.Fatalf("DoMultipart: %v", err)
			}
			if tc.chunked {
				if gotCL != -1 {
					t.Errorf("ContentLength = %d, want -1 (size unknown)", gotCL)
				}
				if !slices.Equal(gotTE, []string{"chunked"}) {
					t.Errorf("TransferEncoding = %v, want [chunked]", gotTE)
				}
			} else {
				if gotCL != gotBody {
					t.Errorf("ContentLength = %d, want the %d body bytes actually sent", gotCL, gotBody)
				}
				if len(gotTE) != 0 {
					t.Errorf("TransferEncoding = %v, want none", gotTE)
				}
			}
			if gotProto != "HTTP/1.1" {
				t.Errorf("Proto = %q, want HTTP/1.1", gotProto)
			}
			if gotBody < int64(len(payload)) {
				t.Errorf("server read %d body bytes, want at least the %d-byte payload", gotBody, len(payload))
			}
		})
	}
}

// unseekableReader is an io.Reader that deliberately does not implement
// io.Seeker — used to assert the not-rewindable path.
type unseekableReader struct{ r io.Reader }

func (u unseekableReader) Read(p []byte) (int, error) { return u.r.Read(p) }

// The point of the protocol split: on a server that speaks HTTP/2, JSON calls
// and the token exchange keep multiplexing on h2 while a multipart upload goes
// over HTTP/1.1. A no-retry JSON write shares uploadClient with multipart and
// must stay on h2 too, which is why the choice is per request and not a swap of
// that client's transport.
func TestDoMultipart_UsesHTTP1WhileEverythingElseUsesHTTP2(t *testing.T) {
	var mu sync.Mutex
	protos := map[string]string{}
	record := func(key string, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		protos[key] = r.Proto
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, r *http.Request) {
		record("token", r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"t","token_type":"bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/json", func(w http.ResponseWriter, r *http.Request) {
		record(r.Method+" json", r)
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/up", func(w http.ResponseWriter, r *http.Request) {
		record("multipart", r)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c := NewTransportWithUserAgent(srv.URL, "id", "secret", "ua/1", WithMinRequestInterval(0))
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	if len(c.tuned) != 2 {
		t.Fatalf("tuned transports = %d, want the HTTP/2-capable one and its HTTP/1.1 twin", len(c.tuned))
	}
	for _, tr := range c.tuned {
		tr.TLSClientConfig = &tls.Config{RootCAs: pool}
	}

	ctx := context.Background()
	if err := c.Do(ctx, http.MethodGet, "/api/json", nil, nil); err != nil {
		t.Fatalf("GET: %v", err)
	}
	if err := c.DoWithContentTypeNoRetry(ctx, http.MethodPut, "/api/json", map[string]string{"k": "v"}, "application/json", http.StatusOK, nil); err != nil {
		t.Fatalf("no-retry PUT: %v", err)
	}
	if err := c.DoMultipart(ctx, http.MethodPost, "/api/up", []MultipartField{
		{Name: "file", Filename: "x.bin", Content: bytes.NewReader([]byte("payload"))},
	}, http.StatusCreated, nil); err != nil {
		t.Fatalf("multipart: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := map[string]string{
		"token":     "HTTP/2.0",
		"GET json":  "HTTP/2.0",
		"PUT json":  "HTTP/2.0",
		"multipart": "HTTP/1.1",
	}
	for k, w := range want {
		if protos[k] != w {
			t.Errorf("%s over %q, want %q (all: %v)", k, protos[k], w, protos)
		}
	}
}

func TestNewHTTP1Transport_DisablesHTTP2ButKeepsTheTuning(t *testing.T) {
	t.Parallel()

	h1, h2 := newHTTP1Transport(), newTunedTransport()
	if h1.ForceAttemptHTTP2 || h1.HTTP2 != nil {
		t.Errorf("HTTP/2 still requested: ForceAttemptHTTP2=%v HTTP2=%v", h1.ForceAttemptHTTP2, h1.HTTP2)
	}
	if h1.TLSNextProto == nil || len(h1.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto = %v, want a non-nil empty map (the only thing that stops net/http enabling h2 itself)", h1.TLSNextProto)
	}
	if h1.WriteBufferSize != h2.WriteBufferSize || h1.MaxIdleConnsPerHost != h2.MaxIdleConnsPerHost || h1.DialContext == nil || h1.Proxy == nil {
		t.Error("the HTTP/1.1 twin lost the tuned transport's buffers, pool ceiling, dialer or proxy support")
	}
}

// A client supplied through WithHTTPClient keeps its own transport, and so its
// own protocol; the framing still applies to it.
func TestDoMultipart_CallerSuppliedClientStillGetsContentLength(t *testing.T) {
	srv, mux := newTestServer(t)
	var gotCL, gotBody int64
	mux.HandleFunc("/api/up", func(w http.ResponseWriter, r *http.Request) {
		gotCL = r.ContentLength
		gotBody, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	})
	c := NewTransportWithUserAgent(srv.URL, "id", "secret", "ua/1",
		WithHTTPClient(&http.Client{Transport: &http.Transport{}}), WithMinRequestInterval(0))

	if err := c.DoMultipart(context.Background(), http.MethodPost, "/api/up", []MultipartField{
		{Name: "file", Filename: "x.bin", Content: bytes.NewReader([]byte("payload"))},
	}, http.StatusCreated, nil); err != nil {
		t.Fatalf("DoMultipart: %v", err)
	}
	if gotCL != gotBody {
		t.Errorf("ContentLength = %d, want the %d body bytes actually sent", gotCL, gotBody)
	}
}

// TestDoMultipart_RewindOn429 verifies the transport seeks the file Content
// back to 0 and retries once when the server responds 429 with a short
// Retry-After, provided all file parts are io.Seeker.
func TestDoMultipart_RewindOn429(t *testing.T) {
	c, _, mux := newTestClient(t)

	var calls atomic.Int32
	mux.HandleFunc("/api/retry", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		// Drain the body so the client can proceed.
		_, _ = io.Copy(io.Discard, r.Body)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	payload := strings.Repeat("C", 2048)
	rd := bytes.NewReader([]byte(payload))
	err := c.DoMultipart(context.Background(), http.MethodPost, "/api/retry", []MultipartField{
		{Name: "file", Filename: "z.bin", Content: rd},
	}, http.StatusCreated, nil)
	if err != nil {
		t.Fatalf("DoMultipart: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d calls, want 2 (one 429, one retry)", got)
	}
}

// TestDoMultipart_RewindOn500ForIdempotentMethod verifies the generalized
// retry: a PUT (idempotent) upload rewinds and retries on a transient 500,
// same as the JSON/XML transport's isRetryableWriteStatus policy.
func TestDoMultipart_RewindOn500ForIdempotentMethod(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond)

	var calls atomic.Int32
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	payload := strings.Repeat("D", 2048)
	rd := bytes.NewReader([]byte(payload))
	err := c.DoMultipart(context.Background(), http.MethodPut, "/api/upload", []MultipartField{
		{Name: "file", Filename: "pkg.bin", Content: rd},
	}, http.StatusCreated, nil)
	if err != nil {
		t.Fatalf("DoMultipart: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d calls, want 2 (one 500, one retry)", got)
	}
}

// TestDoMultipart_NoRetryOn500ForPost verifies the non-idempotency guard
// carries over to multipart: a POST (create) upload must NOT retry on a
// bare 500, matching isRetryableWriteStatus for the JSON/XML transport.
func TestDoMultipart_NoRetryOn500ForPost(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond)

	var calls atomic.Int32
	mux.HandleFunc("/api/create-upload", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	})

	rd := bytes.NewReader([]byte("payload"))
	err := c.DoMultipart(context.Background(), http.MethodPost, "/api/create-upload", []MultipartField{
		{Name: "file", Filename: "pkg.bin", Content: rd},
	}, http.StatusCreated, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr := AsAPIError(err)
	if apiErr == nil || !apiErr.HasStatus(http.StatusInternalServerError) {
		t.Fatalf("expected APIResponseError(500), got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call (no retry on POST+500), got %d", got)
	}
}

// A transport error mid-upload re-sends the whole file, so it is retried once
// and no more, however many attempts retryMax allows for status-driven retries.
func TestDoMultipart_TransportErrorRetriedOnce(t *testing.T) {
	c, _, mux := newTestClient(t)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond)

	var calls atomic.Int32
	mux.HandleFunc("/api/drop", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})

	// PUT, so a drop after the body was written is still retryable and the
	// count measures the bound rather than the idempotency rule.
	err := c.DoMultipart(context.Background(), http.MethodPut, "/api/drop", []MultipartField{
		{Name: "file", Filename: "x.bin", Content: bytes.NewReader([]byte("payload"))},
	}, http.StatusOK, nil)
	if err == nil {
		t.Fatal("DoMultipart succeeded against a server that drops every connection")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2 (one send, one retry)", got)
	}
}

// TestDoMultipart_NoRetryWhenNotRewindable verifies a retryable status
// (500 on an idempotent method) is NOT retried when Content can't be seeked
// back to the start — matches the doc comment: the caller must re-invoke
// with a fresh reader instead.
func TestDoMultipart_NoRetryWhenNotRewindable(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond)

	var calls atomic.Int32
	mux.HandleFunc("/api/unseekable-upload", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	})

	err := c.DoMultipart(context.Background(), http.MethodPut, "/api/unseekable-upload", []MultipartField{
		{Name: "file", Filename: "pkg.bin", Content: unseekableReader{r: strings.NewReader("payload")}},
	}, http.StatusCreated, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call (not rewindable, must not retry), got %d", got)
	}
}
