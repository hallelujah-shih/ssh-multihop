# Daemon Config-Key-Loading Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** daemon 支持配置文件（ssh_dir + keys 显式列表 + 每 key 可选 passphrase_env）与 --env-file 环境变量口令注入，删除从未接线的 PassphraseSocket 全链。

**Architecture:** 新增 leaf 包 `internal/daemoncfg`（YAML 解析 + env 文件加载 + 路径展开）；agent 包新增 KeySpec/LoadOptions 并实现严格模式 key 加载；daemon 装配期一次性解析并注入（ssh config 路径经 hopResolver/构造参数传播，无全局变量）。explicit 模式 fail fast，auto 模式行为字节级不变。

**Tech Stack:** Go 1.25, gopkg.in/yaml.v3（转 direct）, golang.org/x/crypto/ssh, urfave/cli/v2, testify, zap。

**Spec:** `docs/superpowers/specs/2026-10-29-daemon-config-key-passphrase-design.md`（BCD 同目录；不变量 I1-I4 见 BCD 第 7 节）

## Global Constraints

- Go 1.25.0；不新增 direct 依赖（yaml.v3 从 indirect 转 direct 是唯一 go.mod 变化）。
- auto 模式（无 keys 列表）行为零变化（BCD I2）：外部 agent 短路、Warn+continue 兜底、加密 key 跳过、默认路径 RUID 解析全部保持。
- 口令（I4）：只经 env 变量进入内存；日志只出现 key 路径/变量名/数量，禁止输出变量值；不进 API。
- env 文件语义（spec 第 3 节）：`KEY=VALUE`；`#`/`;` 注释；成对单/双引号剥离；不支持 export 与反斜杠转义；非法行 WARN 跳过；不覆盖已存在变量；显式指定的文件缺失=启动失败；权限非 0600 WARN。
- explicit 模式（I1）：任一 key 失败（文件缺失/变量缺失/口令错）-> `runDaemon` 返回 error（urfave/cli 退出非 0）；`SSH_AUTH_SOCK` 可用不豁免该校验。
- `keys: []` 等同 auto（spec 第 4 节）；passphrase_env 仅在实际需要解密时读取（spec 第 4 节）。
- 测试用 testify（`assert`/`require`），测试内禁止真实网络；加密 key 用 `ssh.MarshalPrivateKeyWithPassphrase` 现场生成到 `t.TempDir()`。
- 零注释默认；注释只写代码表达不了的"为什么"。
- 每任务结束 `go build ./... && go vet ./...` 必须绿。

### 业务契约与验证登记

**Business Contract**: `docs/superpowers/specs/2026-10-29-daemon-config-key-passphrase-bcd.md`

**验证入口与 tier 阶梯**（本机开发机即构建机）:
- T1: `go build ./... && go vet ./...`
- T2: `go test ./...`
- T3: `make check`（fmt + vet + lint + test, fail fast）
- T4（合并门槛）: T3 + `go test ./internal/api -tags=integration -v -timeout 30s`

**当轮产物目录**（worktree 外）: `/home/shih/Downloads/tmp/opencode/sdd-config-keys/`（`ledger.md`、`dispatches/`、`runs/`）。目标仓无已登记 flake 签名/故障手册 -> 本批仅通用陷阱库；本批无脚本/配置类非代码资产（文档为 Markdown，无语法检查项）。

**BCD mapping**（五元组按任务）:
- Task 1 daemoncfg: Premortem -> BCD §6 P1(缺 config 空配置 WARN)/P3(env 权限 WARN)；Invariants -> §7 I3(不覆盖)/S1(路径日志)；BDD -> §4 env 文件 4 场景 + config 4 场景；Implicit Policies -> §3 env 语义(注释/引号/非法行)；SbE -> spec §2/§3 示例即解析验收目标。
- Task 2 LoadSpecs: Premortem -> §6 P5(fail fast 取舍)；Invariants -> §7 I1/I4(变量名进日志、值不进)；BDD -> §4 口令正确/错误/变量缺失/无口令不读变量；Implicit Policies -> 懒读变量；SbE -> spec §2 keys 示例。
- Task 3 Initialize/LoadOptions: Premortem -> 不适用（装配内部签名改造, §6 无对应失败原因）；Invariants -> §7 I1(装配可见)/I2(auto 零变化)；BDD -> §4 explicit 失败/auto 现状两条；Implicit Policies -> 无规格回落现状收集（且解析失败不加默认回落, r1 P1-2）；SbE -> spec §2 keys 列表示例的加载语义。
- Task 4 daemon 装配: Premortem -> §6 P1(启动日志可见)/P4(breaking flag 移除)；Invariants -> §7 I1(SSH_AUTH_SOCK 不豁免, 有装配级测试)/I2/S1(env_vars_loaded 入日志)；BDD -> §4 装配相关 4 场景（含外部 agent 两态）；Implicit Policies -> explicit 退出非 0 / auto Warn+continue；SbE -> spec §2/§3 示例 + §8 unit 模板接线。
- Task 5 路径传播: Premortem -> 不适用（内部传播机制, §6 无对应失败原因）；Invariants -> §7 S2(ssh_dir 单点)；BDD -> 不适用（无新增可观察行为, 空值回落由回归测试兜底）；Implicit Policies -> 空值回落 RUID 现状；SbE -> spec §2 ssh_dir 覆盖语义。
- Task 6 builder 死代码: spec G4 删除清单；不触碰任何不变量（零调用死代码删除，回归测试兜底）。
- Task 7 删除+文档: Premortem -> §6 P4(文档标注 breaking)；Invariants -> 不适用（纯文档与死代码删除, 无行为面）；BDD -> 不适用（同前）；Implicit Policies -> 不适用（同前）；SbE -> spec §8 unit 模板落 docs + §2 配置示例落 README。

**不修清单**（清单外改动 = Phase 4 P1）:
1. `internal/connection/builder.go:143-182` 默认 key 探测块——不接 ssh_dir（explicit 模式装配期 agent 已持 key 不可达；auto 模式零变化）。登记: spec §6 模块表 builder 行。
2. `internal/config/parser.go:97-105` processInclude 的 `~` 展开——保持 RUID 语义（spec §2: 跨用户场景文档要求绝对路径）。登记: spec §2 规则条。
3. `internal/agent/user_context.go:69-74` GetSSHConfigPath/GetSSHDir——保留为 auto 模式默认，`LoadOptions.SSHConfigPath` 为唯一覆盖入口。登记: spec §6 agent 行。
4. `internal/api/testutil.go:29` NewWithContext——签名不变，不改。登记: 本 plan Task 5。
5. `internal/util/userhome.go` RUID 语义——现状契约，不动。登记: spec §1 背景。

## Review Focus

spec 未覆盖但最可能咬人的五类输入（每行已绑定到所属任务的测试步骤）:

1. 口令 key 的 `passphrase_env` 变量不存在 -> 启动失败且错误信息含 key 路径与变量名（不是笼统 parse error）-> Task 2 Step 1。
2. env 值含 `#`、`;`、引号或空格 -> 引号剥离只去两侧成对引号，值内部字符原样保留 -> Task 1 Step 6。
3. `--config` 指向目录/坏 YAML/未知字段 -> 报错退出而非 panic 或静默空配置 -> Task 1 Step 1。
4. keys 列表同一路径出现两次 -> 同一指纹只加载一份（memoryAgent 按 key 去重, 数量不翻倍）-> Task 2 Step 7。
5. 无口令 key 配了 `passphrase_env` 且变量缺失 -> 正常加载, 不查询该变量 -> Task 2 Step 1。

---

### Task 1: internal/daemoncfg 包（Config + env 文件）

**Files:**
- Create: `internal/daemoncfg/config.go`
- Create: `internal/daemoncfg/envfile.go`
- Test: `internal/daemoncfg/config_test.go`
- Test: `internal/daemoncfg/envfile_test.go`
- Modify: `go.mod`（yaml.v3 indirect -> direct）

**Interfaces:**
- Consumes: 无（leaf 包，仅依赖 yaml.v3/zap/os）。
- Produces（后续任务依赖的确切签名）:
  - `type KeySpec struct { Path string; PassphraseEnv string }`（yaml tag: `path`, `passphrase_env`）
  - `type Config struct { SSHDir string; Keys []KeySpec }`（yaml tag: `ssh_dir`, `keys`）
  - `func Load(path string) (*Config, error)` —— 严格: 文件缺失/是目录/坏 YAML/未知字段 -> error；空文件 -> `&Config{}, nil`
  - `func LoadDefault() (*Config, error)` —— 默认路径 `<RUID home>/.ssh-multihop/config.yaml`；不存在 -> `(&Config{}, nil)` 并 WARN 日志（路径一并打出）
  - `func (c *Config) ExplicitKeys() bool` —— `len(c.Keys) > 0`
  - `func (c *Config) ResolvedSSHConfigPath() (string, error)` —— SSHDir 为空 -> `("", nil)`；否则 `~` 展开 + `/config`
  - `func (c *Config) ResolvedKeyPaths() error` —— 就地 `~` 展开每个 `Keys[i].Path`（`~user` 形式 -> error "not supported, use absolute path"）
  - `func LoadEnvFile(path string) (int, error)` —— 返回加载的变量条数（供 S1 日志）

- [ ] **Step 1: 写 config.go 的失败测试**

```go
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
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/daemoncfg/ -v`
Expected: FAIL（包不存在，编译错误）

- [ ] **Step 3: 实现 config.go**

```go
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
	if err := dec.Decode(cfg); err != nil && err.Error() != "EOF" {
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
```

注意: `DefaultPath`/`userHomeDir` 用 `os/user` 按 RUID 解析（与 `util.UserHomeDir()` 同语义），**不 import `internal/util`** —— 保持 daemoncfg 为 leaf 包。`Load` 里空文件时 `dec.Decode` 返回 `io.EOF`，用 `errors.Is(err, io.EOF)` 判定（上面伪代码的字符串比较在实现时改为 `errors.Is`）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/daemoncfg/ -v`
Expected: PASS

- [ ] **Step 5: 写 envfile.go 的失败测试**

```go
package daemoncfg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnvFile_MissingFileIsError(t *testing.T) {
	require.Error(t, LoadEnvFile(filepath.Join(t.TempDir(), "absent")))
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
```

- [ ] **Step 6: 运行确认失败**

Run: `go test ./internal/daemoncfg/ -run TestLoadEnvFile -v`
Expected: FAIL（LoadEnvFile 未定义）

- [ ] **Step 7: 实现 envfile.go**

```go
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
	defer f.Close()

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
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return len(key) > 0
}
```

- [ ] **Step 8: 运行确认通过 + go.mod 转 direct**

Run: `go mod tidy && go test ./internal/daemoncfg/ -v && go build ./...`
Expected: PASS；`go.mod` 中 `gopkg.in/yaml.v3` 移入 require 主块

- [ ] **Step 9: Commit**

```bash
git add internal/daemoncfg go.mod go.sum
git commit -m "feat(daemoncfg): config and env-file loading for daemon key setup"
```

---

### Task 2: agent 包 KeySpec + 严格模式 key 加载

**Files:**
- Create: `internal/agent/keys.go`
- Modify: `internal/agent/key_loader.go`（`tryLoadKey` 增加口令路径；新增 `LoadSpecs`）
- Test: `internal/agent/keys_test.go`

**Interfaces:**
- Consumes: `daemoncfg.KeySpec`（Task 1: `Path string`, `PassphraseEnv string`）。
- Produces:
  - `func (kl *KeyLoader) LoadSpecs(specs []daemoncfg.KeySpec) ([]*agent.AddedKey, error)` —— 严格: 任一失败 -> error 聚合（含 key 路径 + 变量名）；全部成功 -> added keys；同路径去重只加载一次
  - `func withCert(keyPath string, privateKey crypto.PrivateKey) (*agent.AddedKey, error)` —— 从 `tryLoadKey` 现有 cert 逻辑（key_loader.go:105-120）提取共享；`LoadSpecs` 与 `tryLoadKey` 共用
  - `tryLoadKey` 签名与行为不变（auto 模式专用, 零变化）

- [ ] **Step 1: 写失败测试（覆盖 Review Focus 1/5/4）**

```go
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
```

（`ssh.MarshalPrivateKeyWithPassphrase(key crypto.PrivateKey, comment string, passphrase []byte) (*pem.Block, error)` 与 `ssh.MarshalPrivateKey(key, comment)` 均为 x/crypto/ssh 现有 API；如签名有出入以 `go doc golang.org/x/crypto/ssh | grep Marshal` 为准，测试语义不变。）

```go
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
	keys, err := kl.LoadSpecs([]daemoncfg.KeySpec{{Path: keyPath}, {Path: keyPath}})
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
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run TestLoadSpecs -v`
Expected: FAIL（LoadSpecs 未定义）

- [ ] **Step 3: 实现 keys.go 与 key_loader 改造**

`internal/agent/keys.go`:

```go
package agent

import (
	"crypto"
	"errors"
	"fmt"
	"os"

	"github.com/hallelujah-shih/ssh-multihop/internal/daemoncfg"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// LoadSpecs loads exactly the configured keys, strictly: any failure aborts.
// The passphrase env var is read only when the key file actually needs decryption.
func (kl *KeyLoader) LoadSpecs(specs []daemoncfg.KeySpec) ([]*agent.AddedKey, error) {
	var keys []*agent.AddedKey
	seen := make(map[string]bool)
	var errs []error
	for i, spec := range specs {
		if seen[spec.Path] {
			continue
		}
		seen[spec.Path] = true
		added, err := kl.loadSpec(spec)
		if err != nil {
			errs = append(errs, fmt.Errorf("keys[%d] %s: %w", i, spec.Path, err))
			continue
		}
		keys = append(keys, added)
		zap.L().Info("Loaded configured SSH key",
			zap.String("key", spec.Path))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to load %d of %d configured keys: %w", len(errs), len(specs), errors.Join(errs...))
	}
	return keys, nil
}

func (kl *KeyLoader) loadSpec(spec daemoncfg.KeySpec) (*agent.AddedKey, error) {
	keyBytes, err := os.ReadFile(spec.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}
	privateKey, err := ssh.ParseRawPrivateKey(keyBytes)
	if err == nil {
		return withCert(spec.Path, privateKey)
	}
	var passErr *ssh.PassphraseMissingError
	if !errors.As(err, &passErr) {
		return nil, fmt.Errorf("failed to parse key: %w", err)
	}
	if spec.PassphraseEnv == "" {
		return nil, fmt.Errorf("key is encrypted but no passphrase_env configured")
	}
	passphrase, ok := os.LookupEnv(spec.PassphraseEnv)
	if !ok {
		return nil, fmt.Errorf("key is encrypted and passphrase_env %q is not set in environment", spec.PassphraseEnv)
	}
	privateKey, err = ssh.ParseRawPrivateKeyWithPassphrase(keyBytes, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt key with passphrase from %q: %w", spec.PassphraseEnv, err)
	}
	return withCert(spec.Path, privateKey)
}

// withCert reuses key_loader's cert convention (<path>-cert.pub):
// func withCert(keyPath string, privateKey crypto.PrivateKey) (*agent.AddedKey, error)
```

`withCert` 直接提取 `key_loader.go:105-120` 现有 cert 逻辑为共享函数（两处调用）。`tryLoadKey` 保留给 auto 模式，行为不变。实现时若发现 `LoadSpecs` 与 `tryLoadKey` 有更多可共享片段，只提取已重复的部分。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/agent/ -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): strict key loading from configured KeySpecs with env passphrases"
```

---

### Task 3: GoAgent / BuiltInAgent 接收 LoadOptions

**Files:**
- Modify: `internal/agent/agent.go:41-83`（Initialize 签名与分支）、`internal/agent/agent.go:86-108`（collectKeyPaths 接收 sshConfigPath）
- Modify: `internal/agent/builtin_agent.go:32-54`（GetBuiltInAgent/newBuiltInAgent/init 传参）
- Modify: `cmd/ssh-multihop/daemon.go:189,301,325`（三处调用点过渡: 传零值 `agent.LoadOptions{}`; :189 所在外部 agent 块由 Task 4 重写, 此处只保编译绿）
- Modify: `internal/connection/builder.go:235`（第四处调用点过渡: `agentpkg.GetBuiltInAgent(agent.LoadOptions{})`; 本任务只改签名调用, 不触不修清单其他区域）
- Test: `internal/agent/agent_options_test.go`

**Interfaces:**
- Consumes: Task 1 的 `daemoncfg.Config`/`KeySpec`；Task 2 的 `LoadSpecs`。
- Produces:
  - `type LoadOptions struct { Keys []daemoncfg.KeySpec; SSHConfigPath string }`（agent 包内）
  - `func (a *GoAgent) Initialize(opts LoadOptions) error` —— `len(opts.Keys)>0`: 严格加载, 失败即 error；否则 auto: 用 `opts.SSHConfigPath`（空 -> `userCtx.GetSSHConfigPath()` 现状）
  - `func GetBuiltInAgent(opts LoadOptions) (*BuiltInAgent, error)` —— 首次调用以当次 opts 初始化（sync.Once 不重试, 失败缓存返回, 无静默二次初始化）；后续调用忽略 opts

- [ ] **Step 1: 写失败测试**

```go
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
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/ -run TestInitialize -v`
Expected: FAIL（Initialize 无参 -> 编译错误）

- [ ] **Step 3: 实现**

`agent.go`:

```go
type LoadOptions struct {
	Keys           []daemoncfg.KeySpec
	SSHConfigPath  string
}

func (a *GoAgent) Initialize(opts LoadOptions) error {
	var keys []*agent.AddedKey
	if len(opts.Keys) > 0 {
		loaded, err := a.keyLoader.LoadSpecs(opts.Keys)
		if err != nil {
			return err
		}
		keys = loaded
	} else {
		keyPaths, err := a.collectKeyPaths(opts.SSHConfigPath)
		if err != nil {
			zap.L().Warn("Failed to collect key paths from SSH config, using defaults only", zap.Error(err))
		}
		// 注意: 不加 "keyPaths == nil -> 默认 key" 回落——现状（agent.go:43-48）解析失败即
		// LoadKeys(nil) -> NoUsableKeysError, auto 模式字节级不变（BCD I2, 盲审 r1 P1-2）。
		loaded, err := a.keyLoader.LoadKeys(keyPaths)
		if err != nil {
			if _, ok := err.(*NoUsableKeysError); ok {
				return fmt.Errorf(`no usable SSH keys found

Please ensure at least one of the following:
1. Add an unencrypted key to ~/.ssh/ (id_ed25519, id_rsa, etc.)
2. Add an unencrypted key in SSH config with IdentityFile
3. Keys with passphrases are not supported in daemon mode

Keys checked: %d`, len(keyPaths))
			}
			return fmt.Errorf("failed to load keys: %w", err)
		}
		keys = loaded
	}
	// ...现有 Add 循环与日志不变...
}

func (a *GoAgent) collectKeyPaths(sshConfigPath string) ([]string, error) {
	path := sshConfigPath
	if path == "" {
		path = a.userCtx.GetSSHConfigPath()
	}
	parser := config.NewParser()
	// ...其余与现状一致, 仅 ParseConfig(path)...
}
```

`builtin_agent.go`: `GetBuiltInAgent(opts LoadOptions)`、`newBuiltInAgent(opts LoadOptions)`、`init(opts LoadOptions)` 逐层透传到 `goAgent.Initialize(opts)`。`daemon.go:301/:325` 改为 `agent.GetBuiltInAgent(agent.LoadOptions{})`。

- [ ] **Step 4: 运行确认通过（全 agent 包 + 编译）**

Run: `go test ./internal/agent/ -v && go build ./...`
Expected: PASS（`cmd` 引用点已同步修改）

- [ ] **Step 5: Commit**

```bash
git add internal/agent cmd/ssh-multihop/daemon.go
git commit -m "feat(agent): Initialize accepts LoadOptions for explicit key mode"
```

---

### Task 4: daemon 装配（--config/--env-file, 删 --passphrase-socket, strict 退出语义）

**Files:**
- Modify: `cmd/ssh-multihop/daemon.go`（flags :79-102, runDaemon :108-139, 外部 agent 分支 :167-194, passphrase socket :147-151/:196-205, svc 注入 :233 附近, shutdown :301/:325）
- Test: `cmd/ssh-multihop/daemon_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 `daemoncfg.Load/LoadDefault/LoadEnvFile/ExplicitKeys/ResolvedSSHConfigPath/ResolvedKeyPaths`；Task 3 `agent.LoadOptions/GetBuiltInAgent`。
- Produces: 无新接口（装配层）。`runDaemon` 在 explicit 失败时返回 error -> urfave/cli 以非 0 退出。

- [ ] **Step 1: 写失败测试（flag 层 + 装配层）**

```go
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
func startFakeAgent(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	ag := agent.NewKeyring()
	require.NoError(t, ag.Add(agent.AddedKey{PrivateKey: priv}))
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
	require.NoError(t, err)
	srv := agent.NewServer(ag)
	go srv.Serve(l)
	t.Cleanup(l.Close)
	return l.Addr().String()
}

func TestInitKeysForRun_ExplicitBadKeyFailsWithoutExternalAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "") // 无外部 agent
	cfg := &daemoncfg.Config{Keys: []daemoncfg.KeySpec{{Path: "/nonexistent/k"}}}
	err := initKeysForRun(cfg, "")
	require.Error(t, err, "explicit 模式任一 key 失败必须报错（runDaemon 返回 error -> 退出非 0）")
	assert.Contains(t, err.Error(), "/nonexistent/k")
}

func TestInitKeysForRun_ExplicitStillValidatesWithExternalAgent(t *testing.T) {
	// spec I1 核心: SSH_AUTH_SOCK 可用不得豁免 explicit 校验
	t.Setenv("SSH_AUTH_SOCK", startFakeAgent(t))
	cfg := &daemoncfg.Config{Keys: []daemoncfg.KeySpec{{Path: "/nonexistent/k"}}}
	err := initKeysForRun(cfg, "")
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
	require.NoError(t, initKeysForRun(cfg, ""))
}

func TestInitKeysForRun_AutoWithExternalAgentTakesAgentPath(t *testing.T) {
	// auto 模式 + 外部 agent: 不初始化内置 agent, 返回 nil（现状短路语义）
	t.Setenv("SSH_AUTH_SOCK", startFakeAgent(t))
	require.NoError(t, initKeysForRun(&daemoncfg.Config{}, ""))
}
```

测试注意: `GetBuiltInAgent` 是 sync.Once 单例，跨测试共享——本组测试全部走「失败路径」或「外部 agent 短路路径」，唯一命中初始化的是 `ExplicitGoodKeySucceeds`（其断言 nil error 对缓存态同样成立），测试间无顺序依赖。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./cmd/ssh-multihop/ -run TestDaemonCommand_Flags -v`
Expected: FAIL

- [ ] **Step 3: 改 daemon.go**

Flags 块 :98-102 的 `--passphrase-socket` 替换为:

```go
&cli.StringFlag{
	Name:  "config",
	Usage: "Path to daemon config YAML (default: ~/.ssh-multihop/config.yaml; missing default = built-in defaults)",
},
&cli.StringFlag{
	Name:  "env-file",
	Usage: "Environment file (KEY=VALUE) providing key passphrases",
},
```

runDaemon: 删 :119 `passphraseSocketPath`、:131-139 默认 socket 路径、:147-151 socket 目录创建、:156 日志的 `passphrase_socket` 字段、:196-205 passphrase socket 启动/`defer Stop`、`connection.PassphraseSocket` 相关 import 使用。替换为:

```go
configFlag := c.String("config")
var cfg *daemoncfg.Config
var err error
if configFlag != "" {
	cfg, err = daemoncfg.Load(configFlag)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
} else {
	cfg, err = daemoncfg.LoadDefault()
	if err != nil {
		return fmt.Errorf("failed to load default config: %w", err)
	}
	configFlag, _ = daemoncfg.DefaultPath() // S1: 默认路径也实打日志（可能为空串, 仅日志用途）
}
envLoaded := 0
if envFile := c.String("env-file"); envFile != "" {
	var envErr error
	envLoaded, envErr = daemoncfg.LoadEnvFile(envFile)
	if envErr != nil {
		return fmt.Errorf("failed to load env file: %w", envErr)
	}
}
if err := cfg.ResolvedKeyPaths(); err != nil {
	return fmt.Errorf("invalid key paths in config: %w", err)
}
sshConfigPath, err := cfg.ResolvedSSHConfigPath()
if err != nil {
	return fmt.Errorf("invalid ssh_dir: %w", err)
}
zap.L().Info("Daemon configuration loaded",
	zap.String("config", configFlag),
	zap.Bool("explicit_keys", cfg.ExplicitKeys()),
	zap.Int("keys", len(cfg.Keys)),
	zap.Int("env_vars_loaded", envLoaded),
	zap.String("ssh_config", sshConfigPath))
```

装配层 key 初始化提取为可测函数（同文件 daemon.go, runDaemon 调用）:

```go
// initKeysForRun initializes key material per config mode.
// Explicit mode is fatal on any key failure; auto mode keeps today's lenient behavior.
func initKeysForRun(cfg *daemoncfg.Config, sshConfigPath string) error {
	externalSocket, keyCount, err := agent.CheckExternalAgent()
	if err != nil {
		zap.L().Warn("External SSH agent check failed", zap.Error(err))
	}
	if cfg.ExplicitKeys() {
		// Strict mode: configured keys must load regardless of any external agent.
		if _, err := agent.GetBuiltInAgent(agent.LoadOptions{Keys: cfg.Keys, SSHConfigPath: sshConfigPath}); err != nil {
			return fmt.Errorf("failed to load configured SSH keys: %w", err)
		}
		if externalSocket != "" {
			zap.L().Info("Configured keys loaded; external agent preferred for authentication",
				zap.String("socket", externalSocket))
		}
		return nil
	}
	if err == nil && externalSocket != "" && keyCount > 0 {
		zap.L().Info("Using existing SSH agent",
			zap.String("socket", externalSocket),
			zap.Int("keys", keyCount))
		return nil
	}
	// CleanupOrphanedAgents 保留现状调用（pure Go agent 下为 no-op, 行为零变化）
	if err := agent.CleanupOrphanedAgents(); err != nil {
		zap.L().Warn("Failed to clean up orphaned SSH agents", zap.Error(err))
	}
	if _, err := agent.GetBuiltInAgent(agent.LoadOptions{SSHConfigPath: sshConfigPath}); err != nil {
		zap.L().Warn("Failed to initialize built-in SSH agent", zap.Error(err))
	}
	return nil
}
```

runDaemon 中原 :167-194 块替换为 `if err := initKeysForRun(cfg, sshConfigPath); err != nil { return err }`。

svc 创建后（:233 之后、svc.Start 之前）注入（**放在本任务不做**——`SetSSHConfigPath` 由 Task 5 引入, 本任务只算出 `sshConfigPath` 并打日志, 避免引用尚不存在的方法）:

```go
if sshConfigPath != "" {
	svc.SetSSHConfigPath(sshConfigPath)
}
```

（随 Task 5 一起落——本任务不写此行。）

shutdown 两处（:301/:325）: `agent.GetBuiltInAgent(agent.LoadOptions{})`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./cmd/ssh-multihop/ -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/ssh-multihop/daemon.go cmd/ssh-multihop/daemon_test.go
git commit -m "feat(daemon): config/env-file driven key setup, remove passphrase-socket"
```

---

### Task 5: ssh config 路径传播（forwarding + service）

**Files:**
- Modify: `internal/forwarding/ssh_helper.go:22-28`（加 configPath 参数）
- Modify: `internal/service/forward_service.go`（`sshConfigPath` 字段 + `SetSSHConfigPath`；:88/:119 hopResolver 闭包；`buildHopsFromSignature` :1106-1110 改用字段）
- Modify: `cmd/ssh-multihop/daemon.go`（svc 创建后注入: `if sshConfigPath != "" { svc.SetSSHConfigPath(sshConfigPath) }`）
- Test: `internal/forwarding/ssh_helper_test.go`（既有用例同步加 configPath 参数 + 追加参数化用例; 簿记项由编译兜底）

**Interfaces:**
- Consumes: Task 4 的 `svc.SetSSHConfigPath(path string)`。
- Produces: `func BuildHopChainFromSSHConfig(targetHost, configPath string) ([]*tunnel.HopConfig, error)` —— `configPath` 为空时回落 RUID `~/.ssh/config`（现状语义, 保证 auto 零变化）。

- [ ] **Step 1: 写失败测试（追加）**

```go
func TestBuildHopChainFromSSHConfig_EmptyPathFallsBackToDefault(t *testing.T) {
	// 现状语义回归: 空 configPath 不改变行为（可能因本机无对应 host 报错, 只断言不 panic 且错误信息含 config 路径解析上下文）
	_, err := BuildHopChainFromSSHConfig("definitely-not-a-host-xyz", "")
	_ = err // 行为与改造前一致即可: 不 panic
}

func TestBuildHopChainFromSSHConfig_ExplicitPathUsed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	content := "Host myjumphost\n  HostName 127.0.0.1\n  User test\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))

	_, err := BuildHopChainFromSSHConfig("myjumphost", cfgPath)
	// 该 host 存在但无 ProxyJump -> hop 链为空或解析成功; 关键断言: 错误（若有）不是"config 不存在"
	if err != nil {
		assert.NotContains(t, err.Error(), "failed to parse SSH config")
	}
}
```

（以仓库现有 ssh_helper_test.go 的实际风格为准调整; 断言核心是"显式路径被采用、空路径回落现状"。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/forwarding/ -run TestBuildHopChainFromSSHConfig -v`
Expected: FAIL（签名未变 -> 编译错误）

- [ ] **Step 3: 实现**

`ssh_helper.go`:

```go
func BuildHopChainFromSSHConfig(targetHost, configPath string) ([]*tunnel.HopConfig, error) {
	if configPath == "" {
		homeDir, err := util.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		configPath = filepath.Join(homeDir, ".ssh", "config")
	}
	// ...其余现状不变...
}
```

`forward_service.go`: 构造函数（:88 与 :119 两处）:

```go
svc := &ForwardService{ ..., sshConfigPath: "", }
svc.hopResolver = func(host string) ([]*tunnel.HopConfig, error) {
	return forwarding.BuildHopChainFromSSHConfig(host, svc.sshConfigPath)
}
```

新增字段 `sshConfigPath string` 与方法:

```go
// SetSSHConfigPath overrides the SSH config location (daemon config ssh_dir).
func (s *ForwardService) SetSSHConfigPath(path string) {
	s.sshConfigPath = path
}
```

`buildHopsFromSignature` :1105-1110 替换为:

```go
configPath := s.sshConfigPath
if configPath == "" {
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	configPath = filepath.Join(homeDir, ".ssh", "config")
}
```

（缓存键仍为 configPath, 逻辑不变。）

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/forwarding/ ./internal/service/ -v && go build ./...`
Expected: PASS（`api/testutil.go:29` 用 `NewWithContext` 不受影响）

- [ ] **Step 5: Commit**

```bash
git add internal/forwarding/ssh_helper.go internal/service/forward_service.go cmd/ssh-multihop/daemon.go
git commit -m "feat(service): injectable SSH config path for hop resolution"
```

---

### Task 6: builder 死代码删除

**Files:**
- Modify: `internal/connection/builder.go`（删 :24 字段、:54-58 `SetPassphraseSocket`、:347-370 口令分支）
- Modify: `internal/connection/doc.go`（删 PassphraseSocket 行）
- Test: `internal/connection/passphrase_socket_test.go` 删除；其余 builder 测试回归

**Interfaces:**
- Consumes: 无。
- Produces: `SSHClientConfigBuilder` 不再有 `SetPassphraseSocket`（全仓无调用方, 纯删除）。

- [ ] **Step 1: 删除死代码**

删 `passphraseSocket *PassphraseSocket` 字段、`SetPassphraseSocket` 方法、`loadPrivateKeyWithPassphrase`（或所在函数）中 `if b.passphraseSocket != nil {...}` 整个分支（:347-370），保留"key requires passphrase but running in daemon mode"错误返回。

- [ ] **Step 2: 删除 passphrase_socket.go / passphrase_socket_test.go，回归**

Run: `rm internal/connection/passphrase_socket.go internal/connection/passphrase_socket_test.go && go build ./... && go test ./internal/connection/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/connection
git commit -m "refactor(connection): remove never-wired PassphraseSocket dead code"
```

---

### Task 7: 删除 passphrase-client + 文档 + 全量门

**Files:**
- Delete: `cmd/passphrase-client/`（整目录）
- Modify: `docs/architecture.md`（删 "Passphrase Socket" 节 :565-571, 补 daemon 配置节）
- Modify: `AGENTS.md`（删 `cmd/passphrase-client/` 行, 项目结构补 `internal/daemoncfg/`）
- Modify: `README.md`（补 --config/--env-file 示例, 3-6 行）

**Interfaces:**
- Consumes: 全部前序任务。
- Produces: 无代码接口; 文档与现状一致。

- [ ] **Step 1: 删目录**

Run: `rm -rf cmd/passphrase-client && go build ./...`
Expected: 编译绿（若有残留引用, 编译会指出）

- [ ] **Step 2: 更新文档**

`AGENTS.md` 项目结构中删除 `cmd/passphrase-client/` 行，`internal/` 列表加 `daemoncfg/`；`docs/architecture.md` 删 Passphrase Socket 节，在配置相关章节加:

```markdown
### Daemon 配置

`daemon --config <path>` 加载 YAML（默认 `~/.ssh-multihop/config.yaml`, 缺省即空配置）:

```yaml
ssh_dir: /home/shih/.ssh            # 可选, 覆盖 ssh config 与 key 目录
keys:                                # 可选, 配置即严格模式: 任一 key 失败 daemon 拒绝启动
  - path: /home/shih/.ssh/dc
    passphrase_env: DC_KEY_PASSPHRASE
```

`--env-file <path>` 提供 `KEY=VALUE` 环境变量（口令来源, 不覆盖已有变量）。
```

`README.md` 加运行示例两行（`--config`/`--env-file` 用法）。

- [ ] **Step 3: 全量门**

Run: `make check`
Expected: fmt/vet/lint/test 全绿

- [ ] **Step 4: Commit**

```bash
git add -A cmd docs AGENTS.md README.md
git commit -m "chore: remove passphrase-client, update docs for daemon config"
```

---

## 手工验收（实施完成后, 不在任务内）

1. 生成测试加密 key: `ssh-keygen -t ed25519 -N test123 -f /tmp/k_enc`，env 文件写入 `TESTPASS=test123`。
2. `./ssh-multihop daemon --config <测试配置> --env-file <env> --db /tmp/t.db --port 28099` -> explicit key 加载成功日志。
3. 口令改错 -> 启动失败, 错误含 key 路径与变量名。
4. 在 vmr.dp 实机建一条 -R 通道验证端到端可用（远端需无幽灵会话残留）。
