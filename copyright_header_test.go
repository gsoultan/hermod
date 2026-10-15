// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package hermod

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// copyrightHeader is the notice every hand-written Go file opens with. It must
// be followed by a blank line so it never merges into a package doc comment or
// sits flush against a //go:build constraint.
const copyrightHeader = `// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

`

// generatedMarker is the line Go tooling recognises as marking a generated
// file. Those are rewritten wholesale by their generator, so a header added by
// hand would not survive the next regeneration; they are exempt.
var generatedMarker = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

func TestEveryGoFileOpensWithTheCopyrightHeader(t *testing.T) {
	var missing []string

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "dist", "build", "graphify-out":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if generatedMarker.Match(src) {
			return nil
		}
		if !strings.HasPrefix(strings.ReplaceAll(string(src), "\r\n", "\n"), copyrightHeader) {
			missing = append(missing, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}

	if len(missing) > 0 {
		t.Errorf("%d Go files do not open with the copyright header (copy it, "+
			"followed by a blank line, from copyright_header_test.go):\n\t%s",
			len(missing), strings.Join(missing, "\n\t"))
	}
}
