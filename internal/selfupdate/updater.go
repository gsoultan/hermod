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
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/selfupdate/install"
	"golang.org/x/mod/semver"
)

// Service phases beyond the installer's own.
const (
	PhaseIdle       = "idle"
	PhaseRestarting = "restarting"
	PhaseFailed     = "failed"
)

var (
	// ErrBusy is an install asked for while one is running.
	ErrBusy = errors.New("an update is already being installed")
	// ErrNotOffered is a version that is not a newer, published release this
	// instance would offer.
	ErrNotOffered = errors.New("that version is not a newer release on the update channel")
	// ErrCannotInstall is an install where this process cannot replace itself.
	ErrCannotInstall = errors.New("this installation cannot update itself")
)

const (
	// installTimeout bounds a whole install, download included.
	installTimeout = 20 * time.Minute
	// pollEvery is how often the loop looks at the clock and the settings. The
	// check itself happens on the configured interval.
	pollEvery = 10 * time.Minute
	// firstCheckDelay keeps the first check off the start-up path.
	firstCheckDelay = time.Minute
)

// SettingsStore is the part of storage the settings live in.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key, value string) error
}

// Logger is the logging this package needs; hermod.Logger satisfies it.
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

// Config wires a Service to the process it updates.
type Config struct {
	// Current is the running build, as version.Version reports it.
	Current      string
	Capability   install.Capability
	GOOS, GOARCH string
	Source       Source
	Install      func(ctx context.Context, plan install.Plan, from string, progress func(install.Phase)) error
	// Store returns the settings storage, or nil before setup has opened one.
	Store func() SettingsStore
	// Restart asks the process to shut down gracefully and start the new binary.
	Restart func()
	// BeforeInstall runs before the download; an error stops the install.
	BeforeInstall func(ctx context.Context) error
	// Outcome is how the last update on this machine ended.
	Outcome func() *install.Outcome
	Logger  Logger
	Now     func() time.Time
}

// Status is everything the Updates page shows.
type Status struct {
	Current         string           `json:"current"`
	Build           string           `json:"build"`
	Latest          *Release         `json:"latest,omitempty"`
	UpdateAvailable bool             `json:"update_available"`
	CheckedAt       *time.Time       `json:"checked_at,omitempty"`
	CheckError      string           `json:"check_error,omitempty"`
	CanInstall      bool             `json:"can_install"`
	InstallBlocked  string           `json:"install_blocked,omitempty"`
	AutoInstallHeld string           `json:"auto_install_held,omitempty"`
	Platform        string           `json:"platform"`
	Phase           string           `json:"phase"`
	PhaseError      string           `json:"phase_error,omitempty"`
	Installing      string           `json:"installing,omitempty"`
	LastOutcome     *install.Outcome `json:"last_outcome,omitempty"`
	Settings        Settings         `json:"settings"`
}

// Service checks for releases, installs them on request or on schedule, and
// reports where it has got to.
type Service struct {
	cfg Config

	mu          sync.Mutex
	latest      *Release
	checkedAt   time.Time
	checkErr    string
	lastAttempt time.Time
	phase       string
	phaseErr    string
	installing  string
	failedTag   string

	wg sync.WaitGroup
}

// New builds a Service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Store == nil {
		cfg.Store = func() SettingsStore { return nil }
	}
	if cfg.Outcome == nil {
		cfg.Outcome = func() *install.Outcome { return nil }
	}
	if cfg.Restart == nil {
		cfg.Restart = func() {}
	}
	return &Service{cfg: cfg, phase: PhaseIdle}
}

// Settings returns the stored choice, or the defaults.
func (s *Service) Settings(ctx context.Context) Settings {
	st := s.cfg.Store()
	if st == nil {
		return DefaultSettings()
	}
	raw, err := st.GetSetting(ctx, SettingsKey)
	if err != nil {
		return DefaultSettings()
	}
	return ParseSettings(raw)
}

// SaveSettings validates and stores the choice.
func (s *Service) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	n, err := in.Normalize()
	if err != nil {
		return in, err
	}
	st := s.cfg.Store()
	if st == nil {
		return n, errors.New("no database is configured yet")
	}
	b, err := json.Marshal(n)
	if err != nil {
		return n, err
	}
	return n, st.SaveSetting(ctx, SettingsKey, string(b))
}

// Status reports the last check and any install in progress.
func (s *Service) Status(ctx context.Context) Status {
	settings := s.Settings(ctx)
	outcome := s.cfg.Outcome()

	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Current:     Canonical(s.cfg.Current),
		Build:       s.cfg.Current,
		CanInstall:  s.cfg.Capability.CanInstall,
		Platform:    s.cfg.Capability.Platform,
		Phase:       s.phase,
		PhaseError:  s.phaseErr,
		Installing:  s.installing,
		LastOutcome: outcome,
		Settings:    settings,
		CheckError:  s.checkErr,
	}
	if !st.CanInstall {
		st.InstallBlocked = s.cfg.Capability.Reason
	}
	if !s.checkedAt.IsZero() {
		t := s.checkedAt
		st.CheckedAt = &t
	}
	if s.latest != nil {
		l := *s.latest
		st.Latest = &l
		st.UpdateAvailable = true
		st.AutoInstallHeld = s.heldLocked(l.Tag, outcome)
	}
	return st
}

// heldLocked says why automatic mode would not install tag by itself.
func (s *Service) heldLocked(tag string, outcome *install.Outcome) string {
	switch {
	case !SameMajor(tag, s.cfg.Current):
		return fmt.Sprintf("%s is a new major version (%s), which may change how Hermod behaves. Automatic updates stay within %s; install it yourself after reading the release notes.",
			tag, semver.Major(Canonical(tag)), semver.Major(Canonical(s.cfg.Current)))
	case outcome != nil && outcome.Result == install.ResultRolledBack && outcome.To == tag:
		return fmt.Sprintf("%s was rolled back on this machine (%s). Install it yourself to try again.", tag, outcome.Detail)
	case s.failedTag == tag:
		return fmt.Sprintf("Installing %s failed (%s). Install it yourself to try again.", tag, s.phaseErr)
	}
	return ""
}

// Check asks for the newest release and records the answer.
func (s *Service) Check(ctx context.Context) (Status, error) {
	include := s.Settings(ctx).IncludePrereleases
	var err error
	if s.cfg.Source == nil {
		err = errors.New("no release source configured")
	}
	var releases []Release
	if err == nil {
		releases, err = s.cfg.Source.Releases(ctx)
	}
	s.mu.Lock()
	s.lastAttempt = s.cfg.Now()
	if err != nil {
		s.checkErr = err.Error()
	} else {
		s.checkErr = ""
		s.checkedAt = s.cfg.Now()
		s.latest = Pick(releases, s.cfg.Current, include)
	}
	s.mu.Unlock()
	return s.Status(ctx), err
}

// Install starts installing tag in the background. It returns once the
// request is accepted; Status reports progress. On success the process is
// asked to restart into the new binary.
func (s *Service) Install(ctx context.Context, tag string) error {
	if !s.cfg.Capability.CanInstall {
		return fmt.Errorf("%w: %s", ErrCannotInstall, s.cfg.Capability.Reason)
	}
	s.mu.Lock()
	if s.installing != "" {
		s.mu.Unlock()
		return ErrBusy
	}
	s.installing = tag
	s.mu.Unlock()

	plan, err := s.plan(ctx, tag)
	if err != nil {
		s.mu.Lock()
		s.installing = ""
		s.mu.Unlock()
		return err
	}

	s.mu.Lock()
	s.phase, s.phaseErr = string(install.PhaseDownloading), ""
	s.mu.Unlock()
	s.wg.Add(1)
	go s.run(plan) //nolint:gosec // G118: an install outlives the request that started it; run bounds it with installTimeout.
	return nil
}

// plan re-reads the release listing rather than trusting an earlier check, so
// only a published, newer release on the configured channel is ever installed.
func (s *Service) plan(ctx context.Context, tag string) (install.Plan, error) {
	if s.cfg.Source == nil {
		return install.Plan{}, errors.New("no release source configured")
	}
	releases, err := s.cfg.Source.Releases(ctx)
	if err != nil {
		return install.Plan{}, err
	}
	rel := findOffered(releases, tag, s.cfg.Current, s.Settings(ctx).IncludePrereleases)
	if rel == nil {
		return install.Plan{}, fmt.Errorf("%w: %s", ErrNotOffered, tag)
	}
	plan := install.Plan{Tag: tag, ArchiveName: install.ArchiveName(tag, s.cfg.GOOS, s.cfg.GOARCH)}
	for _, a := range rel.Assets {
		switch a.Name {
		case plan.ArchiveName:
			plan.ArchiveURL = a.URL
		case "checksums.txt":
			plan.ChecksumsURL = a.URL
		}
	}
	if plan.ArchiveURL == "" || plan.ChecksumsURL == "" {
		return install.Plan{}, fmt.Errorf("release %s has no %s and checksums.txt to install from", tag, plan.ArchiveName)
	}
	return plan, nil
}

// findOffered returns the release tagged tag if it is one this instance would
// offer: published, newer than current, and a pre-release only when asked for.
func findOffered(releases []Release, tag, current string, includePrereleases bool) *Release {
	if Canonical(tag) != tag || !IsNewer(tag, current) {
		return nil
	}
	pre := semver.Prerelease(tag) != ""
	for i := range releases {
		r := &releases[i]
		if r.Tag != tag || r.Draft {
			continue
		}
		if (r.Prerelease || pre) && !includePrereleases {
			continue
		}
		return r
	}
	return nil
}

func (s *Service) run(plan install.Plan) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	from := Canonical(s.cfg.Current)
	s.log("Installing update", "from", from, "to", plan.Tag)

	err := error(nil)
	if s.cfg.BeforeInstall != nil {
		if err = s.cfg.BeforeInstall(ctx); err != nil {
			err = fmt.Errorf("the backup taken before updating failed, so nothing was changed: %w", err)
		}
	}
	if err == nil {
		err = s.cfg.Install(ctx, plan, from, func(p install.Phase) {
			s.mu.Lock()
			s.phase = string(p)
			s.mu.Unlock()
		})
	}

	s.mu.Lock()
	if err != nil {
		s.phase, s.phaseErr, s.installing, s.failedTag = PhaseFailed, err.Error(), "", plan.Tag
		s.mu.Unlock()
		if s.cfg.Logger != nil {
			s.cfg.Logger.Error("Update failed; the running version is unchanged", "to", plan.Tag, "error", err)
		}
		return
	}
	s.phase = PhaseRestarting
	s.mu.Unlock()
	s.log("Update installed; restarting into it", "from", from, "to", plan.Tag)
	s.cfg.Restart()
}

// Wait blocks until a background install has finished.
func (s *Service) Wait() { s.wg.Wait() }

// Run checks on the configured interval until ctx ends, and in automatic
// mode installs what it finds.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(firstCheckDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.tick(ctx)
		t.Reset(pollEvery)
	}
}

func (s *Service) tick(ctx context.Context) {
	settings := s.Settings(ctx)
	if settings.Mode == ModeOff {
		return
	}
	s.mu.Lock()
	due := s.lastAttempt.IsZero() || s.cfg.Now().Sub(s.lastAttempt) >= time.Duration(settings.CheckIntervalHours)*time.Hour
	s.mu.Unlock()
	if !due {
		return
	}
	st, err := s.Check(ctx)
	if err != nil {
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("Update check failed", "error", err)
		}
		return
	}
	if st.UpdateAvailable {
		s.log("A newer Hermod release is available", "current", st.Current, "latest", st.Latest.Tag, "mode", string(settings.Mode))
	}
	if settings.Mode != ModeAutomatic || !st.UpdateAvailable || !st.CanInstall || st.AutoInstallHeld != "" {
		return
	}
	if err := s.Install(ctx, st.Latest.Tag); err != nil && s.cfg.Logger != nil {
		s.cfg.Logger.Warn("Automatic update did not start", "to", st.Latest.Tag, "error", err)
	}
}

func (s *Service) log(msg string, kv ...any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info(msg, kv...)
	}
}
