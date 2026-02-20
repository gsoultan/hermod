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

	// 2. Ensure Bun is installed and use it
	if err := ensureBun(); err != nil {
		return fmt.Errorf("bun preparation failed: %w", err)
	}

	// 3. bun install in ui directory
	fmt.Println("Running 'bun install' in ui directory...")
	installArgs := []string{"install"}
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		installArgs = append(installArgs, "--frozen-lockfile")
	}
	if err := runCommand(uiDir, "bun", installArgs...); err != nil {
		return fmt.Errorf("bun install failed: %w", err)
	}

	// 4. bun run build in ui directory
	fmt.Println("Running 'bun run build' in ui directory...")
	if err := runCommand(uiDir, "bun", "run", "build"); err != nil {
		return fmt.Errorf("bun run build failed: %w", err)
	}

	// 5. Copy ui/dist/* to internal/api/static/
	distDir := filepath.Join(uiDir, "dist")
	if _, err := os.Stat(distDir); os.IsNotExist(err) {
		return fmt.Errorf("build succeeded but dist directory not found at %s", distDir)
	}
	fmt.Printf("Copying assets from %s to %s...\n", distDir, staticDir)

	// Create static directory if it doesn't exist
	if err := os.MkdirAll(staticDir, 0755); err != nil {
		return fmt.Errorf("failed to create static directory: %w", err)
	}

	// Clean static directory
	files, err := os.ReadDir(staticDir)
	if err == nil {
		for _, f := range files {
			if f.Name() == ".gitkeep" {
				continue
			}
			os.RemoveAll(filepath.Join(staticDir, f.Name()))
		}
	}

	if err := copyDir(distDir, staticDir); err != nil {
		return fmt.Errorf("failed to copy assets: %w", err)
	}

	fmt.Println("UI built successfully.")
	return nil
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
	if name == "npm" && runtime.GOOS == "windows" {
		cmdName = "npm.cmd"
	} else if name == "bun" {
		// Check if bun is in PATH
		if _, err := exec.LookPath("bun"); err != nil {
			// Try common install location
			home, _ := os.UserHomeDir()
			var bunPath string
			if runtime.GOOS == "windows" {
				bunPath = filepath.Join(home, ".bun", "bin", "bun.exe")
			} else {
				bunPath = filepath.Join(home, ".bun", "bin", "bun")
			}
			if _, err := os.Stat(bunPath); err == nil {
				cmdName = bunPath
			}
		}
	}

	cmd := exec.Command(cmdName, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("Executing in %s: %s %v\n", dir, cmdName, args)
	return cmd.Run()
}

func ensureBun() error {
	_, err := exec.LookPath("bun")
	if err == nil {
		return nil
	}

	// Try common install location before installing
	home, _ := os.UserHomeDir()
	var bunPath string
	if runtime.GOOS == "windows" {
		bunPath = filepath.Join(home, ".bun", "bin", "bun.exe")
	} else {
		bunPath = filepath.Join(home, ".bun", "bin", "bun")
	}
	if _, err := os.Stat(bunPath); err == nil {
		return nil
	}

	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		return fmt.Errorf("bun not found in PATH in CI environment. Ensure setup-bun step has run and is successful")
	}

	fmt.Println("Bun not found. Installing Bun...")
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", "irm https://bun.sh/install.ps1 | iex")
	} else {
		cmd = exec.Command("sh", "-c", "curl -fsSL https://bun.sh/install | bash")
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install Bun: %w", err)
	}

	return nil
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
