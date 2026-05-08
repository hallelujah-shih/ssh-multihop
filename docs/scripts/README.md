# 测试脚本

此目录包含用于验证 SSH multi-hop 功能的集成测试脚本。

## 前置条件

- SSH multihop daemon 正在运行：`./ssh-multihop daemon --port 8080`
- SSH 配置中已配置测试主机

## 核心测试脚本

### 场景测试
- **`test-7-scenarios.sh`** - 全面的 7 场景测试
  - 测试所有 forward 类型
  - 验证错误处理和自愈能力
  - 运行时间：约 5 分钟
  - 详细指南：[test-7-scenarios-guide.md](test-7-scenarios-guide.md)

### API 测试
- **`test-api.sh`** - REST API 端点测试
  - 创建、列出、删除 forwards
  - 状态端点验证
  - 运行时间：约 2 分钟

## 运行测试

```bash
cd docs/scripts

# 运行所有测试
./test-7-scenarios.sh

# 运行 API 测试
./test-api.sh

# 使用详细输出运行
bash -x ./test-7-scenarios.sh
```

## 辅助工具

- **`check-port.sh`** - 检查端口使用情况
  - 用法：`./check-port.sh [port]`
  - 退出码：0=可用，2=ssh-multihop，3=其他进程

- **`stop-daemon.sh`** - 安全停止 ssh-multihop daemon
  - 用法：`./stop-daemon.sh`
  - 停止前提示，优先优雅关闭

- **`find-available-port.sh`** - 自动查找可用端口
  - 用法：`./find-available-port.sh [start_port]`
  - 从 start_port 扫描到 start_port+1000

## 历史测试脚本

已归档的临时测试脚本位于 [archive/scripts/](../archive/scripts/)：
- `test-daemon.sh` - 基本守护进程测试
- `test-graceful-shutdown.sh` - 优雅关闭验证
- `test-simplified-architecture.sh` - 架构重构验证

## 故障排除

### 端口已被占用

```bash
# 检查 ssh-multihop 是否正在运行
ps aux | grep ssh-multihop | grep -v grep

# 如果需要，使用不同端口
./ssh-multihop daemon --port 8081
```

### 数据库被锁定

```bash
# 检查是否有其他 daemon 实例
ps aux | grep ssh-multihop | grep -v grep

# 如果没有，删除过期的锁文件
rm -f ~/.ssh-multihop/ssh-multihop-fwd.db
```

## 参见

- [系统架构](../architecture.md)
- [API 参考](../api/REFERENCE.md)
