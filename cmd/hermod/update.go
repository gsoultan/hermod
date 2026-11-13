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
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/api"
	"github.com/gsoultan/hermod/internal/selfupdate"
	"github.com/gsoultan/hermod/internal/selfupdate/install"
	"github.com/gsoultan/hermod/internal/service"
	"github.com/gsoultan/hermod/internal/version"
)

// confirmUpdateAfter is how long a new version must stay up before its
// update is confirmed and the rollback guard disarmed.
const confirmUpdateAfter = time.Minute

func updateEnv() install.Env {
	return install.CurrentEnv(version.Version, runtime.GOOS, runtime.GOARCH)
}

// newUpdater builds the self-updater for this process. store is nil for the
// command-line update, which has no database and uses the default settings.
func newUpdater(store func() selfupdate.SettingsStore, server *api.Server, logger hermod.Logger) *selfupdate.Service {
	env := updateEnv()
	inst := &install.Installer{Exe: env.Exe, Fetch: install.Fetcher(install.NewHTTPClient())}
	cfg := selfupdate.Config{
		Current:    version.Version,
		Capability: install.Detect(env),
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		Source:     selfupdate.NewGitHub(),
		Install:    inst.Install,
		Store:      store,
		Restart:    install.RequestRestart,
		Outcome:    func() *install.Outcome { return install.LastOutcome(env.Exe) },
		Logger:     logger,
	}
	if server != nil {
		cfg.BeforeInstall = server.BackupBeforeUpdate
	}
	return selfupdate.New(cfg)
}

// serverStore reads the API's storage each time, because first-run setup
// replaces it.
func serverStore(server *api.Server) func() selfupdate.SettingsStore {
	return func() selfupdate.SettingsStore {
		if server.Handler.Storage == nil {
			return nil
		}
		return server.Handler.Storage
	}
}

// guardUpdate counts this start against a pending update and, when the new
// version has failed to stay up too many times, restores the previous binary
// and restarts into it. It never returns in that case.
func guardUpdate() {
	exe := updateEnv().Exe
	rolledBack, err := install.GuardBoot(exe, version.Version, install.DefaultBootAttempts)
	if err != nil {
		log.Printf("Update guard: %v", err)
		return
	}
	if rolledBack {
		o := install.LastOutcome(exe)
		if o != nil {
			log.Printf("Rolled back to %s: %s", o.From, o.Detail)
		}
		restartIntoBinary(exe)
	}
}

// confirmUpdate disarms the rollback guard once this process has stayed up
// for confirmUpdateAfter. A process that stops sooner confirms nothing.
func confirmUpdate(ctx context.Context, logger hermod.Logger) {
	exe := updateEnv().Exe
	if install.Pending(exe) == nil {
		return
	}
	t := time.NewTimer(confirmUpdateAfter)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-t.C:
	}
	if err := install.Confirm(exe, version.Version); err != nil {
		logger.Warn("Could not confirm the update", "error", err)
		return
	}
	if o := install.LastOutcome(exe); o != nil && o.Result == install.ResultUpdated {
		logger.Info("Update confirmed", "from", o.From, "to", o.To)
	}
}

// restartIntoBinary starts the binary now on disk in place of this process.
// Under a service manager that means exiting and letting it start us again;
// run by hand, the process replaces itself.
func restartIntoBinary(exe string) {
	if install.Supervised(service.Interactive()) {
		os.Exit(install.ExitCodeRestart)
	}
	if err := install.Relaunch(exe, os.Args, os.Environ()); err != nil {
		log.Printf("Could not start %s: %v. Start Hermod again to run the new version.", exe, err)
		os.Exit(install.ExitCodeRestart)
	}
	os.Exit(0)
}

// runUpdateCommand handles --check-update, --update and --rollback, for
// machines without the UI: remote workers, or a platform that will not start.
// It returns the process exit code.
func runUpdateCommand(o *Options) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	exe := updateEnv().Exe

	if o.rollback {
		if err := install.Rollback(exe, "rolled back from the command line"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("Restored the previous binary. Restart Hermod to run it (hermod --service restart, or systemctl restart hermod).")
		return 0
	}

	// Restart only closes a channel nobody listens to here: the command exits
	// and the operator restarts the service.
	svc := newUpdater(nil, nil, nil)
	st, err := svc.Check(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !st.UpdateAvailable {
		fmt.Printf("Hermod %s is the newest release.\n", st.Current)
		return 0
	}
	fmt.Printf("Hermod %s is available (running %s): %s\n", st.Latest.Tag, st.Current, st.Latest.URL)
	if !o.update {
		return 0
	}
	if !st.CanInstall {
		fmt.Fprintln(os.Stderr, st.InstallBlocked)
		return 1
	}
	if err := svc.Install(ctx, st.Latest.Tag); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	svc.Wait()
	if final := svc.Status(ctx); final.Phase == selfupdate.PhaseFailed {
		fmt.Fprintln(os.Stderr, final.PhaseError)
		return 1
	}
	fmt.Printf("Installed %s. Restart Hermod to run it (hermod --service restart, or systemctl restart hermod); "+
		"if it cannot stay up it is rolled back on its third failed start.\n", st.Latest.Tag)
	return 0
}
