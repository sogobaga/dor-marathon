#!/bin/sh
# 從正式圖床 img.dor.tw 下載宣傳片用的關主卡面／場景／立繪到 assets/
# 卡面網址取自 scripts/master_cards.manifest.json（帶 ?v= 版本參數，確保是最新上傳的版本）
set -e
cd "$(dirname "$0")"
mkdir -p assets
REPO=../..
for c in DOR-TPE-001 DOR-TPE-002 DOR-NTP-001 DOR-TXG-001 DOR-KHH-001 DOR-TNN-001 DOR-TAO-001 DOR-HSZ-001 DOR-CHA-001 DOR-ILA-001 DOR-HUA-001 DOR-TTT-001; do
  url=$(python3 -c "import json,sys;print(json.load(open('$REPO/scripts/master_cards.manifest.json'))['$c']['url'])")
  curl -fsS -o "assets/$c-card.webp" "$url"
done
# 大安小鹿：場景圖 + 立繪（未列入 migration 118 的 v6 修圖，沿用 master/{code}.webp）
curl -fsS -o assets/DOR-TPE-001-scene.webp https://img.dor.tw/scene/DOR-TPE-001.webp
curl -fsS -o assets/DOR-TPE-001-master.webp https://img.dor.tw/master/DOR-TPE-001.webp
echo "assets 下載完成"
