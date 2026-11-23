#!/usr/bin/env bash
# Short_NURL v3 — 真机联调脚本（在服务器上执行）
#
# 前置：docker compose 已能起 openresty + shorturl-go，且 config/config.json 已配置 domain。
# 用法：bash tests/interop/run.sh [https://your-domain.com]
set -euo pipefail

DOMAIN="${1:-${NURL_DOMAIN:-}}"
if [ -z "$DOMAIN" ]; then
  echo "用法：bash tests/interop/run.sh https://your-domain.com" >&2
  exit 1
fi

KEY="${NURL_KEY:-}"
if [ -z "$KEY" ]; then
  echo "请先设置 NURL_KEY（用 nurl -new 生成常驻密钥）" >&2
  exit 1
fi

PASS=0
FAIL=0
step() {
  local name="$1" want="$2" got="$3"
  if [ "$got" = "$want" ]; then
    echo "  [ok]   $name"
    PASS=$((PASS + 1))
  else
    echo "  [fail] $name (want=$want got=$got)"
    FAIL=$((FAIL + 1))
  fi
}

CODE="s$(date +%s | tail -c 4)"

echo "▸ 基础连通"
step "ping" 200 "$(curl -s -o /dev/null -w '%{http_code}' "$DOMAIN/api/ping")"
step "ping body" '{"ok":true}' "$(curl -s "$DOMAIN/api/ping")"

echo "▸ WebUI 通道"
step "OPTIONS /api/create" 204 "$(curl -s -o /dev/null -w '%{http_code}' -X OPTIONS "$DOMAIN/api/create")"
step "create 无 Key" 401 "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$DOMAIN/api/create" -H 'Content-Type: application/json' -d '{"url":"https://example.com"}')"
step "PUT /api/create" 405 "$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$DOMAIN/api/create" -H "X-Token: $KEY" -H 'Content-Type: application/json' -d '{}')"
step "GET /api/nope" 404 "$(curl -s -o /dev/null -w '%{http_code}' "$DOMAIN/api/nope")"

echo "▸ 创建 / 跳转 / 删除"
CREATE=$(curl -s -X POST "$DOMAIN/api/create" \
  -H "X-Token: $KEY" -H 'Content-Type: application/json' \
  -d "{\"url\":\"https://example.com/smoke\",\"code\":\"$CODE\"}")
echo "  create: $CREATE"
SHORT=$(echo "$CREATE" | sed -n 's/.*"short_url":"\([^"]*\)".*/\1/p')
step "create synced" true "$(echo "$CREATE" | grep -o '"synced":true' | sed 's/"synced"://' | tr -d '"')"
step "跳转 302" 302 "$(curl -s -o /dev/null -w '%{http_code}' -L --max-redirs 0 "$SHORT" || true)"

echo "▸ 统计与列表"
STAT=$(curl -s -H "X-Token: $KEY" "$DOMAIN/api/stat")
echo "  stat: $STAT"
step "hot_available" true "$(echo "$STAT" | grep -o '"hot_available":true' | sed 's/"hot_available"://' | tr -d '"')"
LIST=$(curl -s -H "X-Token: $KEY" "$DOMAIN/api/list")
step "list contains code" 1 "$(echo "$LIST" | grep -c "\"id\":\"$CODE\"" || true)"

echo "▸ Headless 通道"
HKEY="${NURL_HEADLESS_KEY:-$KEY}"
step "OPTIONS /headless/api/create" 405 "$(curl -s -o /dev/null -w '%{http_code}' -X OPTIONS "$DOMAIN/headless/api/create")"
step "headless list" 200 "$(curl -s -o /dev/null -w '%{http_code}' -H "X-Headless-Token: $HKEY" "$DOMAIN/headless/api/list")"
step "headless get" 200 "$(curl -s -o /dev/null -w '%{http_code}' -H "X-Headless-Token: $HKEY" "$DOMAIN/headless/api/get/$CODE")"

echo "▸ 删除"
DEL_CODE=$(curl -s -o /tmp/nurl_del_body -w '%{http_code}' -X POST "$DOMAIN/api/delete" -H "X-Token: $KEY" -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}")
echo "  delete: $(cat /tmp/nurl_del_body)"
step "delete ok" 200 "$DEL_CODE"
step "重复删除 404" 404 "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$DOMAIN/api/delete" -H "X-Token: $KEY" -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}")"

echo
echo "通过 $PASS 项，失败 $FAIL 项"
[ "$FAIL" -eq 0 ]
