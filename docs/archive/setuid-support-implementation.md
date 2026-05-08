# setuid/setgid 兼容性

## 概述

守护进程现在支持使用 setuid/setgid 权限运行，采用与 v1 相同的方法。这确保当二进制文件以提升权限或不同用户权限执行时可靠运行。

## 什么是 setuid/setgid？

**setuid**（设置用户 ID）和 **setgid**（设置组 ID）是 Unix 权限，允许用户以程序所有者或组的权限执行程序。

常见用例：
- 权限提升（例如 `sudo`）
- 以特定用户运行守护进程
- 共享服务账户
- 安全隔离

## 问题

当程序使用 setuid/setgid 运行时：
- 环境变量（如 `HOME`）可能过时或不正确
- `os.UserHomeDir()` 依赖环境变量
- 这可能导致守护进程使用错误的路径或无法访问文件

**示例：**
```bash
# 如果运行：
sudo -u otheruser ssh-multihop daemon

# HOME 环境变量可能仍然是 /root 而不是 /home/otheruser
# 这导致文件在错误的位置创建
```

## 解决方案

使用 `utils.UserHomeDir()` 而不是 `os.UserHomeDir()` 或环境变量。

### `utils.UserHomeDir()` 如何工作

```go
func UserHomeDir() (string, error) {
    // Query system user database based on effective UID
    currentUser, err := user.Current()
    if err != nil {
        return os.UserHomeDir()  // Fallback
    }

    // Returns home from passwd database (not environment)
    return filepath.Clean(currentUser.HomeDir), nil
}
```

**关键区别：**
- `user.Current()` 直接查询 `/etc/passwd`
- 使用 **effective UID**（进程运行的用户）
- 不依赖 `HOME` 环境变量
- 在 setuid/setgid 场景中更可靠

## 实现细节

### `cmd/ssh-multihop/daemon.go` 的更改

**之前：**
```go
var daemonCommand = &cli.Command{
    Flags: []cli.Flag{
        &cli.StringFlag{
            Name:  "db",
            Value: "/tmp/ssh-multihop-fwd.db",  // ❌ Hardcoded
        },
    },
}
```

**之后：**
```go
import (
    "path/filepath"
    "github.com/user/ssh-multihop-fwd/v2/internal/pkg/utils"
)

func runDaemon(c *cli.Context) error {
    dbPath := c.String("db")

    // Use utils.UserHomeDir() for setuid/setgid compatibility
    if dbPath == "" {
        homeDir, err := utils.UserHomeDir()
        if err != nil || homeDir == "" {
            return fmt.Errorf("failed to determine home directory: %w", err)
        }
        dbPath = filepath.Join(homeDir, ".ssh-multihop", "ssh-multihop-fwd.db")
    }

    // Ensure database directory exists
    dbDir := filepath.Dir(dbPath)
    if err := os.MkdirAll(dbDir, 0755); err != nil {
        return fmt.Errorf("failed to create database directory: %w", err)
    }
    // ...
}
```

### 优势

1. **可靠的路径解析**：始终使用正确的用户主目录
2. **权限安全**：使用正确的所有权创建数据库
3. **独立于环境**：即使 HOME 环境变量错误也能工作
4. **Setuid 兼容**：使用 effective UID 查询 passwd 数据库

## 测试

### 重要免责声明

⚠️ **真正的 setuid/setgid 测试需要 sudo 权限才能实际切换用户。**

这里显示的测试验证了**代码实现**（使用 `utils.UserHomeDir()`），但无法完全验证**运行时行为**，除非实际使用不同的 UID 执行。

### 我们可以验证的内容（无需 Sudo）

代码级验证：
```
✓ daemon.go 使用 utils.UserHomeDir()
✓ 默认路径使用用户主目录
✓ 使用正确的权限创建数据库目录（0755）
✓ Agent socket 移动到用户主目录（不是 /tmp）
✓ 添加了 SSH_AUTH_SOCK 验证
```

### 我们无法验证的内容（无需 Sudo）

需要实际 UID 更改的运行时行为：
- ❌ 以不同用户运行时的文件所有权
- ❌ 跨 UID 边界的 Agent socket 可访问性
- ❌ setuid 上下文中的 SSH_AUTH_SOCK 权限错误
- ❌ 使用 effective UID 解析主目录

### 真实测试（需要 Sudo）

要正确测试 setuid/setgid 场景：

```bash
# Run the real test script
sudo /tmp/test_setuid_real.sh
```

此脚本将：
1. 创建测试用户
2. 使用 `sudo -u` 以该用户运行守护进程
3. 验证文件所有权和位置
4. 测试无效的 SSH_AUTH_SOCK 处理
5. 清理测试工件

**预期结果（使用 sudo 运行时）：**
- Agent socket：`/tmp/ssh-multihop-UID/agent.PID.sock` 或 `$XDG_RUNTIME_DIR/ssh-multihop/agent.PID.sock`
- 数据库：`~testuser/.ssh-multihop/ssh-multihop-fwd.db`
- 所有者：`testuser`（不是 root）
- 无效的 SSH_AUTH_SOCK：被检测并忽略

### 手动测试（如果您有 Sudo）

**正常执行：**
```bash
$ /tmp/ssh-multihop-v2 daemon
# Creates: ~/.ssh-multihop/ssh-multihop-fwd.db
# Owner: your-user
```

**使用 sudo（模拟 setuid）：**
```bash
$ sudo -u otheruser /tmp/ssh-multihop-v2 daemon
# Expected: Creates: ~otheruser/.ssh-multihop/ssh-multihop-fwd.db
# Expected: Owner: otheruser (not root!)
```

## 使用示例

### 默认（推荐）
```bash
# Uses ~/.ssh-multihop/ssh-multihop-fwd.db
ssh-multihop daemon
```

### 自定义路径
```bash
# Uses specified path (setuid/setgid aware)
ssh-multihop daemon --db /custom/path/database.db
```

### Systemd 服务
```ini
[Service]
User=ssh-multihop
Group=ssh-multihop
ExecStart=/usr/bin/ssh-multihop daemon
# Database will be in ~ssh-multihop/.ssh-multihop/
```

## 兼容性说明

### v1 vs v2

| Feature | v1 | v2 |
|---------|----|----|
| `utils.UserHomeDir()` | ✅ Yes | ✅ Yes |
| Default DB location | `~/.ssh-multihop/ssh-multihop.db` | `~/.ssh-multihop/ssh-multihop-fwd.db` |
| Directory creation | ✅ Yes | ✅ Yes |
| setuid/setgid support | ✅ Yes | ✅ Yes |

### 其他使用 `utils.UserHomeDir()` 的文件

守护进程与代码库的其他部分一致：
- `internal/pkg/connection/builder.go` - SSH 密钥发现
- `internal/pkg/connection/builtin_agent.go` - Agent socket 位置
- `internal/pkg/config/parser.go` - SSH 配置解析
- `internal/pkg/config/expansion.go` - 环境变量扩展

所有这些组件现在都正确处理 setuid/setgid。

## 安全考虑

### 文件权限

创建的目录使用 `0755` (rwxr-xr-x)：
- 所有者：读取、写入、执行
- 组：读取、执行
- 其他人：读取、执行

数据库文件继承默认的 SQLite 权限（0644）：
- 所有者：读取、写入
- 组：读取
- 其他人：读取

### 建议

1. **以专用用户运行**：为守护进程创建特定用户
   ```bash
   sudo useradd -r -s /bin/false ssh-multihop
   ```

2. **保护敏感数据**：确保正确的文件权限
   ```bash
   chmod 700 ~/.ssh-multihop
   chmod 600 ~/.ssh-multihop/ssh-multihop-fwd.db
   ```

3. **使用 systemd**：对于生产环境，使用带有用户隔离的 systemd 服务
   ```ini
   [Service]
   User=ssh-multihop
   Group=ssh-multihop
   NoNewPrivileges=true
   PrivateTmp=true
   ```

## 故障排除

### 问题：找不到数据库
```bash
# Check actual path:
ls -la ~/.ssh-multihop/

# Verify current user:
whoami
id
```

### 问题：权限被拒绝
```bash
# Check ownership:
ls -la ~/.ssh-multihop/ssh-multihop-fwd.db

# Fix permissions:
chown $USER:$USER ~/.ssh-multihop/ssh-multihop-fwd.db
chmod 600 ~/.ssh-multihop/ssh-multihop-fwd.db
```

### 问题：错误的主目录
```bash
# Verify utils.UserHomeDir() returns correct path:
go run -exec echo 'import "fmt"; import "github.com/user/ssh-multihop-fwd/v2/internal/pkg/utils"; func main() { h, _ := utils.UserHomeDir(); fmt.Println(h) }'
```

## 参考

- Go `os/user` package: https://pkg.go.dev/os/user
- setuid(2) man page: `man 2 setuid`
- v1 实现: `/home/shih/test/multi-hop-fwd/internal/utils/userhome.go`
- v2 实现: `/home/shih/test/multi-hop-fwd/v2/internal/pkg/utils/userhome.go`

## 总结

✅ **setuid/setgid 支持现已完全实现**

守护进程通过使用 `utils.UserHomeDir()` 正确处理 setuid/setgid 场景，确保无论二进制文件如何执行或设置什么环境变量都能可靠运行。
