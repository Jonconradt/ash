//go:build linux

package main

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func brokerPeerAllowed(connection net.Conn) bool {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return false
	}
	allowed := false
	_ = raw.Control(func(fd uintptr) {
		credential, credentialErr := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		// #nosec G115 -- os.Getuid() is always non-negative on Linux, so this conversion cannot overflow.
		allowed = credentialErr == nil && uint32(os.Getuid()) == credential.Uid
	})
	return allowed
}
