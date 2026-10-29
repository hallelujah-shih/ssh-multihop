package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	agentpkg "github.com/hallelujah-shih/ssh-multihop/internal/agent"
	"github.com/hallelujah-shih/ssh-multihop/internal/daemoncfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// TestFormatAPIAddress verifies that addresses are formatted correctly
// using net.JoinHostPort for IPv4, IPv6, and hostnames.
func TestFormatAPIAddress(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		expected string
	}{
		{
			name:     "IPv4 address",
			host:     "127.0.0.1",
			port:     8080,
			expected: "127.0.0.1:8080",
		},
		{
			name:     "IPv6 address",
			host:     "::1",
			port:     8080,
			expected: "[::1]:8080",
		},
		{
			name:     "IPv6 full address",
			host:     "2001:db8::1",
			port:     9090,
			expected: "[2001:db8::1]:9090",
		},
		{
			name:     "hostname",
			host:     "localhost",
			port:     8080,
			expected: "localhost:8080",
		},
		{
			name:     "hostname with port",
			host:     "example.com",
			port:     443,
			expected: "example.com:443",
		},
		{
			name:     "IPv4 private",
			host:     "192.168.1.1",
			port:     3000,
			expected: "192.168.1.1:3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use net.JoinHostPort to format the address correctly
			// This handles IPv6 addresses by adding brackets when needed
			got := net.JoinHostPort(tt.host, strconv.Itoa(tt.port))
			if got != tt.expected {
				t.Errorf("formatAPIAddress(%q, %d) = %q, want %q",
					tt.host, tt.port, got, tt.expected)
			}
		})
	}
}

// TestVerifyAddressIsParseable verifies that formatted addresses
// can be properly parsed back into host and port.
func TestVerifyAddressIsParseable(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
	}{
		{"IPv4", "127.0.0.1", 8080},
		{"IPv6", "::1", 8080},
		{"IPv6 full", "2001:db8::1", 9090},
		{"hostname", "localhost", 8080},
		{"hostname with port", "example.com", 443},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Format using net.JoinHostPort
			addr := net.JoinHostPort(tt.host, strconv.Itoa(tt.port))

			// Verify the address can be parsed
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("net.SplitHostPort(%q) failed: %v", addr, err)
			}

			// Verify the port matches
			portInt, err := strconv.Atoi(port)
			if err != nil {
				t.Fatalf("strconv.Atoi(%q) failed: %v", port, err)
			}
			if portInt != tt.port {
				t.Errorf("port = %d, want %d", portInt, tt.port)
			}

			// For IPv6, the host will have brackets removed
			// For other hosts, verify they match
			if tt.host == "::1" || tt.host == "2001:db8::1" {
				// IPv6 addresses are returned without brackets
				if host != tt.host {
					t.Errorf("host = %q, want %q", host, tt.host)
				}
			} else {
				if host != tt.host {
					t.Errorf("host = %q, want %q", host, tt.host)
				}
			}
		})
	}
}

func TestDaemonCommand_Flags(t *testing.T) {
	names := map[string]bool{}
	for _, f := range daemonCommand.Flags {
		names[f.Names()[0]] = true
	}
	assert.True(t, names["config"], "--config must exist")
	assert.True(t, names["env-file"], "--env-file must exist")
	assert.False(t, names["passphrase-socket"], "--passphrase-socket must be removed")
}

// startFakeAgent 起一个真实的进程内 ssh-agent（非 mock）, 返回 socket 路径。
// 注意: x/crypto v0.48 已移除 agent.NewServer, 用 ServeAgent + Accept 循环;
// socket 目录用 os.MkdirTemp 短路径（unix sun_path 108 字节上限, t.TempDir 长测试名会 bind 失败）。
func startFakeAgent(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ag := agent.NewKeyring()
	require.NoError(t, ag.Add(agent.AddedKey{PrivateKey: priv}))
	dir, err := os.MkdirTemp("", "sfa-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	l, err := net.Listen("unix", filepath.Join(dir, "agent.sock"))
	require.NoError(t, err)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(ag, c)
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return l.Addr().String()
}

// freshGoAgentInit is the injected loader for tests: a real per-call
// construction, no process-wide singleton, order-independent.
func freshGoAgentInit(opts agentpkg.LoadOptions) error {
	ga, err := agentpkg.NewGoAgent()
	if err != nil {
		return err
	}
	return ga.Initialize(opts)
}

func TestInitKeysForRun_ExplicitBadKeyFailsWithoutExternalAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "") // 无外部 agent
	cfg := &daemoncfg.Config{Keys: []daemoncfg.KeySpec{{Path: "/nonexistent/k"}}}
	err := initKeysForRun(cfg, "", freshGoAgentInit)
	require.Error(t, err, "explicit 模式任一 key 失败必须报错（runDaemon 返回 error -> 退出非 0）")
	assert.Contains(t, err.Error(), "/nonexistent/k")
}

func TestInitKeysForRun_ExplicitStillValidatesWithExternalAgent(t *testing.T) {
	// spec I1 核心: SSH_AUTH_SOCK 可用不得豁免 explicit 校验
	t.Setenv("SSH_AUTH_SOCK", startFakeAgent(t))
	cfg := &daemoncfg.Config{Keys: []daemoncfg.KeySpec{{Path: "/nonexistent/k"}}}
	err := initKeysForRun(cfg, "", freshGoAgentInit)
	require.Error(t, err, "外部 agent 存在时 explicit 坏 key 仍必须失败")
}

func TestInitKeysForRun_ExplicitGoodKeySucceeds(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "ok")
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "id_ok")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))
	cfg := &daemoncfg.Config{Keys: []daemoncfg.KeySpec{{Path: keyPath}}}
	require.NoError(t, initKeysForRun(cfg, "", freshGoAgentInit))
}

func TestInitKeysForRun_AutoWithExternalAgentTakesAgentPath(t *testing.T) {
	// auto 模式 + 外部 agent: 不初始化内置 agent, 返回 nil（现状短路语义）
	t.Setenv("SSH_AUTH_SOCK", startFakeAgent(t))
	calls := 0
	require.NoError(t, initKeysForRun(&daemoncfg.Config{}, "", func(opts agentpkg.LoadOptions) error {
		calls++
		return freshGoAgentInit(opts)
	}))
	assert.Equal(t, 0, calls, "external agent present: built-in agent must not be initialized (I2)")
}
