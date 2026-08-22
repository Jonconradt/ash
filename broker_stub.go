//go:build !darwin && !linux

package main

import "io"

func runBroker(args []string, stdout, stderr io.Writer) int {
	_, _ = io.WriteString(stderr, "broker is only supported on macOS and Linux\n")
	return 1
}
