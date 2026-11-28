// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package queryplan

import "strings"

// Summary is the plan's verdict in a sentence or two, for an error that has to
// say why a lookup's query was slow: what each badly reached table costs and
// the change that fixes it. A plan that reaches every table the right way says
// so, because then the time went somewhere the plan cannot show.
func (c Check) Summary() string {
	var parts []string
	for _, f := range c.Findings {
		if f.Severity == SeverityOK {
			continue
		}
		parts = append(parts, f.sentence())
	}
	if len(parts) == 0 {
		return "every table it reads is reached through an index or is small, so the plan is not what made it slow: " +
			"the query waited inside the database, on a lock another session held or on the server's own load"
	}
	return strings.Join(parts, " ")
}

// sentence is one finding with the change it proposes.
func (f Finding) sentence() string {
	s := strings.TrimSpace(f.Detail)
	if f.Rewrite != "" {
		s += " Rewrite the condition as: " + f.Rewrite + "."
	}
	if f.Proposal != "" {
		s += " Suggested index: " + f.Proposal
	}
	return s
}
