// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Package jcdsupload uploads a file to the Jamf Cloud Distribution Service by the route the
// service itself issues: POST /pro/v1/jcds/files hands back temporary AWS credentials and a
// bucket path, and the file goes to S3 directly. The gateway and CloudFront are not in the data
// path, so the 64 KiB per-stream HTTP/2 window that halves a gateway upload's throughput does
// not apply; a 2 GiB file went through in 3m15s.
//
// It is handwritten and lives outside jamfplatform/ on purpose: that tree is generated and
// pruned. It is also the only part of the SDK that depends on the AWS SDK, so a consumer that
// never imports it never compiles it.
//
// # What Upload does and does not do
//
// Upload moves bytes. It does not create the package record, refresh the inventory or wait for
// Jamf Pro to register the file; those are the caller's, in whatever order suits it. The sequence
// that works is create the record, Upload, then refresh and poll until the record reports
// READY — with the refresh being POST /jcds/refresh-inventory, or the cloud-distribution-point
// equivalent.
//
// # Wire behaviour
//
// Probed 2026-10-27 against two environments: one with a JCDS distribution point (US) and one
// with an Amazon S3 distribution point (EU). Each rule below shapes the code.
//
//   - The initiate response carries accessKeyID, secretAccessKey, sessionToken, region,
//     bucketName, path (always "tmp/{uuid}/", trailing slash included) and uuid. The object key is
//     path + fileName. The credentials are valid for 3600 seconds, and expiration is Unix seconds.
//     The bucket is regional, whatever the tenant's distribution point is set to.
//   - Renewal takes no parameters and returns new credentials with region, bucketName and path
//     empty and a zero uuid. Only the key, secret, token and expiration are usable: bucket, region
//     and path stay those of the initiate. A renewal made before any initiate returns credentials
//     for no upload. Renewed credentials write to the initiate path.
//   - The credentials can put and complete a multipart upload under the issued path and cannot
//     read: HeadObject is 403. An upload therefore cannot be verified from this side, and the
//     caller's poll of the package record is the check.
//   - Objects under tmp/ are removed daily by a bucket lifecycle rule, so a file that is never
//     registered does not accumulate.
//   - The file appears in GET /jcds/files and the package record fills in only after a
//     refresh-inventory. A 1 MiB file registered within 3 seconds; a 2 GiB file took about two
//     minutes after the last byte landed. A caller's poll has to be patient about large files.
//   - On a tenant whose distribution point is Amazon S3 rather than JCDS, the same sequence
//     uploads and registers the file in the JCDS inventory but the package record never links to
//     it (no hashes, no status after a minute of refreshes). The route is a JCDS route.
//   - The whole route is deprecated upstream (2025-08-28) and may be removed. The generated
//     methods carry that marker; this package is the one place in the SDK that depends on them.
//
// # Proxies and the transport
//
// The S3 traffic is not carried by jamfplatform's transport, so WithHeaders, the scope header,
// the rate-limit throttle and the tuned HTTP transport do not apply to it. The default HTTP
// client honours HTTPS_PROXY; WithHTTPClient replaces it for a caller that needs more.
package jcdsupload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/jamf/jamfplatform-go-sdk/internal/client"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

const (
	// DefaultPartSize is the multipart part size when the upload's size does not demand a larger
	// one. It matches the AWS transfer manager's own default.
	DefaultPartSize int64 = 8 << 20

	// MinPartSize is S3's floor for every part but the last.
	MinPartSize int64 = 5 << 20

	// DefaultConcurrency is the number of parts in flight at once.
	DefaultConcurrency = 5

	// DefaultRenewMargin is how long before the credentials expire that Upload replaces them.
	// The credentials live an hour, so five minutes is wide enough to cover a stalled part.
	DefaultRenewMargin = 5 * time.Minute

	// maxParts is S3's ceiling on the parts of one multipart upload.
	maxParts int64 = 10000
)

// API is the part of *pro.Client that Upload needs. It is an interface so a test can supply a
// fake, and so the deprecated methods are named in one place.
type API interface {
	// InitiateJCDSUploadV1 returns temporary credentials and the bucket path for one upload.
	InitiateJCDSUploadV1(ctx context.Context) (*pro.Credentials, error)
	// RenewJCDSCredentialsV1 returns replacement credentials for the upload last initiated.
	RenewJCDSCredentialsV1(ctx context.Context) (*pro.Credentials, error)
}

var _ API = (*pro.Client)(nil)

// Option configures Upload.
type Option func(*config)

type config struct {
	partSize    int64
	concurrency int
	renewMargin time.Duration
	httpClient  *http.Client

	// Test hooks, set only from this package's own tests.
	endpoint     string
	usePathStyle bool
}

// WithPartSize sets the multipart part size in bytes. It must be at least MinPartSize. A size too
// small to keep the upload within S3's 10,000 parts is raised to the smallest that fits when the
// body's length is known.
func WithPartSize(n int64) Option { return func(c *config) { c.partSize = n } }

// WithConcurrency sets how many parts are uploaded at once. Memory use is about concurrency times
// the part size when the body is not seekable.
func WithConcurrency(n int) Option { return func(c *config) { c.concurrency = n } }

// WithRenewMargin sets how long before the credentials expire that they are replaced through
// RenewJCDSCredentialsV1. Zero waits for the expiry itself.
func WithRenewMargin(d time.Duration) Option { return func(c *config) { c.renewMargin = d } }

// WithHTTPClient sets the HTTP client for the S3 requests. The default honours HTTPS_PROXY.
func WithHTTPClient(hc *http.Client) Option { return func(c *config) { c.httpClient = hc } }

// Upload sends body to the Jamf Cloud Distribution Service as fileName. The name is what the
// package record's fileName must be, and what the inventory lists.
//
// body is read once, from its current position. When its length is knowable (an *os.File, an
// io.Seeker, a *bytes.Buffer, or a reader wrapped with jamfplatform.SizedReader) the part size is
// chosen so the upload fits S3's 10,000-part limit; otherwise the default part size caps it at
// about 80 GiB. A failed multipart upload is aborted so no parts are left behind.
//
// Upload initiates one upload per call, so a caller that retries after a failure calls it again
// and gets a new path.
func Upload(ctx context.Context, api API, fileName string, body io.Reader, opts ...Option) error {
	if api == nil {
		return errors.New("jcdsupload.Upload: nil API")
	}
	if body == nil {
		return errors.New("jcdsupload.Upload: nil body")
	}
	if err := validateFileName(fileName); err != nil {
		return fmt.Errorf("jcdsupload.Upload(%q): %w", fileName, err)
	}
	cfg := config{
		partSize:    DefaultPartSize,
		concurrency: DefaultConcurrency,
		renewMargin: DefaultRenewMargin,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.partSize < MinPartSize {
		return fmt.Errorf("jcdsupload.Upload(%q): part size %d is below S3's minimum of %d", fileName, cfg.partSize, MinPartSize)
	}
	if cfg.concurrency < 1 {
		return fmt.Errorf("jcdsupload.Upload(%q): concurrency %d must be at least 1", fileName, cfg.concurrency)
	}

	creds, err := api.InitiateJCDSUploadV1(ctx)
	if err != nil {
		return fmt.Errorf("jcdsupload.Upload(%q): initiate: %w", fileName, err)
	}
	if err := validateInitiate(creds); err != nil {
		return fmt.Errorf("jcdsupload.Upload(%q): %w", fileName, err)
	}

	cache := aws.NewCredentialsCache(newRenewingProvider(api, creds), func(o *aws.CredentialsCacheOptions) {
		o.ExpiryWindow = cfg.renewMargin
	})
	s3Opts := s3.Options{
		Region:       creds.Region,
		Credentials:  cache,
		AppID:        "jamfplatform-go-sdk",
		UsePathStyle: cfg.usePathStyle,
	}
	if cfg.httpClient != nil {
		s3Opts.HTTPClient = cfg.httpClient
	}
	if cfg.endpoint != "" {
		s3Opts.BaseEndpoint = aws.String(cfg.endpoint)
	}

	size, _ := client.ReaderSize(body)
	partSize := fitPartSize(cfg.partSize, size)
	tm := transfermanager.New(s3.New(s3Opts), func(o *transfermanager.Options) {
		o.PartSizeBytes = partSize
		o.MultipartUploadThreshold = partSize
		o.Concurrency = cfg.concurrency
	})
	if _, err := tm.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(creds.BucketName),
		Key:    aws.String(creds.Path + fileName),
		Body:   body,
	}); err != nil {
		return fmt.Errorf("jcdsupload.Upload(%q): %w", fileName, err)
	}
	return nil
}

// fitPartSize returns want, raised to the smallest whole-MiB size that keeps an upload of size
// bytes within maxParts parts. An unknown size (zero or negative) leaves want alone.
func fitPartSize(want, size int64) int64 {
	if size <= 0 {
		return want
	}
	need := (size + maxParts - 1) / maxParts
	need = (need + (1<<20 - 1)) &^ (1<<20 - 1)
	return max(want, need)
}

// validateFileName rejects what cannot be one S3 key segment under the issued path. The service
// lists and addresses files by this name alone, so a separator would put the object where the
// inventory does not look.
func validateFileName(name string) error {
	switch {
	case name == "":
		return errors.New("file name is empty")
	case strings.ContainsAny(name, `/\`):
		return errors.New("file name must not contain a path separator")
	case name == "." || name == "..":
		return errors.New("file name must not be a relative path component")
	}
	return nil
}

// validateInitiate refuses an initiate response that cannot address an upload. The renewal
// response has empty location fields by design, so it is never passed through here.
func validateInitiate(c *pro.Credentials) error {
	if c == nil {
		return errors.New("initiate returned no credentials")
	}
	var missing []string
	for name, v := range map[string]string{
		"accessKeyID":     c.AccessKeyID,
		"secretAccessKey": c.SecretAccessKey,
		"sessionToken":    c.SessionToken,
		"region":          c.Region,
		"bucketName":      c.BucketName,
		"path":            c.Path,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("initiate response is missing %s", strings.Join(missing, ", "))
	}
	if !strings.HasSuffix(c.Path, "/") {
		return fmt.Errorf("initiate path %q does not end in a slash", c.Path)
	}
	return nil
}
