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
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kardianos/service"
)

// serviceActions are the -service actions. Every one but install only
// names the service, so it works from any directory without other flags.
var serviceActions = []string{"install", "uninstall", "start", "stop", "restart", "status"}

// stopGrace is how long a service stop waits for the agent to flush its
// spool and close the stream before the service manager is told it stopped.
const stopGrace = 20 * time.Second

// serviceConfig describes the agent to the platform's service manager: a
// systemd unit on Linux (workstations, edge servers and the Raspberry Pi), a
// launchd daemon on macOS, a Windows service, and rc scripts on the BSDs.
func serviceConfig(args []string) *service.Config {
	cfg := &service.Config{
		Name:        "hermod-agent",
		DisplayName: "Hermod Agent",
		Description: "Reads serial and LAN devices and streams their data to Hermod.",
		Arguments:   args,
		Option: service.KeyValue{
			// Windows: start at boot once the network is up, and restart
			// after a crash.
			"StartType":              "automatic",
			"DelayedAutoStart":       true,
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "10s",
			"OnFailureResetPeriod":   60,
			// systemd
			"Restart": "always",
			// launchd: start at boot and restart on exit. LogOutput writes
			// stdout and stderr to /var/log/hermod-agent.{out,err}.log;
			// it is macOS only, because under systemd it would take the
			// log out of the journal.
			"RunAtLoad": true,
			"KeepAlive": true,
			"LogOutput": runtime.GOOS == "darwin",
		},
	}
	// Dependencies are unit lines under systemd but service names on Windows
	// and OpenRC, so they are given to systemd alone.
	if sys := service.ChosenSystem(); sys != nil && sys.String() == "linux-systemd" {
		cfg.Dependencies = []string{"Wants=network-online.target", "After=network-online.target"}
	}
	return cfg
}

// serviceArgs are the flags the installed service starts with: the ones this
// install was given, with every path made absolute, because a service starts
// in a directory of the service manager's choosing.
func serviceArgs(f flags) ([]string, error) {
	if f.tokenFile == "" {
		// A service does not inherit the installing shell's environment.
		return nil, errors.New("-service install needs -token-file: a service cannot read $" + tokenEnv + " from this shell")
	}
	abs := func(p string) (string, error) {
		a, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("%s: %w", p, err)
		}
		return a, nil
	}
	token, err := abs(f.tokenFile)
	if err != nil {
		return nil, err
	}
	dataDir, err := abs(f.dataDir)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-server", f.server,
		"-token-file", token,
		"-data-dir", dataDir,
		"-spool-max-mb", strconv.FormatInt(f.spoolMaxMB, 10),
	}
	if f.caFile != "" {
		ca, err := abs(f.caFile)
		if err != nil {
			return nil, err
		}
		args = append(args, "-ca-file", ca)
	}
	if f.plaintext {
		args = append(args, "-plaintext")
	}
	return args, nil
}

// controlService carries out a -service action.
func controlService(f flags, out io.Writer) error {
	var args []string
	if f.service == "install" {
		var err error
		if args, err = serviceArgs(f); err != nil {
			return err
		}
	}
	s, err := service.New(nil, serviceConfig(args))
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}
	if f.service == "status" {
		st, err := s.Status()
		if errors.Is(err, service.ErrNotInstalled) {
			_, err = fmt.Fprintln(out, "hermod-agent service is not installed")
			return err
		}
		if err != nil {
			return fmt.Errorf("service status: %w", err)
		}
		_, err = fmt.Fprintln(out, "hermod-agent service is", statusName(st))
		return err
	}
	if err := service.Control(s, f.service); err != nil {
		return fmt.Errorf("service %s: %w", f.service, err)
	}
	_, err = fmt.Fprintf(out, "hermod-agent service: %s done (%s)\n", f.service, s.Platform())
	return err
}

func statusName(st service.Status) string {
	switch st {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "in an unknown state"
	}
}

// program runs the agent for a service manager. Start must return at once
// and Stop must wait for the agent, so the spool is closed before exit.
type program struct {
	work   func(ctx context.Context, logOut io.Writer) error
	logOut io.Writer
	cancel context.CancelFunc
	done   chan error
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan error, 1)
	go func() { p.done <- p.work(ctx, p.logOut) }()
	return nil
}

func (p *program) Stop(service.Service) error {
	p.cancel()
	select {
	case err := <-p.done:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-time.After(stopGrace):
		return errors.New("agent did not stop within " + stopGrace.String())
	}
}

// runAsService hands the agent to the service manager that started it.
func runAsService(work func(ctx context.Context, logOut io.Writer) error) error {
	p := &program{work: work, logOut: stderrWriter{}}
	cfg := serviceConfig(nil)
	s, err := service.New(p, cfg)
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}
	// A Windows service has no console: stderr goes nowhere, so the log goes
	// to the Windows Event Log instead. systemd and launchd keep stderr.
	if runtime.GOOS == "windows" {
		if l, err := s.Logger(nil); err == nil {
			p.logOut = eventLogWriter{l}
		}
	}
	return s.Run()
}

// eventLogWriter turns slog lines into Windows Event Log entries.
type eventLogWriter struct{ l service.Logger }

func (w eventLogWriter) Write(b []byte) (int, error) {
	line := strings.TrimSpace(string(b))
	var err error
	switch {
	case strings.Contains(line, "level=ERROR"):
		err = w.l.Error(line)
	case strings.Contains(line, "level=WARN"):
		err = w.l.Warning(line)
	default:
		err = w.l.Info(line)
	}
	return len(b), err
}
