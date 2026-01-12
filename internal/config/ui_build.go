package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// BuildUI builds the React UI and copies the assets to the internal/api/static directory.
func BuildUI() error {
	fmt.Println("Building UI...")

	// 1. Get the project root
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}

	uiDir := filepath.Join(cwd, "ui")
	staticDir := filepath.Join(cwd, "internal", "api", "static")

	// 2. npm install in ui directory
	if err := runCommand(uiDir, "npm", "install"); err != nil {
		return fmt.Errorf("npm install failed: %w", err)
	}

	// 3. npm run build in ui directory
	if err := runCommand(uiDir, "npm", "run", "build"); err != nil {
		return fmt.Errorf("npm run build failed: %w", err)
	}

	// 4. Copy ui/dist/* to internal/api/static/
	distDir := filepath.Join(uiDir, "dist")

	// Create static directory if it doesn't exist
	if err := os.MkdirAll(staticDir, 0755); err != nil {
		return fmt.Errorf("failed to create static directory: %w", err)
	}

	// Clean static directory
	files, err := os.ReadDir(staticDir)
	if err == nil {
		for _, f := range files {
			os.RemoveAll(filepath.Join(staticDir, f.Name()))
		}
	}

	return copyDir(distDir, staticDir)
}

// IsUIBuilt checks if the UI assets have been built and copied to the static directory.
func IsUIBuilt() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	index := filepath.Join(cwd, "internal", "api", "static", "index.html")
	_, err = os.Stat(index)
	return err == nil
}

func runCommand(dir string, name string, args ...string) error {
	cmdName := name
	if runtime.GOOS == "windows" {
		// On Windows, npm is a cmd/ps1 script
		cmdName = "npm.cmd"
	}

	cmd := exec.Command(cmdName, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, data, info.Mode())
	})
}
