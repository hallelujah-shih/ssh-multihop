// Package config provides SSH configuration file parsing with caching support.
//
// The config package parses OpenSSH-compatible configuration files, supporting:
//   - Standard SSH config directives (Host, HostName, Port, User, etc.)
//   - Include directives for file inclusion with wildcard support
//   - Environment variable expansion (~ expansion)
//   - Automatic caching with file change detection
//
// Key Features:
//   - Parse OpenSSH config files with full directive support
//   - Recursive Include processing with glob patterns
//   - CachedParser for repeated access with automatic cache invalidation
//   - Environment variable and tilde expansion
//   - ProxyJump support for multi-hop configurations
//
// Config Structure:
//   - SSHConfig.Hosts: map of host aliases to HostConfig
//   - SSHConfig.Includes: list of included file paths
//   - HostConfig fields: HostName, User, Port, ProxyJump, IdentityFile, etc.
//
// Usage:
//
//	// Basic parsing (single use)
//	parser := config.NewParser()
//	sshConfig, err := parser.ParseConfig("~/.ssh/config")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	hostConfig, err := parser.GetHostConfig("myhost")
//
//	// Cached parsing (repeated access)
//	cached := config.NewCachedParser()
//	sshConfig, err := cached.ParseConfig("~/.ssh/config")
//	// Subsequent calls use cache if file unchanged
//	sshConfig, err = cached.ParseConfig("~/.ssh/config")
//
// # Thread Safety
//
// Parser is NOT safe for concurrent use. Create a separate Parser instance
// for each goroutine or use external synchronization.
//
// CachedParser IS safe for concurrent use.
//
// # Error Handling
//
// All methods return errors for:
//   - File access failures (missing files, permission errors)
//   - Parse errors (invalid syntax)
//   - Host not found errors
package config
