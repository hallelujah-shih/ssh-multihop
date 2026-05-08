package util

import (
	"fmt"
	"net"
	"strconv"
)

// ParseAddress parses address format "[ip]:port" or ":port"
// ":port" is shorthand for "0.0.0.0:port" (listen on all interfaces)
func ParseAddress(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid address format '%s': %w", addr, err)
	}

	// Handle ":port" shorthand
	if host == "" {
		host = "0.0.0.0"
	}

	// Validate IP
	ip := net.ParseIP(host)
	if ip == nil {
		return "", 0, fmt.Errorf("invalid IP address '%s'", host)
	}

	// Validate port
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port '%s': %w", portStr, err)
	}

	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("port out of range: %d", port)
	}

	return host, port, nil
}
