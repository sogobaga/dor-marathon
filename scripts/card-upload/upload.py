# 城市探索關主卡片批次管線：PNG → WebP(q82, 長邊1200) → R2 explore/cards/ → manifest + UPDATE SQL。
# 增量設計：scripts/master_cards.manifest.json 記每張原始檔 sha256——未來新版圖包重跑本腳本，
# 內容相同的自動跳過，只壓縮/上傳/更新有變動的張。
# 憑證讀 repo 根 r2.env（不進 git、不進聊天）。執行：python scripts/card-upload/upload.py（DRY_RUN=1 只比對不上傳）
# 2026-08-30 快取失效：R2 物件以 immutable 一年快取，同 key 覆蓋不會被瀏覽器/Cloudflare 重抓，所以「有變動的卡」
# 其 card_image_url 一律改帶 ?v=<sha8> 版本參數（SQL 只產出變動卡；未變動的卡不動 DB、不打擾使用者快取）。
import hashlib
import io
import json
import os
import sys
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone

import truststore

truststore.inject_into_ssl()  # 本機有 TLS 攔截層(安控 MITM CA 只在 Windows 系統憑證庫)，certifi 公開鏈驗不過

import boto3
from PIL import Image

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SRC = os.path.join(REPO, "source", "data", "pictures", "Master_Card")
MANIFEST = os.path.join(REPO, "scripts", "master_cards.manifest.json")
SQL_OUT = os.path.join(os.path.dirname(__file__), "update_card_urls.sql")
MAX_DIM = 1200
QUALITY = 82
DRY_RUN = os.environ.get("DRY_RUN") == "1"

env = {}
with open(os.path.join(REPO, "r2.env"), encoding="utf-8-sig") as f:
    for line in f:
        line = line.strip()
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip()

s3 = boto3.client(
    "s3",
    endpoint_url=f"https://{env['R2_ACCOUNT_ID']}.r2.cloudflarestorage.com",
    aws_access_key_id=env["R2_ACCESS_KEY_ID"],
    aws_secret_access_key=env["R2_SECRET_ACCESS_KEY"],
    region_name="auto",
)
BUCKET = env["R2_BUCKET"]

prev = {}
if os.path.exists(MANIFEST):
    with open(MANIFEST, encoding="utf-8") as f:
        prev = json.load(f)

# 檔名=<code>-Card.png，code 前綴為各縣市碼（DOR-CHA 彰化/DOR-TPE 台北/DOR-TRK-* 等），一律 DOR- 開頭
files = sorted(f for f in os.listdir(SRC) if f.lower().endswith("-card.png") and f.startswith("DOR-"))
print(f"total {len(files)} files", flush=True)

manifest = {}
sql_lines = []
failed = []
done = skipped = 0


def process(fname: str):
    global done, skipped
    code = fname[: -len("-Card.png")]
    with open(os.path.join(SRC, fname), "rb") as f:
        src = f.read()
    sha = hashlib.sha256(src).hexdigest()
    key = f"explore/cards/{code}.webp"
    # 變動卡的 URL 帶 ?v=<sha8>（首次上傳的新卡也帶，日後再改版就自然換版本）
    url = f"https://img.dor.tw/{key}?v={sha[:8]}"
    try:
        if prev.get(code, {}).get("srcSha256") == sha:
            manifest[code] = prev[code]
            skipped += 1
        else:
            im = Image.open(io.BytesIO(src))
            im.thumbnail((MAX_DIM, MAX_DIM), Image.LANCZOS)
            buf = io.BytesIO()
            im.save(buf, "WEBP", quality=QUALITY, method=6)
            webp = buf.getvalue()
            if not DRY_RUN:
                s3.put_object(
                    Bucket=BUCKET, Key=key, Body=webp, ContentType="image/webp",
                    CacheControl="public, max-age=31536000, immutable",
                )
            manifest[code] = {
                "srcSha256": sha, "srcBytes": len(src), "webpBytes": len(webp),
                "key": key, "url": url, "uploadedAt": datetime.now(timezone.utc).isoformat(timespec="seconds"),
            }
            sql_lines.append(f"UPDATE explore_bosses SET card_image_url='{url}' WHERE code='{code}';")
    except Exception as e:  # noqa: BLE001
        failed.append((fname, str(e)[:300]))
    done += 1
    if done % 50 == 0 or done == len(files):
        print(f"progress {done}/{len(files)} (skip {skipped}, fail {len(failed)})", flush=True)


with ThreadPoolExecutor(max_workers=5) as ex:
    list(ex.map(process, files))

if not DRY_RUN:
    with open(MANIFEST, "w", encoding="utf-8") as f:
        json.dump(manifest, f, indent=1, ensure_ascii=False)
with open(SQL_OUT, "w", encoding="utf-8") as f:
    f.write("\n".join(sorted(sql_lines)) + "\n")

total_webp = sum(m.get("webpBytes", 0) for m in manifest.values())
print(f"DONE.{' (DRY RUN)' if DRY_RUN else ''} uploaded={done - skipped - len(failed)} skipped={skipped} failed={len(failed)}")
print(f"webp total {total_webp / 1048576:.1f} MB; manifest -> scripts/master_cards.manifest.json; sql -> {SQL_OUT}")
for fname, err in failed:
    print("FAILED:", fname, err)
if failed:
    sys.exit(1)
