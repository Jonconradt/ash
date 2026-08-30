package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed ash_bootstrap/.ash_env
//go:embed ash_bootstrap/.ash_system
//go:embed ash_bootstrap/.ash_tools
//go:embed ash_bootstrap/.ash_bashrc
//go:embed ash_bootstrap/.ash_fish.fish
//go:embed ash_bootstrap/.ash_zshrc
//go:embed ash_bootstrap/rc-source-bash.sh
//go:embed ash_bootstrap/rc-source-zsh.sh
//go:embed ash_bootstrap/rc-source-fish.fish
//go:embed ash_bootstrap/rc-source-pwsh.ps1
//go:embed ash_bootstrap/route_words.txt
//go:embed ash_bootstrap/prompt-instructions/*
//go:embed ash_bootstrap/tools/*
var embeddedBootstrapAssets embed.FS

func readEmbeddedBootstrapAsset(path string) ([]byte, error) {
	return embeddedBootstrapAssets.ReadFile(path)
}

// installSourceBlockFromAsset reads the shell-specific rc-sourcing snippet at srcPath and
// wraps it with the managed install markers. Keeping the snippet in its own file under
// ash_bootstrap/ makes it easy to find and edit without digging through Go string literals.
func installSourceBlockFromAsset(srcPath string) string {
	body, err := readEmbeddedBootstrapAsset(srcPath)
	if err != nil {
		panic("embedded " + srcPath + " is missing: " + err.Error())
	}
	return strings.TrimSpace(installStartMarker + "\n" + string(body) + installEndMarker)
}

func installEmbeddedBootstrapAssets(overwrite bool, skipPath string, stdout io.Writer) error {
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}

	assetFiles := []struct {
		srcPath string
		dstPath string
		mode    fs.FileMode
	}{
		{srcPath: "ash_bootstrap/.ash_env", dstPath: filepath.Join(root, ".ash_env"), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_system", dstPath: filepath.Join(root, systemFileName), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_tools", dstPath: filepath.Join(root, toolsFileName), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_bashrc", dstPath: filepath.Join(root, ".ash_bashrc"), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_fish.fish", dstPath: filepath.Join(root, ".ash_fish.fish"), mode: 0o600},
		{srcPath: "ash_bootstrap/.ash_zshrc", dstPath: filepath.Join(root, ".ash_zshrc"), mode: 0o600},
	}

	for _, asset := range assetFiles {
		// The active shell's wrapper file was already written fresh by ensureInstallShellWrapper;
		// reprocessing it here would report a misleading "kept existing" for a file ash itself just wrote.
		if skipPath != "" && asset.dstPath == skipPath {
			continue
		}
		content, err := bootstrapAssetContent(asset.srcPath, asset.dstPath, overwrite)
		if err != nil {
			return err
		}
		if err := installManagedAssetFile(asset.dstPath, content, overwrite, asset.mode, stdout, false); err != nil {
			return err
		}
	}

	// Bundled tool scripts must always be allowlisted, even when a pre-existing
	// .ash_tools was kept as-is because the user customized it.
	toolsBaseline, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_tools")
	if err != nil {
		return fmt.Errorf("read embedded .ash_tools baseline: %w", err)
	}
	if err := syncAllowlistAdditions(filepath.Join(root, toolsFileName), toolsBaseline, stdout); err != nil {
		return err
	}

	envContent, err := osReadFile(filepath.Join(root, ".ash_env"))
	if err != nil {
		return fmt.Errorf("read managed ash env: %w", err)
	}
	if err := installManagedAssetFile(filepath.Join(root, ".ash_fish_env.fish"), buildFishEnvironmentFile(string(envContent)), true, 0o600, stdout, false); err != nil {
		return err
	}

	entries, err := fs.ReadDir(embeddedBootstrapAssets, "ash_bootstrap/tools")
	if err != nil {
		return fmt.Errorf("read embedded tools directory: %w", err)
	}
	if err := removeLegacyToolScripts(root, entries, stdout); err != nil {
		return err
	}
	for _, entry := range entries {
		// requirements.txt drives venv provisioning; it is not a runnable tool.
		if entry.IsDir() || entry.Name() == "requirements.txt" {
			continue
		}
		srcPath := filepath.ToSlash(filepath.Join("ash_bootstrap", "tools", entry.Name()))
		dstPath := filepath.Join(root, "tools", entry.Name())
		content, err := readEmbeddedBootstrapAsset(srcPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", srcPath, err)
		}
		if err := installManagedAssetFile(dstPath, content, overwrite, 0o600, stdout, true); err != nil {
			return err
		}
	}

	return nil
}

func bootstrapAssetContent(srcPath, dstPath string, overwrite bool) ([]byte, error) {
	if srcPath != "ash_bootstrap/.ash_env" {
		content, err := readEmbeddedBootstrapAsset(srcPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", srcPath, err)
		}
		return content, nil
	}

	values := map[string]string{}
	if overwrite {
		preserved, err := preservedAshEnvValues(dstPath)
		if err != nil {
			return nil, err
		}
		values = preserved
	}

	return []byte(buildManagedAshEnv(values)), nil
}

func preservedAshEnvValues(path string) (map[string]string, error) {
	content, err := osReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing ash env %s: %w", path, err)
	}

	preservedKeys := map[string]struct{}{
		aiEnvEndpoint:  {},
		aiEnvModel:     {},
		aiEnvAuthType:  {},
		aiEnvAuthToken: {},
		aiEnvProvider:  {},
		aiEnvCache:     {},
	}

	values := map[string]string{}
	for _, line := range strings.Split(string(content), "\n") {
		key, value, ok := parseExportAssignment(line)
		if !ok {
			continue
		}
		if _, keep := preservedKeys[key]; !keep {
			continue
		}
		values[key] = value
	}
	return values, nil
}

func parseExportAssignment(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "export ") {
		return "", "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	parts := strings.SplitN(body, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", false
	}
	value, ok := parseShellExportValue(strings.TrimSpace(parts[1]))
	if !ok {
		return "", "", false
	}
	return key, value, true
}

func parseShellExportValue(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		inner := raw[1 : len(raw)-1]
		return strings.ReplaceAll(inner, `'\''`, `'`), true
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return "", false
		}
		return unquoted, true
	}
	return raw, true
}

func buildFishEnvironmentFile(content string) []byte {
	var b strings.Builder
	b.WriteString("# managed by ash install\n")
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := parseExportAssignment(line)
		if !ok || !fishEnvironmentKeyAllowed(key) {
			continue
		}
		if key == "SESSION_ID" {
			b.WriteString("if not set -q SESSION_ID\n")
			b.WriteString("\tset -gx SESSION_ID (command head -c 100 /dev/urandom | command tr -dc 'a-zA-Z0-9' | command fold -w 16 | command head -n 1)\n")
			b.WriteString("end\n")
			continue
		}
		if key == "PATH" {
			b.WriteString("set -gx PATH \"$HOME/.ash/tools\" \"$HOME/.local/bin\" $PATH\n")
			continue
		}
		b.WriteString("set -gx ")
		b.WriteString(key)
		b.WriteString(" ")
		b.WriteString(fishQuotedValue(value))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func fishEnvironmentKeyAllowed(key string) bool {
	if key == "SESSION_ID" || key == "PATH" {
		return true
	}
	return strings.HasPrefix(key, "AI_") || strings.HasPrefix(key, "ASH_")
}

func fishQuotedValue(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "\\'") + "'"
}

func removeLegacyToolScripts(root string, embeddedEntries []fs.DirEntry, stdout io.Writer) error {
	pyScriptNames := make(map[string]struct{})
	for _, entry := range embeddedEntries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".py") {
			continue
		}
		baseName := strings.TrimSuffix(name, ".py")
		if baseName != "" {
			pyScriptNames[baseName] = struct{}{}
		}
	}

	toolsDir := filepath.Join(root, "tools")
	entries, err := os.ReadDir(toolsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read existing tools directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".py") {
			continue
		}
		if _, ok := pyScriptNames[name]; !ok {
			continue
		}
		legacyPath := filepath.Join(toolsDir, name)
		if err := os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove legacy tool script %s: %w", legacyPath, err)
		}
		if stdout != nil {
			_, _ = fmt.Fprintf(stdout, "removed legacy tool script %s\n", legacyPath)
		}
	}

	return nil
}

// syncAllowlistAdditions appends allowlist entries present in baselineContent but missing from
// the file at dstPath, preserving the rest of the file untouched. It is a no-op if dstPath does
// not exist yet or already contains every baseline entry.
func syncAllowlistAdditions(dstPath string, baselineContent []byte, stdout io.Writer) error {
	existingContent, err := osReadFile(dstPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", dstPath, err)
	}

	existing := parseAllowlistFile(string(existingContent))
	baseline := parseAllowlistFile(string(baselineContent))
	missing := make([]string, 0, len(baseline))
	for name := range baseline {
		if _, ok := existing[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)

	var b strings.Builder
	b.Write(existingContent)
	if len(existingContent) > 0 && existingContent[len(existingContent)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("\n# --- ash-managed: entries added automatically from a newer bundled allowlist ---\n")
	for _, name := range missing {
		b.WriteString(name)
		b.WriteString("\n")
	}
	if err := os.WriteFile(dstPath, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dstPath, err)
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "added new allowlist entries to %s: %s\n", dstPath, strings.Join(missing, ", "))
	}
	return nil
}

func installManagedAssetFile(dstPath string, content []byte, overwrite bool, mode fs.FileMode, stdout io.Writer, isScript bool) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", dstPath, err)
	}

	_, err := os.Stat(dstPath)
	if err == nil {
		if !overwrite {
			if stdout != nil {
				_, _ = fmt.Fprintf(stdout, "kept existing %s\n", dstPath)
			}
			return applyAssetFilePermissions(dstPath, mode, isScript)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dstPath, err)
	}

	if err := os.WriteFile(dstPath, content, mode); err != nil {
		return fmt.Errorf("write %s: %w", dstPath, err)
	}
	return applyAssetFilePermissions(dstPath, mode, isScript)
}

func applyAssetFilePermissions(path string, mode fs.FileMode, isScript bool) error {
	if isScript {
		mode = 0o700
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current user: %w", err)
	}
	uid, err := strconv.Atoi(currentUser.Uid)
	if err != nil {
		return fmt.Errorf("parse uid %q: %w", currentUser.Uid, err)
	}
	gid, err := strconv.Atoi(currentUser.Gid)
	if err != nil {
		return fmt.Errorf("parse gid %q: %w", currentUser.Gid, err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}
