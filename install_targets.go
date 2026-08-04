package main

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
)

const (
	shellBash = "bash"
	shellZsh  = "zsh"
	shellPwsh = "pwsh"
)

type installShellTarget struct {
	Name           string
	SupportedOnOS  func(goos string) bool
	RCPath         func(home string) string
	WrapperFile    string
	SourceBlock    func() string
	WrapperContent func() string
	PostInstall    func(dryRun bool, stdout io.Writer) error
}

var installShellTargets = map[string]installShellTarget{
	shellBash: {
		Name:           shellBash,
		SupportedOnOS:  func(goos string) bool { return goos != "windows" },
		RCPath:         bashRCPath,
		WrapperFile:    bashWrapperFileName(),
		SourceBlock:    bashInstallSourceBlock,
		WrapperContent: bashInstallWrapperContent,
		PostInstall:    ensureBashProfileSourcingForInstall,
	},
	shellZsh: {
		Name:           shellZsh,
		SupportedOnOS:  func(goos string) bool { return goos != "windows" },
		RCPath:         zshRCPath,
		WrapperFile:    zshWrapperFileName(),
		SourceBlock:    zshInstallSourceBlock,
		WrapperContent: zshInstallWrapperContent,
		PostInstall:    nil,
	},
	shellPwsh: {
		Name:           shellPwsh,
		SupportedOnOS:  func(goos string) bool { return goos == "windows" },
		RCPath:         pwshProfilePath,
		WrapperFile:    pwshWrapperFileName(),
		SourceBlock:    pwshInstallSourceBlock,
		WrapperContent: pwshInstallWrapperContent,
		PostInstall:    nil,
	},
}

var currentGOOS = runtime.GOOS

func activeGOOS() string {
	return currentGOOS
}

func supportedShellsForOS(goos string) []string {
	names := make([]string, 0, len(installShellTargets))
	for name, target := range installShellTargets {
		if target.SupportedOnOS(goos) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func normalizeShellName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	switch n {
	case "powershell":
		return shellPwsh
	default:
		return n
	}
}

func defaultInstallShell(shellPath, goos string) string {
	detected := detectShellName(shellPath)
	if detected != "" {
		if target, ok := installShellTargets[detected]; ok && target.SupportedOnOS(goos) {
			return detected
		}
	}
	if goos == "windows" {
		return shellPwsh
	}
	return shellBash
}

func resolveInstallShellTarget(shellName, goos string) (installShellTarget, error) {
	normalized := normalizeShellName(shellName)
	target, ok := installShellTargets[normalized]
	if !ok || !target.SupportedOnOS(goos) {
		supported := strings.Join(supportedShellsForOS(goos), ", ")
		return installShellTarget{}, fmt.Errorf("unsupported shell %q (supported on %s: %s)", shellName, goos, supported)
	}
	return target, nil
}
