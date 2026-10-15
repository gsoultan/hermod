// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package config

import "testing"

// ${NAME} in a connector setting reads HERMOD_ENV_NAME, the namespace
// {{env("NAME")}} reads, and nothing else. It read any variable of the
// process, so ${HERMOD_JWT_SECRET} in a Test connection pointed at the
// editor's own host handed them the signing key.
func TestConnectorEnvSubstitutionReadsOnlyTheHermodEnvNamespace(t *testing.T) {
	t.Setenv("HERMOD_ENV_DB_HOST", "db.internal")
	t.Setenv("HERMOD_JWT_SECRET", "signing-key")
	t.Setenv("DB_PASS", "bare")
	t.Setenv(AllowAnyConnectorEnv, "")

	tests := []struct{ in, want string }{
		{"${DB_HOST}:5432", "db.internal:5432"},
		{"${HERMOD_ENV_DB_HOST}", "db.internal"},
		{"${HERMOD_JWT_SECRET}", "${HERMOD_JWT_SECRET}"},
		{"${DB_PASS}", "${DB_PASS}"},
		{"${DB_PASS:-none}", "none"},
		{"plain", "plain"},
	}
	for _, tc := range tests {
		if got := SubstituteConnectorEnvVars(tc.in); got != tc.want {
			t.Errorf("SubstituteConnectorEnvVars(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An operator renaming their variables can let connectors read any variable
// for a release, from their own environment -- nothing a workflow editor
// writes reaches that switch.
func TestConnectorEnvSubstitutionReadsAnyVariableWhenTheOperatorAllowsIt(t *testing.T) {
	t.Setenv("DB_PASS", "bare")
	t.Setenv("HERMOD_ENV_DB_PASS", "namespaced")
	t.Setenv("ONLY_BARE", "bare-only")
	t.Setenv(AllowAnyConnectorEnv, "true")

	if got := SubstituteConnectorEnvVars("${DB_PASS}"); got != "namespaced" {
		t.Errorf("the namespaced variable should win, got %q", got)
	}
	if got := SubstituteConnectorEnvVars("${ONLY_BARE}"); got != "bare-only" {
		t.Errorf("got %q, want the bare variable", got)
	}
}
