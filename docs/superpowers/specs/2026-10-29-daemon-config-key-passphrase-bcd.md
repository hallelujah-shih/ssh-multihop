# BCD: daemon 配置化 key 加载（ssh_dir + key 列表 + env 口令）

- 日期: 2026-10-29
- 状态: 已随 spec 冻结
- 关联 spec: `2026-10-29-daemon-config-key-passphrase-design.md`
- 触发分类: 架构级（新功能引入 + 凭据处理 + CLI 语义变更）

## 1. 客户原话语义

| 原话 | 语义 |
|---|---|
| "如果机器reboot,远程实际是没法登录的，主要应该是没有ssh agent相关有关" | reboot 后 daemon 以服务方式拉起时无桌面 ssh-agent（gcr）可用，且 RUID 指向的 HOME 不含用户的 ssh 配置与 key，SSH 认证失败，全部 forward 恢复失败 |
| "带口令看来是不现实的，应该删除" | PassphraseSocket（连接时经 UDS 按需取口令）在无交互场景永远等不到口令，机制整体删除 |
| "就应该在配置中指向.ssh以及key列表和密码，密码可选" | key 加载来源从"隐式自动收集"升级为"显式列表契约"；口令是每 key 可选属性 |
| "口令可以用环境变量方式加载，可以指定环境变量文件" | 口令不内联进配置文件，经环境变量注入；env 文件可显式指定，兼容 systemd EnvironmentFile 习惯 |

## 2. Impact Mapping

- 角色/场景: 唯一用户（shih）。核心场景: 笔记本 reboot 或长时间 suspend 恢复后，daemon（systemd/手动）无需任何交互完成认证并恢复全部 forward。
- 业务目标:
  - G1 reboot 后无人交互恢复认证能力。
  - G2 配置错误在启动时响亮暴露（fail fast），不以"静默半可用"状态运行。
- 影响组件:
  - `cmd/ssh-multihop/daemon.go`: flag 增删、启动装配顺序。
  - `internal/agent/`: key 加载支持口令解密。
  - `internal/connection/`: 删除 PassphraseSocket 及 builder 挂点。
  - 新增 `internal/daemoncfg/`: daemon 配置解析 + env 文件加载。
  - `docs/architecture.md`、`AGENTS.md`: 删除陈旧记载、补充新配置说明。

## 3. Domain Event 流

新启动序列（括号内为现状对照）:

1. daemon 启动，读 `--config`（未指定则用默认路径，找不到文件则以空配置启动并 WARN）
2. 解析配置（ssh_dir、keys 列表）
3. 读 `--env-file`（未指定则跳过），注入进程 env
4. keys 列表存在: 逐 key 读文件 ->（如需）取 passphrase_env 变量解密 -> 入内存 agent；任一失败则启动终止
5. keys 列表不存在: 现状自动收集（仅无口令 key）
6. 照旧恢复 forwards

删除的流: 连接时解密失败 -> 查询 UDS -> 无口令 -> 认证失败。

## 4. BDD Given-When-Then

- Given 显式 keys 列表中某 key 的 passphrase_env 指向的变量不存在, When daemon 启动, Then 启动失败, 错误信息含 key 路径与变量名。
- Given keys 列表中某 key 文件不存在, When 启动, Then 启动失败, 错误含该路径。
- Given 口令错误, When 启动, Then 启动失败。
- Given 正确口令的加密 key, When 启动, Then key 入内存 agent, 认证可用。
- Given 配置无 keys 字段, When 启动, Then 与现状一致: 自动收集 ssh config IdentityFile + 默认 id_*，加密 key 跳过。
- Given --env-file 含 KEY=VALUE 且同名变量已存在于进程 env, When 加载, Then 保留进程原值。
- Given --config 显式指定但文件不存在, When 启动, Then 报错退出。
- Given 未指定 --config 且默认路径无文件, When 启动, Then 空配置 + WARN 日志, 行为向后兼容。

## 5. Spec by Example

见 spec 第 2、3 节（config.yaml 与 env 文件示例）。

## 6. Premortem（假设"重启后仍登录不了"，回溯原因）

1. systemd unit 没写 `--config`/`--env-file` 绝对路径 -> 默认路径按 RUID 落到 /root -> 空配置启动。缓解: 启动日志必须打印实际加载的 config 路径、env 文件路径、key 数量; 文档给 unit 模板。
2. ssh config 内 IdentityFile 用 `~` 且 RUID=root -> `~` 展开到 /root/.ssh -> key 缺失。缓解: 文档要求该场景用绝对路径; keys 显式列表场景不经过 IdentityFile, 不受影响。
3. env 文件明文口令泄漏 -> 与未加密私钥同权限等价, 建议 0600; 权限过宽 WARN 不拒绝。
4. 旧 systemd unit 残留 `--passphrase-socket` -> 未知 flag 直接启动失败。 breaking change, 唯一用户可控, 文档标注。
5. 单个 key 口令错导致 daemon 整体拒绝启动, 波及不需要该 key 的 forward。取舍: 显式列表=严格契约, 宁响亮不静默; 备选"按 key 延迟失败"被否（隐式半可用状态最难排查）。

## 7. 业务不变量

强不变量（违反即缺陷）:
- I1 keys 显式列表模式下, 任一 key 加载失败（文件缺失 / 变量缺失 / 口令错误）-> daemon 拒绝启动, 不存在部分 key 静默缺失的运行态; 且该校验无条件执行, 与外部 agent（SSH_AUTH_SOCK）是否可用无关。
- I2 未配置 keys 时行为与现状一致: 自动收集, 加密 key 跳过, 不引入新的失败路径。
- I3 env 文件加载不覆盖进程已存在的同名变量。
- I4 口令仅存在于 env 来源与内存 agent, 不写日志、不进 API 响应、不落盘。

软不变量（应满足, 允许权衡）:
- S1 配置/env/key 解析过程在启动日志可见（路径、数量、失败原因）。
- S2 ssh config 与默认 key 路径由 ssh_dir 单点决定。（2026-10-29 实施校准: ssh_dir 不改变 auto 模式默认 key 收集, 见不修清单）
