# SSH Multi-Hop Port Forwarding Tool

强大的 SSH 多跳端口转发工具，提供守护进程模式和 REST API。

## 功能特性

- **多跳隧道**：自动解析支持 ProxyJump 的 OpenSSH 配置
- **守护进程模式**：带 REST API 的后台服务，用于持久化转发
- **自动重连**：具有指数退避的自动连接恢复
- **纯 Go SSH Agent**：内置 SSH agent（无需外部进程）
- **setuid/setgid 支持**：正确使用提升权限运行

## 快速入门

### 安装

```bash
# Build
make build
```

### 基本使用

```bash
# 启动守护进程（带 REST API）
ssh-multihop daemon --port 8080
```

## 文档

- **[系统架构](docs/architecture.md)** - 简化的 Forward 架构设计
- **[API 参考](docs/api/REFERENCE.md)** - REST API 文档
- **[docs/](docs/)** - 完整文档（测试指南、开发文档）

## 开发

```bash
# 运行测试
make test

# 运行集成测试
make test-integration
```

## License

详见 LICENSE 文件。
