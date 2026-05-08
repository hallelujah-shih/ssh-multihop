# 7 场景转发测试指南

## 测试目标

测试 7 种转发场景，并通过重启 **vmr.u24** 来验证系统的自愈能力。

## 前提条件

1. **Daemon 运行中**:
```bash
cd /home/shih/test/multi-hop-fwd/v2
./ssh-multihop daemon
```

2. **测试服务运行**:
```bash
# 在本地启动测试 HTTP 服务器（用于场景 3, 4）
python3 -m http.server 8888 &
python3 -m http.server 4000 &

# 在 vmr.u24 上启动测试 HTTP 服务器（用于场景 7）
ssh vmr.u24 "python3 -m http.server 18789 &"
```

## 7 个场景说明

| 场景 | 服务位置 | 监听位置 | 类型 | 测试命令 |
|------|---------|---------|------|---------|
| 1 | dc4:11434 | local:11434 | LocalListenToRemote | `curl localhost:11434` |
| 2 | dc4:11434 | vmr.u24:11434 | RemoteListenToRemote | `ssh vmr.u24 'curl localhost:11434'` |
| 3 | local:8888 | vmr.u24:8888 | RemoteListenToLocal | `ssh vmr.u24 'curl localhost:8888'` |
| 4 | local:4000 | vmr.u24:4000 | RemoteListenToLocal | `ssh vmr.u24 'curl localhost:4000'` |
| 5 | local:22 | local:2222 | LocalListenToRemote | `ssh -p 2222 shih@localhost 'hostname'` |
| 6 | vmr.u24:22 | vsh:2333 | RemoteListenToRemote | `ssh -p 2333 shih@localhost 'hostname'` |
| 7 | vmr.u24:18789 | local:18789 | RemoteListenToLocal | `curl localhost:18789` |

## 测试步骤

### Phase 1: 初始测试（重启前）

```bash
# 1. 配置所有 7 个场景
./test-7-scenarios-manual.sh setup

# 2. 等待 10 秒让转发建立
sleep 10

# 3. 测试所有场景
./test-7-scenarios-manual.sh test

# 4. 查看状态
./test-7-scenarios-manual.sh status
```

**预期结果**: 所有 7 个场景都显示 ✅ PASSED

---

### Phase 2: 重启 vmr.u24

```bash
# 在另一个终端窗口执行：
ssh vmr.u24 'sudo reboot'
```

**预期行为**:
- vmr.u24 重启，所有 SSH 连接断开
- Daemon 检测到连接失败，触发自愈
- 指数退避重连：1s → 2s → 4s → ... → 120s max

---

### Phase 3: 等待恢复（重启后）

```bash
# 等待 vmr.u24 重启完成（约 30-60 秒）
# 可以通过以下命令检查：
ssh vmr.u24 'uptime'

# 等待额外的 30 秒让自愈机制完成
sleep 30

# 查看转发状态
./test-7-scenarios-manual.sh status
```

**预期状态**:
- 受影响场景（2, 3, 4, 6, 7）应该是 `running`
- 不受影响场景（1, 5）应该保持 `running`

---

### Phase 4: 验证恢复（重启后）

```bash
# 再次测试所有场景
./test-7-scenarios-manual.sh test
```

**预期结果**: 所有 7 个场景都显示 ✅ PASSED

---

## 场景详细分析

### 场景 1: dc4:11434 → local:11434

**数据流**: `用户 → local:11434 → SSH → dc4:11434`

**是否受 vmr.u24 重启影响**: ❌ 否
- 路径不经过 vmr.u24
- 应该在整个测试期间保持正常

**验证**:
```bash
curl localhost:11434
# 应该返回 dc4:11434 的响应
```

---

### 场景 2: dc4:11434 → vmr.u24:11434

**数据流**: `vmr.u24 用户 → vmr.u24:11434 → SSH → dc4:11434`

**是否受 vmr.u24 重启影响**: ✅ 是
- Listener 在 vmr.u24 上
- SSH 连接需要经过 vmr.u24

**自愈机制**:
1. vmr.u24 重启 → SSH 连接断开
2. HealthCheck 检测失败（15 秒内）
3. 触发 `attemptRepair()`
4. 指数退避重连
5. 重建 SSH 连接 + Listener
6. 恢复正常

**验证**:
```bash
ssh vmr.u24 'curl localhost:11434'
# vmr.u24 重启期间: ❌ FAILED
# vmr.u24 恢复后: ✅ PASSED
```

---

### 场景 3: local:8888 → vmr.u24:8888

**数据流**: `vmr.u24 用户 → vmr.u24:8888 → SSH → local:8888`

**是否受 vmr.u24 重启影响**: ✅ 是
- Listener 在 vmr.u24 上
- SSH 连接需要经过 vmr.u24

**自愈机制**: 同场景 2

**验证**:
```bash
# 先在本地启动 HTTP 服务器
python3 -m http.server 8888 &

# 测试
ssh vmr.u24 'curl localhost:8888'
# vmr.u24 重启期间: ❌ FAILED
# vmr.u24 恢复后: ✅ PASSED
```

---

### 场景 4: local:4000 → vmr.u24:4000

**数据流**: `vmr.u24 用户 → vmr.u24:4000 → SSH → local:4000`

**是否受 vmr.u24 重启影响**: ✅ 是

**自愈机制**: 同场景 2

**验证**:
```bash
# 先在本地启动 HTTP 服务器
python3 -m http.server 4000 &

# 测试
ssh vmr.u24 'curl localhost:4000'
# vmr.u24 重启期间: ❌ FAILED
# vmr.u24 恢复后: ✅ PASSED
```

---

### 场景 5: local:22 → vsh:2222

**数据流**: `local 用户 → local:2222 → SSH → local:22 → SSH → vsh`

**是否受 vmr.u24 重启影响**: ❌ 否
- 路径不经过 vmr.u24
- 应该在整个测试期间保持正常

**验证**:
```bash
ssh -p 2222 -o StrictHostKeyChecking=no shih@localhost 'hostname'
# 应该返回 "vsh"
```

---

### 场景 6: vmr.u24:22 → vsh:2333

**数据流**: `local 用户 → vsh:2333 → SSH → vmr.u24:22 → SSH → vsh`

**是否受 vmr.u24 重启影响**: ✅ 是
- 需要 SSH 连接到 vmr.u24
- 然后从 vmr.u24 连接到 vsh

**自愈机制**:
1. vmr.u24 重启 → 双 SSH 连接断开
2. HealthCheck 检测失败（15 秒内）
3. 触发 `attemptRepair()`
4. 重建双 SSH 连接 + Listener
5. 恢复正常

**验证**:
```bash
ssh -p 2333 -o StrictHostKeyChecking=no shih@localhost 'hostname'
# vmr.u24 重启期间: ❌ FAILED
# vmr.u24 恢复后: ✅ PASSED
```

---

### 场景 7: vmr.u24:18789 → local:18789

**数据流**: `local 用户 → local:18789 → SSH → vmr.u24:18789`

**是否受 vmr.u24 重启影响**: ✅ 是
- SSH 连接需要经过 vmr.u24
- 服务在 vmr.u24 上

**自愈机制**: 同场景 2

**验证**:
```bash
# 先在 vmr.u24 上启动 HTTP 服务器
ssh vmr.u24 "python3 -m http.server 18789 &"

# 测试
curl localhost:18789
# vmr.u24 重启期间: ❌ FAILED
# vmr.u24 恢复后: ✅ PASSED
```

---

## 故障排查

### 如果场景测试失败

**1. 检查 daemon 日志**:
```bash
tail -f /tmp/forward-test-*.log
```

**2. 检查转发状态**:
```bash
curl -s http://localhost:8080/api/forwards | jq '.'
```

**3. 检查具体转发的状态**:
```bash
FORWARD_ID="<从上面获取的 ID>"
curl -s "http://localhost:8080/api/forwards/$FORWARD_ID" | jq '.'
```

**4. 检查 SSH 连接**:
```bash
# 手动测试 SSH 连接
ssh vmr.u24 'uptime'
ssh dc4 'uptime'
```

### 如果自愈不工作

**1. 检查 health monitoring 是否启动**:
```bash
# 在 daemon 日志中查找
grep "Health monitoring started" /tmp/forward-test-*.log
```

**2. 检查健康检查是否触发修复**:
```bash
grep "Health check failed" /tmp/forward-test-*.log
grep "Starting self-repair" /tmp/forward-test-*.log
```

**3. 检查重连是否成功**:
```bash
grep "Self-repair successful" /tmp/forward-test-*.log
```

---

## 清理

```bash
# 停止所有转发
./test-7-scenarios-manual.sh cleanup

# 停止测试服务器
pkill -f "python3.*8888"
pkill -f "python3.*4000"
ssh vmr.u24 "pkill -f 'python3.*18789'"
```

---

## 预期时间线

| 时间 | 事件 | 预期状态 |
|------|------|---------|
| T0 | 配置所有 7 个场景 | 全部 `running` |
| T+30s | 初始测试完成 | 7/7 ✅ PASSED |
| T+60s | 重启 vmr.u24 | 场景 2,3,4,6,7 开始失败 |
| T+90s | vmr.u24 重启完成 | 所有受影响场景 `starting`（重试中） |
| T+120s | 自愈机制恢复连接 | 受影响场景恢复 `running` |
| T+150s | 重新测试 | 7/7 ✅ PASSED |

**总测试时间**: 约 2-3 分钟

---

## 成功标准

✅ **初始测试**: 7/7 场景通过
✅ **重启后状态**: 受影响场景自动恢复 `running`
✅ **重启后测试**: 7/7 场景通过
✅ **无手动干预**: 全部自动恢复
✅ **日志清晰**: 可以看到自愈过程
