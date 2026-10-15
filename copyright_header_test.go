// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package hermod

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyrightHeader is the notice every Go file opens with. It must be followed
// by a blank line so it never merges into a package doc comment or sits flush
// against a //go:build constraint.
//
// Generated files are included. The .proto sources carry the same header above
// their syntax statement, and protoc-gen-go and protoc-gen-go-grpc both print a
// file's leading syntax comments before their "Code generated" line, so
// regenerating keeps the header in place.
const copyrightHeader = `// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

`

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
