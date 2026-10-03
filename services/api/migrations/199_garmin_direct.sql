-- Migration 199: Garmin 直連（Activity API 推送模式）第一階段所需的資料表／欄位／預設設定。
-- 依賴：014_integrations.sql（user_integrations）、165（via）、196／197（issuer、last_synced_at）、042（app_settings）。
-- 全部冪等（IF NOT EXISTS／ON CONFLICT DO NOTHING），可重複套用；與 198（COROS GA）無順序相依，
-- 共用欄位（reauth_required_at）在此重述。DB 必須先於程式碼套用（程式會讀寫下列欄位／表）。
--
-- 套用前（唯讀）先確認 provider='garmin' 的 provider_user_id 沒有重複，否則唯一索引會建立失敗：
--   SELECT provider_user_id, count(*) FROM user_integrations
--    WHERE provider='garmin' AND provider_user_id <> '' GROUP BY 1 HAVING count(*) > 1;

-- 1) user_integrations 補欄位（與 Terra／COROS／Strava 列共用同一張表；舊列這些欄位為 NULL，不影響既有流程）
ALTER TABLE user_integrations
    ADD COLUMN IF NOT EXISTS issuer             TEXT,          -- Garmin：App 世代標記（換 client id 時判定需重新授權）
    ADD COLUMN IF NOT EXISTS last_synced_at     TIMESTAMPTZ,   -- 最近一次收到該使用者活動推送的時間
    ADD COLUMN IF NOT EXISTS refresh_expires_at TIMESTAMPTZ,   -- Garmin refresh token 到期時間（約 90 天且每次換發都輪替）
    ADD COLUMN IF NOT EXISTS reauth_required_at TIMESTAMPTZ,   -- 授權失效、需使用者重新授權的時間點（成功換 token 時清空）
    ADD COLUMN IF NOT EXISTS consent_at         TIMESTAMPTZ,   -- 使用者在連接畫面勾選同意的時間
    ADD COLUMN IF NOT EXISTS consent_version    VARCHAR(24),   -- 同意文案／隱私政策版本
    ADD COLUMN IF NOT EXISTS connected_at       TIMESTAMPTZ;   -- 最近一次（重新）授權完成時間；NULL＝以 created_at 為準。created_at 仍是匯入 floor

-- 2) 一個 Garmin 帳號只能綁一個 DOR 帳號（Garmin 專用部分唯一索引；刻意比全供應商索引窄）。
--    ⚠️ 此索引只用於偵測衝突（SaveGarmin 撞 23505→already_linked），沒有任何 ON CONFLICT 以它為推斷目標。
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_integrations_garmin_uid
    ON user_integrations (provider_user_id)
    WHERE provider = 'garmin' AND provider_user_id <> '';

-- 3) 外部供應商事件落地表（目前只有 Garmin 使用；欄位設計為多供應商通用）。
--    先寫入再回 200：Garmin 在我方回 200 後不再重送，所以事件必須先落地，之後才在背景處理，
--    部署重啟或處理失敗都能由這張表重放。payload 只存白名單欄位（無座標、活動名稱、熱量）。
CREATE TABLE IF NOT EXISTS integration_events (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider         VARCHAR(20)  NOT NULL,                       -- 'garmin'
    event_type       VARCHAR(32)  NOT NULL,                       -- activity / deregistration / permission
    provider_user_id VARCHAR(64)  NOT NULL DEFAULT '',
    dedupe_key       VARCHAR(160),                                -- activity: act:<summaryId>；permission: perm:<userId>:<changeTime>；deregistration: NULL
    payload          JSONB        NOT NULL,
    status           VARCHAR(12)  NOT NULL DEFAULT 'pending',     -- pending / done / error / dead
    attempts         SMALLINT     NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ  NOT NULL DEFAULT now() + interval '2 minutes',  -- 租約：就地處理期間不被掃描器搶走
    result           VARCHAR(40),
    last_error       VARCHAR(300),
    received_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    processed_at     TIMESTAMPTZ,
    CONSTRAINT chk_integration_events_status CHECK (status IN ('pending','done','error','dead'))
);

-- ⚠️ 去重索引必須是「非部分」唯一索引：INSERT ... ON CONFLICT (provider, dedupe_key) 只能推斷非部分索引
--    （同 migration 016 對 activities 的教訓）。唯一索引中 NULL 彼此相異，所以 dedupe_key 為 NULL 的
--    deregistration 事件不會互相衝突。
CREATE UNIQUE INDEX IF NOT EXISTS uq_integration_events_dedupe
    ON integration_events (provider, dedupe_key);
CREATE INDEX IF NOT EXISTS idx_integration_events_due
    ON integration_events (provider, next_attempt_at) WHERE status IN ('pending','error');
CREATE INDEX IF NOT EXISTS idx_integration_events_user
    ON integration_events (provider, provider_user_id, received_at DESC);
CREATE INDEX IF NOT EXISTS idx_integration_events_recv
    ON integration_events (received_at);

-- 4) 封鎖清單（License §5.3：須能立即限制違規使用者）。user_id 刻意不加 FK：使用者被刪後封鎖仍有效；
--    被封鎖者無法連接、其 Garmin userId 的推送一律丟棄。目前由 SQL／後續管理介面維護。
CREATE TABLE IF NOT EXISTS garmin_blocklist (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID,
    garmin_user_id VARCHAR(64),
    reason         VARCHAR(120) NOT NULL DEFAULT '',
    created_by     UUID,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT chk_garmin_blocklist_target CHECK (user_id IS NOT NULL OR garmin_user_id IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_garmin_blocklist_user ON garmin_blocklist (user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_garmin_blocklist_gid  ON garmin_blocklist (garmin_user_id) WHERE garmin_user_id IS NOT NULL;

-- 5) 入口預設設定（缺鍵時程式也有同樣預設；這裡只為後台可見）。garmin_whitelist 為文字（email 或帳號編碼），預設空白。
INSERT INTO app_settings (key, value) VALUES
    ('garmin_entry_state', 'whitelist'),
    ('garmin_whitelist',   '')  -- 預設空白：超管恆可用；測試帳號請在後台「Garmin 直連」白名單加入（repo 公開，不在檔案寫 email）
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('199') ON CONFLICT DO NOTHING;
