// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package selfupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/selfupdate/install"
)

type memStore struct {
	mu   sync.Mutex
	rows map[string]string
}

func (m *memStore) GetSetting(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[k], nil
}

func (m *memStore) SaveSetting(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]string{}
	}
	m.rows[k] = v
	return nil
}

type staticSource struct {
	releases []Release
	err      error
	calls    int
}

func (s *staticSource) Releases(context.Context) ([]Release, error) {
	s.calls++
	return s.releases, s.err
}

func release(tag string, pre bool) Release {
	base := "https://github.com/gsoultan/hermod/releases/download/" + tag + "/"
	return Release{Tag: tag, Prerelease: pre, Assets: []Asset{
		{Name: "checksums.txt", URL: base + "checksums.txt"},
		{Name: install.ArchiveName(tag, "linux", "amd64"), URL: base + install.ArchiveName(tag, "linux", "amd64")},
	}}
}

type recorder struct {
	mu       sync.Mutex
	plans    []install.Plan
	err      error
	restarts int
	backups  int
}

func (r *recorder) install(_ context.Context, p install.Plan, _ string, progress func(install.Phase)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	progress(install.PhaseDownloading)
	r.plans = append(r.plans, p)
	return r.err
}

func newService(t *testing.T, current string, src *staticSource, rec *recorder) *Service {
	t.Helper()
	store := &memStore{}
	svc := New(Config{
		Current:    current,
		Capability: install.Capability{CanInstall: true, Platform: "linux/amd64"},
		GOOS:       "linux", GOARCH: "amd64",
		Source:  src,
		Install: rec.install,
		Store:   func() SettingsStore { return store },
		Restart: func() { rec.mu.Lock(); rec.restarts++; rec.mu.Unlock() },
		BeforeInstall: func(context.Context) error {
			rec.mu.Lock()
			rec.backups++
			rec.mu.Unlock()
			return nil
		},
	})
	return svc
}

func TestCheckReportsNewerRelease(t *testing.T) {
	src := &staticSource{releases: []Release{release("v1.24.9", false), release("v1.25.0", false)}}
	svc := newService(t, "v1.24.9 (Enterprise Edition)", src, &recorder{})
	st, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.UpdateAvailable || st.Latest == nil || st.Latest.Tag != "v1.25.0" || st.CheckedAt == nil {
		t.Fatalf("status = %+v", st)
	}
	if st.Current != "v1.24.9" {
		t.Fatalf("current = %q, want the release tag", st.Current)
	}
}

func TestCheckFailureIsReportedNotFatal(t *testing.T) {
	src := &staticSource{err: errors.New("rate limited")}
	svc := newService(t, "v1.24.9", src, &recorder{})
	st, err := svc.Check(context.Background())
	if err == nil || !strings.Contains(st.CheckError, "rate limited") || st.UpdateAvailable {
		t.Fatalf("Check = %+v, %v", st, err)
	}
}

func TestInstallRunsBackupThenInstallerThenRestart(t *testing.T) {
	src := &staticSource{releases: []Release{release("v1.25.0", false)}}
	rec := &recorder{}
	svc := newService(t, "v1.24.9", src, rec)
	if err := svc.Install(context.Background(), "v1.25.0"); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if len(rec.plans) != 1 || rec.backups != 1 || rec.restarts != 1 {
		t.Fatalf("plans=%d backups=%d restarts=%d", len(rec.plans), rec.backups, rec.restarts)
	}
	p := rec.plans[0]
	if p.Tag != "v1.25.0" || p.ArchiveName != "hermod_1.25.0_linux_amd64.tar.gz" ||
		!strings.HasSuffix(p.ChecksumsURL, "/v1.25.0/checksums.txt") || !strings.HasSuffix(p.ArchiveURL, p.ArchiveName) {
		t.Fatalf("plan = %+v", p)
	}
	if st := svc.Status(context.Background()); st.Phase != PhaseRestarting {
		t.Fatalf("phase = %q, want restarting", st.Phase)
	}
}

func TestInstallFailureDoesNotRestart(t *testing.T) {
	src := &staticSource{releases: []Release{release("v1.25.0", false)}}
	rec := &recorder{err: install.ErrChecksum}
	svc := newService(t, "v1.24.9", src, rec)
	if err := svc.Install(context.Background(), "v1.25.0"); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	st := svc.Status(context.Background())
	if rec.restarts != 0 || st.Phase != PhaseFailed || !strings.Contains(st.PhaseError, "SHA-256") {
		t.Fatalf("restarts=%d status=%+v", rec.restarts, st)
	}
}

func TestInstallRefusals(t *testing.T) {
	src := &staticSource{releases: []Release{release("v1.24.0", false), release("v1.25.0", false), release("v1.26.0-rc.1", true)}}
	rec := &recorder{}
	svc := newService(t, "v1.24.9", src, rec)
	for _, tag := range []string{"v1.24.0", "v1.24.9", "v9.9.9", "v1.26.0-rc.1"} {
		if err := svc.Install(context.Background(), tag); !errors.Is(err, ErrNotOffered) {
			t.Errorf("Install(%s) = %v, want ErrNotOffered", tag, err)
		}
	}
	svc.Wait()
	if len(rec.plans) != 0 {
		t.Fatalf("installed %v", rec.plans)
	}
}

func TestInstallBlockedWhereBinaryCannotBeReplaced(t *testing.T) {
	src := &staticSource{releases: []Release{release("v1.25.0", false)}}
	rec := &recorder{}
	svc := newService(t, "v1.24.9", src, rec)
	svc.cfg.Capability = install.Capability{Reason: "container"}
	if err := svc.Install(context.Background(), "v1.25.0"); !errors.Is(err, ErrCannotInstall) {
		t.Fatalf("Install in a container = %v, want ErrCannotInstall", err)
	}
}

func TestOneInstallAtATime(t *testing.T) {
	src := &staticSource{releases: []Release{release("v1.25.0", false)}}
	block := make(chan struct{})
	rec := &recorder{}
	svc := newService(t, "v1.24.9", src, rec)
	svc.cfg.Install = func(ctx context.Context, p install.Plan, f string, pr func(install.Phase)) error {
		<-block
		return rec.install(ctx, p, f, pr)
	}
	if err := svc.Install(context.Background(), "v1.25.0"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Install(context.Background(), "v1.25.0"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Install = %v, want ErrBusy", err)
	}
	close(block)
	svc.Wait()
}

func TestTickAutomaticInstallsWithinMajorOnly(t *testing.T) {
	cases := []struct {
		name     string
		mode     Mode
		releases []Release
		want     int
		held     string
	}{
		{"automatic installs a minor release", ModeAutomatic, []Release{release("v1.25.0", false)}, 1, ""},
		{"automatic holds a major release for a person", ModeAutomatic, []Release{release("v2.0.0", false)}, 0, "major"},
		{"manual only reports", ModeManual, []Release{release("v1.25.0", false)}, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recorder{}
			svc := newService(t, "v1.24.9", &staticSource{releases: c.releases}, rec)
			if _, err := svc.SaveSettings(context.Background(), Settings{Mode: c.mode}); err != nil {
				t.Fatal(err)
			}
			svc.tick(context.Background())
			svc.Wait()
			if len(rec.plans) != c.want {
				t.Fatalf("installs = %d, want %d", len(rec.plans), c.want)
			}
			st := svc.Status(context.Background())
			if !st.UpdateAvailable {
				t.Fatal("tick did not check")
			}
			if c.held != "" && !strings.Contains(st.AutoInstallHeld, c.held) {
				t.Fatalf("held = %q, want it to mention %q", st.AutoInstallHeld, c.held)
			}
		})
	}
}

func TestTickDoesNotReinstallAVersionThatWasRolledBack(t *testing.T) {
	rec := &recorder{}
	svc := newService(t, "v1.24.9", &staticSource{releases: []Release{release("v1.25.0", false)}}, rec)
	svc.cfg.Outcome = func() *install.Outcome {
		return &install.Outcome{Result: install.ResultRolledBack, From: "v1.24.9", To: "v1.25.0"}
	}
	if _, err := svc.SaveSettings(context.Background(), Settings{Mode: ModeAutomatic}); err != nil {
		t.Fatal(err)
	}
	svc.tick(context.Background())
	svc.Wait()
	if len(rec.plans) != 0 {
		t.Fatal("automatic mode reinstalled a version this machine already rolled back")
	}
	if st := svc.Status(context.Background()); !strings.Contains(st.AutoInstallHeld, "rolled back") {
		t.Fatalf("held = %q", st.AutoInstallHeld)
	}
}

func TestTickRespectsOffAndInterval(t *testing.T) {
	src := &staticSource{releases: []Release{release("v1.25.0", false)}}
	now := time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)
	svc := newService(t, "v1.24.9", src, &recorder{})
	svc.cfg.Now = func() time.Time { return now }

	if _, err := svc.SaveSettings(context.Background(), Settings{Mode: ModeOff}); err != nil {
		t.Fatal(err)
	}
	svc.tick(context.Background())
	if src.calls != 0 {
		t.Fatal("off mode checked on its own")
	}

	if _, err := svc.SaveSettings(context.Background(), Settings{Mode: ModeManual, CheckIntervalHours: 6}); err != nil {
		t.Fatal(err)
	}
	svc.tick(context.Background())
	now = now.Add(5 * time.Hour)
	svc.tick(context.Background())
	if src.calls != 1 {
		t.Fatalf("checks = %d after 5h of a 6h interval, want 1", src.calls)
	}
	now = now.Add(time.Hour)
	svc.tick(context.Background())
	if src.calls != 2 {
		t.Fatalf("checks = %d after 6h, want 2", src.calls)
	}
}

func TestSettingsWithoutStorage(t *testing.T) {
	svc := New(Config{Current: "v1.0.0", Store: func() SettingsStore { return nil }})
	if got := svc.Settings(context.Background()); got != DefaultSettings() {
		t.Fatalf("Settings before setup = %+v", got)
	}
	if _, err := svc.SaveSettings(context.Background(), Settings{Mode: ModeOff}); err == nil {
		t.Fatal("saving settings with no database succeeded")
	}
}

func TestGitHubSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/gsoultan/hermod/releases" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v1.25.0","name":"v1.25.0","body":"notes","html_url":"https://github.com/gsoultan/hermod/releases/tag/v1.25.0","published_at":"2026-11-01T00:00:00Z","prerelease":false,"draft":false,
			"assets":[{"name":"checksums.txt","browser_download_url":"https://github.com/gsoultan/hermod/releases/download/v1.25.0/checksums.txt","size":10}]}]`))
	}))
	defer srv.Close()
	gh := &GitHub{Client: srv.Client(), BaseURL: srv.URL, Repo: "gsoultan/hermod"}
	rels, err := gh.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Tag != "v1.25.0" || rels[0].Notes != "notes" || len(rels[0].Assets) != 1 || rels[0].Published.IsZero() {
		t.Fatalf("releases = %+v", rels)
	}
}

func TestGitHubSourceReportsRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	gh := &GitHub{Client: srv.Client(), BaseURL: srv.URL, Repo: "gsoultan/hermod"}
	if _, err := gh.Releases(context.Background()); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("Releases = %v, want a rate limit error", err)
	}
}
