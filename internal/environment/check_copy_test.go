// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"strings"
	"testing"
)

// The secrets check said only names are sent and the approver types the
// values, right above the boxes where the sender types them. It also pointed
// at a settings page by a path the menu does not have.
func TestTheSecretsCheckSaysWhoTypesTheValues(t *testing.T) {
	checks := presenceChecks(Link{Name: "Production"}, CheckResult{MissingSecrets: []string{"CRM_KEY"}}, []string{"CRM_KEY"}, nil)
	if len(checks) != 1 || checks[0].OK {
		t.Fatalf("checks = %+v", checks)
	}
	d := checks[0].Detail
	if strings.Contains(d, "Only their names are sent") {
		t.Fatalf("detail still says the sender cannot type values: %q", d)
	}
	if !strings.Contains(d, "CRM_KEY") || !strings.Contains(d, "approver") {
		t.Fatalf("detail = %q", d)
	}
}
