# 城市探索「純打卡點」場景圖批次管線：PNG → WebP(q82, 長邊960) → R2 scene/ → manifest + migration SQL。
#
# 背景：explore_bosses 同時存「關主」與「純打卡點(checkin_only)」。572 個關主的 scene/master 圖早已上線
# （migration 115 以 'https://img.dor.tw/scene/' || code || '.webp' 批次填值）；本腳本只處理**其餘 486 個
# 純打卡點**的場景圖，一張都不准動到那 572 張線上圖。三道獨立防線：
#   ① 硬排除名單：直接解析 migration 115 的 572 個 code，命中即跳過（不壓縮、不上傳、不產 SQL）
#   ② 遠端已存在即跳過：上傳前 head_object，R2 已有 scene/{code}.webp 就不覆蓋（保護任何線上物件）
#   ③ SQL 只填空值：UPDATE ... WHERE scene_image_url = ''，任何已有 URL 的列都不會被改寫
# 尺寸/畫質對齊線上既有場景圖（實測 960×480 WebP，60~120KB）；原始檔為 1600×800 PNG。
# 憑證讀 repo 根 r2.env（不進 git、不進聊天）。執行：python scripts/scene-upload/upload_scenes.py
#   DRY_RUN=1 只比對與壓縮、不上傳不寫 manifest（仍會產 SQL 供檢查）
import hashlib
import io
import json
import os
import re
import sys
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone

import truststore

truststore.inject_into_ssl()  # 本機有 TLS 攔截層(安控 MITM CA 只在 Windows 系統憑證庫)，certifi 公開鏈驗不過

import boto3
from botocore.exceptions import ClientError
from PIL import Image

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SRC = os.path.join(REPO, "source", "data", "pictures", "check_point_scene")
MIGRATIONS = os.path.join(REPO, "services", "api", "migrations")
M115 = os.path.join(MIGRATIONS, "115_explore_boss_image_urls.sql")   # 線上 572 張的權威名單
M079 = os.path.join(MIGRATIONS, "079_explore_checkin_points_seed.sql")  # 純打卡點 486 個 code
MANIFEST = os.path.join(REPO, "scripts", "scene_images.manifest.json")
SQL_OUT = os.path.join(MIGRATIONS, "164_explore_checkin_scene_images.sql")
SUFFIX = "-Scene.png"
MAX_DIM = 960
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

with open(M115, encoding="utf-8") as f:
    ONLINE = set(re.findall(r"'(DOR-[A-Z0-9-]+)'", f.read()))          # 防線①
with open(M079, encoding="utf-8") as f:
    CHECKIN = set(re.findall(r"\('(DOR-[A-Z0-9-]+)'", f.read()))
if len(ONLINE) != 572:
    sys.exit(f"ABORT: migration 115 解析出 {len(ONLINE)} 個 code，預期 572——排除名單不可信，停止。")

prev = {}
if os.path.exists(MANIFEST):
    with open(MANIFEST, encoding="utf-8") as f:
        prev = json.load(f)

allfiles = sorted(f for f in os.listdir(SRC) if f.endswith(SUFFIX) and f.startswith("DOR-"))
files = [f for f in allfiles if f[: -len(SUFFIX)] not in ONLINE]
excluded = len(allfiles) - len(files)
codes = {f[: -len(SUFFIX)] for f in files}
print(f"{len(allfiles)} scene files: {excluded} 已在線上(排除) / {len(files)} 待處理", flush=True)
print(f"待處理 vs migration 079 純打卡點：多出 {len(codes - CHECKIN)} 個、缺 {len(CHECKIN - codes)} 個", flush=True)
if codes - CHECKIN:
    print("  多出(不在 079 名單):", sorted(codes - CHECKIN)[:10], flush=True)
if CHECKIN - codes:
    print("  缺圖(079 有但無檔案):", sorted(CHECKIN - codes)[:10], flush=True)

manifest = dict(prev)
uploaded, skipped_same, skipped_remote, failed = [], [], [], []


def process(fname: str):
    code = fname[: -len(SUFFIX)]
    if code in ONLINE:  # 防線①（雙保險：files 已濾過，這裡再擋一次）
        return
    key = f"scene/{code}.webp"
    url = f"https://img.dor.tw/{key}"
    try:
        with open(os.path.join(SRC, fname), "rb") as f:
            src = f.read()
        sha = hashlib.sha256(src).hexdigest()
        if prev.get(code, {}).get("srcSha256") == sha:
            skipped_same.append(code)
            return
        if not DRY_RUN:  # 防線②：R2 已有同名物件就不覆蓋
            try:
                s3.head_object(Bucket=BUCKET, Key=key)
                skipped_remote.append(code)
                return
            except ClientError as e:
                if e.response["Error"]["Code"] not in ("404", "NoSuchKey", "NotFound"):
                    raise
        im = Image.open(io.BytesIO(src))
        if im.mode not in ("RGB", "RGBA"):
            im = im.convert("RGB")
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
            "srcSha256": sha, "srcBytes": len(src), "webpBytes": len(webp), "dim": list(im.size),
            "key": key, "url": url, "uploadedAt": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        }
        uploaded.append(code)
    except Exception as e:  # noqa: BLE001
        failed.append((fname, str(e)[:300]))
    n = len(uploaded) + len(skipped_same) + len(skipped_remote) + len(failed)
    if n % 50 == 0 or n == len(files):
        print(f"progress {n}/{len(files)} (up {len(uploaded)}, same {len(skipped_same)}, remote {len(skipped_remote)}, fail {len(failed)})", flush=True)


with ThreadPoolExecutor(max_workers=5) as ex:
    list(ex.map(process, files))

if not DRY_RUN:
    with open(MANIFEST, "w", encoding="utf-8") as f:
        json.dump(manifest, f, indent=1, ensure_ascii=False)

# 產出 migration：只填空值(防線③)、只碰本次確認有圖的 code、明寫 checkin_only 條件
sql_codes = sorted(set(uploaded) | set(skipped_same) | set(skipped_remote))
if sql_codes:
    body = ",\n".join(f"  '{c}'" for c in sql_codes)
    with open(SQL_OUT, "w", encoding="utf-8", newline="\n") as f:
        f.write(
            "-- Migration 164: 城市探索「純打卡點」場景圖上線（R2: img.dor.tw/scene/{code}.webp）\n"
            "-- 由 scripts/scene-upload/upload_scenes.py 產生；圖檔已上傳 R2，本檔只把 URL 寫進 DB。\n"
            "-- 安全條件：scene_image_url = '' —— 只填從未有圖的列，既有 572 張關主場景圖（migration 115）\n"
            "-- 與任何已設定過的圖片一律不受影響；重複執行亦為 no-op。\n"
            "UPDATE explore_bosses SET scene_image_url = 'https://img.dor.tw/scene/' || code || '.webp'\n"
            "WHERE checkin_only = TRUE AND scene_image_url = '' AND code IN (\n"
            f"{body}\n);\n\n"
            "INSERT INTO schema_migrations (version) VALUES ('164') ON CONFLICT DO NOTHING;\n"
        )

total_new = sum(manifest[c]["webpBytes"] for c in sql_codes if c in manifest)
print(f"DONE.{' (DRY RUN)' if DRY_RUN else ''} uploaded={len(uploaded)} skipped_same={len(skipped_same)} "
      f"skipped_remote_exists={len(skipped_remote)} failed={len(failed)} excluded_online={excluded}")
print(f"webp {total_new / 1048576:.1f} MB; manifest -> {os.path.relpath(MANIFEST, REPO)}; sql -> {os.path.relpath(SQL_OUT, REPO)} ({len(sql_codes)} codes)")
for fname, err in failed:
    print("FAILED:", fname, err)
if failed:
    sys.exit(1)
