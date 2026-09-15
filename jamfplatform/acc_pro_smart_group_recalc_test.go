// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Smart-group recalculation endpoints. Each kicks off a best-effort
// background sweep against a target group or per-device trigger. Run
// against a bogus id so we verify routing without disturbing live
// groups; expect 4xx rejection.

func TestAcceptance_Pro_RecalculateProbesV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	const bogus = "-1"

	tolerate := func(label string, err error) {
		t.Helper()
		if err == nil {
			t.Logf("%s: unexpectedly succeeded for bogus id", label)
			return
		}
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("%s: status=%d — expected for bogus id", label, apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("%s: %v", label, err)
	}

	_, err := p.RecalculateSmartComputerGroupV1(ctx, bogus)
	tolerate("RecalculateSmartComputerGroupV1", err)

	_, err = p.RecalculateSmartMobileDeviceGroupV1(ctx, bogus)
	tolerate("RecalculateSmartMobileDeviceGroupV1", err)

	_, err = p.RecalculateSmartUserGroupV1(ctx, bogus)
	tolerate("RecalculateSmartUserGroupV1", err)

	_, err = p.RecalculateComputerSmartGroupsV1(ctx, bogus)
	tolerate("RecalculateComputerSmartGroupsV1", err)

	_, err = p.RecalculateMobileDeviceSmartGroupsV1(ctx, bogus)
	tolerate("RecalculateMobileDeviceSmartGroupsV1", err)

	_, err = p.RecalculateUserSmartGroupsV1(ctx, bogus)
	tolerate("RecalculateUserSmartGroupsV1", err)
}
