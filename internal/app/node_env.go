// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Jonconradt/ash/internal/localize"
)

const nvmInstallVersion = "v0.40.3"
const nodeInstallScriptURL = "https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh"

var provisionNodeEnv = provisionManagedNodeEnv

func ensureManagedNodeForCalculator(commandName string) {
	if filepath.Base(strings.TrimSpace(commandName)) != "calculator" {
		return
	}
	if _, err := managedNodeExecutable(); err == nil {
		return
	}
	provisionNodeEnv(io.Discard)
}

// managedNodeExecutable finds Node on PATH, in the ASH_NODE override, or in an
// existing NVM installation. It does not depend on any operating-system package
// manager.
func managedNodeExecutable() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ASH_NODE")); override != "" {
		if path, err := execLookPath(override); err == nil {
			return path, nil
		}
	}
	if path, err := execLookPath("node"); err == nil {
		return path, nil
	}

	home, err := osUserHomeDir()
	if err == nil {
		nvmDir := strings.TrimSpace(os.Getenv("NVM_DIR"))
		if nvmDir == "" {
			nvmDir = filepath.Join(home, ".nvm")
		}
		entries, readErr := os.ReadDir(filepath.Join(nvmDir, "versions", "node"))
		if readErr == nil {
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() > entries[j].Name()
			})
			for _, entry := range entries {
				candidate := filepath.Join(nvmDir, "versions", "node", entry.Name(), "bin", "node")
				// #nosec G703 -- candidate stays below the user-owned NVM directory selected by the runtime.
				if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
					return candidate, nil
				}
			}
		}
	}
	return "", errors.New("node.js runtime not found")
}

func managedNodeBinDir(nodePath string) string {
	return filepath.Dir(nodePath)
}

func managedRuntimeEnvironment() []string {
	path := os.Getenv("PATH")
	nodePath, err := managedNodeExecutable()
	if err == nil {
		binDir := managedNodeBinDir(nodePath)
		if binDir != "" {
			path = binDir + string(os.PathListSeparator) + path
		}
	}
	root, err := ashDataDir()
	if err == nil {
		managedBin := filepath.Join(root, "bin")
		path = managedBin + string(os.PathListSeparator) + path
	}
	return withEnvironmentValues(map[string]string{"PATH": path})
}

func linkManagedNodeBin(nodePath string) error {
	root, err := ashDataDir()
	if err != nil {
		return err
	}
	binDir := filepath.Join(root, "bin")
	if err := osMkdirAll(binDir, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"node", "npm", "npx"} {
		sourceName := name
		source := filepath.Join(filepath.Dir(nodePath), sourceName)
		if name == "node" {
			source = nodePath
		}
		if info, statErr := os.Stat(source); statErr != nil || info.IsDir() {
			continue
		}
		target := filepath.Join(binDir, name)
		_ = os.Remove(target)
		if err := os.Symlink(source, target); err != nil {
			return err
		}
	}
	return nil
}

func provisionManagedNodeEnv(stdout io.Writer) {
	if nodePath, err := managedNodeExecutable(); err == nil {
		if err := linkManagedNodeBin(nodePath); err == nil {
			_, _ = fmt.Fprintln(stdout, localize.Format("install.node.ready", []any{nodePath}))
		}
		return
	}

	curlPath, curlErr := execLookPath("curl")
	bashPath, bashErr := execLookPath("bash")
	if curlErr != nil || bashErr != nil {
		_, _ = fmt.Fprintln(stdout, localize.Format("install.node.setup_skipped", []any{"curl and bash are required"}))
		_, _ = fmt.Fprintln(stdout, localize.Text("install.node.install_runtime"))
		return
	}

	home, err := osUserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintln(stdout, localize.Format("install.node.setup_skipped", []any{err}))
		return
	}
	nvmDir := strings.TrimSpace(os.Getenv("NVM_DIR"))
	if nvmDir == "" {
		nvmDir = filepath.Join(home, ".nvm")
	}

	script, err := os.CreateTemp("", "ash-nvm-install-*.sh")
	if err != nil {
		_, _ = fmt.Fprintln(stdout, localize.Format("install.node.setup_skipped", []any{fmt.Sprintf("could not prepare the installer: %v", err)}))
		return
	}
	scriptPath := script.Name()
	_ = script.Close()
	defer func() { _ = os.Remove(scriptPath) }()

	if err := runProvisionCommand(stdout, curlPath, "-fsSL", nodeInstallScriptURL, "-o", scriptPath); err != nil {
		_, _ = fmt.Fprintln(stdout, localize.Format("install.node.download_failed", []any{nvmInstallVersion, err}))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout())
	cmd := execCommandContext(ctx, bashPath, scriptPath)
	cmd.Env = append(os.Environ(), "NVM_DIR="+nvmDir)
	output, err := cmd.CombinedOutput()
	cancel()
	if err != nil {
		detail := fmt.Errorf("installing NVM %s: %v: %s", nvmInstallVersion, err, firstLines(strings.TrimSpace(string(output)), 3))
		_, _ = fmt.Fprintln(stdout, localize.Format("install.node.install_failed", []any{detail}))
		return
	}

	nvmScript := filepath.Join(nvmDir, "nvm.sh")
	command := "source \"$NVM_DIR/nvm.sh\" && nvm install --lts && nvm use --lts && nvm alias default lts/*"
	ctx, cancel = context.WithTimeout(context.Background(), provisionTimeout())
	cmd = execCommandContext(ctx, bashPath, "-c", command)
	cmd.Env = append(os.Environ(), "NVM_DIR="+nvmDir)
	output, err = cmd.CombinedOutput()
	cancel()
	// #nosec G703 -- nvmScript is fixed below the user-owned NVM directory.
	_, nvmScriptErr := os.Stat(nvmScript)
	if err != nil || nvmScriptErr != nil {
		detail := fmt.Errorf("installing the LTS runtime: %v: %s", err, firstLines(strings.TrimSpace(string(output)), 3))
		_, _ = fmt.Fprintln(stdout, localize.Format("install.node.install_failed", []any{detail}))
		return
	}

	if nodePath, err := managedNodeExecutable(); err == nil {
		if err := linkManagedNodeBin(nodePath); err == nil {
			_, _ = fmt.Fprintln(stdout, localize.Format("install.node.ready", []any{nodePath}))
			return
		}
	}
	_, _ = fmt.Fprintln(stdout, localize.Text("install.node.unresolved"))
}
