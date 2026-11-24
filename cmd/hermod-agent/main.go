// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Command hermod-agent runs next to hardware. It finds serial and LAN
// devices, reads the ones claimed in Hermod, keeps every reading in a disk
// spool, and streams the spool to a Hermod server, which acknowledges each
// reading once a workflow has delivered it.
//
// It is deliberately separate from the Hermod server: it imports none of the
// engine, so it stays small enough for a Raspberry Pi. guard_test.go holds it
// to that.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/kardianos/service"
	"google.golang.org/grpc/credentials"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/agent"
	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/spool"
	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/uplink"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// tokenEnv carries the agent token. A flag would show it in the process list.
const tokenEnv = "HERMOD_AGENT_TOKEN" //nolint:gosec // G101: the name of an environment variable, not a credential

type flags struct {
	server, tokenFile, caFile, dataDir string
	plaintext, showVersion             bool
	spoolMaxMB                         int64
	// service is a -service action: install, uninstall, start, stop,
	// restart or status. Empty runs the agent.
	service string
}

func parseFlags(args []string, out io.Writer) (flags, error) {
	var f flags
	fs := flag.NewFlagSet("hermod-agent", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&f.server, "server", "", "Hermod agent address, host:port (required)")
	fs.StringVar(&f.tokenFile, "token-file", "", "file holding the agent token; otherwise $"+tokenEnv)
	fs.StringVar(&f.caFile, "ca-file", "", "extra CA certificate (PEM) to trust for the server")
	fs.BoolVar(&f.plaintext, "plaintext", false, "connect without TLS; only allowed to localhost")
	fs.StringVar(&f.dataDir, "data-dir", defaultDataDir(runtime.GOOS, os.Getenv), "where the spool and cached settings live")
	fs.Int64Var(&f.spoolMaxMB, "spool-max-mb", 1024, "largest the spool may grow on disk, in MiB")
	fs.BoolVar(&f.showVersion, "version", false, "print the version and exit")
	fs.StringVar(&f.service, "service", "", "manage the agent as a system service: "+strings.Join(serviceActions, ", "))
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.service != "" && !slices.Contains(serviceActions, f.service) {
		return f, fmt.Errorf("-service %q: want one of %s", f.service, strings.Join(serviceActions, ", "))
	}
	// Controlling an installed service needs nothing but its name; installing
	// one and running the agent both need to know where Hermod is.
	needsServer := !f.showVersion && (f.service == "" || f.service == "install")
	if needsServer && f.server == "" {
		return f, errors.New("-server is required")
	}
	if f.spoolMaxMB < 1 {
		return f, errors.New("-spool-max-mb must be at least 1")
	}
	return f, nil
}

// defaultDataDir is where each platform keeps a system service's state:
// %ProgramData% on Windows, /Library/Application Support on macOS, and
// /var/lib everywhere else (Linux workstations, edge servers, the BSDs).
func defaultDataDir(goos string, getenv func(string) string) string {
	switch goos {
	case "windows":
		pd := getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "hermod-agent")
	case "darwin":
		return "/Library/Application Support/hermod-agent"
	default:
		return "/var/lib/hermod-agent"
	}
}

func readToken(file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file) //nolint:gosec // G304: the operator's own -token-file
		if err != nil {
			return "", err
		}
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
		return "", errors.New(file + " is empty")
	}
	if t := strings.TrimSpace(os.Getenv(tokenEnv)); t != "" {
		return t, nil
	}
	return "", errors.New("no agent token: set " + tokenEnv + " or pass -token-file")
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "hermod-agent:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	f, err := parseFlags(args, out)
	if err != nil {
		return err
	}
	if f.showVersion {
		_, err := fmt.Fprintln(out, "hermod-agent", version)
		return err
	}
	if f.service != "" {
		return controlService(f, out)
	}
	token, err := readToken(f.tokenFile)
	if err != nil {
		return err
	}
	creds, err := uplink.TransportCredentials(f.server, f.caFile, f.plaintext)
	if err != nil {
		return err
	}
	work := func(ctx context.Context, logOut io.Writer) error {
		log := slog.New(slog.NewTextHandler(logOut, nil))
		log.Info("hermod-agent starting", "version", version, "server", f.server, "data_dir", f.dataDir,
			"os", runtime.GOOS, "arch", runtime.GOARCH)
		return runAgent(ctx, f, token, creds, log)
	}
	// Started by systemd, launchd or the Windows Service Control Manager: let
	// the service manager drive the start and the stop.
	if !service.Interactive() {
		return runAsService(work)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return work(ctx, os.Stderr)
}

func runAgent(ctx context.Context, f flags, token string, creds credentials.TransportCredentials, log *slog.Logger) error {
	return agent.Run(ctx, agent.Options{
		DataDir: f.dataDir,
		Server:  f.server,
		Token:   token,
		Creds:   creds,
		Version: version,
		Spool:   spool.Options{MaxBytes: f.spoolMaxMB << 20},
		Log:     log,
	})
}

// stderrWriter writes to whatever os.Stderr is when the line is written.
type stderrWriter struct{}

func (stderrWriter) Write(b []byte) (int, error) { return os.Stderr.Write(b) }
