//go:build !darwin && !freebsd && !linux

package main

import "io"

func runBroker(args []string, stdout, stderr io.Writer) int {
	_, _ = io.WriteString(stderr, "broker is only supported on macOS, Linux, and FreeBSD\n")
	return 1
}
