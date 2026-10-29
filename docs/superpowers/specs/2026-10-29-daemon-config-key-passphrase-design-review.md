# 设计审查报告: daemon 配置化 key 加载（ssh_dir + key 列表 + env 口令）

- 审查对象: `2026-10-29-daemon-config-key-passphrase-design.md`（spec）
- 业务契约: `2026-10-29-daemon-config-key-passphrase-bcd.md`（BCD）
- 审查方法: adversarial-review SKILL.md + design-review.md + requirements-review.md（能力语义追加）
- 审查深度: 架构级 + 单向门（凭据处理 + CLI breaking）+ 威胁模型 -> 全量四层 + 线面 + 约束审计
- 审查日期: 2026-10-29
- 审查者: 独立 subagent（无设计者共享上下文）

## verdict: CONDITIONAL（= APPROVE-WITH-FIXES，修复清单折入后即可动工，无需完整重审）

方向层成立：问题根因（RUID HOME 解析 + 口令无来源）被根除而非绕行；配置形状、
加载语义、删除范围整体健康，外部事实（yaml.v3 / ssh 库 / systemd）核心可行。
但存在 2 个 P1：BCD I1（strict fail-fast）在 daemon 装配层按现状推演不可达，
spec 未定义装配语义，实现者无论走哪条自然路径都会违反 I1 或 I2。修复条款明确，
折入 spec 后放行。

## BCD 完整性检查

7 节齐备，实质抽查通过，不退回：

| BCD 节 | 判定 |
|---|---|
| 1 客户原话语义 | 通过。4 条原话各有语义映射，覆盖 reboot 场景的两条失败根（agent 缺失 + RUID HOME）。解读有据，非单选凑数 |
| 2 Impact Mapping | 通过。Who（唯一用户 shih）+ 核心场景（reboot/suspend 恢复）+ 组件影响面齐 |
| 3 Domain Event 流 | 通过。新序列含现状对照，删除的流明确 |
| 4 BDD | 通过。8 条覆盖 I1-I3 与向后兼容（BDD 8 条 = spec 引用的"第 8 条"，对得上） |
| 5 Spec by Example | 通过（指针指向 spec 2/3 节，示例存在） |
| 6 Premortem | 通过。5 条失败原因均可推出具体失败序列，#5 显式记录了"按 key 延迟失败"备选被否的取舍 |
| 7 业务不变量 | 通过。I1-I4 强 / S1-S2 软，强/软分类有真实业务后果支撑（宁响亮不静默） |

配置变更传播：spec 非目标明确"改配置需重启 daemon"，传播语义已定义。

## 问题清单

### P1-1 strict fail-fast 在 daemon 装配层不可达；一刀切改 fatal 又违反 I2

- 位置: spec 第 6 节（daemon.go 改动行）+ BCD I1；代码依据 `cmd/ssh-multihop/daemon.go:189-193`
- 问题: 现状对 agent 初始化失败的处理是 Warn + "Continue anyway"（daemon.go:190-192），
  daemon 继续拉起 DB/service/forwards。按 spec 推演：GoAgent.Initialize 返回错误 ->
  GetBuiltInAgent 返回错误 -> daemon 仅 Warn，**照常启动** -> BDD 第 1/2/3 条
  （"Then 启动失败"）落空，I1 不可实现。spec 第 6 节 daemon.go 改动行只写了
  flag 增删与装配顺序，未要求改该错误处理语义。实现者的两条自然路径都错：
  (a) 保持 Warn+continue -> I1 失效；(b) 把该处一律改 fatal -> auto 模式下
  无可用 key 的机器（现状 Warn+继续跑）变成启动失败，引入新失败路径，违反 I2
  （"行为零变化，不引入新的失败路径"）。
- 修复: spec 第 6 节补装配语义：strict 模式 key 加载失败 -> 返回错误、退出非零；
  auto 模式保持现状 Warn+continue。测试计划补 daemon 装配级用例（strict 失败
  exit != 0；auto 零 key 仍启动）。

### P1-2 SSH_AUTH_SOCK 外部 agent 存在时，显式 keys 列表被整体静默跳过，I1 失守

- 位置: spec 第 4 节 + 非目标"不改变 SSH_AUTH_SOCK 外部 agent 优先逻辑"；代码依据
  `cmd/ssh-multihop/daemon.go:168-179`
- 问题: 外部 agent 可用时 daemon 根本不初始化内置 agent -> 显式 keys 列表不加载、
  不校验、不告警。此时配置写错（路径错/变量缺/口令错）完全无信号，forward 依赖
  外部 agent 里恰好有哪些 key 运气——正是 BCD G2 要消灭的"静默半可用"。
  触发不罕见：BCD 第 2 节场景含 suspend 恢复与手动启动，桌面会话里
  SSH_AUTH_SOCK 常在；unit 也可能向进程注入 SSH_AUTH_SOCK。
- 修复（二选一，写入 spec）: (a) strict 模式下 key 加载校验与 agent 选择解耦——
  无论外部 agent 是否存在，逐 key 加载校验并 fail fast（认证时外部 agent 优先的
  现状不变，内置 key 仅作 fallback）；(b) 显式声明"外部 agent 存在时跳过内置
  agent 与 key 校验"，并把 I1 限定为"无外部 agent 时"。推荐 (a)，保住 I1 原文。

### P2-1 事实错误: "cmd/passphrase-client 已不存在"不成立；删除清单漏项

- 位置: spec 第 5 节删除清单末条
- 问题: `cmd/passphrase-client/main.go` 存在于磁盘（纯 net.Dial，不 import
  internal/connection，删除 socket 不会造成编译断裂）。且 AGENTS.md:226 与
  docs/architecture.md:567-569 的记载并非"本已陈旧"——architecture.md:569 准确
  记录了"特性当前不可用……没有任何代码将 socket 注入 config builder"。按 G4
  "删除 PassphraseSocket 全链"，该 client 属于全链，应在删除清单内。
- 修复: 删除清单补 `cmd/passphrase-client/` 整目录；第 5 节表述改为"client
  存在但机制从未接通（architecture.md:569 已记载协议不匹配 + 无注入），一并删除"。
  顺带修正第 1 节背景：现状不是"需要人工推口令"，而是"推了也不生效"——这是
  删除风险为零的最强证据，值得写明。

### P2-2 "语义对齐 systemd EnvironmentFile=" 声明过强（虚标），四处差异未声明

- 位置: spec 第 3 节
- 问题: 对照 systemd.exec(5)（man7 最新版 + 本机 v259 一致）并用 v259 实测：
  (a) spec"允许可选 `export ` 前缀"——systemd **不支持** export 前缀，实测
  `export FOO=bar` 行被忽略（FOO 未设置）；这是超出 systemd 的扩展，不是对齐。
  (b) systemd 同时忽略 `;` 注释行，spec 只写了 `#`。(c) systemd 对"invalid
  content"（如未闭合引号）是服务启动失败，spec 只定义了"无 `=` 行跳过"，
  未定义非法引号行。(d) systemd 有反斜杠转义与多行引号规则；spec 的简化解析
  在含反斜杠/引号的口令值上与 systemd 分叉。后果：用户把同一 env 文件同时给
  unit 的 EnvironmentFile= 与 `--env-file` 时，两边变量集合/取值可能不一致，
  排查成本落在"唯一用户"身上。
- 修复: 要么改口为"参考 systemd 语法的子集 + 显式扩展"，逐条列差异；要么
  补齐语义（`;` 注释、未闭合引号 -> 启动失败、转义规则声明为不解析原样值）
  使声明成立。export 前缀保留与否都行，但必须标注为扩展。

### P2-3 模块改动表缺 builtin_agent.go 链路；单例装配语义未定

- 位置: spec 第 6 节模块改动表（internal/agent/agent.go 行）
- 问题: KeySpec 传入需要连改 4 个签名：`GetBuiltInAgent`（builtin_agent.go:32）、
  `newBuiltInAgent`（:41）、`BuiltInAgent.init`（:57）、`GoAgent.Initialize`
  （agent.go:41，现为无参，唯一调用方 builtin_agent.go:68——此处 spec 引用正确）。
  spec 只列了 agent.go。且 `globalBuiltInAgentOnce`（builtin_agent.go:34）是
  sync.Once 单例：首次调用决定初始化参数，builder.go:235 也会调
  GetBuiltInAgent——若实现者让 daemon 绕开单例直接 NewGoAgent+Initialize(specs)，
  单例会在首次连接时按现状收集重新初始化，strict 错误再次不可见。这是设计必须
  封死的分叉，不是实现细节。
- 修复: 模块表补 builtin_agent.go，写明参数唯一来源（如 daemon 启动时经单例
  入口注入 specs，builder 不传参只消费）。

### P2-4 ssh_dir"单点解析"（S2）的传播机制未定义，实现者被迫翻源码发明

- 位置: spec 第 2 节末条 + 第 6 节改动表最后三行
- 问题: 三个收敛点分属三个包：forward_service.go:1110（包内函数
  buildHopsFromSignature，sshConfigCache 以 configPath 为键）、
  ssh_helper.go:28（包级函数 BuildHopChainFromSSHConfig，且以函数值注入
  ForwardService.hopResolver，见 forward_service.go:88,119）、
  user_context.go:71（UserContext 方法）。改"由 ssh_dir 单点决定"需要签名变更
  或新的传递通道（注入 vs 包级变量），各有并发/可测性后果，spec 未定。契约
  文档迫使实现者翻实现源码才能确定行为 = 该处契约失败（design-review 入场
  程序第 3 步判据）。
- 修复: 明确传播方式与改动面（建议：BuildHopChainFromSSHConfig 改为接收
  configPath 的函数值并在 service.NewWithContext 装配时注入；UserContext 增加
  ssh_dir 覆盖字段），并把"改后 sshConfigCache 键不变导致旧路径缓存残留"这类
  生命周期点一并钉住。

### P2-5 测试计划判别力缺口：第 4 节的明确规则无用例

- 位置: spec 第 7 节
- 问题: (a) 第 4 节明确"passphrase_env 仅在 key 实际需要解密时读取；无口令 key
  而变量缺失 -> 不报错、不读该变量"——这是负向行为承诺，第 7 节 key_loader 用例
  列表没有它；pre-fix 实现（无口令 key 也去读变量）能全绿通过。(b) P1-1/P1-2
  修复后的 daemon 装配语义（strict 失败退出、外部 agent 场景）无用例。
  (c) env 文件"权限非 0600 -> WARN 不拒绝"无用例。
- 修复: 至少补 (a)；(b)(c) 随 P1 修复条款一起补。

### P2-6 "复用 expansion.go 现有逻辑"与现实有落差

- 位置: spec 第 2 节规则第 1 条
- 问题: internal/config/expansion.go 的 expandPath（:16）/expandTilde（:35）
  均未导出，daemoncfg 无法直接复用，需导出或复制；且 expandPath 含
  os.ExpandEnv（:28）——复用即引入 `$VAR` 展开，超出 spec 声明的 `~` 语义，
  需声明或规避。另外 `~user` 语法的 home 解析是 /home、/users 猜测式实现
  （:71-88），若一并复用需知悉。
- 修复: 写明"导出 config.ExpandTilde（仅 ~，不做 env 展开）"或接受 env 展开
  并写进语义。

## 事实核对结果表

spec/BCD 引用逐条对照（行号差异在 +/-3 行内记为锚点勘误，不构成 finding）：

| 引用 | 核对结果 |
|---|---|
| daemon.go `--passphrase-socket` flag（:99 附近） | 属实（:98-101） |
| daemon.go 默认路径解析（:132-137） | 属实（:132-138） |
| daemon.go socket 目录创建（:147-151） | 属实（:147-151） |
| daemon.go PassphraseSocket 启动/停止（:196-205, :203） | 属实（defer Stop 在 :203） |
| builder.go passphraseSocket 字段（:24） | 属实 |
| builder.go SetPassphraseSocket（:54-58） | 属实（:54-59） |
| builder.go GetPassphrase 调用链（:348-357） | 属实（block :347-370，关键行 :348/:357） |
| passphrase_socket.go + test 整文件删除 | 两文件均存在，可整删 |
| doc.go 移除 PassphraseSocket 描述 | 属实（doc.go:13） |
| cmd/passphrase-client 已不存在 | **不实**（cmd/passphrase-client/main.go 存在，见 P2-1） |
| 三处 ~/.ssh/config 硬编码（forward_service.go:1110 / ssh_helper.go:28 / user_context.go:71） | 全部属实，且仅此三处（builder.go:151 的默认 key 目录由 S2 单独覆盖，spec 第 6 节已列） |
| tryLoadKey（key_loader.go） | 属实（:69；现状对加密 key 返回 nil 跳过，:92 类型断言 PassphraseMissingError） |
| GoAgent.Initialize 现签名与调用链 | 属实（agent.go:41 无参；唯一调用方 builtin_agent.go:68）；但改造波及 builtin_agent.go 三个签名，spec 未列（P2-3） |
| go.mod yaml.v3 已在依赖树 | 属实（v3.0.1，indirect，go.mod:58） |
| ~ 展开复用 expansion.go | 半属实（逻辑存在、按 RUID HOME 展开 via util.UserHomeDir，userhome.go:23-28；但未导出 + 含 env 展开，见 P2-6） |
| SetPassphraseSocket 从未被接线 | 属实（全仓无调用方；architecture.md:569 早已记载"特性当前不可用"） |
| 删除 :156 启动日志字段 | spec 未列（含 passphrase_socket 字段的 Info 日志）——编译期可查的锚点级勘误，非 finding |
| .wolf/anatomy.md 提及 passphrase_injection_test.go | 该测试文件磁盘上不存在，.wolf 元数据陈旧（工具生成物，提示重新生成） |

## 可行性核对（四项专项）

1. **yaml.v3 KnownFields(true)**: 可行。`Decoder.KnownFields(enable bool)` 存在
   （v3.0.1，go doc 证实）。注意两点写入实现约束：必须走
   `yaml.NewDecoder(r)` + `KnownFields(true)` + `Decode(&cfg)`，`yaml.Unmarshal`
   无此能力；嵌套结构（keys 列表元素 KeySpec）同样受未知字段约束，`passphrase_env`
   需显式 `yaml:"passphrase_env"` tag（默认小写折叠会得到 passphraseenv）。
2. **ParseRawPrivateKey / PassphraseMissingError / WithPassphrase 顺序**: 可行。
   go doc 证实：加密 key 时 ParseRawPrivateKey 返回 PassphraseMissingError；
   错误口令时 ParseRawPrivateKeyWithPassphrase 返回 x509.IncorrectPasswordError
   （可区分）。判定顺序如 spec 所述成立。实现建议用 errors.As（builder.go:342
   有先例）而非 key_loader.go:92 的类型断言。
3. **systemd EnvironmentFile= 语义**: 部分一致（见 P2-2）。实测（本机 v259）：
   `FOO2="quoted val"` 引号剥离属实；`BAR=baz` 属实；`export FOO=bar` 行被
   systemd 忽略（spec 的 export 支持是扩展非对齐）；"无 = 行忽略"与 spec 的
   "跳过不失败"一致（WARN 是超集，无害）；"显式文件缺失 -> 失败"与 systemd
   无 `-` 前缀行为一致。spec 的 unit 模板只用 `--env-file`，不用 unit 的
   EnvironmentFile=，主路径不受 P2-2 影响。
4. **GoAgent.Initialize 改造**: 可行不破坏其他调用方（唯一调用方
   builtin_agent.go:68），但需按 P2-3 连带改 4 个签名并封死单例分叉；按 P1-1
   同步定义 daemon 装配层的错误语义，否则 I1 不可达。

## 线面与反例（第 1-3 层摘要）

- 方向层（线）: 症状（reboot 后认证失败）-> 机制（RUID HOME + 口令无来源）->
  根因（隐式解析无显式契约）。方案落在根因层：显式配置取代隐式解析。非绕行。
- 架构层（面，五格）: 消费方 = daemon 自身 + 唯一用户，S1 日志有消费者
  （premortem #1）；部署/回滚 = 第 8 节 unit 模板 + breaking 标注，回滚
  （revert 二进制 + 旧 unit）可行；失败邻座 = 删除 socket 不牵连 API/DB；
  退役 = G4 整体删除，无过渡层、无永久税；既有约束触碰 = SSH_AUTH_SOCK 优先
  （非目标，但与 I1 的交互漏定义 = P1-2）。
- 第 3 层反例（逐类构造，均不成立或已被 spec 覆盖）: 崩溃时序（无新增持久化点，
  无窗口）；文件序（config -> env -> agent 顺序由 BCD 第 3 节固定，env 注入
  先于一切 os.Getenv，含 GetAgentDir 的 XDG_RUNTIME_DIR）；双主角（启动期单线程
  加载，socket 字段删除后 builder 无共享可变态）；残留（旧 socket 文件 inert；
  旧 unit 传已删 flag -> 响亮报未知 flag，已登记）；生命周期闭环（listener/
  目录随整文件删除，无遗留清理者）；双重失败（config 与 env 任一失败即退出，
  无中间态）。唯一反例成立处 = P1-1/P1-2 的装配路径。
- 威胁模型: I4 凭据面收敛方向正确；口令不进日志（spec 第 4 节限定只记
  路径/变量名/数量，zap 与 x/crypto 错误串均不含口令值）；API 无口令暴露面
  （grep 证实 api 包无 passphrase 引用）。env 明文 = 未加密私钥的等价性声明
  （第 9 节）成立。已删组件 passphrase_socket.go:104 曾有"错误行原样进日志"
  的泄漏面，随删除消失。

## 正典老化 / 冲突

- AGENTS.md:226 项目结构列 cmd/passphrase-client —— 与磁盘一致（存在），非陈旧；
  spec 删除该目录后须同步更新 AGENTS.md（spec 已含 AGENTS.md 更新项，但前提
  表述有误，见 P2-1）。
- docs/architecture.md:567-569 对 PassphraseSocket 的记载是**准确的现状描述**
  （特性不可用 + 无注入），不是陈旧记载；它反而是支持 G4 删除的仓库内最强证据，
  spec 背景应引用它而非把它当包袱。
- docs/archive/plans/2026-03-17-architecture-improvements.md 为归档，不动，无冲突。
- 未发现 ADR x ADR 或 ADR x 现行设计冲突。

## 过度工程

无可删产物。daemoncfg 新包、KnownFields 严格校验（服务 G2 fail-fast）、权限
WARN（一行成本）、unit 模板（文档一行）均有直接业务对应。S2 三处收敛与本次
根因同源，不算搭车重构。"keys: [] 等同未配置"防脚枪规则成本一行为判定，保留。

## 疑点待核（不占 verdict，供作者定向补证）

1. 第 8 节 unit 模板 `--db /opt/forward/mydb.db` 与 `User=shih`：/opt/forward
   对 shih 的写权限是实机细节，vmr.dp 验收时确认。
2. 口令经 os.Setenv 注入后将驻留进程 environ（/proc/pid/environ 对同用户可读）
   至退出——第 9 节等价性声明可覆盖；可选 hardening：agent 初始化完成后对已知
   口令变量 os.Unsetenv，成本数行，作者定夺。
3. 第 7 节实机验收（vmr.dp 建 -R 通道）的可达性与执行结果，审查环境无法验证。

## 未核区（显式列出，非默认干净）

- internal/config/parser（kevinburke/ssh_config）对 IdentityFile `~` 的展开行为
  未深查（premortem #2 已用文档缓解，且 keys 显式模式不经过该路径）。
- systemd "invalid content"（未闭合引号）的实测失败行为未跑（以 man 页语义为准）。
- research/ 目录历史运行日志未逐条审（与本 spec 无契约关系）。
- api 包逐文件未读（grep 证实无 passphrase 引用，I4 的 API 面判定基于全仓 grep）。
