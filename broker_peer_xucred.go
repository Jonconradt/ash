//go:build darwin || freebsd

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
		credential, credentialErr := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		uid := os.Getuid()
		allowed = credentialErr == nil && uid >= 0 && uint64(uid) == uint64(credential.Uid)
	})
	return allowed
}
