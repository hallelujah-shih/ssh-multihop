package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"github.com/kevinburke/ssh_config"
)

// Parser parses SSH configuration files.
type Parser struct {
	config     *ssh_config.Config
	path       string
	allConfigs []*ssh_config.Config // All parsed configs (main + included)
}

// NewParser creates a new SSH config parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseConfig parses an SSH config file at the given path, including any included files.
func (p *Parser) ParseConfig(path string) (*SSHConfig, error) {
	p.allConfigs = nil

	// Parse the main config file and all included files
	config, err := p.parseWithIncludes(path)
	if err != nil {
		return nil, err
	}

	p.config = config
	p.path = path
	sshConfig := p.buildSSHConfig()
	sshConfig.parser = p
	return sshConfig, nil
}

// parseWithIncludes recursively parses a config file and its includes.
func (p *Parser) parseWithIncludes(path string) (*ssh_config.Config, error) {
	// Read the config file
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Parse the config
	config, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	// Store this config
	p.allConfigs = append(p.allConfigs, config)

	// Process Include directives from all hosts
	includes := p.extractIncludes(config)
	for _, include := range includes {
		err := p.processInclude(include, filepath.Dir(path))
		if err != nil {
			return nil, fmt.Errorf("failed to process include %q: %w", include, err)
		}
	}

	return config, nil
}

// extractIncludes extracts all Include directives from a config.
func (p *Parser) extractIncludes(config *ssh_config.Config) []string {
	var includes []string

	for _, host := range config.Hosts {
		for _, node := range host.Nodes {
			// Check if this node is an Include directive
			if include, ok := node.(*ssh_config.Include); ok {
				// Include.String() returns "Include /path/to/file"
				// We need to extract just the path part
				includeStr := include.String()
				if strings.HasPrefix(includeStr, "Include ") {
					includes = append(includes, strings.TrimPrefix(includeStr, "Include "))
				} else {
					includes = append(includes, includeStr)
				}
			}
		}
	}

	return includes
}

// processInclude processes a single Include directive, which may contain wildcards.
func (p *Parser) processInclude(pattern, baseDir string) error {
	// Expand tilde
	if strings.HasPrefix(pattern, "~/") {
		homeDir, err := util.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		pattern = filepath.Join(homeDir, pattern[2:])
	}

	// Make pattern absolute if relative
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(baseDir, pattern)
	}

	// Check if pattern contains wildcards
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to expand glob pattern %q: %w", pattern, err)
	}

	// If no matches (wildcard didn't match anything), that's ok - silently ignore
	if len(matches) == 0 {
		return nil
	}

	// Parse each matched file
	for _, match := range matches {
		_, err := p.parseWithIncludes(match)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetHostConfig retrieves the configuration for a specific host.
// It checks both the host alias and the HostName value to ensure all
// configuration directives are picked up.
func (p *Parser) GetHostConfig(host string) (*HostConfig, error) {
	if p.config == nil {
		return nil, fmt.Errorf("config not loaded, call ParseConfig first")
	}

	// Check if host exists in any of the configs
	if !p.hostExists(host) {
		return nil, fmt.Errorf("host %q not found in configuration", host)
	}

	// Build HostConfig by checking all configs in reverse order
	// (later configs override earlier ones, matching SSH behavior)
	hostConfig := &HostConfig{
		Host: host,
	}

	// Get HostName first (needed for additional lookup)
	if hostname, err := p.get(host, "HostName"); err == nil && hostname != "" {
		hostConfig.HostName = hostname
	} else {
		hostConfig.HostName = host
	}

	// Get Port (default to 22)
	port, err := p.get(host, "Port")
	if err == nil && port != "" {
		if _, err := fmt.Sscanf(port, "%d", &hostConfig.Port); err != nil {
			// If parsing fails, use default port
			hostConfig.Port = 22
		}
	}
	if hostConfig.Port == 0 {
		hostConfig.Port = 22
	}

	// Get User
	if user, err := p.get(host, "User"); err == nil {
		hostConfig.User = user
	}

	// Get IdentityFile (with path expansion)
	// Check both the host alias AND the HostName value
	identityFile := ""
	if identFile, err := p.get(host, "IdentityFile"); err == nil {
		identityFile = identFile
	}
	// Also check HostName in case IdentityFile is defined there
	if hostConfig.HostName != host && hostConfig.HostName != "" {
		if identFile, err := p.get(hostConfig.HostName, "IdentityFile"); err == nil {
			// HostName-specific IdentityFile takes precedence
			identityFile = identFile
		}
	}
	if identityFile != "" {
		expandedPath, expandErr := expandPath(identityFile)
		if expandErr != nil {
			return nil, fmt.Errorf("failed to expand IdentityFile path %q: %w", identityFile, expandErr)
		}
		hostConfig.IdentityFile = expandedPath
	}

	// Get CertificateFile (with path expansion)
	// Check both the host alias AND the HostName value
	certFile := ""
	if certPath, err := p.get(host, "CertificateFile"); err == nil {
		certFile = certPath
	}
	// Also check HostName in case CertificateFile is defined there
	if hostConfig.HostName != host && hostConfig.HostName != "" {
		if certPath, err := p.get(hostConfig.HostName, "CertificateFile"); err == nil {
			// HostName-specific CertificateFile takes precedence
			certFile = certPath
		}
	}
	if certFile != "" {
		expandedPath, expandErr := expandPath(certFile)
		if expandErr != nil {
			return nil, fmt.Errorf("failed to expand CertificateFile path %q: %w", certFile, expandErr)
		}
		hostConfig.CertificateFile = expandedPath
	}

	// Get ProxyJump
	if proxyJump, err := p.get(host, "ProxyJump"); err == nil {
		hostConfig.ProxyJump = proxyJump
	}

	return hostConfig, nil
}

// get retrieves a configuration value for a host from all configs (last match wins).
func (p *Parser) get(host, key string) (string, error) {
	// Check configs in reverse order (later includes override earlier ones)
	for i := len(p.allConfigs) - 1; i >= 0; i-- {
		val, err := p.allConfigs[i].Get(host, key)
		if err == nil && val != "" {
			return val, nil
		}
	}
	return "", fmt.Errorf("key %q not found for host %q", key, host)
}

// hostExists checks if a host is defined in any of the configs.
func (p *Parser) hostExists(host string) bool {
	// Debug: check if allConfigs is populated
	if len(p.allConfigs) == 0 {
		// Fallback to checking config directly
		if p.config != nil {
			val, err := p.config.Get(host, "HostName")
			// Check both error and if value is non-empty
			// ssh_config.Get returns empty string with nil error for non-existent hosts
			if err == nil && val != "" {
				return true
			}
		}
		return false
	}

	// Try to get a value for this host - if successful, host exists
	// The Get method handles pattern matching internally
	for _, config := range p.allConfigs {
		// Try to get HostName - check if value is non-empty
		val, err := config.Get(host, "HostName")
		if err == nil && val != "" {
			return true
		}
	}
	return false
}

// buildSSHConfig builds an SSHConfig from the parsed configs.
func (p *Parser) buildSSHConfig() *SSHConfig {
	sshConfig := &SSHConfig{
		Hosts: make(map[string]*HostConfig),
	}
	return sshConfig
}

// ListHosts returns a list of all hosts defined in the SSH config.
func (p *Parser) ListHosts() ([]*HostInfo, error) {
	if p.config == nil {
		return nil, fmt.Errorf("config not loaded, call ParseConfig first")
	}

	// Use a map to deduplicate hosts (same host may appear in multiple files)
	hostMap := make(map[string]*HostInfo)

	// Process all configs (main + includes)
	for _, config := range p.allConfigs {
		// Iterate through all host declarations
		for _, host := range config.Hosts {
			// Get the host patterns (e.g., "server1", "server*")
			patterns := host.Patterns

			// For each pattern, extract host information
			for _, pattern := range patterns {
				// Get the pattern string
				patternStr := pattern.String()

				// SSH config allows comma-separated aliases in a single pattern
				// Split them into individual aliases
				aliases := strings.Split(patternStr, ",")

				// Process each alias
				for _, alias := range aliases {
					alias = strings.TrimSpace(alias)

					// Skip wildcard-only patterns like "*" - these are catch-alls
					if alias == "*" {
						continue
					}

					// Skip empty aliases
					if alias == "" {
						continue
					}

					// Skip if we already have this host
					if _, exists := hostMap[alias]; exists {
						continue
					}

					info := &HostInfo{
						Name: alias,
					}

					// Get HostName (use the original pattern string for lookup)
					if hostname, err := p.get(patternStr, "HostName"); err == nil && hostname != "" {
						info.HostName = hostname
					} else {
						info.HostName = alias // Default to the alias itself
					}

					// Get Port
					if port, err := p.get(patternStr, "Port"); err == nil && port != "" {
						if _, err := fmt.Sscanf(port, "%d", &info.Port); err != nil {
							info.Port = 22 // Default SSH port
						}
					}
					if info.Port == 0 {
						info.Port = 22 // Default SSH port
					}

					// Get User
					if user, err := p.get(patternStr, "User"); err == nil {
						info.User = user
					}

					// Get IdentityFile
					if identityFile, err := p.get(patternStr, "IdentityFile"); err == nil {
						expandedPath, expandErr := expandPath(identityFile)
						if expandErr == nil {
							info.IdentityFile = expandedPath
						}
					}

					// Get ProxyJump
					if proxyJump, err := p.get(patternStr, "ProxyJump"); err == nil {
						info.ProxyJump = proxyJump
					}

					hostMap[alias] = info
				}
			}
		}
	}

	// Convert map to slice
	result := make([]*HostInfo, 0, len(hostMap))
	for _, info := range hostMap {
		result = append(result, info)
	}

	return result, nil
}

// CalculateFileChecksum calculates SHA256 checksum of a file
func CalculateFileChecksum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// ResolveHost resolves hostname to IP address following SSH resolution order:
// 1. Special case: "local" → 127.0.0.1
// 2. SSH config lookup
// 3. DNS fallback
func ResolveHost(hostname string) (string, error) {
	// 1. Special handling
	if hostname == "local" {
		return "127.0.0.1", nil
	}

	// 2. SSH config priority
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, ".ssh", "config")

	parser := NewParser()
	_, err = parser.ParseConfig(configPath)
	if err != nil {
		// If SSH config doesn't exist, skip to DNS
		return resolveByDNS(hostname)
	}

	// Try to get HostName from SSH config
	hostConfig, err := parser.GetHostConfig(hostname)
	if err == nil && hostConfig.HostName != "" {
		// Use the HostName from SSH config
		hostname = hostConfig.HostName
	}

	// 3. DNS resolution
	return resolveByDNS(hostname)
}

// resolveByDNS performs DNS lookup and returns the first IP address
func resolveByDNS(hostname string) (string, error) {
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return "", fmt.Errorf("DNS resolution failed for '%s': %w", hostname, err)
	}

	if len(ips) == 0 {
		return "", fmt.Errorf("no IPs found for host '%s'", hostname)
	}

	return ips[0], nil
}
