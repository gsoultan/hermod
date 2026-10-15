// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package factory

import "testing"

func TestIdempotencyTableSuffix(t *testing.T) {
	for _, tc := range []struct {
		name      string
		namespace string
		want      string
	}{
		{"plain", "tenant1", "tenant1"},
		{"keeps underscores and hyphens", "acme_eu-west", "acme_eu-west"},
		{"drops a quote that would end the identifier", `a"; DROP TABLE x --`, "aDROPTABLEx--"},
		{"drops spaces and dots", "acme corp.eu", "acmecorpeu"},
		{"empty stays empty, leaving the default table", "", ""},
		{"nothing survivable stays empty", "!!!", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := idempotencyTableSuffix(tc.namespace); got != tc.want {
				t.Errorf("idempotencyTableSuffix(%q) = %q, want %q", tc.namespace, got, tc.want)
			}
		})
	}
}

// Two sinks sharing the database must not share a keyspace. The same source
// message rendered as mail through SMTP and through panmail is two different
// sends, and one suppressing the other is a message that silently never goes.
func TestSinkIdempotencyTablesDoNotCollide(t *testing.T) {
	const namespace = "acme"
	smtpTable := "smtp_idempotency_" + idempotencyTableSuffix(namespace)
	panmailTable := "panmail_idempotency_" + idempotencyTableSuffix(namespace)

	if smtpTable == panmailTable {
		t.Fatalf("both sinks resolved to %q", smtpTable)
	}
}
