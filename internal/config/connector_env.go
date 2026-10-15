// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gsoultan/hermod/pkg/security/secrets"
)

// AllowAnyConnectorEnv, set to true in the operator's own environment, lets
// ${NAME} in a connector setting fall back to the bare variable NAME, as every
// substitution did before. It exists so a deployment can rename its variables
// to HERMOD_ENV_NAME without an outage, and it will be removed.
const AllowAnyConnectorEnv = "HERMOD_ENV_ALLOW_ANY"

var warnBareConnectorEnv sync.Once

// SubstituteConnectorEnvVars is SubstituteEnvVars for a source or sink
// setting: ${NAME} reads HERMOD_ENV_NAME (secrets.EnvVarPrefix), the namespace
// {{env("NAME")}} reads, and nothing else. ${NAME:-default} still falls back
// to its default, and a ${NAME} with nothing to read is left as written.
//
// Hermod's own config file keeps SubstituteEnvVars, which reads any variable:
// the operator writes that file. A connector setting is written by whoever can
// edit a workflow, and reading any variable let ${HERMOD_JWT_SECRET} in a Test
// connection pointed at their own host hand them the signing key.
func SubstituteConnectorEnvVars(input string) string {
	if !strings.Contains(input, "${") {
		return input
	}
	allowAny, _ := strconv.ParseBool(os.Getenv(AllowAnyConnectorEnv))
	return envRegex.ReplaceAllStringFunc(input, func(m string) string {
		matches := envRegex.FindStringSubmatch(m)
		if len(matches) < 2 {
			return m
		}
		name := matches[1]
		if val := secrets.LookupEnvVar(name); val != "" {
			return val
		}
		if allowAny {
			if val, ok := os.LookupEnv(name); ok {
				// Once, and the name only: this runs for every connector built.
				warnBareConnectorEnv.Do(func() {
					log.Printf("config: a connector read ${%s} from the unprefixed variable because %s is set; rename it to %s%s -- the fallback will be removed",
						name, AllowAnyConnectorEnv, secrets.EnvVarPrefix, strings.TrimPrefix(name, secrets.EnvVarPrefix))
				})
				return val
			}
		}
		if len(matches) > 2 && strings.Contains(m, ":-") {
			return matches[2]
		}
		return m
	})
}
