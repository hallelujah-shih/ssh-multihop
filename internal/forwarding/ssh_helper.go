package forwarding

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hallelujah-shih/ssh-multihop/internal/config"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
)

// BuildHopChainFromSSHConfig builds a hop chain by parsing SSH config
//
// This function:
// 1. Parses ~/.ssh/config
// 2. Looks up the target host
// 3. Resolves ProxyJump chain
// 4. Builds complete hop configuration
//
// Returns the hop chain (excluding "local" - only remote hops).
func BuildHopChainFromSSHConfig(targetHost string) ([]*tunnel.HopConfig, error) {
	// Get SSH config path
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, ".ssh", "config")

	// Parse SSH config
	parser := config.NewParser()
	_, err = parser.ParseConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH config: %w", err)
	}

	// Resolve ProxyJump chain
	chain, err := resolveProxyJumpChain(parser, targetHost, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ProxyJump chain: %w", err)
	}

	// Return the chain directly without dummy "local" hop
	// The connection pool's establisher connects to all hops in the chain
	return chain, nil
}

// resolveProxyJumpChain recursively resolves ProxyJump configuration
func resolveProxyJumpChain(parser *config.Parser, host string, visited map[string]bool) ([]*tunnel.HopConfig, error) {
	if visited == nil {
		visited = make(map[string]bool)
	}

	// Detect circular references
	if visited[host] {
		return nil, fmt.Errorf("circular ProxyJump reference detected: %s", host)
	}
	visited[host] = true

	// Get host configuration
	hostConfig, err := parser.GetHostConfig(host)
	if err != nil {
		// Host not found in config, use defaults
		hostConfig = &config.HostConfig{
			HostName: host,
			Port:     22,
		}
	}

	// Check for ProxyJump
	proxyJump := hostConfig.ProxyJump

	// If no ProxyJump, this is the final hop
	if proxyJump == "" {
		hop := &tunnel.HopConfig{
			Host:         host,
			HostName:     hostConfig.HostName,
			Port:         hostConfig.Port,
			User:         hostConfig.User,
			IdentityFile: hostConfig.IdentityFile,
		}

		// Apply defaults
		if hop.HostName == "" {
			hop.HostName = host
		}
		if hop.Port == 0 {
			hop.Port = 22
		}

		return []*tunnel.HopConfig{hop}, nil
	}

	// Parse ProxyJump (comma-separated list)
	jumps := parseProxyJumpList(proxyJump)

	// Build chain recursively
	var chain []*tunnel.HopConfig
	for _, jumpHost := range jumps {
		// Recursively resolve each jump
		jumpChain, err := resolveProxyJumpChain(parser, jumpHost, visited)
		if err != nil {
			return nil, err
		}
		chain = append(chain, jumpChain...)
	}

	// Add the final target
	targetHop := &tunnel.HopConfig{
		Host:         host,
		HostName:     hostConfig.HostName,
		Port:         hostConfig.Port,
		User:         hostConfig.User,
		IdentityFile: hostConfig.IdentityFile,
	}

	// Apply defaults
	if targetHop.HostName == "" {
		targetHop.HostName = host
	}
	if targetHop.Port == 0 {
		targetHop.Port = 22
	}

	chain = append(chain, targetHop)

	return chain, nil
}

// parseProxyJumpList parses a ProxyJump directive into a list of hosts
//
// ProxyJump can be:
// - Single host: "jump1"
// - Multiple hosts: "jump1,jump2,jump3"
// - With port: "jump1:2222"
func parseProxyJumpList(proxyJump string) []string {
	var hosts []string

	// Split by comma
	parts := splitComma(proxyJump)

	for _, part := range parts {
		host := parseHostAndPort(part)
		hosts = append(hosts, host)
	}

	return hosts
}

// splitComma splits a string by comma, respecting quotes
func splitComma(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false

	for _, r := range s {
		switch r {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				parts = append(parts, strings.TrimSpace(current.String()))
				current.Reset()
				continue
			}
			fallthrough
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, strings.TrimSpace(current.String()))
	}

	return parts
}

// parseHostAndPort extracts hostname from a host:port string
func parseHostAndPort(s string) string {
	// Remove port if present
	// Simple implementation - doesn't handle IPv6
	for i, r := range s {
		if r == ':' {
			return s[:i]
		}
	}
	return s
}
