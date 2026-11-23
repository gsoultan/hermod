// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hermod

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// masterKeySwitchers are the only files allowed to change the process's master
// key, each for a reason that keeps stored data readable:
//
//   - cmd/hermod/main.go installs the key at start-up, in the documented order
//     (--master-key, HERMOD_MASTER_KEY, db_config.yaml);
//   - internal/infra/transport/http/infra.go installs it at first setup (where
//     a pinned key wins) and after a rotation has re-encrypted everything.
//
// A production server once stopped reading its own secrets because setup
// installed a key that the next restart replaced. Any new caller has to show
// it cannot do the same, and be added here deliberately.
var masterKeySwitchers = map[string]bool{
	"cmd/hermod/main.go":                     true,
	"internal/infra/transport/http/infra.go": true,
}

func TestOnlyKnownPlacesSwitchTheMasterKey(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "ui", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(filepath.ToSlash(path), "pkg/security/crypto/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "crypto.SetMasterKey(") && !masterKeySwitchers[filepath.ToSlash(path)] {
			offenders = append(offenders, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("these files change the master key but are not in masterKeySwitchers: %v\n"+
			"a key installed without re-encrypting what is stored, or one a restart replaces, "+
			"leaves stored credentials unreadable", offenders)
	}
}
