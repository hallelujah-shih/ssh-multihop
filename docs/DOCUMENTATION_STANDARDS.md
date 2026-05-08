# 文档标准

## 文件命名约定

### 根目录 (`/`)
仅保留面向用户的文档：
- `README.md` - 项目概述和快速入门
- `CLAUDE.md` - Claude Code 项目说明（必需文件）

**原则：** 根目录应该保持简洁，只保留用户首次接触项目时需要看到的文档。

### `docs/` 目录
开发者文档：
- `architecture.md` - 系统架构和设计原则（不是 ARCHITECTURE.md）
- `index.md` - 文档索引（小写）
- `api/REFERENCE.md` - API 文档（不是 NEW-API-REFERENCE.md）
- `scripts/README.md` - 测试脚本概述
- `scripts/<name>-guide.md` - 详细的测试指南（如 test-7-scenarios-guide.md）
- `DOCUMENTATION_STANDARDS.md` - 本文档
- `DOCUMENTATION_COVERAGE.md` - 文档覆盖范围

**注意：** 一般性的开发者指南应直接放在 `docs/` 根目录，使用小写命名（如 `deployment.md`）

### `docs/archive/` 目录
历史/临时文档：
- `refactor-summary-<date>.md` - 重构总结
- `implementation-<feature>-<date>.md` - 实现总结
- `scripts/<name>.sh` - 已归档的临时测试脚本
- 命名模式：`<type>-<description>-YYYY-MM-DD.md`

**注意：** 临时测试脚本（如架构验证、特定 bug 修复测试）在完成使命后应移至 `archive/scripts/`

## 编写风格指南

### 语言
- **主要语言：** 文档使用中文编写
- **例外保持英文：** 技术术语、命令、代码、API端点等保持英文
  - 术语示例：SSH, HTTP, REST API, Forward, Context
  - 命令示例：`git commit`, `go build`, `./ssh-multihop daemon`
  - 代码示例：所有代码块中的内容
- **代码注释：** 英文编写（遵循Go惯例）

### Markdown 格式

#### 标题
```markdown
# Title (1-3 words)
## Section Name
### Subsection Name
```

#### 代码块
```markdown
**Usage:**
\`\`\`bash
command_here
\`\`\`

**Example:**
\`\`\`go
func Example() {
    // code
}
\`\`\`
```

#### 列表
```markdown
- Use dashes for unordered lists
- Second level item
  - Third level item

1. Use numbers for ordered steps
2. Second step
   - Detail for step 2
```

### 文档结构模板
```markdown
# [Feature/Topic Name]

## Overview
[2-3 sentences describing what this is]

## Purpose
[Why this exists, 1-2 sentences]

## Usage/Details
[Main content with examples]

## See Also
- [Related link 1]
- [Related link 2]
```

## 长度指南

- **README.md:** 50-100 lines（简洁概述）
- **ARCHITECTURE.md:** 100-200 lines（系统设计）
- **API docs:** As needed（全面）
- **Guides:** 50-150 lines（专注）
- **Archive docs:** Keep as-is（历史记录）

## 代码注释标准

### 哲学：通过注释进行文档化

代码注释是**文档**，而非冗余。应注重准确性和完整性，而非极简主义。

**核心原则：**
1. **准确性优先：** 注释必须与代码行为匹配。如有不同，修正注释。
2. **解释原因：** 注释应说明非显而易见的决策、算法和权衡。
3. **记录副作用：** 提及 goroutines、数据库更新、context 使用。
4. **保留价值：** 保留有助于理解的注释，即使略显冗余。

### 包注释

每个包必须有说明其用途的注释：

```go
// Package forwarding provides port forwarding implementations.
//
// The package supports four forward types:
//   - LocalListenToRemote: SSH -L (local listen to remote service)
//   - RemoteListenToLocal: SSH -R (remote listen to local service)
//   - RemoteListenToRemote: Remote-to-remote bridging
//   - InlineForwardOrchestrator: Composed forwarding using UDS bridge
//
// Architecture:
// All forwards follow the simplified architecture:
//   - Fail fast on errors (no internal retry logic)
//   - Update database status on errors
//   - ForwardService handles rebuild and recovery
//
// Thread Safety:
// Forward instances are not thread-safe. Use external synchronization
// if calling Start/Stop from multiple goroutines.
package forwarding
```

**必需元素：**
- 包用途（1-2 句话）
- 主要导出的类型/函数
- 架构/模式说明
- 线程安全注意事项
- 复杂包的使用示例

### 函数注释

导出的函数必须有注释：

```go
// Start begins the port forwarding.
//
// This method blocks until the forward is stopped or an error occurs.
// On error, the database status is set to "error" and resources are cleaned up.
//
// Parameters:
//   - ctx: Context for cancellation. Canceling triggers graceful shutdown.
//
// Returns:
//   - error: Non-nil if startup fails or forward encounters error
//
// Side effects:
//   - Opens SSH connections to target hosts
//   - Creates listener (local or remote depending on type)
//   - Launches health monitoring goroutine (15s interval)
//   - Updates database status on errors
//
// The forward runs independently until stopped or error occurs.
func (f *Forward) Start(ctx context.Context) error {
```

**必需元素：**
- 函数的功能
- 阻塞行为（blocks until X）
- 参数（特别是 context 使用）
- 返回值（特别是错误条件）
- 副作用（goroutines、数据库更新、状态变化）

### 类型注释

结构和接口必须记录其用途和使用方法：

```go
// ForwardService manages the lifecycle of all port forwards.
//
// The service runs a sync loop every 5 seconds to:
//   - Start new forwards from database
//   - Stop deleted forwards
//   - Rebuild forwards in error state
//
// Thread Safety:
// All methods are thread-safe and can be called concurrently.
//
// Lifecycle:
//   - Created with New()
//   - Started with Start()
//   - Stopped with Stop() or StopWithContext()
type ForwardService struct {
    // Database for persisting forward configurations
    db *db.Database

    // Active forwards indexed by forward ID
    // Protected by forwardsMu
    forwards map[string]*ForwardWrapper
    forwardsMu sync.RWMutex

    // Context for canceling all operations
    ctx    context.Context
    cancel context.CancelFunc

    // Notification channels
    syncDone   chan struct{} // Closed when syncLoop exits
    shutdownCh chan struct{} // Closed when Stop() is called
}
```

**必需元素：**
- 用途和职责
- 关键操作
- 线程安全保证
- 生命周期管理
- 重要字段注释

### 注释质量检查清单

审查注释时，验证：

✅ **准确性：** 注释与实际代码行为匹配
✅ **完整性：** 解释参数、返回值、副作用
✅ **上下文：** 解释为什么，而不仅仅是做什么
✅ **非显而易见：** 注释增加超出阅读代码的价值

❌ **避免：** 仅重复代码的注释
❌ **避免：** 与实现不匹配的过时注释
❌ **避免：** 模糊的注释，如 "do the needful"

### 注释维护工作流

1. **先修改代码：** 修改代码时立即更新注释
2. **验证：** 更改代码后，重新阅读注释以确保它们仍然匹配
3. **同行评审：** 在 PR 中与代码一起审查注释
4. **定期审计：** 每季度审查注释准确性

## 审查检查清单

提交文档之前：
- [ ] 文件名遵循命名约定
- [ ] 主要语言使用中文（技术术语除外）
- [ ] Markdown 格式遵循模板
- [ ] 代码示例已测试
- [ ] 链接有效
- [ ] 拼写正确
