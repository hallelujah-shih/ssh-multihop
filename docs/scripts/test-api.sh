#!/bin/bash
# API测试脚本 - 验证API稳定性和类型对齐

set -e

API_BASE="http://localhost:18080/api/v1"

echo "=== API 稳定性和类型对齐测试 ==="
echo ""

# 1. Health check
echo "1. Health Check"
curl -s "http://localhost:18080/health" | jq '.'
echo ""

# 2. 测试类型验证 - 无效类型
echo "2. 类型验证测试 - 无效类型"
curl -s -X POST "$API_BASE/forwards" \
    -H "Content-Type: application/json" \
    -d '{
        "type": "invalid_type",
        "service_host": "dc4",
        "service_port": 11434,
        "expose_host": "local",
        "expose_port": 11434
    }' | jq '.'
echo ""

# 3. 测试端口验证 - 无效端口
echo "3. 端口验证测试 - 端口超出范围"
curl -s -X POST "$API_BASE/forwards" \
    -H "Content-Type: application/json" \
    -d '{
        "type": "local_listen_to_remote",
        "service_host": "dc4",
        "service_port": 99999,
        "expose_host": "local",
        "expose_port": 11434
    }' | jq '.'
echo ""

# 4. 测试maxConns验证
echo "4. maxConns验证测试 - RemoteListenToRemote不支持maxConns"
curl -s -X POST "$API_BASE/forwards" \
    -H "Content-Type: application/json" \
    -d '{
        "type": "remote_listen_to_remote",
        "service_host": "dc4",
        "service_port": 11434,
        "expose_host": "vmr.u24",
        "expose_port": 11434,
        "max_conns": 10
    }' | jq '.'
echo ""

# 5. 创建有效的转发 - LocalListenToRemote
echo "5. 创建有效转发 - LocalListenToRemote"
RESULT=$(curl -s -X POST "$API_BASE/forwards" \
    -H "Content-Type: application/json" \
    -d '{
        "type": "local_listen_to_remote",
        "service_host": "dc4",
        "service_port": 11434,
        "expose_host": "local",
        "expose_port": 11434,
        "description": "Test forward"
    }')
echo "$RESULT" | jq '.'
FORWARD_ID=$(echo "$RESULT" | jq -r '.id')
echo "Created forward ID: $FORWARD_ID"
echo ""

# 6. 列出所有转发
echo "6. 列出所有转发"
curl -s "$API_BASE/forwards" | jq '.[] | {id, type, service_host, service_port, expose_host, expose_port}'
echo ""

# 7. 获取特定转发
echo "7. 获取特定转发"
curl -s "$API_BASE/forwards/$FORWARD_ID" | jq '.'
echo ""

# 8. 测试404错误 - 不存在的转发
echo "8. 测试404错误 - 不存在的转发"
curl -s "$API_BASE/forwards/nonexistent-id" | jq '.'
echo ""

# 9. 获取状态
echo "9. 获取转发状态"
sleep 3  # 等待转发启动
curl -s "$API_BASE/status" | jq '.[] | {forward_id, status}'
echo ""

# 10. 删除转发
echo "10. 删除转发"
curl -s -X DELETE "$API_BASE/forwards/$FORWARD_ID" | jq '.'
echo ""

# 11. 验证删除后再获取应该返回404
echo "11. 验证删除后再获取应该返回404"
curl -s "$API_BASE/forwards/$FORWARD_ID" | jq '.'
echo ""

echo "=== API测试完成 ==="
