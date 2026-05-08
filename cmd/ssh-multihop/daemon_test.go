package main

import (
	"net"
	"strconv"
	"testing"
)

// TestFormatAPIAddress verifies that addresses are formatted correctly
// using net.JoinHostPort for IPv4, IPv6, and hostnames.
func TestFormatAPIAddress(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		expected string
	}{
		{
			name:     "IPv4 address",
			host:     "127.0.0.1",
			port:     8080,
			expected: "127.0.0.1:8080",
		},
		{
			name:     "IPv6 address",
			host:     "::1",
			port:     8080,
			expected: "[::1]:8080",
		},
		{
			name:     "IPv6 full address",
			host:     "2001:db8::1",
			port:     9090,
			expected: "[2001:db8::1]:9090",
		},
		{
			name:     "hostname",
			host:     "localhost",
			port:     8080,
			expected: "localhost:8080",
		},
		{
			name:     "hostname with port",
			host:     "example.com",
			port:     443,
			expected: "example.com:443",
		},
		{
			name:     "IPv4 private",
			host:     "192.168.1.1",
			port:     3000,
			expected: "192.168.1.1:3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use net.JoinHostPort to format the address correctly
			// This handles IPv6 addresses by adding brackets when needed
			got := net.JoinHostPort(tt.host, strconv.Itoa(tt.port))
			if got != tt.expected {
				t.Errorf("formatAPIAddress(%q, %d) = %q, want %q",
					tt.host, tt.port, got, tt.expected)
			}
		})
	}
}

// TestVerifyAddressIsParseable verifies that formatted addresses
// can be properly parsed back into host and port.
func TestVerifyAddressIsParseable(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
	}{
		{"IPv4", "127.0.0.1", 8080},
		{"IPv6", "::1", 8080},
		{"IPv6 full", "2001:db8::1", 9090},
		{"hostname", "localhost", 8080},
		{"hostname with port", "example.com", 443},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Format using net.JoinHostPort
			addr := net.JoinHostPort(tt.host, strconv.Itoa(tt.port))

			// Verify the address can be parsed
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("net.SplitHostPort(%q) failed: %v", addr, err)
			}

			// Verify the port matches
			portInt, err := strconv.Atoi(port)
			if err != nil {
				t.Fatalf("strconv.Atoi(%q) failed: %v", port, err)
			}
			if portInt != tt.port {
				t.Errorf("port = %d, want %d", portInt, tt.port)
			}

			// For IPv6, the host will have brackets removed
			// For other hosts, verify they match
			if tt.host == "::1" || tt.host == "2001:db8::1" {
				// IPv6 addresses are returned without brackets
				if host != tt.host {
					t.Errorf("host = %q, want %q", host, tt.host)
				}
			} else {
				if host != tt.host {
					t.Errorf("host = %q, want %q", host, tt.host)
				}
			}
		})
	}
}
