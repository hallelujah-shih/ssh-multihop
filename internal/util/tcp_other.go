//go:build !linux
// +build !linux

package util

import (
	"errors"
	"net"
)

// SetTCPKeepalive is not supported on non-Linux platforms
//
// Platform: Linux-only implementation
// TODO: Add macOS/Windows support using golang.org/x/sys/unix
func SetTCPKeepalive(conn net.Conn) error {
	return errors.New("SetTCPKeepalive not supported on this platform (Linux only)")
}
