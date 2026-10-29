# Spec: daemon 配置化 key 加载（ssh_dir + key 列表 + env 口令）

- 日期: 2026-10-29
- 状态: 待审
- BCD: `2026-10-29-daemon-config-key-passphrase-bcd.md`（不变量见其第 7 节）
- 分类: 架构级（新配置形态 + 凭据处理 + CLI 语义变更）

## 1. 背景与目标

reboot 后 daemon 以服务方式拉起时，`util.UserHomeDir()` 按 RUID 解析 HOME：RUID=root 则找 `/root/.ssh`，用户的 ssh config 与 key 全部不可见；加密 key 依赖的 PassphraseSocket 需要人工推口令，无交互场景不可用。目标: 以显式配置取代隐式解析，使 daemon 在无人交互下完成认证。

目标:
- G1 新增 daemon 配置文件: ssh_dir、keys 显式列表、每 key 可选 passphrase_env。
- G2 新增 `--env-file`: 口令经环境变量注入，兼容 systemd `EnvironmentFile=` 习惯。
- G3 keys 显式列表模式 fail fast（BCD I1）。
- G4 删除 PassphraseSocket 全链（机制在无交互场景不可用; 且该特性从未接线: `SetPassphraseSocket` 全仓无调用方, builder 内为死代码, `docs/architecture.md` "Passphrase Socket" 节已注明"特性当前不可用"）。

非目标:
- 配置热重载（改配置需重启 daemon）。
- 改变 SSH_AUTH_SOCK 外部 agent 优先逻辑。
- 多 agent 认证策略、口令加密存储（明文 env 等价于未加密私钥文件，见安全说明）。
- Windows 支持。

## 2. 配置文件

默认路径: `<RUID-HOME>/.ssh-multihop/config.yaml`（RUID-HOME 按 `util.UserHomeDir()` 现状语义）。`--config <path>` 显式指定。YAML 格式（gopkg.in/yaml.v3，已在依赖树，转 direct 依赖）。

```yaml
# ~/.ssh-multihop/config.yaml
ssh_dir: /home/shih/.ssh        # 可选。缺省 = util.UserHomeDir() 下的 ~/.ssh（现状语义）
keys:                            # 可选。缺省 = 现状自动收集
  - path: /home/shih/.ssh/dc     # 私钥路径；<path>-cert.pub 证书按现有命名约定自动带出
    passphrase_env: DC_KEY_PASSPHRASE   # 可选。解密口令的 env 变量名
  - path: /home/shih/.ssh/id_ed25519   # 无口令 key
```

规则:
- 路径展开: `~` 前缀按 RUID HOME 展开, 在 `internal/daemoncfg` 内实现（`internal/config/expansion.go` 的 `expandPath` 未导出且内含 `os.ExpandEnv`, 不复用）。路径不做 `$VAR` 内插——口令经 passphrase_env 显式传递, 路径内插是无声明的额外魔法。
- 字段校验: `keys[].path` 必填; 未知字段报错（yaml.v3 `KnownFields(true)`）; 空文件 = 空配置。
- 显式 `--config` 指定的文件不存在 -> 启动失败; 未指定且默认路径不存在 -> 空配置 + WARN（向后兼容, BDD 第 8 条）。
- ssh config 路径 = `<ssh_dir>/config`, 收敛 `forward_service.go:1110`、`ssh_helper.go:28`、`user_context.go:71` 三处硬编码为单点解析。

## 3. env 文件

`--env-file <path>`（可选, 可与 `--config` 独立使用）。语义自定义, 不虚标对齐（systemd v259 的 `EnvironmentFile=` 实测不支持 `export` 前缀, 二者本就不同）:
- 每行 `KEY=VALUE`; `#` 与 `;` 开头为注释, 空行忽略; VALUE 去除两侧成对单/双引号; 忽略行尾空白; 不支持 `export` 前缀与反斜杠转义。
- 不覆盖进程已存在的同名变量（BCD I3）。
- 非法行（无 `=`）: WARN 跳过, 不失败。
- 显式指定的文件不存在 -> 启动失败。
- 权限非 0600 -> WARN（不拒绝）。
- systemd 场景指引: 优先用 unit 自身 `EnvironmentFile=`, 不再传 `--env-file`, 避免双源分叉; 若两者同用, I3 保证 systemd 注入的值优先。

## 4. key 加载语义

- keys 列表存在 -> 精确模式: 按列表逐 key 加载, 失败即启动失败（BCD I1）:
  - 文件不存在 -> 失败, 报路径。
- keys 列表存在 -> 精确模式: 按列表逐 key 加载, 失败即启动失败（BCD I1）:
  - **加载与校验无条件进行, 与 SSH_AUTH_SOCK 是否可用无关**（现状 daemon.go:168-179 在外部 agent 可用时短路内置 agent; 该短路不得吞掉显式 key 的加载校验, 否则 I1 静默失效）。显式 key 加载进内存 agent; 外部 agent 优先的认证顺序不变（认证时仍先试外部 agent, 内存 agent 为后备）。
  - 文件不存在 -> 失败, 报路径。
  - 解密需要口令: passphrase_env 未配置或变量不存在 -> 失败, 报 key 路径 + 变量名; 口令错误 -> 失败。
  - passphrase_env 仅在该 key 实际需要解密时读取: key 本身无口令而 passphrase_env 指向的变量缺失 -> 不报错、不读该变量。
  - `keys: []` 空列表等同未配置（auto 模式），避免"显式清空导致零 key 启动"的脚枪。
  - 解密实现: `ssh.ParseRawPrivateKey` 失败且为 `PassphraseMissingError` 时, 以 `ssh.ParseRawPrivateKeyWithPassphrase` 重试。
  - 证书: `<path>-cert.pub` 存在则自动附带（复用 key_loader 现有逻辑）。
  - **装配语义（修 daemon.go:188-193）**: 现状对内置 agent 初始化失败是 Warn+continue。该兜底仅 auto 模式保留（保持现状）; explicit 模式任一 key 失败 -> daemon 打印错误后**退出非 0**, 不进入半可用运行态。
- keys 列表不存在 -> 现状模式: 自动收集 ssh config IdentityFile + 默认 id_*，仅加载无口令 key; 外部 agent 短路与 Warn+continue 兜底均保持现状（BCD I2, 行为零变化）。
- 口令处理边界（BCD I4）: 口令只经 env 变量进入内存 agent, 日志仅记录 key 路径/变量名/成功数量, 不输出变量值; API 无任何口令暴露面。

## 5. 删除清单

- `cmd/ssh-multihop/daemon.go`: `--passphrase-socket` flag 定义（:99 附近）、默认路径解析（:132-137）、socket 目录创建（:147-151）、PassphraseSocket 启动/停止（:196-205, :203）。
- `cmd/passphrase-client/`: 整目录删除（UDS 客户端, 机制删除后无存在理由）。
- `internal/connection/passphrase_socket.go` 与 `passphrase_socket_test.go`: 整文件删除。
- `internal/connection/builder.go`: `passphraseSocket` 字段、`SetPassphraseSocket`（全仓无调用方）、`GetPassphrase` 死代码链（:24,:54-58,:348-357）。
- `internal/connection/doc.go`: 移除 PassphraseSocket 描述。
- 文档: `docs/architecture.md` "Passphrase Socket" 节、`AGENTS.md` 中 passphrase-client 记载, 随删除一并移除。

## 6. 模块改动点

| 文件 | 改动 |
|---|---|
| `internal/daemoncfg/`（新增） | Config 结构体、YAML 解析（KnownFields）、env 文件加载、`~` 展开、默认路径解析 |
| `cmd/ssh-multihop/daemon.go` | 加 `--config`/`--env-file`, 删 `--passphrase-socket`; 装配顺序: 读配置 -> 加载 env -> 按 keys 模式初始化 agent（explicit 失败退出, auto 保持 Warn+continue） |
| `internal/agent/key_loader.go` | `tryLoadKey` 支持口令解密; 新增按 KeySpec 列表加载的入口（严格模式） |
| `internal/agent/agent.go`、`internal/agent/builtin_agent.go` | `GoAgent.Initialize` / `GetBuiltInAgent` 链路接收 KeySpec 规格（共约 4 处签名）; 单例 `sync.Once` 语义注意: once 不重试, 失败错误被缓存返回, 不存在静默二次初始化; explicit 模式下 agent 初始化提前到装配期（而非首次连接时） |
| `internal/connection/builder.go` | 删口令查询死代码链（`SetPassphraseSocket` 无调用方, `b.passphraseSocket` 恒为 nil）。默认 key 探测块保持现状: explicit 模式装配期内存 agent 已持有 key, 该块不可达; auto 模式行为零变化（BCD I2）, 不接 ssh_dir 管道 |
| `internal/forwarding/ssh_helper.go`、`internal/service/forward_service.go`、`internal/agent/user_context.go` | ssh config 路径改由 ssh_dir 单点解析。传播机制: 启动装配期解析一次得到绝对路径, 以**显式参数/构造函数注入**传入三处（hop 链构造、service 的 sshConfigCache 键、agent 的 GetSSHConfigPath）, 不引入包级全局变量 |
| `go.mod` | gopkg.in/yaml.v3 转 direct |
| docs | architecture.md、AGENTS.md 更新; README 增配置示例 |

启动日志（S1）: config 路径、env 文件路径与加载条数、keys 模式（explicit/auto）、成功加载 key 数。

## 7. 测试计划

- daemoncfg: YAML 解析（正常/未知字段/空文件/路径展开）、env 文件（注释/引号/export/不覆盖已有变量/非法行跳过/文件缺失）。
- key_loader: 无口令 key、正确口令、错误口令、passphrase_env 变量缺失、**无口令 key + passphrase_env 变量缺失（不读变量, 正常加载）**、cert 自动附带、严格模式失败聚合报错。
- agent: explicit 模式任一失败 -> Initialize 返回错误; auto 模式加密 key 跳过（现状回归）。
- 装配级: explicit 失败 -> 进程退出非 0（不是 Warn+continue）; explicit + SSH_AUTH_SOCK 指向有 key 的 agent -> 显式 key 仍被加载校验; auto + 外部 agent -> 行为与现状一致。
- daemon 装配: --config 文件缺失报错; 默认路径缺失空配置启动; 删除 --passphrase-socket 后 flag 集回归。
- 全量 `make test` 回归; 手工验收: 按 spec 示例配置在 vmr.dp 实机建通一条 -R 通道。

## 8. 迁移与兼容

- 无配置文件的用户（现状全部场景）: 默认路径不存在 -> 空配置, 行为与今天一致, 无迁移动作。
- breaking: `--passphrase-socket` 移除, 传该 flag 的启动命令/unit 会报未知 flag。唯一用户, 一次性手工调整, 文档标注。
- systemd unit 模板（文档提供）: `ExecStart=/opt/forward/ssh-multihop daemon --config /home/shih/.ssh-multihop/config.yaml --env-file /home/shih/.ssh-multihop/env --db /opt/forward/mydb.db --port 28080`, `User=shih`。权限事实: /opt/forward 对 shih 只读可执行（二进制放行即可）, db 文件已由 shih 属主, config/env/keys 均在 /home/shih 下, unit 无需特权目录写权限。

## 9. 安全说明

env 文件明文口令的威胁模型与未加密私钥文件等价（拿到文件即拿到 key）。自用单机工具可接受; 通过 0600 权限 + 权限 WARN 缓解。不做口令加密存储（加密存放需再引入一个口令, 无收益）。不做加载后 Unsetenv 清理: `/proc/PID/environ` 暴露的是 exec 时的 env 块, Unsetenv 不改变该暴露面; 同 uid 进程本就能直接读私钥文件, 额外清理是安全剧场。
