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

// writeEncryptedKey writes an ed25519 private key encrypted with passphrase.
func writeEncryptedKey(t *testing.T, dir, name, passphrase string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, name, []byte(passphrase))
	require.NoError(t, err)
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, pem.EncodeToMemory(block), 0o600))
	return p
}

func TestLoadSpecs_PlainKeyWithoutEnvVarLoadsWithoutReadingEnv(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "plain")
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "id_plain")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))

	kl := NewKeyLoader(mustUserCtx(t))
	keys, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath, PassphraseEnv: "VAR_THAT_MUST_NOT_BE_READ"}})
	require.NoError(t, err)
	require.Len(t, keys, 1)
}

func TestLoadSpecs_EncryptedKeyWithCorrectPassphrase(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeEncryptedKey(t, dir, "id_enc", "s3cret")
	t.Setenv("TEST_KEY_PASS", "s3cret")

	kl := NewKeyLoader(mustUserCtx(t))
	keys, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath, PassphraseEnv: "TEST_KEY_PASS"}})
	require.NoError(t, err)
	require.Len(t, keys, 1)
}

func TestLoadSpecs_EncryptedKeyWithWrongPassphraseFails(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeEncryptedKey(t, dir, "id_enc", "right")
	t.Setenv("TEST_KEY_PASS", "wrong")

	kl := NewKeyLoader(mustUserCtx(t))
	_, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath, PassphraseEnv: "TEST_KEY_PASS"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), keyPath)
}

func TestLoadSpecs_MissingEnvVarFailsWithPathAndVarName(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeEncryptedKey(t, dir, "id_enc", "pw")

	kl := NewKeyLoader(mustUserCtx(t))
	_, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath, PassphraseEnv: "ABSENT_VAR_XYZ"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), keyPath)
	assert.Contains(t, err.Error(), "ABSENT_VAR_XYZ")
	assert.Contains(t, err.Error(), "not set in environment")
}

func TestLoadSpecs_EncryptedKeyWithoutPassphraseEnvFails(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeEncryptedKey(t, dir, "id_noenv", "pw")

	kl := NewKeyLoader(mustUserCtx(t))
	_, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), keyPath)
	assert.Contains(t, err.Error(), "no passphrase_env")
}

func TestLoadSpecs_MissingFileFails(t *testing.T) {
	kl := NewKeyLoader(mustUserCtx(t))
	_, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: "/nonexistent/key"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/nonexistent/key")
}

func TestLoadSpecs_DuplicatePathLoadsOnce(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeEncryptedKey(t, dir, "id_dup", "pw")
	t.Setenv("DUP_PASS", "pw")

	kl := NewKeyLoader(mustUserCtx(t))
	keys, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath, PassphraseEnv: "DUP_PASS"}, {Path: keyPath, PassphraseEnv: "DUP_PASS"}})
	require.NoError(t, err)
	assert.Len(t, keys, 1)
}

func TestLoadSpecs_CertAutoAttached(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "withcert")
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "id_wc")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))

	cert := &ssh.Certificate{
		Key:             signer.PublicKey(),
		Serial:          1,
		CertType:        ssh.UserCert,
		KeyId:           "test",
		ValidPrincipals: []string{"test"},
		ValidAfter:      0,
		ValidBefore:     0xFFFFFFFFFFFFFFFF,
	}
	require.NoError(t, cert.SignCert(rand.Reader, signer))
	require.NoError(t, os.WriteFile(keyPath+"-cert.pub", cert.Marshal(), 0o600))

	kl := NewKeyLoader(mustUserCtx(t))
	keys, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath}})
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.NotNil(t, keys[0].Certificate, "cert must be auto-attached")
}

func mustUserCtx(t *testing.T) *UserContext {
	t.Helper()
	uc, err := NewUserContext()
	require.NoError(t, err)
	return uc
}
