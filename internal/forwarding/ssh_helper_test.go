package forwarding

import (
	"strings"
	"testing"

	"github.com/hallelujah-shih/ssh-multihop/internal/config"
)

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestResolveProxyJumpChain_CircularReference(t *testing.T) {
	// Create a mock parser with circular reference
	parser := config.NewParser()

	// Simulate circular reference: host1 -> host2 -> host1
	// We'll test this by manually calling resolveProxyJumpChain

	t.Run("detects circular reference", func(t *testing.T) {
		// This should detect circular reference and return error
		// instead of panicking
		visited := make(map[string]bool)
		visited["host1"] = true
		visited["host2"] = true

		// After fix, this should return an error instead of panicking
		chain, err := resolveProxyJumpChain(parser, "host1", visited)
		if err == nil {
			t.Error("Expected error on circular reference, got nil")
		}
		if chain != nil {
			t.Error("Expected nil chain on circular reference")
		}
		if err != nil && !containsString(err.Error(), "circular") {
			t.Errorf("Expected error message to contain 'circular', got: %v", err)
		}
	})
}

func TestBuildHopChainFromSSHConfig_CircularReference(t *testing.T) {
	// Test the public API to ensure it properly handles circular references
	// This requires an actual SSH config file with circular references

	// For now, we'll skip this as it requires file system setup
	t.Skip("Requires SSH config file setup")
}

func TestResolveProxyJumpChain_Simple(t *testing.T) {
	parser := config.NewParser()

	// Test simple chain without ProxyJump
	t.Run("simple host without ProxyJump", func(t *testing.T) {
		chain, err := resolveProxyJumpChain(parser, "testhost", nil)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if len(chain) != 1 {
			t.Errorf("Expected chain length 1, got %d", len(chain))
		}
		if chain[0].Host != "testhost" {
			t.Errorf("Expected host 'testhost', got '%s'", chain[0].Host)
		}
		if chain[0].Port != 22 {
			t.Errorf("Expected default port 22, got %d", chain[0].Port)
		}
	})
}

func TestParseProxyJumpList(t *testing.T) {
	tests := []struct {
		name      string
		proxyJump string
		expected  []string
	}{
		{
			name:      "single host",
			proxyJump: "jump1",
			expected:  []string{"jump1"},
		},
		{
			name:      "multiple hosts",
			proxyJump: "jump1,jump2,jump3",
			expected:  []string{"jump1", "jump2", "jump3"},
		},
		{
			name:      "with port",
			proxyJump: "jump1:2222",
			expected:  []string{"jump1"},
		},
		{
			name:      "multiple with ports",
			proxyJump: "jump1:2222,jump2:3333",
			expected:  []string{"jump1", "jump2"},
		},
		{
			name:      "with spaces",
			proxyJump: "jump1, jump2, jump3",
			expected:  []string{"jump1", "jump2", "jump3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseProxyJumpList(tt.proxyJump)
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d hosts, got %d", len(tt.expected), len(result))
				return
			}
			for i, host := range result {
				if host != tt.expected[i] {
					t.Errorf("Expected host '%s' at index %d, got '%s'", tt.expected[i], i, host)
				}
			}
		})
	}
}

func TestSplitComma(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple",
			input:    "a,b,c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with spaces",
			input:    "a, b, c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with quotes",
			input:    `a,"b,c",d`,
			expected: []string{"a", "b,c", "d"},
		},
		{
			name:     "single element",
			input:    "a",
			expected: []string{"a"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitComma(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d parts, got %d", len(tt.expected), len(result))
				return
			}
			for i, part := range result {
				if part != tt.expected[i] {
					t.Errorf("Expected '%s' at index %d, got '%s'", tt.expected[i], i, part)
				}
			}
		})
	}
}

func TestParseHostAndPort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "hostname only",
			input:    "example.com",
			expected: "example.com",
		},
		{
			name:     "hostname with port",
			input:    "example.com:2222",
			expected: "example.com",
		},
		{
			name:     "localhost with port",
			input:    "localhost:8080",
			expected: "localhost",
		},
		{
			name:     "IP with port",
			input:    "192.168.1.1:22",
			expected: "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseHostAndPort(tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
