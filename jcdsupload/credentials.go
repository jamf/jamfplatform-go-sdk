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
	api API

	mu      sync.Mutex
	initial *pro.Credentials
}

func newRenewingProvider(api API, initial *pro.Credentials) *renewingProvider {
	return &renewingProvider{api: api, initial: initial}
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
	if !time.Unix(c.Expiration, 0).After(time.Now()) {
		return aws.Credentials{}, fmt.Errorf("renew credentials: the response is already expired (expiration %d)", c.Expiration)
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
