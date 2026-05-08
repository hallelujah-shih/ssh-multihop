# SSH-Multihop Multiplexing Architecture Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 从"单一隧道"架构演进到"连接复用"架构，通过引入 ConnectionManager 连接池实现多隧道共享单一物理 SSH 链路，降低资源占用并提升系统性能。

**Architecture:** 引入全局 ConnectionManager 作为连接池层，通过特征哈希（`user@host:port + [hop_chain]`）管理物理连接的生命周期。Forward 实例不再直接持有 SSH 连接，而是向 ConnectionManager 订阅/取消订阅连接。连接池使用引用计数和延迟回收（Lingering）机制管理连接生命周期，健康检查从 Forward 层提升到 ConnectionManager 层统一管理。

**Tech Stack:**
- Go 1.25
- golang.org/x/crypto/ssh (SSH 协议和 Channel 复用)
- golang.org/x/sync/singleflight (避免缓存击穿)
- github.com/stretchr/testify (单元测试)
- existing: GORM + SQLite (数据库层)

**Review 集成:** 本计划已整合以下review建议（详见 `docs/plans/2026-03-17-multiplexing-review-analysis.md`）：
- ✅ **Review A**: 使用 singleflight 避免缓存击穿（Task 2.2）
- ✅ **Review B**: 添加 TCP_NODELAY 选项（扩展 SetTCPKeepalive）
- ⚠️ **Review C**: 非交互式认证不在 ConnectionManager 层处理
- ✅ **Review D**: API 统计增强（Task 6.2）

---

## 当前实现状态 (2026-03-17)

### ✅ 已实现（无需实施）

以下功能已在当前架构中实现，本计划**不涉及**这些内容：

1. **并发安全（Pending Starts）**
   - 位置: `internal/service/forward_service.go`
   - 实现: `pendingStarts` 映射 + `pendingMu` 互斥锁
   - 功能: 防止 ForwardService 重复启动竞态

2. **双向复制修复（bidirectionalCopy）**
   - 位置: `internal/forwarding/util.go:79`
   - 功能: 修复半关闭场景的连接挂起问题

3. **TCP Keepalive 配置**
   - 位置: `internal/util/tcp_linux.go`, `internal/connection/establisher.go:64`
   - 功能: 30秒检测死连接（15s + 3×5s）

4. **数据库查询优化（JOIN）**
   - 位置: `internal/db/database.go:179`
   - 功能: `ListForwardsWithStatus()` 使用 LEFT JOIN，O(1)查询

### ❌ 未实现（本计划目标）

连接复用（Multiplexing）的核心功能全部未实现：

| Phase | 任务 | 预估工时 |
|-------|------|---------|
| Phase 1 | 连接签名和元数据 | 2-3 小时 |
| Phase 2 | ConnectionManager 核心 | 6-8 小时 |
| Phase 3 | Forward 层集成 | 4-6 小时 |
| Phase 4 | ForwardService 集成 | 2-3 小时 |
| Phase 5 | 测试和验证 | 4-5 小时 |
| Phase 6 | 文档和监控 | 2-3 小时 |
| **总计** | **MVP** | **约 20-28 小时** |

### Review 总结

本计划已整合以下review建议：

| Review | 建议 | 优先级 | 实施位置 |
|--------|------|--------|---------|
| **A** | 使用 singleflight 避免缓存击穿 | **P0** | Task 2.2 |
| **B** | 添加 TCP_NODELAY 选项 | P1 | 扩展 SetTCPKeepalive |
| **C** | 非交互式认证限制 | P2 | Builder 层（不在本计划） |
| **D** | API 统计增强（延迟、成功率） | **P1** | Task 6.2 |

**关键改进**：
- ✅ Singleflight: 10个连接从30秒降到3秒
- ✅ TCP_NODELAY: 减少SSH交互延迟40-200ms
- ✅ API监控: 连接延迟P95/P99、Channel成功率

---

## 目录结构变更

**新增文件：**
- `internal/connection/pool.go` - ConnectionManager 实现
- `internal/connection/pool_test.go` - ConnectionManager 单元测试
- `internal/connection/multiplexed_forward.go` - 复用转发的抽象层
- `internal/connection/health_checker.go` - 统一健康检查器
- `internal/connection/signature.go` - 连接特征哈希计算

**修改文件：**
- `internal/connection/connection.go` - 扩展连接元数据
- `internal/forwarding/forward.go` - 使用连接池
- `internal/forwarding/local_listen_to_remote.go` - 集成连接池
- `internal/forwarding/remote_listen_to_local.go` - 集成连接池
- `internal/forwarding/remote_listen_to_remote.go` - 集成连接池
- `internal/service/forward_service.go` - 管理 ConnectionManager 生命周期
- `cmd/ssh-multihop/main.go` - 初始化 ConnectionManager

---

## Phase 1: Connection Signature 和基础数据结构

### Task 1.1: 实现连接特征签名（Connection Signature）

**Files:**
- Create: `internal/connection/signature.go`
- Test: `internal/connection/signature_test.go`

**验收标准：**
- 相同的 user@host:port 和 hop_chain 生成相同的签名
- 不同的 user、host、port 或 hop_chain 生成不同的签名
- 签名是确定性的（多次调用相同输入产生相同输出）
- 签名格式适合作为 map key

**Step 1: 设计签名数据结构**

```go
// 伪码
type ConnectionSignature struct {
    Username string
    Hostname string
    Port     int
    // Jump hosts 表示跳板链路
    // 例如: local -> jump1 -> jump2 -> target
    // JumpChain = ["jump1", "jump2"]
    JumpChain []string
}

// Hash() 生成唯一标识符，格式示例:
// "user@host:port:[jump1,jump2]" 或使用 SHA256
func (s ConnectionSignature) Hash() string {
    // 实现确定性哈希
}
```

**Step 2: 编写测试用例**

```go
// 伪码
func TestSignatureHash_Deterministic(t *testing.T) {
    sig := ConnectionSignature{
        Username: "root",
        Hostname: "example.com",
        Port:     22,
        JumpChain: []string{"jump1", "jump2"},
    }

    hash1 := sig.Hash()
    hash2 := sig.Hash()

    assert.Equal(t, hash1, hash2)
}

func TestSignatureHash_DifferentInputs(t *testing.T) {
    sig1 := ConnectionSignature{Username: "user1", Hostname: "host", Port: 22}
    sig2 := ConnectionSignature{Username: "user2", Hostname: "host", Port: 22}

    assert.NotEqual(t, sig1.Hash(), sig2.Hash())
}

func TestSignatureHash_SameInputs(t *testing.T) {
    sig1 := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}
    sig2 := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    assert.Equal(t, sig1.Hash(), sig2.Hash())
}
```

**Step 3: 实现签名哈希算法**

```go
// 伪码
func (s ConnectionSignature) Hash() string {
    // 使用 SHA256 或确定性字符串拼接
    // 格式: "user@host:port:jump1,jump2"
    // 确保空 jump chain 处理正确
}
```

**Step 4: 运行测试验证**

```bash
go test ./internal/connection -run TestSignature -v
```

**Step 5: 提交**

```bash
git add internal/connection/signature.go internal/connection/signature_test.go
git commit -m "feat(connection): add connection signature for multiplexing support"
```

---

### Task 1.2: 扩展连接元数据（Connection Metadata）

**Files:**
- Modify: `internal/connection/connection.go`

**验收标准：**
- 连接结构包含签名信息
- 连接记录创建时间戳
- 连接包含引用计数器
- 连接包含状态字段（active、idle、closed）

**Step 1: 扩展连接结构**

```go
// 伪码
type PooledConnection struct {
    // 嵌入原有 SSH client
    Client *ssh.Client

    // 元数据
    Signature    ConnectionSignature
    CreatedAt    time.Time
    LastUsedAt   time.Time
    Status       ConnectionStatus  // enum: Active, Idle, Closed
    RefCount     int               // 引用计数
    Context      context.Context   // 用于级联取消
    CancelFunc   context.CancelFunc // 取消函数

    // 保护并发访问
    mu sync.RWMutex
}

func (pc *PooledConnection) Acquire() error {
    // 原子增加引用计数
    // 更新 LastUsedAt
}

func (pc *PooledConnection) Release() error {
    // 原子减少引用计数
    // 如果计数归零，启动延迟回收定时器
}

func (pc *PooledConnection) IsActive() bool {
    // 检查连接是否活跃
}
```

**Step 2: 编写测试验证并发安全**

```go
// 伪码
func TestPooledConnection_RefCountConcurrency(t *testing.T) {
    conn := &PooledConnection{RefCount: 0}

    // 并发获取
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            conn.Acquire()
        }()
    }
    wg.Wait()

    assert.Equal(t, 100, conn.RefCount)
}
```

**Step 3: 运行测试**

```bash
go test ./internal/connection -run TestPooledConnection -v
```

**Step 4: 提交**

```bash
git add internal/connection/connection.go
git commit -m "feat(connection): add pooled connection with reference counting"
```

---

## Phase 2: ConnectionManager 核心实现

### Task 2.1: 实现 ConnectionManager 基础结构

**Files:**
- Create: `internal/connection/pool.go`
- Test: `internal/connection/pool_test.go`

**验收标准：**
- ConnectionManager 维护连接池 map
- 支持通过签名获取或创建连接
- 支持订阅/取消订阅连接
- 线程安全（使用 sync.RWMutex）

**Step 1: 定义 ConnectionManager 结构**

```go
// 伪码
type ConnectionManager struct {
    // 连接池：signature hash -> PooledConnection
    pools map[string]*PooledConnection

    // 保护并发访问
    mu sync.RWMutex

    // 配置
    config PoolConfig

    // 健康检查器
    healthChecker *HealthChecker
}

type PoolConfig struct {
    // 连接空闲超时后延迟回收时间
    IdleTimeout time.Duration  // 默认: 60秒

    // 最大空闲连接数
    MaxIdleConnections int  // 默认: 10

    // 连接建立超时
    DialTimeout time.Duration  // 默认: 30秒
}

func NewConnectionManager(config PoolConfig) *ConnectionManager {
    // 初始化连接池
    // 启动后台清理 goroutine
}
```

**Step 2: 实现核心方法签名**

```go
// 伪码
// Acquire 获取或创建连接
func (cm *ConnectionManager) Acquire(ctx context.Context, sig ConnectionSignature) (*PooledConnection, error)

// Release 释放连接引用
func (cm *ConnectionManager) Release(conn *PooledConnection) error

// Close 关闭所有连接
func (cm *ConnectionManager) Close() error

// Stats 获取连接池统计信息
func (cm *ConnectionManager) Stats() PoolStats
```

**Step 3: 编写基础测试**

```go
// 伪码
func TestConnectionManager_AcquireSameSignature(t *testing.T) {
    cm := NewConnectionManager(DefaultConfig())

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    conn1, err := cm.Acquire(context.Background(), sig)
    assert.NoError(t, err)

    conn2, err := cm.Acquire(context.Background(), sig)
    assert.NoError(t, err)

    // 应该返回同一个连接实例
    assert.Same(t, conn1, conn2)
    assert.Equal(t, 2, conn1.RefCount)
}
```

**Step 4: 运行测试（预期失败，方法未实现）**

```bash
go test ./internal/connection -run TestConnectionManager -v
```

**Step 5: 提交**

```bash
git add internal/connection/pool.go internal/connection/pool_test.go
git commit -m "feat(connection): add ConnectionManager structure and test skeleton"
```

---

### Task 2.2: 实现 Acquire 方法

**Files:**
- Modify: `internal/connection/pool.go`
- Modify: `internal/connection/pool_test.go`

**验收标准：**
- 已存在的连接直接返回并增加引用计数
- 不存在的连接创建新连接
- 连接创建失败返回错误
- 并发 Acquire 安全

**Step 1: 实现 Acquire 方法（使用 Singleflight）**

```go
// 伪码 - 集成 review 建议：使用 singleflight 避免缓存击穿
import "golang.org/x/sync/singleflight"

type ConnectionManager struct {
    pools map[string]*PooledConnection
    mu    sync.RWMutex

    // singleflight 组（避免并发创建相同连接）
    dialGroup singleflight.Group

    config        PoolConfig
    healthChecker *HealthChecker
}

func (cm *ConnectionManager) Acquire(ctx context.Context, sig ConnectionSignature, forwardID string) (*PooledConnection, error) {
    hash := sig.Hash()

    // 快速路径：读锁检查已存在的连接
    cm.mu.RLock()
    if conn, exists := cm.pools[hash]; exists {
        cm.mu.RUnlock()
        if err := conn.Acquire(); err != nil {
            return nil, err
        }
        return conn, nil
    }
    cm.mu.RUnlock()

    // 使用 singleflight 执行 dialSSH（避免缓存击穿）
    result, err, shared := cm.dialGroup.Do(hash, func() (interface{}, error) {
        // 获取写锁（时间很短）
        cm.mu.Lock()
        defer cm.mu.Unlock()

        // Double-check：可能已被其他 goroutine 创建
        if conn, exists := cm.pools[hash]; exists {
            return conn, nil
        }

        // 慢操作：dialSSH（不持有 cm.mu，允许其他签名并发）
        sshClient, dialErr := cm.dialSSH(ctx, sig)
        if dialErr != nil {
            // 失败时忘记结果，允许重试
            cm.dialGroup.Forget(hash)
            return nil, fmt.Errorf("failed to dial SSH: %w", dialErr)
        }

        // 创建 PooledConnection（持有 cm.mu）
        connCtx, connCancel := context.WithCancel(context.Background())
        conn := &PooledConnection{
            Client:     sshClient,
            Signature:  sig,
            CreatedAt:  time.Now(),
            LastUsedAt: time.Now(),
            Status:     StatusActive,
            RefCount:   1,
            Context:    connCtx,
            CancelFunc: connCancel,
        }

        // 加入连接池
        cm.pools[hash] = conn

        // 启动健康检查
        cm.healthChecker.Monitor(conn)

        return conn, nil
    })

    if err != nil {
        return nil, err
    }

    conn := result.(*PooledConnection)

    // 如果结果被共享，需要增加引用计数
    if shared {
        conn.Acquire()
    }

    // 注册 forward 到健康检查器
    cm.healthChecker.RegisterForward(conn, forwardID)

    return conn, nil
}

// dialSSH 建立实际 SSH 连接
// review 建议：设置 TCP_NODELAY 和 KeepAlive 选项
func (cm *ConnectionManager) dialSSH(ctx context.Context, sig ConnectionSignature) (*ssh.Client, error) {
    // 复用现有的 connection.Establisher 逻辑
    // 或使用 net.Dialer.Control 设置 socket 选项

    // 示例：使用现有的 Establisher
    hops := buildHopsFromSignature(sig)
    client, _, err := cm.establisher.Establish(hops...)
    return client, err
}
```

**Step 2: 更新测试用例**

```go
// 伪码
func TestConnectionManager_AcquireCreatesNew(t *testing.T) {
    // 使用 mock SSH client
    cm := NewConnectionManager(DefaultConfig())
    // ... 设置 mock ...

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    conn, err := cm.Acquire(context.Background(), sig)
    assert.NoError(t, err)
    assert.NotNil(t, conn)
    assert.Equal(t, 1, conn.RefCount)
}

func TestConnectionManager_AcquireReusesExisting(t *testing.T) {
    cm := NewConnectionManager(DefaultConfig())

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    conn1, _ := cm.Acquire(context.Background(), sig)
    conn2, _ := cm.Acquire(context.Background(), sig)

    assert.Same(t, conn1, conn2)
    assert.Equal(t, 2, conn1.RefCount)
}
```

**Step 3: 运行测试验证**

```bash
go test ./internal/connection -run TestConnectionManager_Acquire -v
```

**Step 4: 提交**

```bash
git add internal/connection/pool.go internal/connection/pool_test.go
git commit -m "feat(connection): implement Acquire method with connection reuse"
```

---

### Task 2.3: 实现 Release 方法和延迟回收

**Files:**
- Modify: `internal/connection/pool.go`
- Modify: `internal/connection/pool_test.go`

**验收标准：**
- Release 减少引用计数
- 引用计数归零时进入 Idle 状态
- Idle 超时后关闭连接
- 期间有新 Acquire 则取消关闭定时器

**Step 1: 实现 Release 方法**

```go
// 伪码
func (cm *ConnectionManager) Release(conn *PooledConnection) error {
    conn.mu.Lock()
    defer conn.mu.Unlock()

    // 减少引用计数
    conn.RefCount--
    conn.LastUsedAt = time.Now()

    // 如果还有引用，直接返回
    if conn.RefCount > 0 {
        return nil
    }

    // 引用计数归零，进入 Idle 状态
    conn.Status = StatusIdle

    // 启动延迟回收定时器
    go cm.lingerAndClose(conn)

    return nil
}

func (cm *ConnectionManager) lingerAndClose(conn *PooledConnection) {
    // 等待 IdleTimeout
    select {
    case <-time.After(cm.config.IdleTimeout):
        // 超时，关闭连接
        cm.closeConnection(conn)

    case <-conn.Context.Done():
        // 连接被重新使用（Context 被取消）
        return
    }
}

func (cm *ConnectionManager) closeConnection(conn *PooledConnection) {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    hash := conn.Signature.Hash()

    // 再次检查引用计数（可能已被重新获取）
    if conn.RefCount > 0 {
        return
    }

    // 从连接池移除
    delete(cm.pools, hash)

    // 关闭 SSH 连接
    conn.Client.Close()
    conn.Status = StatusClosed
    conn.CancelFunc()
}
```

**Step 2: 更新 Acquire 以取消延迟回收**

```go
// 伪码 - 在 Acquire 方法中添加
if conn, exists := cm.pools[hash]; exists {
    cm.mu.RUnlock()

    // 如果连接处于 Idle 状态，取消关闭定时器
    if conn.Status == StatusIdle {
        conn.CancelFunc()  // 取消之前的 context

        // 创建新的 context
        newCtx, newCancel := context.WithCancel(context.Background())
        conn.Context = newCtx
        conn.CancelFunc = newCancel
        conn.Status = StatusActive
    }

    if err := conn.Acquire(); err != nil {
        return nil, err
    }

    return conn, nil
}
```

**Step 3: 编写测试验证延迟回收**

```go
// 伪码
func TestConnectionManager_ReleaseWithLingering(t *testing.T) {
    cm := NewConnectionManager(PoolConfig{IdleTimeout: 100 * time.Millisecond})

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    conn, _ := cm.Acquire(context.Background(), sig)
    assert.Equal(t, 1, conn.RefCount)

    // 释放连接
    cm.Release(conn)
    assert.Equal(t, 0, conn.RefCount)
    assert.Equal(t, StatusIdle, conn.Status)

    // 连接仍应存在（lingering）
    cm.mu.RLock()
    _, exists := cm.pools[sig.Hash()]
    cm.mu.RUnlock()
    assert.True(t, exists)

    // 等待超时
    time.Sleep(150 * time.Millisecond)

    // 连接应该被关闭
    cm.mu.RLock()
    _, exists = cm.pools[sig.Hash()]
    cm.mu.RUnlock()
    assert.False(t, exists)
}

func TestConnectionManager_AcquireDuringLingering(t *testing.T) {
    cm := NewConnectionManager(PoolConfig{IdleTimeout: 500 * time.Millisecond})

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    conn1, _ := cm.Acquire(context.Background(), sig)
    cm.Release(conn1)

    // 在 lingering 期间重新获取
    time.Sleep(100 * time.Millisecond)
    conn2, _ := cm.Acquire(context.Background(), sig)

    // 应该是同一个连接
    assert.Same(t, conn1, conn2)
    assert.Equal(t, 1, conn2.RefCount)
    assert.Equal(t, StatusActive, conn2.Status)
}
```

**Step 4: 运行测试验证**

```bash
go test ./internal/connection -run TestConnectionManager_Release -v
```

**Step 5: 提交**

```bash
git add internal/connection/pool.go internal/connection/pool_test.go
git commit -m "feat(connection): implement Release with lingering timeout"
```

---

### Task 2.4: 实现统一健康检查器

**Files:**
- Create: `internal/connection/health_checker.go`
- Test: `internal/connection/health_checker_test.go`

**验收标准：**
- 健康检查器监控所有活动连接
- 检测失败时级联取消连接的所有订阅者
- 可配置检查间隔（默认 15 秒）
- 健康检查失败时更新数据库状态

**Step 1: 定义健康检查器结构**

```go
// 伪码
type HealthChecker struct {
    // 监控的连接：connection ID -> connection
    monitoredConnections map[string]*PooledConnection

    // 受影响的 forwards：connection ID -> []forwardID
    // 当连接失效时，需要通知所有使用该连接的 forward
    affectedForwards map[string][]string

    mu sync.RWMutex

    // 健康检查配置
    config HealthCheckConfig
}

type HealthCheckConfig struct {
    Interval time.Duration  // 默认: 15秒
    Timeout  time.Duration  // 默认: 5秒
}

func NewHealthChecker(config HealthCheckConfig) *HealthChecker {
    hc := &HealthChecker{
        monitoredConnections: make(map[string]*PooledConnection),
        affectedForwards:     make(map[string][]string),
        config:               config,
    }

    // 启动健康检查循环
    go hc.checkLoop()

    return hc
}
```

**Step 2: 实现核心方法**

```go
// 伪码
// Monitor 开始监控连接
func (hc *HealthChecker) Monitor(conn *PooledConnection) {
    hc.mu.Lock()
    defer hc.mu.Unlock()

    hash := conn.Signature.Hash()
    hc.monitoredConnections[hash] = conn
}

// Unregister 停止监控连接
func (hc *HealthChecker) Unregister(conn *PooledConnection) {
    hc.mu.Lock()
    defer hc.mu.Unlock()

    hash := conn.Signature.Hash()
    delete(hc.monitoredConnections, hash)
}

// RegisterForward 注册 forward 到连接的映射
func (hc *HealthChecker) RegisterForward(conn *PooledConnection, forwardID string) {
    hc.mu.Lock()
    defer hc.mu.Unlock()

    hash := conn.Signature.Hash()
    hc.affectedForwards[hash] = append(hc.affectedForwards[hash], forwardID)
}

// checkLoop 健康检查循环
func (hc *HealthChecker) checkLoop() {
    ticker := time.NewTicker(hc.config.Interval)
    defer ticker.Stop()

    for range ticker.C {
        hc.checkAllConnections()
    }
}

func (hc *HealthChecker) checkAllConnections() {
    hc.mu.RLock()
    connections := make([]*PooledConnection, 0, len(hc.monitoredConnections))
    for _, conn := range hc.monitoredConnections {
        connections = append(connections, conn)
    }
    hc.mu.RUnlock()

    for _, conn := range connections {
        if err := hc.checkConnection(conn); err != nil {
            hc.handleFailure(conn, err)
        }
    }
}

func (hc *HealthChecker) checkConnection(conn *PooledConnection) error {
    ctx, cancel := context.WithTimeout(context.Background(), hc.config.Timeout)
    defer cancel()

    // 发送 SSH keepalive 或执行简单命令
    // 方法1: 使用 Client.SendRequest 发送 keepalive@openssh.com
    // 方法2: 执行简单命令如 "echo 1"

    // 伪码
    _, err := conn.Client.SendRequest("keepalive@openssh.com", true, nil)
    return err
}

func (hc *HealthChecker) handleFailure(conn *PooledConnection, err error) {
    conn.CancelFunc()  // 级联取消所有使用该连接的 forwards

    // 通知所有受影响的 forwards 设置错误状态
    hash := conn.Signature.Hash()

    hc.mu.RLock()
    forwardIDs := hc.affectedForwards[hash]
    hc.mu.RUnlock()

    for _, forwardID := range forwardIDs {
        // 更新数据库状态为 error
        // db.UpdateForwardStatus(forwardID, "error", err.Error())
    }
}
```

**Step 3: 编写测试**

```go
// 伪码
func TestHealthChecker_DetectsFailure(t *testing.T) {
    // 使用 mock SSH client 模拟失败
    hc := NewHealthChecker(HealthCheckConfig{Interval: 100 * time.Millisecond})

    mockConn := &PooledConnection{
        // Mock client that returns error
    }

    hc.Monitor(mockConn)

    // 等待健康检查
    time.Sleep(200 * time.Millisecond)

    // 验证失败被检测到
    // 验证 Context 被取消
}

func TestHealthChecker_RegisterForward(t *testing.T) {
    hc := NewHealthChecker(DefaultHealthCheckConfig())

    conn := &PooledConnection{Signature: ConnectionSignature{...}}
    hc.RegisterForward(conn, "forward-1")
    hc.RegisterForward(conn, "forward-2")

    hc.mu.RLock()
    forwards := hc.affectedForwards[conn.Signature.Hash()]
    hc.mu.RUnlock()

    assert.Len(t, forwards, 2)
}
```

**Step 4: 运行测试**

```bash
go test ./internal/connection -run TestHealthChecker -v
```

**Step 5: 提交**

```bash
git add internal/connection/health_checker.go internal/connection/health_checker_test.go
git commit -m "feat(connection): add unified health checker for connection pool"
```

---

### Task 2.5: 实现 ConnectionManager 集成健康检查

**Files:**
- Modify: `internal/connection/pool.go`
- Modify: `internal/connection/pool_test.go`

**验收标准：**
- ConnectionManager 持有 HealthChecker 实例
- Acquire 时自动注册健康检查
- Release 时取消健康检查
- Forward 注册到 HealthChecker 以支持级联失败

**Step 1: 更新 ConnectionManager 初始化**

```go
// 伪码
func NewConnectionManager(config PoolConfig) *ConnectionManager {
    hc := NewHealthChecker(HealthCheckConfig{
        Interval: 15 * time.Second,
        Timeout:  5 * time.Second,
    })

    return &ConnectionManager{
        pools:         make(map[string]*PooledConnection),
        mu:            sync.RWMutex{},
        config:        config,
        healthChecker: hc,
    }
}
```

**Step 2: 更新 Acquire 注册 forward**

```go
// 伪码
func (cm *ConnectionManager) Acquire(ctx context.Context, sig ConnectionSignature, forwardID string) (*PooledConnection, error) {
    // ... 获取或创建连接 ...

    // 注册 forward 到健康检查器
    cm.healthChecker.RegisterForward(conn, forwardID)

    return conn, nil
}
```

**Step 3: 更新 Release 清理**

```go
// 伪码
func (cm *ConnectionManager) Release(conn *PooledConnection, forwardID string) error {
    // 从健康检查器移除 forward
    cm.healthChecker.UnregisterForward(conn, forwardID)

    // 减少引用计数...
}
```

**Step 4: 编写集成测试**

```go
// 伪码
func TestConnectionManager_HealthCheckIntegration(t *testing.T) {
    cm := NewConnectionManager(DefaultConfig())

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    conn1, _ := cm.Acquire(context.Background(), sig, "forward-1")
    conn2, _ := cm.Acquire(context.Background(), sig, "forward-2")

    // 验证两个 forward 都注册到同一个连接
    // 模拟连接失败
    // 验证两个 forward 的 context 都被取消
}
```

**Step 5: 运行测试**

```bash
go test ./internal/connection -run TestConnectionManager_HealthCheck -v
```

**Step 6: 提交**

```bash
git add internal/connection/pool.go internal/connection/pool_test.go
git commit -m "feat(connection): integrate health checker with connection manager"
```

---

## Phase 3: Forward 层集成连接池

### Task 3.1: 创建 MultiplexedForward 抽象

**Files:**
- Create: `internal/connection/multiplexed_forward.go`

**验收标准：**
- 定义 Forward 使用连接池的接口
- 提供 NewStream 方法创建 SSH Channel
- 提供 Close 方法释放连接

**Step 1: 定义 MultiplexedForward 接口**

```go
// 伪码
// MultiplexedForward 表示使用连接池的转发
type MultiplexedForward struct {
    // 连接管理器
    pool *ConnectionManager

    // 连接签名
    signature ConnectionSignature

    // Forward ID（用于健康检查注册）
    forwardID string

    // 当前持有的连接
    currentConn *PooledConnection

    // Context 用于取消
    ctx context.Context
    cancel context.CancelFunc
}

// NewMultiplexedForward 创建新的复用转发
func NewMultiplexedForward(pool *ConnectionManager, sig ConnectionSignature, forwardID string) *MultiplexedForward {
    ctx, cancel := context.WithCancel(context.Background())

    return &MultiplexedForward{
        pool:      pool,
        signature: sig,
        forwardID: forwardID,
        ctx:       ctx,
        cancel:    cancel,
    }
}

// Connect 获取连接并创建 SSH Channel
func (mf *MultiplexedForward) Connect() (net.Conn, error) {
    // 从连接池获取连接
    conn, err := mf.pool.Acquire(mf.ctx, mf.signature, mf.forwardID)
    if err != nil {
        return nil, err
    }

    mf.currentConn = conn

    // 创建新的 SSH Channel
    // 这不会创建新的 TCP 连接，只是在现有 SSH 连接上创建通道
    sshConn, streams, err := conn.Client.OpenChannel("direct-streamlocal@openssh.com", nil)
    if err != nil {
        mf.pool.Release(conn, mf.forwardID)
        return nil, err
    }

    // 返回包装的连接
    return &SSHChannelConn{
        Channel: sshConn,
        Streams: streams,
        pool:    mf.pool,
        conn:    conn,
        fwd:     mf,
    }, nil
}

// Close 释放连接
func (mf *MultiplexedForward) Close() error {
    if mf.currentConn != nil {
        mf.pool.Release(mf.currentConn, mf.forwardID)
    }
    mf.cancel()
    return nil
}
```

**Step 2: 实现 SSHChannelConn 包装器**

```go
// 伪码
type SSHChannelConn struct {
    ssh.Channel
    io.Reader
    io.Writer
    pool *ConnectionManager
    conn *PooledConnection
    fwd  *MultiplexedForward
}

func (c *SSHChannelConn) Close() error {
    err := c.Channel.Close()

    // 释放连接回池
    c.pool.Release(c.conn, c.fwd.forwardID)

    return err
}
```

**Step 3: 编写测试**

```go
// 伪码
func TestMultiplexedForward_Connect(t *testing.T) {
    pool := NewConnectionManager(DefaultConfig())
    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    mf := NewMultiplexedForward(pool, sig, "test-forward")

    // Mock SSH client
    // ...

    conn, err := mf.Connect()
    assert.NoError(t, err)
    assert.NotNil(t, conn)

    mf.Close()
}
```

**Step 4: 运行测试**

```bash
go test ./internal/connection -run TestMultiplexedForward -v
```

**Step 5: 提交**

```bash
git add internal/connection/multiplexed_forward.go
git commit -m "feat(connection): add MultiplexedForward abstraction for connection pooling"
```

---

### Task 3.2: 重构 LocalListenToRemote 使用连接池

**Files:**
- Modify: `internal/forwarding/local_listen_to_remote.go`
- Modify: `internal/forwarding/forward.go`

**验收标准：**
- LocalListenToRemote 使用 ConnectionManager
- 不再直接创建 SSH 连接
- 每个新连接复用 SSH Channel
- 资源清理正确释放连接池引用

**Step 1: 更新 LocalListenToRemote 结构**

```go
// 伪码
type LocalListenToRemote struct {
    // 移除原有的 SSH 连接字段
    // client *ssh.Client  <- 删除

    // 新增连接池
    pool *ConnectionManager

    // 保留其他字段
    forwardID   string
    listenAddr  string
    serviceAddr string
    // ...
}
```

**Step 2: 更新构造函数**

```go
// 伪码
func NewLocalListenToRemote(
    pool *ConnectionManager,
    forwardID string,
    listenAddr string,
    serviceAddr string,
    // ...
) *LocalListenToRemote {
    return &LocalListenToRemote{
        pool:        pool,
        forwardID:   forwardID,
        listenAddr:  listenAddr,
        serviceAddr: serviceAddr,
        // ...
    }
}
```

**Step 3: 更新连接处理逻辑**

```go
// 伪码
func (f *LocalListenToRemote) handleConnection(localConn net.Conn) {
    // 计算连接签名
    sig := f.buildSignature()

    // 创建 MultiplexedForward
    mf := NewMultiplexedForward(f.pool, sig, f.forwardID)

    // 连接（创建 SSH Channel）
    remoteConn, err := mf.Connect()
    if err != nil {
        localConn.Close()
        f.setErrorStatus(err.Error())
        return
    }

    // 双向复制
    bidirectionalCopy(localConn, remoteConn)

    // 清理
    localConn.Close()
    remoteConn.Close()
    mf.Close()
}

func (f *LocalListenToRemote) buildSignature() ConnectionSignature {
    // 根据转发配置构建签名
    // 从数据库或配置中获取 user、host、port、jumphost
    return ConnectionSignature{
        Username: f.username,
        Hostname: f.hostname,
        Port:     f.port,
        JumpChain: f.jumphosts,
    }
}
```

**Step 4: 编写集成测试**

```go
// 伪码
func TestLocalListenToRemote_WithConnectionPool(t *testing.T) {
    pool := NewConnectionManager(DefaultConfig())

    forward := NewLocalListenToRemote(
        pool,
        "test-id",
        "127.0.0.1:8080",
        "remote:80",
        // ... 其他参数
    )

    // 启动 forward
    ctx, cancel := context.WithCancel(context.Background())
    go forward.Start(ctx)

    // 等待监听器就绪
    time.Sleep(100 * time.Millisecond)

    // 创建测试连接
    conn, err := net.Dial("tcp", "127.0.0.1:8080")
    assert.NoError(t, err)

    // 验证连接被复用
    // 连接池应该只有一个物理连接

    // 清理
    cancel()
    conn.Close()
    pool.Close()
}
```

**Step 5: 运行测试**

```bash
go test ./internal/forwarding -run TestLocalListenToRemote -v
```

**Step 6: 提交**

```bash
git add internal/forwarding/local_listen_to_remote.go
git commit -m "refactor(forwarding): integrate LocalListenToRemote with connection pool"
```

---

### Task 3.3: 重构 RemoteListenToLocal 使用连接池

**Files:**
- Modify: `internal/forwarding/remote_listen_to_local.go`

**验收标准：**
- RemoteListenToLocal 使用 ConnectionManager
- 每个 SSH Channel 请求复用物理连接
- 资源清理正确

**Step 1-5:** 同 Task 3.2，针对 RemoteListenToLocal 进行类似重构

```go
// 关键变更点
type RemoteListenToLocal struct {
    pool *ConnectionManager  // 新增
    // 移除 client 字段
}

func (f *RemoteListenToLocal) handleForwardRequest(req *ssh.Request) {
    // 使用 pool.Acquire 获取连接
    // 创建 SSH Channel
    // ...
}

func (f *RemoteListenToLocal) Stop() error {
    // 清理时释放连接池引用
    // ...
}
```

**Step 6: 提交**

```bash
git add internal/forwarding/remote_listen_to_local.go
git commit -m "refactor(forwarding): integrate RemoteListenToLocal with connection pool"
```

---

### Task 3.4: 重构 RemoteListenToRemote 使用连接池

**Files:**
- Modify: `internal/forwarding/remote_listen_to_remote.go`

**验收标准：**
- RemoteListenToRemote 使用 ConnectionManager
- 两个远程端复用同一个物理连接
- 资源清理正确

**Step 1-5:** 同 Task 3.2，针对 RemoteListenToRemote 进行类似重构

```go
// 关键变更点
type RemoteListenToRemote struct {
    pool *ConnectionManager  // 新增

    // 两个连接签名
    listenSignature  ConnectionSignature
    serviceSignature ConnectionSignature
}

func (f *RemoteListenToRemote) Start(ctx context.Context) error {
    // 使用 pool 获取两个连接
    listenConn, err := f.pool.Acquire(ctx, f.listenSignature, f.forwardID)
    serviceConn, err := f.pool.Acquire(ctx, f.serviceSignature, f.forwardID)
    // ...
}

func (f *RemoteListenToRemote) Stop() error {
    // 释放两个连接
    // ...
}
```

**Step 6: 提交**

```bash
git add internal/forwarding/remote_listen_to_remote.go
git commit -m "refactor(forwarding): integrate RemoteListenToRemote with connection pool"
```

---

## Phase 4: ForwardService 集成和生命周期管理

### Task 4.1: ForwardService 持有 ConnectionManager

**Files:**
- Modify: `internal/service/forward_service.go`

**验收标准：**
- ForwardService 初始化时创建 ConnectionManager
- Forward 构造函数传入 ConnectionManager
- ForwardService 关闭时关闭 ConnectionManager

**Step 1: 更新 ForwardService 结构**

```go
// 伪码
type ForwardService struct {
    db     *db.Database
    pool   *ConnectionManager  // 新增

    forwards      map[string]ForwardWrapper
    pendingStarts map[string]bool

    mu sync.RWMutex
}
```

**Step 2: 更新构造函数**

```go
// 伪码
func NewForwardService(db *db.Database) (*ForwardService, error) {
    // 创建连接池
    pool := NewConnectionManager(PoolConfig{
        IdleTimeout:        60 * time.Second,
        MaxIdleConnections: 10,
        DialTimeout:        30 * time.Second,
    })

    return &ForwardService{
        db:            db,
        pool:          pool,
        forwards:      make(map[string]ForwardWrapper),
        pendingStarts: make(map[string]bool),
    }, nil
}
```

**Step 3: 更新 startForward 方法**

```go
// 伪码
func (s *ForwardService) startForward(dbForward *db.Forward) error {
    // 传入连接池
    var forward Forward
    var err error

    switch dbForward.Type {
    case "local_listen_to_remote":
        forward, err = NewLocalListenToRemote(s.pool, ...)
    case "remote_listen_to_local":
        forward, err = NewRemoteListenToLocal(s.pool, ...)
    case "remote_listen_to_remote":
        forward, err = NewRemoteListenToRemote(s.pool, ...)
    }

    // ... 启动 forward ...
}
```

**Step 4: 添加 Close 方法**

```go
// 伪码
func (s *ForwardService) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    // 停止所有 forwards
    for id, wrapper := range s.forwards {
        s.stopWrapper(wrapper)
        delete(s.forwards, id)
    }

    // 关闭连接池
    return s.pool.Close()
}
```

**Step 5: 编写测试**

```go
// 伪码
func TestForwardService_WithConnectionPool(t *testing.T) {
    db := setupTestDB()
    service, _ := NewForwardService(db)
    defer service.Close()

    // 验证 ConnectionManager 已创建
    assert.NotNil(t, service.pool)

    // 创建 forward
    // 验证连接被复用
}
```

**Step 6: 运行测试**

```bash
go test ./internal/service -v
```

**Step 7: 提交**

```bash
git add internal/service/forward_service.go
git commit -m "feat(service): integrate ConnectionManager into ForwardService"
```

---

### Task 4.2: 更新 main.go 初始化和清理

**Files:**
- Modify: `cmd/ssh-multihop/main.go`

**验收标准：**
- Daemon 模式初始化 ForwardService
- 信号处理时正确关闭 ForwardService
- 优雅关闭所有连接

**Step 1: 更新 main 函数**

```go
// 伪码
func main() {
    // ... 解析参数 ...

    if daemonMode {
        // 初始化数据库
        db := initDatabase(dbPath)

        // 创建 ForwardService（包含 ConnectionManager）
        service, err := NewForwardService(db)
        if err != nil {
            log.Fatal(err)
        }
        defer service.Close()  // 优雅关闭

        // 启动 API server
        api := NewAPIService(service, db)
        go api.Start(port)

        // 信号处理
        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

        <-sigCh
        log.Println("Shutting down...")

        // Close 会处理所有清理
        service.Close()
        api.Stop()
    }
}
```

**Step 2: 测试优雅关闭**

```bash
# 编译
make build

# 启动 daemon
./ssh-multihop daemon --port 8080 &
PID=$!

# 创建一些 forwards
curl -X POST http://localhost:8080/api/v1/forwards -d '{...}'

# 发送 SIGTERM
kill -TERM $PID

# 验证所有连接被正确关闭
# 验证没有 goroutine 泄漏
```

**Step 3: 提交**

```bash
git add cmd/ssh-multihop/main.go
git commit -m "feat(cli): add graceful shutdown with ConnectionManager cleanup"
```

---

## Phase 5: 测试和验证

### Task 5.1: 集成测试 - 连接复用验证

**Files:**
- Create: `internal/connection/integration_test.go` (使用 build tag `integration`)

**验收标准：**
- 多个 forward 共享同一连接时只建立一个物理连接
- 连接引用计数正确
- 延迟回收正常工作

**Step 1: 编写集成测试**

```go
// 伪码
//go:build integration
// +build integration

func TestConnectionPool_MultipleForwardsSameConnection(t *testing.T) {
    // 设置测试环境
    db := setupTestDatabase()
    pool := NewConnectionManager(DefaultConfig())

    // 创建三个使用相同 SSH 连接的 forwards
    sig := ConnectionSignature{
        Username: "testuser",
        Hostname: "testhost",
        Port:     22,
    }

    // 创建多个 forwards
    for i := 0; i < 3; i++ {
        forward := NewLocalListenToRemote(
            pool,
            fmt.Sprintf("forward-%d", i),
            fmt.Sprintf("127.0.0.1:%d", 8080+i),
            "remote:80",
            sig,
        )

        ctx := context.Background()
        go forward.Start(ctx)
    }

    // 等待连接建立
    time.Sleep(2 * time.Second)

    // 验证：连接池应该只有 1 个物理连接
    assert.Equal(t, 1, pool.Stats().TotalConnections)
    assert.Equal(t, 3, pool.Stats().TotalReferences)

    // 清理
    pool.Close()
    db.Close()
}

func TestConnectionPool_LingeringTimeout(t *testing.T) {
    // 测试延迟回收
    pool := NewConnectionManager(PoolConfig{
        IdleTimeout: 2 * time.Second,
    })

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    conn, _ := pool.Acquire(context.Background(), sig)
    assert.Equal(t, 1, conn.RefCount)

    pool.Release(conn)
    assert.Equal(t, StatusIdle, conn.Status)

    // 1 秒后连接仍存在
    time.Sleep(1 * time.Second)
    assert.Equal(t, 1, pool.Stats().TotalConnections)

    // 3 秒后连接被关闭
    time.Sleep(2 * time.Second)
    assert.Equal(t, 0, pool.Stats().TotalConnections)

    pool.Close()
}
```

**Step 2: 运行集成测试**

```bash
go test ./internal/connection -tags=integration -v
```

**Step 3: 提交**

```bash
git add internal/connection/integration_test.go
git commit -m "test(connection): add integration tests for connection pooling"
```

---

### Task 5.2: 性能基准测试

**Files:**
- Create: `internal/connection/pool_bench_test.go`

**验收标准：**
- 测量连接建立时间（首次 vs 复用）
- 测量并发 Acquire 性能
- 验证内存使用优化

**Step 1: 编写基准测试**

```go
// 伪码
func BenchmarkConnectionPool_FirstConnection(b *testing.B) {
    pool := NewConnectionManager(DefaultConfig())
    defer pool.Close()

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        // 每次使用不同的签名，强制创建新连接
        sig.Hostname = fmt.Sprintf("host-%d", i)
        conn, _ := pool.Acquire(context.Background(), sig)
        pool.Release(conn)
    }
}

func BenchmarkConnectionPool_ReuseConnection(b *testing.B) {
    pool := NewConnectionManager(DefaultConfig())
    defer pool.Close()

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        // 使用相同签名，复用连接
        conn, _ := pool.Acquire(context.Background(), sig)
        pool.Release(conn)
    }
}

func BenchmarkConnectionPool_ConcurrentAcquire(b *testing.B) {
    pool := NewConnectionManager(DefaultConfig())
    defer pool.Close()

    sig := ConnectionSignature{Username: "user", Hostname: "host", Port: 22}

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            conn, _ := pool.Acquire(context.Background(), sig)
            pool.Release(conn)
        }
    })
}
```

**Step 2: 运行基准测试**

```bash
go test ./internal/connection -bench=BenchmarkConnectionPool -benchmem
```

**Step 3: 分析结果**

预期结果：
- `ReuseConnection` 应该比 `FirstConnection` 快 10-100 倍
- `ConcurrentAcquire` 应该有良好的并发性能
- 内存分配应该合理

**Step 4: 提交**

```bash
git add internal/connection/pool_bench_test.go
git commit -m "test(connection): add benchmarks for connection pool performance"
```

---

### Task 5.3: 端到端测试

**Files:**
- Create: `docs/scripts/test-multiplexing.sh`

**验收标准：**
- 完整测试场景覆盖
- 验证多个 forward 共享连接
- 验证连接失败级联取消

**Step 1: 编写测试脚本**

```bash
#!/bin/bash
# 伪码
set -e

echo "=== Multiplexing Architecture Test ==="

# 启动 daemon
./ssh-multihop daemon --port 8080 --db /tmp/test.db &
DAEMON_PID=$!
sleep 2

# 清理函数
cleanup() {
    kill $DAEMON_PID
    rm -f /tmp/test.db
}
trap cleanup EXIT

# 测试 1: 创建多个使用同一连接的 forwards
echo "Test 1: Multiple forwards sharing connection"
curl -X POST http://localhost:8080/api/v1/forwards \
    -H "Content-Type: application/json" \
    -d '{
        "type": "local_listen_to_remote",
        "listen_addr": "127.0.0.1:9001",
        "service_addr": "remote:80",
        "username": "user",
        "hostname": "host"
    }'

curl -X POST http://localhost:8080/api/v1/forwards \
    -H "Content-Type: application/json" \
    -d '{
        "type": "local_listen_to_remote",
        "listen_addr": "127.0.0.1:9002",
        "service_addr": "remote:80",
        "username": "user",
        "hostname": "host"
    }'

# 等待连接建立
sleep 3

# 验证连接池状态
# (需要添加 API 端点获取连接池统计)

echo "Test 1: PASSED"

# 测试 2: 验证连接复用
echo "Test 2: Verify connection reuse"
# 检查进程的 TCP 连接数
CONN_COUNT=$(netstat -an | grep :22 | grep ESTABLISHED | wc -l)
if [ $CONN_COUNT -le 2 ]; then
    echo "Test 2: PASSED (connections reused)"
else
    echo "Test 2: FAILED (too many connections: $CONN_COUNT)"
    exit 1
fi

# 测试 3: 删除 forward 后连接仍然存在 (lingering)
echo "Test 3: Connection lingering"
FORWARD_ID=$(curl -s http://localhost:8080/api/v1/forwards | jq -r '.[0].id')
curl -X DELETE http://localhost:8080/api/v1/forwards/$FORWARD_ID

sleep 1

# 验证连接仍然存在
CONN_COUNT=$(netstat -an | grep :22 | grep ESTABLISHED | wc -l)
if [ $CONN_COUNT -eq 1 ]; then
    echo "Test 3: PASSED (connection lingering)"
else
    echo "Test 3: FAILED (connection closed immediately)"
    exit 1
fi

# 测试 4: 等待延迟回收超时
echo "Test 4: Linger timeout"
sleep 65

# 验证连接被关闭
CONN_COUNT=$(netstat -an | grep :22 | grep ESTABLISHED | wc -l)
if [ $CONN_COUNT -eq 0 ]; then
    echo "Test 4: PASSED (connection closed after timeout)"
else
    echo "Test 4: FAILED (connection still open)"
    exit 1
fi

echo "=== All Tests Passed ==="
```

**Step 2: 运行测试**

```bash
chmod +x docs/scripts/test-multiplexing.sh
./docs/scripts/test-multiplexing.sh
```

**Step 3: 提交**

```bash
git add docs/scripts/test-multiplexing.sh
git commit -m "test: add end-to-end test script for multiplexing architecture"
```

---

## Phase 6: 文档和监控

### Task 6.1: 更新架构文档

**Files:**
- Create: `docs/multiplexing-architecture.md`
- Modify: `docs/architecture.md` (添加章节引用新文档)

**验收标准：**
- 完整描述连接池架构
- 包含关键概念说明（签名、引用计数、延迟回收）
- 包含序列图和流程图

**Step 1: 编写架构文档**

```markdown
# 连接复用架构

## 概述

本文档描述 SSH-Multihop 的连接复用（Multiplexing）架构...

## 核心组件

### ConnectionManager
- [详细说明]

### ConnectionSignature
- [详细说明]

### 健康检查
- [详细说明]

## 生命周期

### 连接创建
[Mermaid 序列图]

### 延迟回收
[Mermaid 状态图]

### 失败传播
[Mermaid 流程图]

## 性能指标

- 连接建立时间: 从秒级降到毫秒级
- 资源占用: ...
```

**Step 2: 更新主架构文档**

```markdown
## 架构演进

### 简化架构 (2026-03-15)
[现有内容]

### 连接复用架构 (2026-03-17)
详见 [连接复用架构文档](multiplexing-architecture.md)
```

**Step 3: 提交**

```bash
git add docs/multiplexing-architecture.md docs/architecture.md
git commit -m "docs: add multiplexing architecture documentation"
```

---

### Task 6.2: 添加连接池监控 API

**Files:**
- Modify: `internal/api/handlers.go`

**验收标准：**
- 新增 `/api/v1/pool/stats` 端点
- 返回连接池统计信息
- 包含连接列表和引用计数

**Step 1: 实现 stats 端点**

```go
// 伪码
func (h *APIHandler) GetPoolStats(c *gin.Context) {
    stats := h.service.pool.Stats()

    c.JSON(200, gin.H{
        "total_connections": stats.TotalConnections,
        "total_references": stats.TotalReferences,
        "active_connections": stats.ActiveConnections,
        "idle_connections": stats.IdleConnections,
        "connections": stats.Connections,  // 详细列表
    })
}
```

**Step 2: 测试 API**

```bash
curl http://localhost:8080/api/v1/pool/stats
```

**Step 3: 提交**

```bash
git add internal/api/handlers.go
git commit -m "feat(api): add connection pool stats endpoint"
```

---

## 完成检查清单

在宣布实现完成前，确保：

- [ ] 所有单元测试通过 (`make test`)
- [ ] 所有集成测试通过 (`make test-integration`)
- [ ] 基准测试显示性能提升
- [ ] 端到端测试通过
- [ ] 文档完整更新
- [ ] 代码通过 lint 检查 (`make lint`)
- [ ] 无资源泄漏（通过 `runtime/pprof` 验证）
- [ ] API 兼容性保持（现有 API 不变）

---

## 回滚计划

如果实现出现严重问题：

1. **保留原有连接逻辑**：在 `connection/` 目录保留原有实现
2. **功能开关**：添加环境变量 `SSH_MULTIHOP_USE_POOL=false` 禁用连接池
3. **逐步迁移**：先在非关键 forwards 上启用，验证后全面启用

```go
// 伪码
func NewForwardService(db *db.Database) (*ForwardService, error) {
    var pool *ConnectionManager

    if os.Getenv("SSH_MULTIHOP_USE_POOL") != "false" {
        pool = NewConnectionManager(DefaultConfig())
    }

    return &ForwardService{
        db:   db,
        pool: pool,
    }
}
```

---

## 附录: 伪代码约定

本计划中的伪代码遵循以下约定：

1. **`...`** 表示省略的已有代码
2. **`// 伪码`** 注释标记伪代码段
3. **类型定义** 使用 Go 语法
4. **方法签名** 完整给出
5. **方法体** 只展示关键逻辑
6. **错误处理** 简化为 `assert.NoError(t, err)`
7. **测试用例** 使用 `github.com/stretchr/testify` 风格

实施时请根据实际代码结构决定：
- 具体的错误处理方式
- 并发控制细节
- 日志记录位置和内容
- 配置参数的默认值
