#!/bin/bash
# 7场景自动化测试脚本 - 简洁版
#
# 测试7个场景并验证vmr.u24重启后的自动恢复能力

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

API_BASE="http://localhost:18080/api/v1"
PASS=0
FAIL=0
TEST_PASS=0
TEST_FAIL=0

log() {
    echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}✓${NC} $1"
    PASS=$((PASS + 1))
}

failure() {
    echo -e "${RED}✗${NC} $1"
    FAIL=$((FAIL + 1))
}

test_success() {
    echo -e "${GREEN}✓${NC} $1"
    TEST_PASS=$((TEST_PASS + 1))
}

test_failure() {
    echo -e "${RED}✗${NC} $1"
    TEST_FAIL=$((TEST_FAIL + 1))
}

# 清理环境
cleanup() {
    log "清理环境..."
    sqlite3 ~/.ssh-multihop/ssh-multihop-fwd.db "DELETE FROM forwards;" 2>/dev/null || true
    sqlite3 ~/.ssh-multihop/ssh-multihop-fwd.db "DELETE FROM forward_status;" 2>/dev/null || true
    pkill -f "ssh-multihop daemon" 2>/dev/null || true
    sleep 2
    success "环境已清理"
}

# 创建7个场景
create_scenarios() {
    log "创建7个场景..."

    # 场景1: 127.0.0.1:11434@dc4 → 127.0.0.1:11434@local
    curl -s -X POST "$API_BASE/forwards" -H "Content-Type: application/json" \
        -d '{"type": "local_listen_to_remote", "listen_host": "local", "listen_addr": "127.0.0.1:11434", "service_host": "dc4", "service_addr": "127.0.0.1:11434"}' > /dev/null
    success "场景1: dc4:11434 → local:11434"

    # 场景2: 127.0.0.1:11434@dc4 → 127.0.0.1:11434@vmr.u24
    curl -s -X POST "$API_BASE/forwards" -H "Content-Type: application/json" \
        -d '{"type": "remote_listen_to_remote", "listen_host": "vmr.u24", "listen_addr": "127.0.0.1:11434", "service_host": "dc4", "service_addr": "127.0.0.1:11434"}' > /dev/null
    success "场景2: dc4:11434 → vmr.u24:11434"

    # 场景3: 127.0.0.1:8888@local → 127.0.0.1:8888@vmr.u24
    curl -s -X POST "$API_BASE/forwards" -H "Content-Type: application/json" \
        -d '{"type": "remote_listen_to_local", "listen_host": "vmr.u24", "listen_addr": "127.0.0.1:8888", "service_host": "local", "service_addr": "127.0.0.1:8888"}' > /dev/null
    success "场景3: local:8888 → vmr.u24:8888"

    # 场景4: 127.0.0.1:4000@local → 127.0.0.1:4000@vmr.u24
    curl -s -X POST "$API_BASE/forwards" -H "Content-Type: application/json" \
        -d '{"type": "remote_listen_to_local", "listen_host": "vmr.u24", "listen_addr": "127.0.0.1:4000", "service_host": "local", "service_addr": "127.0.0.1:4000"}' > /dev/null
    success "场景4: local:4000 → vmr.u24:4000"

    # 场景5: 127.0.0.1:22@local → 0.0.0.0:2222@vsh
    curl -s -X POST "$API_BASE/forwards" -H "Content-Type: application/json" \
        -d '{"type": "remote_listen_to_local", "listen_host": "vsh", "listen_addr": "127.0.0.1:2222", "service_host": "local", "service_addr": "127.0.0.1:22"}' > /dev/null
    success "场景5: local:22 → vsh:2222"

    # 场景6: 127.0.0.1:22@vmr.u24 → 0.0.0.0:2333@vsh
    curl -s -X POST "$API_BASE/forwards" -H "Content-Type: application/json" \
        -d '{"type": "remote_listen_to_remote", "listen_host": "vsh", "listen_addr": "0.0.0.0:2333", "service_host": "vmr.u24", "service_addr": "127.0.0.1:22"}' > /dev/null
    success "场景6: vmr.u24:22 → vsh:2333"

    # 场景7: 127.0.0.1:18789@vmr.u24 → 127.0.0.1:18789@local
    curl -s -X POST "$API_BASE/forwards" -H "Content-Type: application/json" \
        -d '{"type": "local_listen_to_remote", "listen_host": "local", "listen_addr": "127.0.0.1:18789", "service_host": "vmr.u24", "service_addr": "127.0.0.1:18789"}' > /dev/null
    success "场景7: vmr.u24:18789 → local:18789"
}

# 测试连接性
test_connectivity() {
    local phase=$1
    log "测试连接性 ($phase)..."

    # 场景1: dc4:11434 → local:11434
    if curl -s -m 5 http://localhost:11434/api/tags > /dev/null 2>&1; then
        test_success "[$phase] 场景1: localhost:11434 → dc4"
    else
        test_failure "[$phase] 场景1: localhost:11434 → dc4"
    fi

    # 场景2: dc4:11434 → vmr.u24:11434
    if ssh vmr.u24 "curl -s -m 5 http://localhost:11434/api/tags" > /dev/null 2>&1; then
        test_success "[$phase] 场景2: vmr.u24:11434 → dc4"
    else
        test_failure "[$phase] 场景2: vmr.u24:11434 → dc4"
    fi

    # 场景3: local:8888 → vmr.u24:8888 (通过HTTP代理测试)
    if ssh vmr.u24 "curl -x 127.0.0.1:8888 -m 5 -I https://www.baidu.com 2>/dev/null | grep -q 'HTTP'" > /dev/null 2>&1; then
        test_success "[$phase] 场景3: vmr.u24:8888 → local (HTTP代理)"
    else
        test_failure "[$phase] 场景3: vmr.u24:8888 → local (HTTP代理)"
    fi

    # 场景4: local:4000 → vmr.u24:4000
    if ssh vmr.u24 "curl -s -m 5 http://localhost:4000" > /dev/null 2>&1; then
        test_success "[$phase] 场景4: vmr.u24:4000 → local"
    else
        test_failure "[$phase] 场景4: vmr.u24:4000 → local"
    fi

    # 场景5: local:22 → vsh:2222 (期望hostname: fedora)
    RESULT=$(ssh -p 2222 -o ConnectTimeout=5 -o BatchMode=yes shih@vsh 'hostname && echo SUCCESS' 2>&1 || true)
    if echo "$RESULT" | grep -q SUCCESS; then
        HOSTNAME=$(echo "$RESULT" | head -1)
        if [[ "$HOSTNAME" == "fedora" ]]; then
            test_success "[$phase] 场景5: vsh:2222 → local:22 (hostname: $HOSTNAME)"
        else
            test_failure "[$phase] 场景5: vsh:2222 → 错误主机 (期望: fedora, 实际: $HOSTNAME)"
        fi
    else
        test_failure "[$phase] 场景5: vsh:2222 → local:22"
    fi

    # 场景6: vmr.u24:22 → vsh:2333 (期望hostname: shih)
    RESULT=$(ssh -p 2333 -o ConnectTimeout=5 -o BatchMode=yes shih@vsh 'hostname && echo SUCCESS' 2>&1 || true)
    if echo "$RESULT" | grep -q SUCCESS; then
        HOSTNAME=$(echo "$RESULT" | head -1)
        if [[ "$HOSTNAME" == "shih" ]]; then
            test_success "[$phase] 场景6: vsh:2333 → vmr.u24:22 (hostname: $HOSTNAME)"
        else
            test_failure "[$phase] 场景6: vsh:2333 → 错误主机 (期望: shih, 实际: $HOSTNAME)"
        fi
    else
        test_failure "[$phase] 场景6: vsh:2333 → vmr.u24:22"
    fi

    # 场景7: vmr.u24:18789 → local:18789
    if curl -s -m 5 http://localhost:18789 > /dev/null 2>&1; then
        test_success "[$phase] 场景7: localhost:18789 → vmr.u24"
    else
        test_failure "[$phase] 场景7: localhost:18789 → vmr.u24"
    fi
}

# 重启vmr.u24
reboot_vmr() {
    log "重启vmr.u24..."
    ssh vmr.u24 "sudo reboot" > /dev/null 2>&1 || true
    success "重启命令已发送"

    log "等待vmr.u24离线..."
    local count=0
    while ssh vmr.u24 "hostname" > /dev/null 2>&1; do
        sleep 1
        count=$((count + 1))
        if [ $count -gt 15 ]; then
            log "vmr.u24开始重启"
            break
        fi
    done

    log "等待vmr.u24恢复（通过SSH hostname检测）..."
    count=0
    while ! ssh vmr.u24 "hostname" > /dev/null 2>&1; do
        sleep 1
        count=$((count + 1))
        echo -n "."
        if [ $count -gt 60 ]; then
            echo ""
            failure "vmr.u24恢复超时"
            return 1
        fi
    done
    echo ""
    success "vmr.u24已恢复（耗时${count}秒）"

    # 显示vmr.u24上的监听端口
    log "检查vmr.u24端口状态..."
    ssh vmr.u24 "ss -tpnl | grep LISTEN" 2>/dev/null || true
}

# 主测试流程
main() {
    echo "======================================"
    echo "7场景自动化测试"
    echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "======================================"
    echo ""

    # 阶段1: 清理并创建
    cleanup
    ./ssh-multihop daemon --port 18080 > /tmp/daemon-test.log 2>&1 &
    sleep 2
    create_scenarios
    echo ""

    # 阶段2: 等待启动
    log "等待启动（20秒）..."
    for i in {1..20}; do echo -n "."; sleep 1; done
    echo ""
    sleep 2
    echo ""

    # 阶段3: 初始测试
    TEST_PASS=0
    TEST_FAIL=0
    test_connectivity "初始测试"
    INITIAL_PASS=$TEST_PASS
    INITIAL_FAIL=$TEST_FAIL
    echo ""

    # 阶段4: vmr.u24重启测试
    echo "======================================"
    log "vmr.u24重启测试"
    echo "======================================"
    reboot_vmr
    echo ""

    # 阶段5: 等待自动恢复（智能等待）
    log "等待自动恢复（最多30秒，每5秒检测一次）..."
    echo "预期: 15s(健康检查) + 5s(sync) + 10s(重建)"
    local max_wait=30
    local waited=0
    while [ $waited -lt $max_wait ]; do
        echo -n "  [$(date '+%H:%M:%S')] ${waited}/${max_wait}秒"
        sleep 5
        waited=$((waited + 5))
    done
    echo ""

    # 显示恢复后的端口状态
    log "恢复后vmr.u24端口状态..."
    ssh vmr.u24 "ss -tpnl | grep -E ':(11434|8888|4000|18789)' || echo '  (无相关端口监听)'"
    echo ""

    # 阶段6: 恢复后测试
    TEST_PASS=0
    TEST_FAIL=0
    test_connectivity "恢复测试"
    RECOVER_PASS=$TEST_PASS
    RECOVER_FAIL=$TEST_FAIL
    echo ""

    # 总结
    echo "======================================"
    log "测试总结"
    echo "======================================"
    echo "初始测试: $INITIAL_PASS/7 通过, $INITIAL_FAIL/7 失败"
    echo "恢复测试: $RECOVER_PASS/7 通过, $RECOVER_FAIL/7 失败"
    echo ""

    TOTAL_PASS=$((INITIAL_PASS + RECOVER_PASS))
    TOTAL_FAIL=$((INITIAL_FAIL + RECOVER_FAIL))

    echo "总测试: $TOTAL_PASS/14 通过, $TOTAL_FAIL/14 失败"
    echo ""

    if [ $TOTAL_FAIL -eq 0 ]; then
        echo -e "${GREEN}✓ 所有测试通过！${NC}"
        echo "✓ 新架构验证成功：StatusError-driven rebuild工作正常"
        exit 0
    else
        echo -e "${RED}✗ 有 $TOTAL_FAIL 个测试失败${NC}"
        exit 1
    fi
}

# 运行测试
main
