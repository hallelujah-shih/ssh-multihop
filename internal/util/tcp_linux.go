//go:build linux

package util

import (
	"fmt"
	"net"
	"syscall"
)

// SetTCPKeepalive configures TCP keepalive using SyscallConn
// This uses Linux-specific syscall constants and does not interfere with Go's netpoller
//
// Platform: Linux only
// TODO: Add macOS/Windows support using golang.org/x/sys/unix
func SetTCPKeepalive(conn net.Conn) error {
	syscallConn, ok := conn.(syscall.Conn)
	if !ok {
		return fmt.Errorf("connection does not implement syscall.Conn")
	}

	rawConn, err := syscallConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("failed to get raw syscall conn: %w", err)
	}

	var setErr error
	controlErr := rawConn.Control(func(fd uintptr) {
		// Enable keepalive
		setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
		if setErr != nil {
			return
		}

		// Set keepalive parameters (Linux-specific)
		// TCP_KEEPIDLE: 5 seconds idle before first probe (reduced from 15s for faster failure detection)
		setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, 5)
		if setErr != nil {
			return
		}

		// TCP_KEEPINTVL: 2 seconds between probes (reduced from 5s)
		setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, 2)
		if setErr != nil {
			return
		}

		// TCP_KEEPCNT: 2 probes before giving up (reduced from 3)
		// Total detection time: 5s + 2 * 2s = 9 seconds (reduced from 30s)
		// This allows faster detection of remote host reboots
		setErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT, 2)
	})

	if controlErr != nil {
		return controlErr
	}
	return setErr
}
