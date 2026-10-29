# Plan: E2E_SLOW 静默归类（协议沉默 = 链路健康）

- 基分支: master @ d2caef4；worktree `.worktree/e2e-slow-silent`；分支 `fix/e2e-slow-silent-classification`
- 后果面: A 观测面。最坏后果 = 依赖 `E2E probe inconclusive` WARN 做日志告警的运维读数失明（SLOW 本就不触发重建/错误状态，健康判定不变）
- 通道: 紧凑全门（档位判定与命中行见 ledger）
- Oracle 环境: go1.26.1 linux/amd64；tier1 `go test ./internal/forwarding`；tier2（最高/合并门槛）`make check`

## 契约三锚

**1. 规范逐字原文**（user 裁决 2026-10-29，本会话）:

> A. 静默归类 | SLOW 改判为健康（连接已证明链路通），日志降为 Info……理由：今天 SLOW 本来就不会把 forward 判死，只是刷 WARN 噪音。

**实施偏差登记（一处）**: 早期口头口径「日志降为 Info」→ 实施定为「SLOW 成功路径零日志」。理由：逐探针 Info（每 forward 每 45-90s 一条）本身即新噪音源，与 user 诉求「消除噪音」矛盾；成功路径与 E2E_OPEN 同权静默。PR 描述向 user 复述此偏差。

**2. 代码锚点**（@d2caef4 实测行号）:
- `internal/forwarding/e2e_probe.go:96-97` — SLOW case 返回 errE2EUnknown（本次唯一实现改动点）
- `internal/forwarding/e2e_probe.go:19-21` — errE2EUnknown 注释把 "slow target" 列为 unknown 成因（需同步）
- `internal/forwarding/e2e_probe_test.go:118-127` — canned "E2E_SLOW" 断言 ErrorIs errE2EUnknown
- `internal/forwarding/e2e_probe_test.go:188-190` — real-path silent 服务断言 ErrorIs errE2EUnknown
- 监控调用点三处**不改动**: `local_listen_to_remote.go:591`（connect-only 无 SLOW）、`remote_listen_to_local.go:687`、`remote_listen_to_remote.go:733`（后两者 unknown 分支保留给真超时/exec 失败）

**3. 逐例期望**:
| 用例 | 改前 | 改后 |
|---|---|---|
| canned `E2E_SLOW` 输出（test:118-127） | ErrorIs errE2EUnknown | **NoError**（红→绿） |
| real-path silent 服务（test:188-190） | ErrorIs errE2EUnknown | **NoError**（红→绿） |
| canned `E2E_FAIL`（test:96-106） | "e2e probe failed" 非 unknown | 不变 |
| 探针超时（test:108-116） | ErrorIs errE2EUnknown | 不变 |
| banner 目标 / dead 目标（test:180-187） | NoError / death | 不变 |
| policy refusal（test:151-159） | ErrorIs errE2EUnknown | 不变 |
| 生产面 | vmr.dp:8888/4000、ci1:27017 每 45-90s 刷 WARN | 静默；链路真死（连接拒绝）仍置 error 状态 |

## 原子任务

**T1（红）**: `e2e_probe_test.go` 两处断言改 NoError；测试名/消息同步（`SlowCannedOutputYieldsUnknown`→`...YieldsAlive`；case 名 "silent target unknown"→"silent target alive"）。
canned 测试内第二处断言 `:126 assert.NotContains(t, err.Error(), "e2e probe failed")` **随契约一并删除**（err 为 nil 后无断言对象，保留必 panic）。
验证: `go test ./internal/forwarding -run 'TestProbeThroughTunnel_Slow|TestE2EProbeRealPath' -v` → 恰好 2 个测试单元红（1 顶层 canned + 1 子测试 silent；父测试 TestE2EProbeRealPath 自身的 FAIL 行属预期输出，不计入）。红证据存 `runs/t1-red.log`。

**T1 修订（实施中发现，盲审 r1 后追加）**: real-path runner（test:198）传给 probeThroughTunnel 的预算为 250ms，而 `read -t 2` 需 2s——旧断言（ErrorIs unknown）靠整体超时空洞通过，SLOW 分类从未端到端可达。修订：预算 250ms→3s（仅该测试调用点；生产 e2eProbeTimeout=5s 不变）。此为 oracle 能力修正而非放宽：断言从「超时必 unknown」改为「静默必 alive」，判别力增强。预算变更使仪器变更 → 红证据重新基线（t1-red.log 标 superseded，新红证据 t1-red-v2.log，红绿同源以 v2 为准）。

**T2（绿）**: `e2e_probe.go:96-97` SLOW case 改 `return nil`；:19-21 注释去掉 "slow target"、改述「连接已建立即全链路已证，静默协议不发首字节」；SLOW case 加一行 why 注释（代码无法自明的语义裁决）。
验证: tier1 全绿（红→绿同源）；`make check` 全绿。

## 不修清单

| 项 | file:line | 理由 |
|---|---|---|
| 探针模板与 read -t 2 语义 | e2e_probe.go:29 | 探针机制不变，仅分类变 |
| 三处监控调用点 unknown 分支 | :591/:687/:733 | 真超时/exec 失败仍 WARN，职责不变 |
| E2E_FAIL → error 状态路径 | 调用点 else 分支 | 链路死判定不变 |
| probeTargetThroughClient 全部 | e2e_probe.go:106-123 | connect-only，无 SLOW 概念 |
| 探针间隔 45-90s | e2e_probe.go:31-38 | 与本裁决正交 |
| 方案 B（发包探针/新配置面） | - | user 已否决 |

## 影响面自查

①对外契约: 无（无 API/格式变化）。②信任边界: 无。③热路径: 否（周期探针）。④业务规则: 否（SLOW 从不参与重建/禁决决策）。

## 语义边界（审者重点核对）

SLOW=alive 的新契约只覆盖「bash /dev/tcp 连接成功但 2s 无数据」这一种输出；exec 失败、probe 整体超时、无法解析输出仍必须走 errE2EUnknown。改判不得吞并这三类。
