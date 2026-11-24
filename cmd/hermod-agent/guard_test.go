// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// hermod-agent runs on small machines next to hardware and ships on its own,
// so it must not pull in the Hermod server. The design keeps the shared part
// to the wire contract alone; this test holds the build to that by listing
// every package the binary links and refusing any other Hermod package.
func TestAgentImportsNothingOfTheServer(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const module = "github.com/gsoultan/hermod"
	allowed := []string{module + "/cmd/hermod-agent", module + "/pkg/agent/proto"}
	for _, pkg := range strings.Fields(string(out)) {
		if pkg != module && !strings.HasPrefix(pkg, module+"/") {
			continue
		}
		ok := false
		for _, a := range allowed {
			if pkg == a || strings.HasPrefix(pkg, a+"/") {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("hermod-agent links %s; it may only use its own packages and pkg/agent/proto", pkg)
		}
	}
}
