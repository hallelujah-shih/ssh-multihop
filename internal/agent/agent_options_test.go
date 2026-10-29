package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/hallelujah-shih/ssh-multihop/internal/daemoncfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestInitialize_ExplicitModeFailsOnBadKey(t *testing.T) {
	ga, err := NewGoAgent()
	require.NoError(t, err)
	err = ga.Initialize(LoadOptions{Keys: []daemoncfg.KeySpec{{Path: "/nonexistent/k"}}})
	require.Error(t, err)
}

func TestInitialize_ExplicitModeLoadsGoodKey(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "ok")
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "id_ok")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))

	ga, err := NewGoAgent()
	require.NoError(t, err)
	require.NoError(t, ga.Initialize(LoadOptions{Keys: []daemoncfg.KeySpec{{Path: keyPath}}}))
	assert.Equal(t, 1, ga.GetKeyCount())
}

func TestInitialize_AutoModeBadConfigErrorsLikeToday(t *testing.T) {
	// 现状行为回归: ssh config 解析失败 -> LoadKeys(nil) -> NoUsableKeysError（agent.go:43-64 现状）。
	// 确定性锚: 用目录当 config —— os.Open(目录) 成功但首次 Read 得 EISDIR,
	// Decode 必失败且 errors.Is(err, fs.ErrNotExist)==false（不落 "用默认 key" 分支）,
	// 结果不依赖本机 ~/.ssh 的 key 状态。
	ga, err := NewGoAgent()
	require.NoError(t, err)
	err = ga.Initialize(LoadOptions{SSHConfigPath: t.TempDir()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no usable SSH keys found")
}

// TestGetBuiltInAgent_InitFailureIsCachedAndRethrown consumes the process-wide
// singleton: once it runs, globalBuiltInAgentOnce is burned for the whole test
// binary. No other test in this package may rely on GetBuiltInAgent's init
// state (currently none calls it; keep it that way).
func TestGetBuiltInAgent_InitFailureIsCachedAndRethrown(t *testing.T) {
	opts := LoadOptions{Keys: []daemoncfg.KeySpec{{Path: "/nonexistent/t3fix"}}}

	ag1, err1 := GetBuiltInAgent(opts)
	require.Error(t, err1)
	assert.Nil(t, ag1)

	ag2, err2 := GetBuiltInAgent(opts)
	require.Error(t, err2, "cached init failure must be rethrown on every call")
	assert.Nil(t, ag2, "no silent second initialization may yield a usable agent")
}
