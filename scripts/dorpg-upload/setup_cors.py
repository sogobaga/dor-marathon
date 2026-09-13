# DORPG R2 bucket CORS 設定：Web Audio decodeAudioData 與 canvas drawImage(atlas) 取像素資料
# 都需要對應物件回 Access-Control-Allow-Origin，否則瀏覽器視為「已污染」的 canvas/buffer 而拒讀。
#
# 背景：curl -sI -H "Origin: https://www.dor.tw" 對既有 scene 物件測試，回應沒有
# access-control-allow-origin（bucket 目前無 CORS 設定），故本檔用 boto3 put_bucket_cors 補上。
# 只加 GET/HEAD 給前台三個網域，不動其餘 bucket 設定；重跑為 no-op（覆寫同一份設定值）。
#
# 執行：python scripts/dorpg-upload/setup_cors.py
import json
import os

import truststore

truststore.inject_into_ssl()  # 同 upload_dorpg.py：本機 TLS 攔截層，certifi 公開鏈驗不過

import boto3
from botocore.exceptions import ClientError

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

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

DESIRED = {
    "CORSRules": [
        {
            "AllowedOrigins": ["https://www.dor.tw", "https://dor.tw", "http://localhost:3123"],
            "AllowedMethods": ["GET", "HEAD"],
            "AllowedHeaders": ["*"],
            "MaxAgeSeconds": 86400,
        }
    ]
}


def get_cors():
    try:
        return s3.get_bucket_cors(Bucket=BUCKET)["CORSRules"]
    except ClientError as e:
        if e.response["Error"]["Code"] == "NoSuchCORSConfiguration":
            return None
        raise


print("BEFORE:", json.dumps(get_cors(), ensure_ascii=False))
s3.put_bucket_cors(Bucket=BUCKET, CORSConfiguration=DESIRED)
print("AFTER: ", json.dumps(get_cors(), ensure_ascii=False))
