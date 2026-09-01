package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	shellBash = "bash"
	shellFish = "fish"
	shellZsh  = "zsh"
)

type installShellTarget struct {
	Name           string
	RCPath         func(home string) string
	WrapperFile    string
	SourceBlock    func() string
	WrapperContent func() string
	PostInstall    func(dryRun bool, stdout io.Writer) error
}

var installShellTargets = map[string]installShellTarget{
	shellBash: {
		Name:           shellBash,
		RCPath:         bashRCPath,
		WrapperFile:    bashWrapperFileName(),
		SourceBlock:    bashInstallSourceBlock,
		WrapperContent: bashInstallWrapperContent,
		PostInstall:    ensureBashProfileSourcingForInstall,
	},
	shellFish: {
		Name:           shellFish,
		RCPath:         fishRCPath,
		WrapperFile:    fishWrapperFileName(),
		SourceBlock:    fishInstallSourceBlock,
		WrapperContent: fishInstallWrapperContent,
		PostInstall:    nil,
	},
	shellZsh: {
		Name:           shellZsh,
		RCPath:         zshRCPath,
		WrapperFile:    zshWrapperFileName(),
		SourceBlock:    zshInstallSourceBlock,
		WrapperContent: zshInstallWrapperContent,
		PostInstall:    nil,
	},
}

func supportedShells() []string {
	names := make([]string, 0, len(installShellTargets))
	for name := range installShellTargets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeShellName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func defaultInstallShell(shellPath string) string {
	detected := detectShellName(shellPath)
	if detected != "" {
		if _, ok := installShellTargets[detected]; ok {
			return detected
		}
	}
	return shellBash
}

func resolveInstallShellTarget(shellName string) (installShellTarget, error) {
	normalized := normalizeShellName(shellName)
	target, ok := installShellTargets[normalized]
	if !ok {
		supported := strings.Join(supportedShells(), ", ")
		return installShellTarget{}, fmt.Errorf("unsupported shell %q (supported: %s)", shellName, supported)
	}
	return target, nil
}
