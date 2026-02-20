package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kardianos/service"
)

// Config represents the service configuration.
type Config struct {
	Name             string
	DisplayName      string
	Description      string
	UserName         string
	Arguments        []string
	WorkingDirectory string
}

// program implements service.Interface.
type program struct {
	exit    chan struct{}
	runFunc func(ctx context.Context)
	ctx     context.Context
	cancel  context.CancelFunc
}

func (p *program) Start(s service.Service) error {
	// Start should not block. Do the actual work in a goroutine.
	go p.runFunc(p.ctx)
	return nil
}

func (p *program) Stop(s service.Service) error {
	// Stop should be graceful.
	p.cancel()
	close(p.exit)
	return nil
}

// Manage handles service installation, uninstallation, starting, and stopping.
func Manage(cfg Config, action string, runFunc func(ctx context.Context)) error {
	svcConfig := &service.Config{
		Name:        cfg.Name,
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		UserName:    cfg.UserName,
		Arguments:   cfg.Arguments,
	}
	// Sensible service defaults for production
	svcConfig.Option = service.KeyValue{
		// Windows
		"StartType":        "automatic",
		"DelayedAutoStart": true,
		"OnFailure":        "restart",
		"ResetPeriod":      60,
		// systemd (Linux)
		"Restart":    "always",
		"RestartSec": 10,
		// launchd (macOS)
		"RunAtLoad": true,
		"KeepAlive": true,
	}

	if cfg.WorkingDirectory != "" {
		svcConfig.WorkingDirectory = cfg.WorkingDirectory
	} else {
		// Default to the directory where the executable is located
		if execPath, err := os.Executable(); err == nil {
			svcConfig.WorkingDirectory = filepath.Dir(execPath)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &program{
		exit:    make(chan struct{}),
		runFunc: runFunc,
		ctx:     ctx,
		cancel:  cancel,
	}

	s, err := service.New(p, svcConfig)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	if action != "" && action != "run" {
		if action == "status" {
			status, err := s.Status()
			if err != nil {
				return fmt.Errorf("failed to get service status: %w", err)
			}
			statusStr := "unknown"
			switch status {
			case service.StatusRunning:
				statusStr = "running"
			case service.StatusStopped:
				statusStr = "stopped"
			}
			fmt.Printf("Service %q is %s.\n", cfg.Name, statusStr)
			return nil
		}
		err = service.Control(s, action)
		if err != nil {
			return fmt.Errorf("service control %q failed: %w", action, err)
		}
		fmt.Printf("Service %q %sed successfully.\n", cfg.Name, action)
		return nil
	}

	// If no action or "run", run the service.
	// This will block until the service is stopped.
	err = s.Run()
	if err != nil {
		return fmt.Errorf("service failed to run: %w", err)
	}
	return nil
}

// GetDefaultLogger returns a logger that works with the service manager.
func GetDefaultLogger(s service.Service) (service.Logger, error) {
	return s.Logger(nil)
}
