#!/usr/bin/env bash
# DOR API MVP 整合測試腳本
# 使用方式：bash scripts/test-api.sh
# 前置需求：API 服務在 localhost:8080 運行，seed.sql 已執行

set -euo pipefail

BASE="http://localhost:8080/api/v1"
RACE_ID=""   # 執行後自動取得
TOKEN=""     # 登入後自動取得
REFRESH=""

divider() { echo; echo "========== $1 =========="; }

assert_ok() {
  local res="$1" label="$2"
  if echo "$res" | grep -q '"error"'; then
    echo "FAIL [$label]: $res"
    exit 1
  fi
  echo "OK   [$label]"
}

# ---- 1. 健康檢查 ----
divider "HEALTH"
res=$(curl -sf "$BASE/../health")
echo "$res"

# ---- 2. 取得公開賽事列表 ----
divider "LIST RACES"
res=$(curl -sf "$BASE/races")
echo "$res" | head -c 200
RACE_ID=$(echo "$res" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo
echo "→ raceID: $RACE_ID"

# ---- 3. 新使用者註冊 ----
divider "REGISTER"
ts=$(date +%s)
res=$(curl -sf -X POST "$BASE/auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"tester_${ts}@dor.tw\",\"handle\":\"tester_${ts}\",\"name\":\"測試跑者${ts}\",\"password\":\"Test1234!\"}")
assert_ok "$res" "register"

# ---- 4. 登入（取得 token）----
divider "LOGIN"
res=$(curl -sf -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"tester_${ts}@dor.tw\",\"password\":\"Test1234!\"}")
assert_ok "$res" "login"
TOKEN=$(echo "$res" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
REFRESH=$(echo "$res" | grep -o '"refresh_token":"[^"]*"' | cut -d'"' -f4)
echo "→ token: ${TOKEN:0:40}..."

# ---- 5. 取得自己資料 ----
divider "GET ME"
res=$(curl -sf "$BASE/auth/me" -H "Authorization: Bearer $TOKEN")
assert_ok "$res" "me"
echo "$res"

# ---- 6. 報名賽事 ----
divider "REGISTER RACE"
if [ -z "$RACE_ID" ]; then
  echo "SKIP: no open race found"
else
  res=$(curl -sf -X POST "$BASE/races/$RACE_ID/register" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"distance":10}')
  assert_ok "$res" "race register"
  echo "$res"
fi

# ---- 7. 上傳活動（跑步數據）----
divider "UPLOAD ACTIVITY"
if [ -z "$RACE_ID" ]; then
  echo "SKIP: no race_id"
else
  NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
  res=$(curl -sf -X POST "$BASE/activities/upload" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"race_id\":\"$RACE_ID\",\"distance_km\":5.2,\"avg_pace_s\":320,\"recorded_at\":\"$NOW\"}")
  # 允許「不在賽事期間」的錯誤（seed 資料未來才開始）
  echo "$res"
  echo "→ (如果賽事尚未開始，此處傳回 error 是正常的)"
fi

# ---- 8. 查看排行榜 ----
divider "RANKING"
if [ -n "$RACE_ID" ]; then
  res=$(curl -sf "$BASE/races/$RACE_ID/ranking")
  echo "$res" | head -c 300
fi

# ---- 9. Refresh Token ----
divider "REFRESH TOKEN"
res=$(curl -sf -X POST "$BASE/auth/refresh" \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$REFRESH\"}")
assert_ok "$res" "refresh"
TOKEN=$(echo "$res" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
echo "→ new token: ${TOKEN:0:40}..."

# ---- 10. 抽獎額度 ----
divider "SPIN QUOTA"
if [ -n "$RACE_ID" ]; then
  res=$(curl -sf "$BASE/rewards/quota/$RACE_ID" -H "Authorization: Bearer $TOKEN")
  assert_ok "$res" "spin quota"
  echo "$res"
fi

# ---- 11. 貼紙卡狀態 ----
divider "STICKER CARD"
if [ -n "$RACE_ID" ]; then
  res=$(curl -sf "$BASE/rewards/stickers/$RACE_ID" -H "Authorization: Bearer $TOKEN")
  assert_ok "$res" "sticker card"
  echo "$res"
fi

# ---- 12. 個人統計 ----
divider "PROFILE STATS"
res=$(curl -sf "$BASE/profile/stats" -H "Authorization: Bearer $TOKEN")
assert_ok "$res" "profile stats"
echo "$res"

# ---- 13. 完賽紀錄 ----
divider "PROFILE RECORDS"
res=$(curl -sf "$BASE/profile/records" -H "Authorization: Bearer $TOKEN")
assert_ok "$res" "profile records"
echo "$res"

# ---- 14. Admin 登入 ----
divider "ADMIN LOGIN"
admin_res=$(curl -sf -X POST "$BASE/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@dor.tw","password":"password"}')
assert_ok "$admin_res" "admin login"
ADMIN_TOKEN=$(echo "$admin_res" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
echo "→ admin token: ${ADMIN_TOKEN:0:40}..."

# ---- 15. Admin 取得賽事報名名單 ----
divider "ADMIN SIGNUPS"
if [ -n "$RACE_ID" ]; then
  res=$(curl -sf "$BASE/admin/races/$RACE_ID/signups" \
    -H "Authorization: Bearer $ADMIN_TOKEN")
  echo "$res" | head -c 300
fi

divider "ALL TESTS PASSED"
