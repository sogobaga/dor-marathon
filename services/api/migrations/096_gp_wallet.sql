-- Migration 096: GP（Game Point）貨幣地基 —「環台大富翁」Phase 0
-- 依賴：095_registration_cancel.sql
--
-- 產品設計：
--   - GP 是獨立於 DP 的第二種遊戲貨幣，餘額存在 users.gp（比照既有 users.dp 的模式）。
--   - gp_events 是純流水表，只供客服對帳／除錯回溯用，不參與任何業務判斷（餘額一律以
--     users.gp 為準，不得從 gp_events SUM 回推）——比照 DP 目前完全沒有獨立流水表、
--     直接散落內聯 CAS SQL 的做法，這裡刻意多留一份流水，但不影響餘額真實來源單一化。
--   - 加/扣值一律走 internal/wallet 套件的 AwardGP/SpendGP（原子 CAS，餘額不足時 SpendGP
--     回 ok=false 且不寫入、不變負），確保 gp_events 每一筆都對應一次成功的餘額異動。

ALTER TABLE users ADD COLUMN IF NOT EXISTS gp INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS gp_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delta      INT NOT NULL,                    -- 正=加值、負=扣值
    reason     VARCHAR(40) NOT NULL,            -- 事由代碼（例如 admin_adjust）
    ref_type   VARCHAR(24) NOT NULL DEFAULT '', -- 關聯物件類型（例如 admin），空字串=無
    ref_id     UUID,                            -- 關聯物件 id（例如操作的管理員 user id），NULL=無
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_gp_events_user_created ON gp_events(user_id, created_at);

INSERT INTO schema_migrations (version) VALUES ('096') ON CONFLICT DO NOTHING;
