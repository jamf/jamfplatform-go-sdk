// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Handwritten, unlike most of this package. jamfplatform/errors.go is emitted
// from a template in tools/generate/emit.go, and the public sentinel is only
// useful if it stays the *same object* the transport raises. A template edited
// to `errors.New(...)` instead of `client.ErrUnexpectedResponse` still compiles
// and still reads correctly, but silently breaks errors.Is for every consumer.
// This pins that.
package jamfplatform_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/internal/client"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
)

func TestErrUnexpectedResponseIsTheTransportSentinel(t *testing.T) {
	t.Parallel()

	if jamfplatform.ErrUnexpectedResponse == nil {
		t.Fatal("jamfplatform.ErrUnexpectedResponse is nil")
	}
	if !errors.Is(jamfplatform.ErrUnexpectedResponse, client.ErrUnexpectedResponse) {
		t.Error("the exported sentinel is not the one the transport raises, so errors.Is will fail for consumers")
	}

	// The way consumers actually use it: matched through a wrap.
	wrapped := fmt.Errorf("ValidateCredentials: %w", client.ErrUnexpectedResponse)
	if !errors.Is(wrapped, jamfplatform.ErrUnexpectedResponse) {
		t.Error("expected the exported sentinel to match a wrapped transport error")
	}
}
