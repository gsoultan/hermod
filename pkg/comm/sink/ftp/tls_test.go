// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ftp

import (
	"testing"
	"time"
)

// FTPS used to dial with InsecureSkipVerify always on: the TLS was there, but
// any machine in the path could present its own certificate and read the
// login. The certificate is now checked against the host unless the sink is
// told, explicitly, to skip it.
func TestFTPSVerifiesTheServerCertificateByDefault(t *testing.T) {
	s, err := NewFTPSink("files.example.com", 21, "u", "p", true, time.Second, "", "", "f", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.tlsConfig()
	if cfg.InsecureSkipVerify {
		t.Error("FTPS skips certificate verification by default")
	}
	if cfg.ServerName != "files.example.com" {
		t.Errorf("ServerName = %q, want the configured host", cfg.ServerName)
	}
}

func TestFTPSSkipsVerificationOnlyWhenAskedTo(t *testing.T) {
	s, err := NewFTPSink("10.0.0.5", 990, "u", "p", true, time.Second, "", "", "f", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.SetInsecureSkipVerify(true)
	if !s.tlsConfig().InsecureSkipVerify {
		t.Error("SetInsecureSkipVerify(true) did not take effect")
	}
}
