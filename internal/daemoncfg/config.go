// Package daemoncfg loads the daemon's YAML configuration and env files.
package daemoncfg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

type KeySpec struct {
	Path          string `yaml:"path"`
	PassphraseEnv string `yaml:"passphrase_env"`
}

type Config struct {
	SSHDir string    `yaml:"ssh_dir"`
	Keys   []KeySpec `yaml:"keys"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}
	cfg := &Config{}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to parse config %s: %w", path, err)
	}
	return cfg, nil
}

func DefaultPath() (string, error) {
	uid := os.Getuid()
	// util.UserHomeDir resolves by RUID; replicate the two-step lookup here to
	// keep daemoncfg a leaf package.
	currentUser, err := user.LookupId(fmt.Sprintf("%d", uid))
	if err != nil {
		return "", fmt.Errorf("failed to resolve home for uid %d: %w", uid, err)
	}
	if currentUser.HomeDir == "" {
		return "", fmt.Errorf("uid %d has no home directory", uid)
	}
	return filepath.Join(currentUser.HomeDir, ".ssh-multihop", "config.yaml"), nil
}

func LoadDefault() (*Config, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(p); statErr != nil {
		zap.L().Warn("No daemon config file found, using built-in defaults", zap.String("config", p))
		return &Config{}, nil
	}
	return Load(p)
}

func (c *Config) ExplicitKeys() bool { return len(c.Keys) > 0 }

func expandTilde(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return "", fmt.Errorf("~user paths not supported, use absolute path: %s", p)
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

func userHomeDir() (string, error) {
	currentUser, err := user.LookupId(fmt.Sprintf("%d", os.Getuid()))
	if err != nil || currentUser.HomeDir == "" {
		return "", fmt.Errorf("failed to resolve current user home: %w", err)
	}
	return currentUser.HomeDir, nil
}

func (c *Config) ResolvedSSHConfigPath() (string, error) {
	if c.SSHDir == "" {
		return "", nil
	}
	dir, err := expandTilde(c.SSHDir)
	if err != nil {
		return "", fmt.Errorf("invalid ssh_dir: %w", err)
	}
	return filepath.Join(dir, "config"), nil
}

func (c *Config) ResolvedKeyPaths() error {
	for i := range c.Keys {
		p, err := expandTilde(c.Keys[i].Path)
		if err != nil {
			return fmt.Errorf("keys[%d]: %w", i, err)
		}
		c.Keys[i].Path = p
	}
	return nil
}
