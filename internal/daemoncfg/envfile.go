package daemoncfg

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

func LoadEnvFile(path string) (int, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("failed to stat env file %s: %w", path, err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		zap.L().Warn("Env file permissions are too open, expected 0600",
			zap.String("env_file", path), zap.String("mode", fi.Mode().Perm().String()))
	}

	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open env file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	loaded := 0
	sc := bufio.NewScanner(f)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := cutEnvLine(line)
		if !ok {
			zap.L().Warn("Skipping invalid env file line",
				zap.String("env_file", path), zap.Int("line", lineNo))
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			zap.L().Info("Env variable already set, keeping existing value",
				zap.String("key", key))
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return loaded, fmt.Errorf("failed to set %s from %s: %w", key, path, err)
		}
		loaded++
	}
	return loaded, sc.Err()
}

// cutEnvLine splits KEY=VALUE, stripping one pair of surrounding quotes from VALUE.
// Key charset is [A-Za-z0-9_]; anything else (e.g. "export FOO", spaces) is invalid.
// No export prefix, no backslash escaping (deliberately not systemd-compatible, see spec).
func cutEnvLine(line string) (string, string, bool) {
	key, value, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || !validEnvKey(key) {
		return "", "", false
	}
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}

func validEnvKey(key string) bool {
	for _, r := range key {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return len(key) > 0
}
