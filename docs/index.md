# 文档

## 用户指南

- **[README](../README.md)** - 项目概述和快速入门
- **[架构](architecture.md)** - 系统架构和设计原则
- **[setuid/setgid 支持](archive/setuid-support-implementation.md)** - 使用提升权限运行

## 开发者文档

### API 参考
- **[API Reference](api/REFERENCE.md)** - REST API 文档
  - 地址格式和语义
  - Forward 类型和验证规则
  - 所有场景的示例配置

### 指南
- **[文档标准](DOCUMENTATION_STANDARDS.md)** - 如何编写和格式化文档
- **[测试脚本](scripts/README.md)** - 测试指南和脚本使用

## 历史文档

- **[归档](archive/)** - 历史实现说明和重构总结

## 快速链接

### Forward 类型
- `LocalListenToRemote` - SSH -L（本地监听远程服务）
- `RemoteListenToLocal` - SSH -R（远程监听本地服务）
- `RemoteListenToRemote` - 远程到远程桥接

### 关键组件
- `ForwardService` - 生命周期管理和恢复
- `Forward interface` - 统一的端口转发 API
- 健康检查和优雅关闭
