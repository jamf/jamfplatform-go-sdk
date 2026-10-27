// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jcdsupload

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// renewingProvider is an aws.CredentialsProvider over the credentials an initiate returned. Its
// first Retrieve hands back those, and every later one asks the service for replacements, so the
// AWS credentials cache, which only calls it once the credentials are inside the renew margin,
// is what decides when renewal happens.
//
// Only the key, secret, token and expiry are taken from a renewal. Its region, bucket and path
// are empty by design and the upload keeps the initiate's.
type renewingProvider struct {
	api    API
	margin time.Duration

	mu      sync.Mutex
	initial *pro.Credentials
}

func newRenewingProvider(api API, initial *pro.Credentials, margin time.Duration) *renewingProvider {
	return &renewingProvider{api: api, initial: initial, margin: margin}
}

// Retrieve implements aws.CredentialsProvider.
func (p *renewingProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c := p.initial; c != nil {
		p.initial = nil
		return toAWS(c), nil
	}
	c, err := p.api.RenewJCDSCredentialsV1(ctx)
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("renew credentials: %w", err)
	}
	if c == nil || c.AccessKeyID == "" || c.SecretAccessKey == "" || c.SessionToken == "" {
		return aws.Credentials{}, errors.New("renew credentials: the response carries no credentials")
	}
	expires, now := time.Unix(c.Expiration, 0), time.Now()
	if !expires.After(now) {
		return aws.Credentials{}, fmt.Errorf("renew credentials: the response is already expired (expiration %d)", c.Expiration)
	}
	// Credentials the cache already counts as expiring would be renewed again on the next
	// request, and every one after it.
	if !expires.After(now.Add(p.margin)) {
		return aws.Credentials{}, fmt.Errorf("renew credentials: they live %s, inside the renew margin of %s", expires.Sub(now).Round(time.Second), p.margin)
	}
	return toAWS(c), nil
}

func toAWS(c *pro.Credentials) aws.Credentials {
	return aws.Credentials{
		AccessKeyID:     c.AccessKeyID,
		SecretAccessKey: c.SecretAccessKey,
		SessionToken:    c.SessionToken,
		Source:          "jamf-jcds",
		CanExpire:       true,
		Expires:         time.Unix(c.Expiration, 0),
	}
}
