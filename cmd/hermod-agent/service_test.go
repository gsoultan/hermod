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
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultDataDirFollowsEachPlatformsConvention(t *testing.T) {
	env := func(vals map[string]string) func(string) string {
		return func(k string) string { return vals[k] }
	}
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"windows", map[string]string{"ProgramData": `C:\ProgramData`}, filepath.Join(`C:\ProgramData`, "hermod-agent")},
		{"windows", nil, filepath.Join(`C:\ProgramData`, "hermod-agent")},
		{"darwin", nil, "/Library/Application Support/hermod-agent"},
		{"linux", nil, "/var/lib/hermod-agent"},
		{"freebsd", nil, "/var/lib/hermod-agent"},
	}
	for _, c := range cases {
		if got := defaultDataDir(c.goos, env(c.env)); got != c.want {
			t.Errorf("%s %v: got %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}

func TestUnknownServiceActionIsRefused(t *testing.T) {
	_, err := parseFlags([]string{"-server", "h:1", "-service", "explode"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "-service") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceControlNeedsNoServer(t *testing.T) {
	for _, a := range []string{"uninstall", "start", "stop", "restart", "status"} {
		if _, err := parseFlags([]string{"-service", a}, &bytes.Buffer{}); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
	if _, err := parseFlags([]string{"-service", "install"}, &bytes.Buffer{}); err == nil {
		t.Error("install without -server accepted")
	}
}

func TestInstalledServiceNeedsATokenFile(t *testing.T) {
	// A service does not inherit the installing shell's environment, so a
	// token held only in $HERMOD_AGENT_TOKEN would be gone at the first boot.
	f, err := parseFlags([]string{"-server", "h:1", "-service", "install"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serviceArgs(f); err == nil || !strings.Contains(err.Error(), "-token-file") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstalledServiceKeepsTheSettingsWithAbsolutePaths(t *testing.T) {
	t.Chdir(t.TempDir())
	f, err := parseFlags([]string{
		"-server", "hermod.example.com:9443", "-token-file", "token", "-ca-file", "ca.pem",
		"-data-dir", "data", "-spool-max-mb", "64", "-service", "install",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	args, err := serviceArgs(f)
	if err != nil {
		t.Fatal(err)
	}
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	want := []string{
		"-server", "hermod.example.com:9443",
		"-token-file", abs("token"),
		"-data-dir", abs("data"),
		"-spool-max-mb", "64",
		"-ca-file", abs("ca.pem"),
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q\nwant   %q", args, want)
	}
	if slices.Contains(args, "-service") {
		t.Fatal("the service would try to install itself")
	}
}

func TestInstalledServiceKeepsPlaintext(t *testing.T) {
	f, err := parseFlags([]string{"-server", "localhost:9443", "-token-file", "/t", "-plaintext", "-service", "install"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	args, err := serviceArgs(f)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "-plaintext") {
		t.Fatalf("args = %q", args)
	}
}

func TestServiceStopWaitsForTheAgentToFinish(t *testing.T) {
	finished := make(chan struct{})
	p := &program{logOut: &bytes.Buffer{}, work: func(ctx context.Context, _ io.Writer) error {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // closing the spool
		close(finished)
		return ctx.Err()
	}}
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(nil); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before the agent had finished")
	}
}

func TestServiceStopReportsAnAgentFailure(t *testing.T) {
	p := &program{logOut: &bytes.Buffer{}, work: func(ctx context.Context, _ io.Writer) error {
		<-ctx.Done()
		return errors.New("spool corrupt")
	}}
	_ = p.Start(nil)
	if err := p.Stop(nil); err == nil || !strings.Contains(err.Error(), "spool corrupt") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstalledServiceKeepsDiscovery(t *testing.T) {
	f, err := parseFlags([]string{"-server", "h:1", "-token-file", "/t", "-service", "install",
		"-mdns-services", "", "-scan-subnet", "192.168.1.0/24", "-discover-interval", "10m"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	args, err := serviceArgs(f)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-mdns-services= ", "-scan-subnet 192.168.1.0/24", "-discover-interval 10m0s"} {
		if !strings.Contains(joined+" ", want) {
			t.Fatalf("args = %q, missing %q", args, want)
		}
	}
}

func TestInstalledServiceKeepsAllowUpdate(t *testing.T) {
	f, err := parseFlags([]string{"-server", "h:1", "-token-file", "/t", "-service", "install", "-allow-update"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	args, err := serviceArgs(f)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "-allow-update") {
		t.Fatalf("args = %q", args)
	}
	f, _ = parseFlags([]string{"-server", "h:1", "-token-file", "/t", "-service", "install"}, &bytes.Buffer{})
	if args, _ := serviceArgs(f); slices.Contains(args, "-allow-update") {
		t.Fatal("updates were turned on without being asked for")
	}
}
