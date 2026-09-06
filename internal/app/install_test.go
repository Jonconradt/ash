package app

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseInstallArgs(t *testing.T) {
	t.Run("empty args", func(t *testing.T) {
		shellName, dryRun, overwrite, err := parseInstallArgs(nil)
		if err != nil {
			t.Fatalf("parseInstallArgs returned error: %v", err)
		}
		if shellName != "" || dryRun || overwrite {
			t.Fatalf("unexpected parse result: shell=%q dryRun=%v overwrite=%v", shellName, dryRun, overwrite)
		}
	})

	t.Run("shell and dry run", func(t *testing.T) {
		shellName, dryRun, overwrite, err := parseInstallArgs([]string{"--shell", "zsh", "--dry-run", "--overwrite"})
		if err != nil {
			t.Fatalf("parseInstallArgs returned error: %v", err)
		}
		if shellName != "zsh" || !dryRun || !overwrite {
			t.Fatalf("unexpected parse result: shell=%q dryRun=%v overwrite=%v", shellName, dryRun, overwrite)
		}
	})

	t.Run("missing shell value", func(t *testing.T) {
		_, _, _, err := parseInstallArgs([]string{"--shell"})
		if err == nil || !strings.Contains(err.Error(), "--shell requires a value") {
			t.Fatalf("expected missing value error, got %v", err)
		}
	})

	t.Run("unknown arg", func(t *testing.T) {
		_, _, _, err := parseInstallArgs([]string{"--wat"})
		if err == nil || !strings.Contains(err.Error(), "unknown install argument") {
			t.Fatalf("expected unknown argument error, got %v", err)
		}
	})
}

func TestBuildFishEnvironmentFile(t *testing.T) {
	content := strings.Join([]string{
		"export SESSION_ID=`head -c 100 /dev/urandom`",
		`export PATH="$HOME/.ash/tools:$PATH"`,
		"export AI_ENDPOINT='https://example.test'",
		"export ASH_MAX_TOOL_ITERS=16",
		"export UNRELATED=value",
		"",
	}, "\n")

	got := string(buildFishEnvironmentFile(content))
	for _, want := range []string{
		"# managed by ash install",
		"if not set -q SESSION_ID",
		`set -gx PATH "$HOME/.ash/tools" "$HOME/.local/bin" $PATH`,
		"set -gx AI_ENDPOINT 'https://example.test'",
		"set -gx ASH_MAX_TOOL_ITERS '16'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected Fish environment file to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "UNRELATED") {
		t.Fatalf("Fish environment file must not contain unrelated variables: %q", got)
	}
}

func TestDetectShellName(t *testing.T) {
	tests := []struct {
		name      string
		shellPath string
		want      string
	}{
		{name: "bash path", shellPath: "/bin/bash", want: "bash"},
		{name: "zsh path", shellPath: "/usr/bin/zsh", want: "zsh"},
		{name: "fish path", shellPath: "/bin/fish", want: "fish"},
		{name: "ksh unsupported", shellPath: "/bin/ksh", want: ""},
		{name: "unknown", shellPath: "/bin/sh", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectShellName(tt.shellPath); got != tt.want {
				t.Fatalf("detectShellName(%q)=%q want %q", tt.shellPath, got, tt.want)
			}
		})
	}
}

func TestInstallTargetResolutionByOS(t *testing.T) {
	if _, err := resolveInstallShellTarget("bash"); err != nil {
		t.Fatalf("expected bash target, got error: %v", err)
	}
	if _, err := resolveInstallShellTarget("zsh"); err != nil {
		t.Fatalf("expected zsh target, got error: %v", err)
	}
	if _, err := resolveInstallShellTarget("fish"); err != nil {
		t.Fatalf("expected fish target, got error: %v", err)
	}
	if _, err := resolveInstallShellTarget("pwsh"); err == nil {
		t.Fatalf("expected pwsh to be unsupported")
	}
}

func TestDefaultInstallShell(t *testing.T) {
	t.Run("defaults to bash when undetected", func(t *testing.T) {
		got := defaultInstallShell("/bin/sh")
		if got != shellBash {
			t.Fatalf("defaultInstallShell returned %q, want %q", got, shellBash)
		}
	})

	t.Run("keeps detected fish shell", func(t *testing.T) {
		got := defaultInstallShell("/bin/fish")
		if got != shellFish {
			t.Fatalf("defaultInstallShell returned %q, want %q", got, shellFish)
		}
	})
}

func TestInstallRecommendation(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	reco, err := installRecommendation()
	if err != nil {
		t.Fatalf("installRecommendation returned error: %v", err)
	}
	if !strings.Contains(reco, "ash install --shell bash") {
		t.Fatalf("expected recommendation for bash install, got %q", reco)
	}

	rcPath := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rcPath, []byte(installSourceBlockForShell("bash")), 0o600); err != nil {
		t.Fatalf("write rc file: %v", err)
	}

	reco, err = installRecommendation()
	if err != nil {
		t.Fatalf("installRecommendation returned error: %v", err)
	}
	if reco != "" {
		t.Fatalf("expected no recommendation when installed, got %q", reco)
	}

	oldBlock := installSourceBlockForShell("bash")
	oldBlock = strings.Replace(oldBlock, ".ash_bashrc", ".ash_old_bashrc", 1)
	if err := os.WriteFile(rcPath, []byte(oldBlock), 0o600); err != nil {
		t.Fatalf("write outdated rc file: %v", err)
	}

	reco, err = installRecommendation()
	if err != nil {
		t.Fatalf("installRecommendation returned error: %v", err)
	}
	if !strings.Contains(reco, "outdated") || !strings.Contains(reco, "ash install --shell bash") {
		t.Fatalf("expected outdated recommendation, got %q", reco)
	}

	if err := os.Remove(rcPath); err != nil {
		t.Fatalf("remove rc file: %v", err)
	}

	wrapperPath := filepath.Join(home, ashWorkspaceDirName, ".ash_bashrc")
	if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o700); err != nil {
		t.Fatalf("mkdir ash workspace: %v", err)
	}
	if err := os.WriteFile(wrapperPath, []byte("# wrapper\n"), 0o600); err != nil {
		t.Fatalf("write wrapper file: %v", err)
	}

	profilePath := filepath.Join(home, ".bash_profile")
	if err := os.WriteFile(profilePath, []byte(`[ -f "$HOME/.ash/.ash_bashrc" ] && . "$HOME/.ash/.ash_bashrc"`+"\n"), 0o600); err != nil {
		t.Fatalf("write bash profile: %v", err)
	}

	reco, err = installRecommendation()
	if err != nil {
		t.Fatalf("installRecommendation returned error: %v", err)
	}
	if reco != "" {
		t.Fatalf("expected no recommendation when installed via bash_profile sourcing, got %q", reco)
	}

	if err := os.Remove(wrapperPath); err != nil {
		t.Fatalf("remove wrapper file: %v", err)
	}

	reco, err = installRecommendation()
	if err != nil {
		t.Fatalf("installRecommendation returned error: %v", err)
	}
	if !strings.Contains(reco, "ash install --shell bash") {
		t.Fatalf("expected recommendation when bash wrapper file is missing, got %q", reco)
	}
}

func forceNonInteractiveInstallEnv(t *testing.T) {
	t.Helper()
	original := shouldPromptInstallEnv
	shouldPromptInstallEnv = func() bool { return false }
	t.Cleanup(func() { shouldPromptInstallEnv = original })
}

func TestInstallUsesEmbeddedBootstrapAssets(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}

	assetSystem, err := os.ReadFile(filepath.Join(originalCwd, "ash_bootstrap", ".ash_system"))
	if err != nil {
		t.Fatalf("read embedded system asset: %v", err)
	}
	workspaceSystemPath := filepath.Join(home, ashWorkspaceDirName, systemFileName)
	workspaceSystem, err := os.ReadFile(workspaceSystemPath)
	if err != nil {
		t.Fatalf("read workspace system file: %v", err)
	}
	if string(workspaceSystem) != string(assetSystem) {
		t.Fatalf("expected workspace system file from embedded asset, got %q want %q", string(workspaceSystem), string(assetSystem))
	}

	workspaceEnvPath := filepath.Join(home, ashWorkspaceDirName, ".ash_env")
	if _, err := os.Stat(workspaceEnvPath); err != nil {
		t.Fatalf("expected workspace env file to be created: %v", err)
	}

	workspaceToolsPath := filepath.Join(home, ashWorkspaceDirName, "tools", "wikipedia.py")
	if _, err := os.Stat(workspaceToolsPath); err != nil {
		t.Fatalf("expected tool script to be installed: %v", err)
	}
	workspacePluginsPath := filepath.Join(home, ashWorkspaceDirName, "plugins")
	pluginsInfo, err := os.Stat(workspacePluginsPath)
	if err != nil {
		t.Fatalf("expected plugins directory to be created: %v", err)
	}
	if !pluginsInfo.IsDir() {
		t.Fatalf("expected plugins path to be a directory")
	}
}

func TestRunInstallFish(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"install", "--shell", "fish"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected Fish install to succeed, got %d stderr=%q", code, stderr.String())
	}

	rcPath := filepath.Join(configHome, "fish", "config.fish")
	rcContent, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read Fish config: %v", err)
	}
	if !strings.Contains(string(rcContent), ".ash_fish.fish") {
		t.Fatalf("expected Fish config to source wrapper, got %q", rcContent)
	}

	workspace := filepath.Join(home, ashWorkspaceDirName)
	for _, path := range []string{
		filepath.Join(workspace, ".ash_fish.fish"),
		filepath.Join(workspace, ".ash_fish_env.fish"),
	} {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if !strings.Contains(string(content), installStartMarker) && strings.HasSuffix(path, ".ash_fish.fish") {
			t.Fatalf("expected Fish wrapper markers in %s", path)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"install", "--shell", "fish"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "already present") {
		t.Fatalf("expected idempotent Fish install, code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestInstallOverwriteMode(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	workspaceDir := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatalf("mkdir workspace dir: %v", err)
	}
	workspaceEnvPath := filepath.Join(workspaceDir, ".ash_env")
	existingEnv := strings.Join([]string{
		"# managed by ash install",
		"export ASH_OLD=1",
		"export AI_ENDPOINT='https://api.openai.com/v1'",
		"export AI_MODEL='gpt-4.1-mini'",
		"export AI_AUTH_TYPE='bearer'",
		"export AI_AUTH_TOKEN='secret-token'",
		"export AI_PROVIDER='openai'",
		"export AI_CACHE='off'",
		"",
	}, "\n")
	if err := os.WriteFile(workspaceEnvPath, []byte(existingEnv), 0o600); err != nil {
		t.Fatalf("write existing env file: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"install", "--shell", "bash", "--overwrite"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected overwrite install to succeed, got %d stderr=%q", code, stderr.String())
	}

	content, err := os.ReadFile(workspaceEnvPath)
	if err != nil {
		t.Fatalf("read workspace env after overwrite: %v", err)
	}
	if strings.Contains(string(content), "ASH_OLD") {
		t.Fatalf("expected overwrite mode to replace existing env file, got %q", string(content))
	}
	for _, want := range []string{
		managedPathExportLine,
		"export AI_ENDPOINT='https://api.openai.com/v1'",
		"export AI_MODEL='gpt-4.1-mini'",
		"export AI_AUTH_TYPE='bearer'",
		"export AI_AUTH_TOKEN='secret-token'",
		"export AI_PROVIDER='openai'",
		"export AI_CACHE='off'",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("expected overwrite mode to preserve %q, got %q", want, string(content))
		}
	}
	if !strings.Contains(string(content), "export SESSION_ID=") {
		t.Fatalf("expected overwritten env file to keep SESSION_ID, got %q", string(content))
	}
}

func TestInstallRemovesLegacyToolScripts(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	toolsDir := filepath.Join(home, ashWorkspaceDirName, "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}

	legacyPath := filepath.Join(toolsDir, "yfinance")
	if err := os.WriteFile(legacyPath, []byte("#!/usr/bin/env python3\n"), 0o600); err != nil {
		t.Fatalf("write legacy tool script: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"install", "--shell", "bash", "--overwrite"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected overwrite install to succeed, got %d stderr=%q", code, stderr.String())
	}

	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy tool script to be removed, got err=%v", err)
	}

	modernPath := filepath.Join(toolsDir, "yfinance.py")
	if _, err := os.Stat(modernPath); err != nil {
		t.Fatalf("expected modern tool script to exist, err=%v", err)
	}
}

func TestRunInstall(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv(aiEnvEndpoint, "")
	t.Setenv(aiEnvModel, "")
	t.Setenv(aiEnvAuthToken, "")
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("canonical system"), 0o600); err != nil {
		t.Fatalf("write local system file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, allowFileName), []byte("say\n"), 0o600); err != nil {
		t.Fatalf("write local allow file: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}

	rcPath := filepath.Join(home, ".bashrc")
	rcContent, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc file: %v", err)
	}

	content := string(rcContent)
	if !strings.Contains(content, installStartMarker) || !strings.Contains(content, installEndMarker) {
		t.Fatalf("expected install block markers in rc file, got %q", content)
	}

	if !strings.Contains(stdout.String(), "AI provider not configured automatically") {
		t.Fatalf("expected non-interactive configuration guidance, got %q", stdout.String())
	}

	wrapperPathAfterFreshInstall := filepath.Join(home, ashWorkspaceDirName, ".ash_bashrc")
	if strings.Contains(stdout.String(), "kept existing "+wrapperPathAfterFreshInstall) {
		t.Fatalf("fresh install should not report the active shell wrapper as pre-existing, got %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected second install to succeed, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already present") {
		t.Fatalf("expected idempotent install message, got %q", stdout.String())
	}

	rcContentAfter, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc file after second install: %v", err)
	}
	if strings.Count(string(rcContentAfter), installStartMarker) != 1 {
		t.Fatalf("expected single install block, got %d", strings.Count(string(rcContentAfter), installStartMarker))
	}

	profilePath := filepath.Join(home, ".bash_profile")
	profileContent, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read bash_profile: %v", err)
	}
	if !strings.Contains(string(profileContent), ".bashrc") {
		t.Fatalf("expected bash_profile to source .bashrc, got %q", string(profileContent))
	}

	canonicalSystemPath := filepath.Join(home, ashWorkspaceDirName, systemFileName)
	canonicalSystemContent, err := os.ReadFile(canonicalSystemPath)
	if err != nil {
		t.Fatalf("read canonical system file: %v", err)
	}
	if string(canonicalSystemContent) != "canonical system" {
		t.Fatalf("canonical system mismatch: got %q", string(canonicalSystemContent))
	}

	canonicalAllowPath := filepath.Join(home, ashWorkspaceDirName, allowFileName)
	canonicalAllowContent, err := os.ReadFile(canonicalAllowPath)
	if err != nil {
		t.Fatalf("read canonical allow file: %v", err)
	}
	if !strings.Contains(string(canonicalAllowContent), "say") {
		t.Fatalf("expected canonical allow content to include say, got %q", string(canonicalAllowContent))
	}

	staleBlock := installSourceBlockForShell("bash")
	staleBlock = strings.Replace(staleBlock, ".ash_bashrc", ".ash_old_bashrc", 1)
	if err := os.WriteFile(rcPath, []byte(staleBlock), 0o600); err != nil {
		t.Fatalf("write stale rc file: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected update install to succeed, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated wrappers") {
		t.Fatalf("expected update install message, got %q", stdout.String())
	}

	rcContentUpdated, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("read rc file after update: %v", err)
	}
	if !strings.Contains(string(rcContentUpdated), ".ash/.ash_bashrc") {
		t.Fatalf("expected refreshed install block to source .ash/.ash_bashrc")
	}

	wrapperPath := filepath.Join(home, ashWorkspaceDirName, ".ash_bashrc")
	wrapperContent, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read bash wrapper file: %v", err)
	}
	if !strings.Contains(string(wrapperContent), `[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"`) {
		t.Fatalf("expected wrapper file to source .ash/.ash_env")
	}
	if !strings.Contains(string(wrapperContent), "command_not_found_handle") {
		t.Fatalf("expected wrapper file to include command_not_found_handle")
	}
}

func TestRunInstallMigratesLegacyBashProfileSourcing(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	profilePath := filepath.Join(home, ".bash_profile")
	if err := os.WriteFile(profilePath, []byte(`[ -f "$HOME/.ash/.ash_bashrc" ] && . "$HOME/.ash/.ash_bashrc"`+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy bash_profile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected install to succeed, got %d stderr=%q", code, stderr.String())
	}

	profileContent, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read bash_profile: %v", err)
	}
	if strings.Contains(string(profileContent), ".ash/.ash_bashrc") {
		t.Fatalf("expected legacy bash_profile source to be removed, got %q", string(profileContent))
	}
	if !strings.Contains(string(profileContent), ".bashrc") {
		t.Fatalf("expected bash_profile to source .bashrc, got %q", string(profileContent))
	}
}

func TestRunInstallCleansLegacyAshSourcingWhenBashRCAlreadyPresent(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")

	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	profilePath := filepath.Join(home, ".bash_profile")
	profileContent := strings.Join([]string{
		`alias pip='python -m pip'`,
		`[ -f ~/.bashrc ] && source ~/.bashrc`,
		`[ -f ~/.ash/.ash_env ] && source ~/.ash/.ash_env`,
		`[ -f "$HOME/.ash/.ash_bashrc" ] && . "$HOME/.ash/.ash_bashrc"`,
		"",
	}, "\n")
	if err := os.WriteFile(profilePath, []byte(profileContent), 0o600); err != nil {
		t.Fatalf("write mixed bash_profile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected install to succeed, got %d stderr=%q", code, stderr.String())
	}

	updatedProfileContent, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read bash_profile: %v", err)
	}
	updated := string(updatedProfileContent)
	if strings.Contains(updated, ".ash/.ash_env") || strings.Contains(updated, ".ash/.ash_bashrc") {
		t.Fatalf("expected direct ash sourcing to be removed from bash_profile, got %q", updated)
	}
	if strings.Count(updated, `[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"`) != 1 {
		t.Fatalf("expected a single bashrc source line in bash_profile, got %q", updated)
	}
}

func TestRunInstallDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"install", "--shell", "zsh", "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected dry-run exit code 0, got %d stderr=%q", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "[dry-run]") {
		t.Fatalf("expected dry-run output, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "[EID=") {
		t.Fatalf("expected dry-run output without EIDs, got %q", stdout.String())
	}

	rcPath := filepath.Join(home, ".zshrc")
	if _, err := os.Stat(rcPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no rc file write in dry-run, stat err=%v", err)
	}
}

func TestShouldConfigureInstallEnv(t *testing.T) {
	t.Run("configures when ash env file missing and required env missing", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(aiEnvEndpoint, "")
		t.Setenv(aiEnvModel, "")
		t.Setenv(aiEnvAuthType, "")
		t.Setenv(aiEnvAuthToken, "")

		got, err := shouldConfigureInstallEnv()
		if err != nil {
			t.Fatalf("shouldConfigureInstallEnv returned error: %v", err)
		}
		if !got {
			t.Fatalf("expected shouldConfigureInstallEnv=true when .ash_env is missing and required env is absent")
		}
	})

	t.Run("skips when ash env file exists", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(aiEnvEndpoint, "")
		t.Setenv(aiEnvModel, "")
		t.Setenv(aiEnvAuthType, "")
		t.Setenv(aiEnvAuthToken, "")

		ashPath := filepath.Join(home, ashWorkspaceDirName, ".ash_env")
		if err := os.MkdirAll(filepath.Dir(ashPath), 0o700); err != nil {
			t.Fatalf("mkdir ash dir: %v", err)
		}
		if err := os.WriteFile(ashPath, []byte("export AI_ENDPOINT='http://localhost:11434'\n"), 0o600); err != nil {
			t.Fatalf("write ash env file: %v", err)
		}

		got, err := shouldConfigureInstallEnv()
		if err != nil {
			t.Fatalf("shouldConfigureInstallEnv returned error: %v", err)
		}
		if got {
			t.Fatalf("expected shouldConfigureInstallEnv=false when .ash_env exists")
		}
	})

	t.Run("skips when required local env values already set", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(aiEnvEndpoint, "http://localhost:11434")
		t.Setenv(aiEnvModel, "llama3.1")
		t.Setenv(aiEnvAuthType, "")
		t.Setenv(aiEnvAuthToken, "")

		got, err := shouldConfigureInstallEnv()
		if err != nil {
			t.Fatalf("shouldConfigureInstallEnv returned error: %v", err)
		}
		if got {
			t.Fatalf("expected shouldConfigureInstallEnv=false when required local env is set")
		}
	})

	t.Run("skips when required cloud env values already set", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(aiEnvEndpoint, "https://api.openai.com/v1")
		t.Setenv(aiEnvModel, "gpt-4.1")
		t.Setenv(aiEnvAuthType, "bearer")
		t.Setenv(aiEnvAuthToken, "token")

		got, err := shouldConfigureInstallEnv()
		if err != nil {
			t.Fatalf("shouldConfigureInstallEnv returned error: %v", err)
		}
		if got {
			t.Fatalf("expected shouldConfigureInstallEnv=false when required cloud env is set")
		}
	})

	t.Run("configures when cloud env is incomplete", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(aiEnvEndpoint, "https://api.openai.com/v1")
		t.Setenv(aiEnvModel, "gpt-4.1")
		t.Setenv(aiEnvAuthType, "bearer")
		t.Setenv(aiEnvAuthToken, "")

		got, err := shouldConfigureInstallEnv()
		if err != nil {
			t.Fatalf("shouldConfigureInstallEnv returned error: %v", err)
		}
		if !got {
			t.Fatalf("expected shouldConfigureInstallEnv=true when cloud auth vars are incomplete")
		}
	})
}

func TestPromptEndpointWithPresets(t *testing.T) {
	t.Run("accepts numeric preset selection", func(t *testing.T) {
		reader := bufio.NewReader(strings.NewReader("2\n"))
		var stdout bytes.Buffer

		got, err := promptEndpointWithPresets(reader, &stdout)
		if err != nil {
			t.Fatalf("promptEndpointWithPresets returned error: %v", err)
		}
		if got != installEndpointPresets[1].URL {
			t.Fatalf("promptEndpointWithPresets returned %q, want %q", got, installEndpointPresets[1].URL)
		}
	})

	t.Run("accepts and normalizes custom URL", func(t *testing.T) {
		reader := bufio.NewReader(strings.NewReader("https://example.com/custom/\n"))
		var stdout bytes.Buffer

		got, err := promptEndpointWithPresets(reader, &stdout)
		if err != nil {
			t.Fatalf("promptEndpointWithPresets returned error: %v", err)
		}
		if got != "https://example.com/custom" {
			t.Fatalf("promptEndpointWithPresets returned %q, want %q", got, "https://example.com/custom")
		}
	})

	t.Run("Other preset prompts for a custom URL", func(t *testing.T) {
		otherIdx := len(installEndpointPresets)
		reader := bufio.NewReader(strings.NewReader(strconv.Itoa(otherIdx) + "\nhttps://example.com/other/\n"))
		var stdout bytes.Buffer

		got, err := promptEndpointWithPresets(reader, &stdout)
		if err != nil {
			t.Fatalf("promptEndpointWithPresets returned error: %v", err)
		}
		if got != "https://example.com/other" {
			t.Fatalf("promptEndpointWithPresets returned %q, want %q", got, "https://example.com/other")
		}
	})
}

func TestRunInstallHardensWorkspacePermissions(t *testing.T) {
	forceNonInteractiveInstallEnv(t)
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalCwd)
	})

	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	ashRoot := filepath.Join(home, ashWorkspaceDirName)
	if err := os.MkdirAll(filepath.Join(ashRoot, "nested"), 0o777); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.Chmod(ashRoot, 0o777); err != nil {
		t.Fatalf("chmod ash root: %v", err)
	}
	if err := os.Chmod(filepath.Join(ashRoot, "nested"), 0o777); err != nil {
		t.Fatalf("chmod nested dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ashRoot, "nested", "loose.txt"), []byte("secret"), 0o666); err != nil {
		t.Fatalf("write loose file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cwd, systemFileName), []byte("system"), 0o600); err != nil {
		t.Fatalf("write cwd system: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, allowFileName), []byte("say\n"), 0o600); err != nil {
		t.Fatalf("write cwd allow: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"install", "--shell", "bash"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected install success, got %d stderr=%q", code, stderr.String())
	}

	rootInfo, err := os.Stat(ashRoot)
	if err != nil {
		t.Fatalf("stat ash root: %v", err)
	}
	if got := rootInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("ash root permissions mismatch: got %o want %o", got, 0o700)
	}

	nestedInfo, err := os.Stat(filepath.Join(ashRoot, "nested"))
	if err != nil {
		t.Fatalf("stat nested dir: %v", err)
	}
	if got := nestedInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("nested dir permissions mismatch: got %o want %o", got, 0o700)
	}

	looseInfo, err := os.Stat(filepath.Join(ashRoot, "nested", "loose.txt"))
	if err != nil {
		t.Fatalf("stat loose file: %v", err)
	}
	if got := looseInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("loose file permissions mismatch: got %o want %o", got, 0o600)
	}

	canonicalSystemInfo, err := os.Stat(filepath.Join(ashRoot, systemFileName))
	if err != nil {
		t.Fatalf("stat canonical system file: %v", err)
	}
	if got := canonicalSystemInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("canonical system permissions mismatch: got %o want %o", got, 0o600)
	}

	canonicalAllowInfo, err := os.Stat(filepath.Join(ashRoot, allowFileName))
	if err != nil {
		t.Fatalf("stat canonical allow file: %v", err)
	}
	if got := canonicalAllowInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("canonical allow permissions mismatch: got %o want %o", got, 0o600)
	}
}
