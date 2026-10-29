package daemoncfg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnvFile_MissingFileIsError(t *testing.T) {
	_, err := LoadEnvFile(filepath.Join(t.TempDir(), "absent"))
	require.Error(t, err)
}

func TestLoadEnvFile_ParsesValuesAndStripsPairedQuotes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	content := "# comment\n; also comment\n\nPLAIN=hello\nQUOTED=\"hi there\"\nSINGLE='x y'\nHASH_IN_VALUE=a#b\nSEMI_IN_VALUE=c;d\n"
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))

	n, err := LoadEnvFile(p)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	t.Cleanup(func() { os.Unsetenv("PLAIN"); os.Unsetenv("QUOTED"); os.Unsetenv("SINGLE"); os.Unsetenv("HASH_IN_VALUE"); os.Unsetenv("SEMI_IN_VALUE") })

	for name, want := range map[string]string{
		"PLAIN": "hello", "QUOTED": "hi there", "SINGLE": "x y",
		"HASH_IN_VALUE": "a#b", "SEMI_IN_VALUE": "c;d",
	} {
		got, ok := os.LookupEnv(name)
		require.True(t, ok, name)
		assert.Equal(t, want, got, name)
	}
}

func TestLoadEnvFile_ExportPrefixLineIsInvalidAndSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	require.NoError(t, os.WriteFile(p, []byte("export FOO=bar\nGOOD=1\n"), 0o600))
	n, err := LoadEnvFile(p)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	t.Cleanup(func() { os.Unsetenv("GOOD") })
	_, ok := os.LookupEnv("export FOO")
	assert.False(t, ok)
}

func TestLoadEnvFile_DoesNotOverrideExisting(t *testing.T) {
	t.Setenv("EXISTING_VAR", "original")
	p := filepath.Join(t.TempDir(), "env")
	require.NoError(t, os.WriteFile(p, []byte("EXISTING_VAR=overwritten\n"), 0o600))
	_, err := LoadEnvFile(p)
	require.NoError(t, err)
	got, _ := os.LookupEnv("EXISTING_VAR")
	assert.Equal(t, "original", got)
}

func TestLoadEnvFile_SkipsInvalidLinesWithWarning(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	require.NoError(t, os.WriteFile(p, []byte("NO_EQUALS_SIGN\nOK=1\n"), 0o600))
	n, err := LoadEnvFile(p)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	t.Cleanup(func() { os.Unsetenv("OK") })
	got, ok := os.LookupEnv("OK")
	require.True(t, ok)
	assert.Equal(t, "1", got)
}

func TestLoadEnvFile_WarnsOnLoosePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	require.NoError(t, os.WriteFile(p, []byte("LOOSE=1\n"), 0o644))
	_, err := LoadEnvFile(p)
	require.NoError(t, err)
	t.Cleanup(func() { os.Unsetenv("LOOSE") })
}
