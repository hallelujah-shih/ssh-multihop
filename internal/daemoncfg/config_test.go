package daemoncfg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_MissingFileIsError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	require.Error(t, err)
}

func TestLoad_DirectoryIsError(t *testing.T) {
	_, err := Load(t.TempDir())
	require.Error(t, err)
}

func TestLoad_UnknownFieldIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("ssh_dir: /x\nbogus: 1\n"), 0o600))
	_, err := Load(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

func TestLoad_EmptyFileIsEmptyConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte(""), 0o600))
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.False(t, cfg.ExplicitKeys())
	assert.Empty(t, cfg.Keys)
}

func TestLoad_ParsesKeysAndSSHDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	content := "ssh_dir: /home/shih/.ssh\nkeys:\n  - path: /home/shih/.ssh/dc\n    passphrase_env: DC_KEY_PASSPHRASE\n  - path: ~/id_ed25519\n"
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.True(t, cfg.ExplicitKeys())
	require.Len(t, cfg.Keys, 2)
	assert.Equal(t, "/home/shih/.ssh/dc", cfg.Keys[0].Path)
	assert.Equal(t, "DC_KEY_PASSPHRASE", cfg.Keys[0].PassphraseEnv)
}

func TestResolvedSSHConfigPath(t *testing.T) {
	cfg := &Config{SSHDir: "/home/shih/.ssh"}
	got, err := cfg.ResolvedSSHConfigPath()
	require.NoError(t, err)
	assert.Equal(t, "/home/shih/.ssh/config", got)

	empty := &Config{}
	got, err = empty.ResolvedSSHConfigPath()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestResolvedKeyPaths_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	cfg := &Config{Keys: []KeySpec{{Path: "~/keys/k1"}}}
	require.NoError(t, cfg.ResolvedKeyPaths())
	assert.Equal(t, filepath.Join(home, "keys/k1"), cfg.Keys[0].Path)
}

func TestResolvedKeyPaths_RejectsUserTilde(t *testing.T) {
	cfg := &Config{Keys: []KeySpec{{Path: "~other/k"}}}
	require.Error(t, cfg.ResolvedKeyPaths())
}

func TestLoadDefault_MissingFileReturnsEmptyConfig(t *testing.T) {
	cfg, err := LoadDefault()
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.False(t, cfg.ExplicitKeys())
}
