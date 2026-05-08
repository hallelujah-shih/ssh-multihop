# SSH-Multihop 现有架构优化建议 (2026-03-17)

## 1. 并发与同步优化
### 1.1 引入 `pendingForwards` 机制
*   **问题**：`ForwardService.sync()` 的异步启动逻辑存在竞态，慢连接会导致重复启动协程。
*   **优化**：在 `ForwardService` 中增加内存集合记录正在启动的任务 ID，确保同一隧道在初始化完成前不被二次触发。

### 1.2 数据库 $O(N)$ 查询消除
*   **问题**：同步循环中对每个转发项单独查询状态，产生大量 DB 往返。
*   **优化**：扩展 `ListForwards` 接口，利用 SQL `JOIN` 一次性加载所有 `Forward` 及其 `Status` 到内存进行全量比对。

## 2. 网络传输加固
### 2.1 优化 `bidirectionalCopy`
*   **问题**：依靠 `WaitGroup` 等待可能导致半关闭连接永久挂起。
*   **优化**：只要一方 `io.Copy` 结束，立即显式调用双方连接的 `Close()`，强制回收资源。

### 2.2 非阻塞 Socket 配置 (`SyscallConn`)
*   **问题**：`tcpConn.File()` 会将 Socket 强制切回阻塞模式，干扰 Go 调度器。
*   **优化**：改用 `SyscallConn` 接口，在不脱离 netpoller 控制的前提下配置 TCP Keepalive 选项。

## 3. 安全与交互改进
### 3.1 移除终端交互挂起
*   **问题**：`SSHClientConfigBuilder` 内部使用 `fmt.Scanln`。在守护进程模式下会导致进程无响应。
*   **优化**：重构认证逻辑，禁止在 `internal/connection` 层触发交互。若密钥加密且 Agent 无效，应直接报错并触发退避（Backoff）策略。

## 4. API 响应增强
### 4.1 同步启动反馈
*   **优化**：为 `POST /api/v1/forwards` 增加参数，允许在响应前同步尝试建立连接，而非纯异步等待轮询。
