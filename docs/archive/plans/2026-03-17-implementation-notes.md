# 架构改进实施注意事项

> 创建日期: 2026-03-17
> 相关文档: `docs/plans/2026-03-17-architecture-improvements.md`

本文档记录实施架构改进计划时需要特别注意的代码适配事项。

---

## 1. ForwardService 结构差异

### 计划中的假设结构
```go
type ForwardService struct {
    db             *db.Database
    logger         *util.Logger
    forwards       map[string]*forwarding.Forward
    forwardsMu     sync.RWMutex  // ❌ 错误
    stopCh         chan struct{}
}
```

### 实际代码结构（`internal/service/forward_service.go`）
```go
type ForwardService struct {
    db *db.Database

    // ctx is the root context for all operations
    ctx    context.Context
    cancel context.CancelFunc

    // Active forwards
    forwards map[string]ForwardWrapper  // ✅ 使用 ForwardWrapper
    mu       sync.RWMutex               // ✅ 字段名是 mu

    // Sync loop control
    syncCancel   context.CancelFunc
    syncDone     chan struct{}
    syncInterval time.Duration

    // Rebuild backoff control (已有指数退避机制)
    consecutiveFailures map[string]int
    lastRebuildTime     map[string]time.Time
    baseBackoff         time.Duration
    maxBackoff          time.Duration
}
```

### 关键差异

| 项目 | 计划假设 | 实际代码 | 适配要求 |
|------|---------|---------|---------|
| 互斥锁字段名 | `forwardsMu` | `mu` | ✅ 使用 `mu` |
| Forwards 类型 | `*forwarding.Forward` | `ForwardWrapper` | ✅ 使用 `ForwardWrapper` |
| 构造函数 | `NewForwardService()` | `New()` / `NewWithContext()` | ✅ 使用实际函数名 |
| Stops map | `stops map[string]chan struct{}` | ForwardWrapper 内嵌 context | ✅ 使用 wrapper.ctx |
| 日志 | `logger *util.Logger` | `zap.L()` 全局日志 | ✅ 使用 zap 全局日志 |

### ForwardWrapper 结构
```go
type ForwardWrapper struct {
    Type                 db.ForwardType
    LocalListenToRemote  *forwarding.LocalListenToRemote
    RemoteListenToLocal  *forwarding.RemoteListenToLocal
    RemoteListenToRemote *forwarding.RemoteListenToRemote
    ctx                  context.Context
    cancel               context.CancelFunc
}
```

---

## 2. 数据库模型差异

### ForwardStatus 模型（`internal/db/models.go`）

```go
type ForwardStatus struct {
    ForwardID     string    `gorm:"primaryKey;size:64"`  // ✅ 字符串主键，不是 uint
    Status        string    `gorm:"type:varchar(20);not null"`
    LastHeartbeat time.Time
    ErrorMessage  string    `gorm:"type:text"`
    CreatedAt     time.Time
    UpdatedAt     time.Time
    // ❌ 没有 ID uint 字段
}
```

### Task 1.4 JOIN 实现建议

**❌ 不要这样做**（会与 GORM 混淆）：
```go
type Forward struct {
    // ...
    Status *ForwardStatus `gorm:"foreignKey:ForwardID"`  // 不要添加
}
```

**✅ 正确做法**（使用独立结果结构）：
```go
// 在 database.go 中定义
type ForwardWithStatus struct {
    Forward Forward
    Status  *ForwardStatus
}

func (d *Database) ListForwardsWithStatus() ([]ForwardWithStatus, error) {
    var results []ForwardWithStatus

    err := d.db.Table("forwards").
        Select("forwards.*, forward_status.*").
        Joins("LEFT JOIN forward_status ON forwards.id = forward_status.forward_id").
        Scan(&results).Error

    return results, err
}
```

---

## 3. 平台兼容性：Linux 特定代码

### Task 1.3: SyscallConn 实现

**问题**：计划中的代码使用了 Linux 特定的常量：
```go
syscall.IPPROTO_TCP    // Linux 特定
syscall.TCP_KEEPIDLE   // Linux 特定
syscall.TCP_KEEPINTVL  // Linux 特定
syscall.TCP_KEEPCNT    // Linux 特定
```

**当前环境**：Fedora 43 Linux - 这些代码可以正常工作

**未来跨平台支持**：使用 `golang.org/x/sys/unix` + Build Tags

```go
// +build linux

package util

import "golang.org/x/sys/unix"

func SetTCPKeepalive(conn net.Conn) error {
    syscallConn, err := conn.(syscall.Conn).SyscallConn()
    // ...
    setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_KEEPALIVE, 1)
    setErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPIDLE, 15)
    // ...
}
```

**macOS 对应常量**：
```go
// +build darwin

// macOS 使用不同的常量名
// TCP_KEEPALIVE vs TCP_KEEPIDLE
// TCP_KEEPINTVL (相同)
```

**建议**：
1. 当前实现仅支持 Linux（文档说明）
2. 未来 PR 添加 macOS/Windows 支持
3. 使用 Build Tags 分离平台特定代码

---

## 4. Task 1.1 具体实现指南

### pendingStarts 机制集成

**在 ForwardService 结构体添加**（line 41 之后）：
```go
type ForwardService struct {
    // ... 现有字段 ...

    // Pending starts (NEW)
    pendingStarts map[string]bool     // Track IDs currently being started
    pendingMu     sync.Mutex          // Protect pendingStarts
}
```

**在 New() 初始化**（line 59 之后）：
```go
func New(database *db.Database) *ForwardService {
    ctx, cancel := context.WithCancel(context.Background())
    return &ForwardService{
        // ... 现有初始化 ...
        pendingStarts: make(map[string]bool),  // NEW
    }
}
```

**在 NewWithContext() 初始化**（line 76 之后）：
```go
func NewWithContext(ctx context.Context, database *db.Database) *ForwardService {
    childCtx, cancel := context.WithCancel(ctx)
    return &ForwardService{
        // ... 现有初始化 ...
        pendingStarts: make(map[string]bool),  // NEW
    }
}
```

**在 sync() 方法中添加检查**（line 284-333 修改）：

关键点：
1. 在启动 goroutine 之前检查 `pendingStarts`
2. 在 goroutine 完成后释放锁
3. 在指数退避跳过时也要释放锁

```go
for _, fwd := range dbForwards {
    s.mu.RLock()
    _, exists := s.forwards[fwd.ID]
    s.mu.RUnlock()

    if !exists {
        // ✅ 新增：检查是否已启动
        s.pendingMu.Lock()
        if s.pendingStarts[fwd.ID] {
            s.pendingMu.Unlock()
            zap.L().Debug("Forward already starting, skipping",
                zap.String("id", fwd.ID))
            continue
        }
        s.pendingStarts[fwd.ID] = true
        s.pendingMu.Unlock()

        // 检查错误状态和退避策略
        status, err := s.db.GetStatus(fwd.ID)
        shouldStart := true

        if err == nil && status.Status == "error" {
            shouldRebuild, nextRebuildIn := s.shouldRebuild(fwd.ID)

            if !shouldRebuild {
                zap.L().Debug("Skipping start due to exponential backoff",
                    zap.String("id", fwd.ID),
                    zap.Duration("next_rebuild_in", nextRebuildIn),
                    zap.Int("consecutive_failures", s.consecutiveFailures[fwd.ID]))
                shouldStart = false

                // ✅ 新增：释放 pending 锁
                s.pendingMu.Lock()
                delete(s.pendingStarts, fwd.ID)
                s.pendingMu.Unlock()
            } else {
                zap.L().Info("Starting error forward after backoff",
                    zap.String("id", fwd.ID),
                    zap.Int("consecutive_failures", s.consecutiveFailures[fwd.ID]))
            }
        }

        if !shouldStart {
            continue
        }

        zap.L().Info("Starting new forward from database",
            zap.String("id", fwd.ID),
            zap.String("type", string(fwd.Type)))

        // 启动 goroutine
        go func(f db.Forward) {
            defer func() {
                // ✅ 新增：完成后释放 pending 锁
                s.pendingMu.Lock()
                delete(s.pendingStarts, f.ID)
                s.pendingMu.Unlock()
            }()

            if err := s.startForward(&f); err != nil {
                zap.L().Error("Failed to start forward",
                    zap.String("id", f.ID),
                    zap.Error(err))
                s.updateStatus(f.ID, "error", err.Error())
                s.recordRebuildFailure(f.ID)
            } else {
                s.recordRebuildSuccess(f.ID)
            }
        }(fwd)
    }
}
```

---

## 5. 测试适配示例

### TestPendingStartsPreventsRace

```go
package service

import (
    "context"
    "sync"
    "testing"

    "github.com/hallelujah-shih/ssh-multihop/internal/db"
    "go.uber.org/zap"
)

func TestPendingStartsPreventsRace(t *testing.T) {
    // Setup
    logger, _ := zap.NewDevelopment()
    database, _ := db.NewMemoryDatabase()

    ctx := context.Background()
    service := NewWithContext(ctx, database, logger)

    // Create test forward
    testForward := &db.Forward{
        Type:         db.LocalListenToRemote,
        ListenHost:   "local",
        ListenAddr:   "127.0.0.1:9999",
        ServiceHost:  "example.com",
        ServiceAddr:  "127.0.0.1:80",
    }

    if err := database.Create(testForward).Error; err != nil {
        t.Fatalf("Failed to create test forward: %v", err)
    }

    // 模拟 5 个并发 sync 调用
    var wg sync.WaitGroup
    for i := 0; i < 5; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            service.sync()
        }()
    }
    wg.Wait()

    // 验证只创建了一个 wrapper
    service.mu.Lock()
    count := len(service.forwards)
    service.mu.Unlock()

    if count != 1 {
        t.Errorf("Expected 1 forward wrapper, got %d", count)
    }
}
```

---

## 6. 实施检查清单

在实施每个任务前：

### Task 1.1 (pendingStarts)
- [ ] 确认使用 `mu` 而非 `forwardsMu`
- [ ] 确认使用 `ForwardWrapper` 而非 `*forwarding.Forward`
- [ ] 确认在 `New()` 和 `NewWithContext()` 都初始化
- [ ] 确认在指数退避跳过时释放锁
- [ ] 确认使用 `zap.L()` 而非 `s.logger`

### Task 1.2 (bidirectionalCopy)
- [ ] 确认所有三种 forward 类型都使用新的 copy 函数
- [ ] 确认显式关闭双方连接

### Task 1.3 (SyscallConn)
- [ ] 文档说明仅支持 Linux
- [ ] 添加 TODO 注释标记跨平台需求

### Task 1.4 (Database JOIN)
- [ ] 使用 `ForwardWithStatus` 结构体
- [ ] 不修改 Forward 模型添加 Status 字段
- [ ] 使用 LEFT JOIN 确保 status 为空时也返回 forward
- [ ] 更新 sync() 使用 joined 结果

### Task 1.5 (sync API)
- [ ] 端口使用 18080 而非 8080
- [ ] 添加 context.Context 超时控制
- [ ] 返回最终状态而非 pending

---

## 7. 调试技巧

### 验证 pendingStarts 工作
```bash
# 启用调试日志
export ZAP_LOG_LEVEL=debug
./ssh-multihop daemon --port 18080 --db /tmp/test.db

# 观察日志
# 应该看到 "Forward already starting, skipping" 消息
```

### 验证 JOIN 查询效率
```go
// 在 database.go 中添加日志
func (d *Database) ListForwardsWithStatus() ([]ForwardWithStatus, error) {
    start := time.Now()
    defer func() {
        zap.L().Debug("ListForwardsWithStatus duration",
            zap.Duration("took", time.Since(start)))
    }()

    // ... 实现代码
}
```

### 验证 TCP Keepalive
```bash
# 检查套接字选项
ss --tcp --options | grep KEEPALIVE
```

---

## 8. 常见错误

### 错误 1: 使用了不存在的字段名
```go
s.forwardsMu.Lock()  // ❌ 错误
s.mu.Lock()          // ✅ 正确
```

### 错误 2: 修改了 Forward 模型
```go
type Forward struct {
    // ...
    Status *ForwardStatus  // ❌ 不要添加
}
```

### 错误 3: 在退避跳过时未释放 pending 锁
```go
if !shouldRebuild {
    continue  // ❌ 忘记释放锁
}
```

正确做法：
```go
if !shouldRebuild {
    s.pendingMu.Lock()
    delete(s.pendingStarts, fwd.ID)
    s.pendingMu.Unlock()
    continue  // ✅ 释放了锁
}
```

---

## 9. 提交消息模板

```bash
git commit -m "feat: add pendingStarts mechanism to prevent duplicate starts

- Add pendingStarts map to track forwards currently being started
- Modify sync() to check pendingStarts before launching goroutine
- Release pending lock when startForward completes or is skipped
- Prevents duplicate forwards when SSH handshake is slow

Adapted for actual codebase:
- Use mu instead of forwardsMu
- Use ForwardWrapper instead of *forwarding.Forward
- Update both New() and NewWithContext() constructors
- Use zap.L() for logging

Fixes race condition identified in architecture review 2026-03-17

Refs: docs/analysis/architecture-review-2026-03-17.md"
```

---

## 10. 下一步行动

1. ✅ 阅读本文档
2. ✅ 阅读 `internal/service/forward_service.go` 实际代码
3. ✅ 阅读 `internal/db/models.go` 数据库模型
4. ⏳ 按照 Task 1.1 实施指南添加 pendingStarts
5. ⏳ 运行测试验证
6. ⏳ 提交代码

---

**重要提醒**：
- 始终参考实际源代码，不要完全依赖计划中的示例
- 计划展示了模式和思路，但字段名必须匹配实际代码
- 当有疑问时，先 `git grep` 查找实际用法
- 每个任务实施前运行 `make check` 确保代码质量
