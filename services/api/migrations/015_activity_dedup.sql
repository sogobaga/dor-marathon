-- Migration 015: 活動去重 / 防弊（避免重複計入賽事造成不公平）
-- 依賴：014_integrations.sql
-- 兩種偵測：
--   1) 同帳號「時間區間重疊」→ 多裝置(Apple/Garmin/COROS)同一筆活動 → 只計一次
--   2) 跨帳號「近乎完全相同」(fingerprint=起始秒+距離公尺+移動秒) → 複製洗資料 → 標記排除
-- flagged=TRUE 者保留供稽核，但 race_id 置 NULL 不計入賽事；dup_of 指向原始活動。

ALTER TABLE activities
    ADD COLUMN IF NOT EXISTS fingerprint VARCHAR(64),  -- 精確指紋（跨帳號複製偵測）
    ADD COLUMN IF NOT EXISTS flagged     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS flag_reason VARCHAR(40),  -- multi_device_duplicate | cross_account_duplicate | duplicate
    ADD COLUMN IF NOT EXISTS dup_of      UUID REFERENCES activities(id) ON DELETE SET NULL;

-- 精確指紋查詢（跨帳號）
CREATE INDEX IF NOT EXISTS idx_activities_fingerprint ON activities(fingerprint) WHERE fingerprint IS NOT NULL;
-- 同帳號時間重疊查詢（時間範圍掃描）
CREATE INDEX IF NOT EXISTS idx_activities_user_recorded ON activities(user_id, recorded_at);

INSERT INTO schema_migrations (version) VALUES ('015') ON CONFLICT DO NOTHING;
