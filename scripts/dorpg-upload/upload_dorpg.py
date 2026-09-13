# DORPG P1（本機互動手感）素材批次上傳：怪物圖集 + BGM + 戰鬥特效包 → R2 dorpg/ 前綴。
#
# 背景：DORPG_P1 契約（scratchpad/dorpg_p1/CONTRACT.md §3）把「上傳到 CDN」與「純本地小音效」分開——
# 大檔（怪物 sprite 圖集、BGM、特效 atlas）走 R2 公開網域 img.dor.tw；18 個 .web.wav 小音效改放
# apps/web/public/ui/dorpg/audio/（同源，免 CORS），不進 R2，見同目錄的 copy_audio.py 段落（本檔只管上傳部分）。
#
# 來源三包：
#   - source/ui/08_DORPG_Content_Pack_v1/assets/monsters/<id>/sprites/<action>.webp（5 隻 × 4 動作 = 20 張，1536×1024）
#   - source/music/Battle_BGM/{Master_BGM,BOSS_BGM}.mp3
#   - source/music/Battle_FX/DORPG_Combat_FX_Pack_v12/assets/{effects,labels,digits}/**/atlas.webp、labels 另有 label.webp
#
# 比照 scripts/scene-upload/upload_scenes.py 的三個習慣：
#   ① 憑證只從 repo 根 r2.env 讀（不進 git、不進聊天、輸出絕不印憑證值）
#   ② head_object 已存在即跳過（不覆蓋任何已上線物件；重跑本腳本是 no-op）
#   ③ DRY_RUN=1 只列清單與總量、不上傳、不寫 manifest
#
# 執行：
#   cd "scripts/dorpg-upload" 或在 repo 任意處
#   DRY_RUN=1 python scripts/dorpg-upload/upload_dorpg.py   # 先看清單
#   python scripts/dorpg-upload/upload_dorpg.py             # 實際上傳
#
# 輸出 scripts/dorpg-upload/manifest.json：key -> { size, etag, contentType, url, uploadedAt }。
import hashlib
import json
import os
import sys
from datetime import datetime, timezone

import truststore

truststore.inject_into_ssl()  # 本機 TLS 攔截層（安控 MITM CA 只在 Windows 系統憑證庫），certifi 公開鏈驗不過——比照 scene-upload

import boto3
from botocore.exceptions import ClientError

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
CONTENT_PACK = os.path.join(REPO, "source", "ui", "08_DORPG_Content_Pack_v1")
BGM_DIR = os.path.join(REPO, "source", "music", "Battle_BGM")
FX_PACK = os.path.join(REPO, "source", "music", "Battle_FX", "DORPG_Combat_FX_Pack_v12")

MANIFEST = os.path.join(os.path.dirname(__file__), "manifest.json")
DRY_RUN = os.environ.get("DRY_RUN") == "1"

MONSTER_IDS = [
    "DOR-MON-A-67000200001",
    "DOR-MON-B-0089",
    "DOR-MON-C-0229",
    "DOR-MON-D-0182",
    "DOR-MON-E-0052",
]
ACTIONS = ["idle", "attack", "hit", "death"]
WEAPONS = ["sword", "staff", "bow", "greatsword"]
RESULTS = ["normal", "critical", "miss", "immune"]
LABEL_KINDS = ["critical", "miss"]

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
PUBLIC_BASE = "https://img.dor.tw"


def plan():
    """組出 (local_path, key, content_type) 清單——路徑形狀照 CONTRACT.md §3。"""
    items = []
    for mid in MONSTER_IDS:
        for action in ACTIONS:
            local = os.path.join(CONTENT_PACK, "assets", "monsters", mid, "sprites", f"{action}.webp")
            items.append((local, f"dorpg/mon/{mid}/{action}.webp", "image/webp"))
    items.append((os.path.join(BGM_DIR, "Master_BGM.mp3"), "dorpg/bgm/master.mp3", "audio/mpeg"))
    items.append((os.path.join(BGM_DIR, "BOSS_BGM.mp3"), "dorpg/bgm/boss.mp3", "audio/mpeg"))
    for weapon in WEAPONS:
        for result in RESULTS:
            local = os.path.join(FX_PACK, "assets", "effects", weapon, result, "atlas.webp")
            items.append((local, f"dorpg/fx/effects/{weapon}/{result}/atlas.webp", "image/webp"))
    for kind in LABEL_KINDS:
        for variant in ("atlas", "label"):
            local = os.path.join(FX_PACK, "assets", "labels", kind, f"{variant}.webp")
            items.append((local, f"dorpg/fx/labels/{kind}/{variant}.webp", "image/webp"))
    items.append((os.path.join(FX_PACK, "assets", "digits", "ivory_gold", "atlas.webp"), "dorpg/fx/digits/atlas.webp", "image/webp"))
    return items


def main():
    items = plan()
    missing = [local for local, _key, _ct in items if not os.path.isfile(local)]
    if missing:
        sys.exit("ABORT: 來源檔案缺失（前 5 筆）：\n" + "\n".join(missing[:5]))

    prev = {}
    if os.path.exists(MANIFEST):
        with open(MANIFEST, encoding="utf-8") as f:
            prev = json.load(f)

    manifest = dict(prev)
    uploaded, skipped_same, skipped_remote, total_bytes = [], [], [], 0

    for local, key, content_type in items:
        with open(local, "rb") as f:
            data = f.read()
        sha = hashlib.sha256(data).hexdigest()
        size = len(data)
        total_bytes += size
        url = f"{PUBLIC_BASE}/{key}"

        if prev.get(key, {}).get("srcSha256") == sha:
            skipped_same.append(key)
            continue

        if not DRY_RUN:
            try:  # 防覆蓋：R2 已有同名物件就不覆蓋（可能是別的流程已上傳過同路徑）
                s3.head_object(Bucket=BUCKET, Key=key)
                skipped_remote.append(key)
                continue
            except ClientError as e:
                if e.response["Error"]["Code"] not in ("404", "NoSuchKey", "NotFound"):
                    raise
            resp = s3.put_object(
                Bucket=BUCKET, Key=key, Body=data, ContentType=content_type,
                CacheControl="public, max-age=31536000, immutable",
            )
            etag = resp.get("ETag", "").strip('"')
        else:
            etag = None

        manifest[key] = {
            "srcSha256": sha, "size": size, "etag": etag, "contentType": content_type,
            "url": url, "uploadedAt": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        }
        uploaded.append(key)

    print(f"total items={len(items)} bytes={total_bytes} ({total_bytes / 1048576:.2f} MB)")
    print(f"{'(DRY RUN) would upload' if DRY_RUN else 'uploaded'}={len(uploaded)} skipped_same={len(skipped_same)} skipped_remote_exists={len(skipped_remote)}")
    if DRY_RUN:
        print("keys:")
        for local, key, content_type in items:
            print(f"  {key}  <- {os.path.relpath(local, REPO)}  ({content_type})")
        return

    with open(MANIFEST, "w", encoding="utf-8") as f:
        json.dump(manifest, f, indent=1, ensure_ascii=False, sort_keys=True)
    print(f"manifest -> {os.path.relpath(MANIFEST, REPO)}")


if __name__ == "__main__":
    main()
