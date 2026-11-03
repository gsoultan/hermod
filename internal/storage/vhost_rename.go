// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

// RenameInVHostList returns a user's vhost list with from replaced by to, and
// whether from was in it. The list keeps its order; to appears once even if
// the user already had it or had from twice. The input is not modified.
func RenameInVHostList(vhosts []string, from, to string) ([]string, bool) {
	found := false
	for _, v := range vhosts {
		if v == from {
			found = true
			break
		}
	}
	if !found {
		return vhosts, false
	}
	out := make([]string, 0, len(vhosts))
	seen := make(map[string]bool, len(vhosts))
	for _, v := range vhosts {
		if v == from {
			v = to
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out, true
}
