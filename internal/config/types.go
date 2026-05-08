package config

import "fmt"

// HostConfig represents the configuration for a single SSH host.
type HostConfig struct {
	// Host is the pattern or alias for this host (e.g., "server*" or "example")
	Host string
	// HostName is the actual hostname or IP address
	HostName string
	// Port is the SSH port number (0 means use default 22)
	Port int
	// User is the username for SSH authentication
	User string
	// IdentityFile is the path to the SSH private key file
	IdentityFile string
	// CertificateFile is the path to the SSH certificate file
	CertificateFile string
	// ProxyJump specifies the jump host(s) to connect through
	ProxyJump string
	// Match conditions for this configuration
	MatchConditions []*MatchCondition
}

// HostInfo represents basic information about a host for listing purposes.
type HostInfo struct {
	// Name is the host pattern or alias (e.g., "server1", "*.example.com")
	Name string
	// HostName is the actual hostname or IP address
	HostName string
	// Port is the SSH port number (0 means default 22)
	Port int
	// User is the username for SSH authentication
	User string
	// IdentityFile is the path to the SSH private key file
	IdentityFile string
	// CertificateFile is the path to the SSH certificate file
	CertificateFile string
	// ProxyJump is the jump host chain
	ProxyJump string
}

// SSHConfig represents the complete SSH configuration.
type SSHConfig struct {
	// parser holds a reference to the parser for dynamic host lookup
	// Can be either *Parser or *CachedParser
	parser interface{}
	// Hosts is deprecated but kept for backward compatibility - use GetHostConfig instead
	Hosts map[string]*HostConfig
}

// GetHostConfig retrieves the configuration for a specific host.
// This method performs pattern matching just like SSH does.
func (c *SSHConfig) GetHostConfig(host string) (*HostConfig, error) {
	// First try direct lookup in Hosts map (for simple cases and tests)
	if hc, ok := c.Hosts[host]; ok {
		return hc, nil
	}

	// Fall back to parser if available
	if c.parser == nil {
		return nil, fmt.Errorf("host %q not found in configuration", host)
	}

	// Handle *Parser
	if p, ok := c.parser.(*Parser); ok {
		return p.GetHostConfig(host)
	}

	return nil, fmt.Errorf("unsupported parser type: %T", c.parser)
}

// MatchCondition represents a Match directive in SSH config.
type MatchCondition struct {
	// Type is the match type (host, originalhost, localuser, user)
	Type string
	// Pattern is the pattern to match against
	Pattern string
}
