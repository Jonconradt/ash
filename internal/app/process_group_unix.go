// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package app

import (
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
