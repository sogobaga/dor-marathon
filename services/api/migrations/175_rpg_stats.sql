-- 175_rpg_stats.sql
-- 遊戲化系統第一輪：玩家角色數值（參考 RO 仙境傳說素質系統）——STR/AGI/VIT/DEX/INT/LUK 六圍 +
-- HP/MP + Job Lv。此輪僅資料層 + 後續 internal/rpg 套件讀寫；不含裝備（equip atk/matk 現階段恆
-- 為 0，武器類型只影響「素質攻擊力」算式走近戰或遠程分支，見 internal/rpg/config.go）。
--
-- 入口刻意做到「連 super_admin 都預設看不到」（比一般 *_entry_state 多一道限制）：owner 要求
-- 「只有被設定為 VVIP 的用戶與白名單管理者體驗得到，避免揭露給現有會員」——開發期系統設定
-- rpg_entry_state 預設 hidden，此時任何人都看不到（含超管），見 internal/rpg 的 ResolveEntry。

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS is_vvip BOOLEAN NOT NULL DEFAULT FALSE;

-- player_characters：每位使用者最多一筆（PK=user_id）。六圍存「目前值」（非「加點數」），
-- free_points 為尚未分配的可配點數；job_level/job_exp 現階段只能由後台調整（reserved，之後
-- 若接上職業任務鏈才會有自然成長路徑）。
CREATE TABLE IF NOT EXISTS player_characters (
    user_id      UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    str_pt       INT NOT NULL DEFAULT 1,
    agi_pt       INT NOT NULL DEFAULT 1,
    vit_pt       INT NOT NULL DEFAULT 1,
    dex_pt       INT NOT NULL DEFAULT 1,
    int_pt       INT NOT NULL DEFAULT 1,
    luk_pt       INT NOT NULL DEFAULT 1,
    free_points  INT NOT NULL DEFAULT 40,
    job_level    INT NOT NULL DEFAULT 1,
    job_exp      INT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- player_stat_log：加點/後台重置的異動軌跡（稽核用，比照其餘帳本類表格慣例）。stat 存四字元
-- 代碼（str/agi/vit/dex/int/luk），cost 為這筆異動實際扣掉的 free_points（重置退回時為負值）。
CREATE TABLE IF NOT EXISTS player_stat_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stat        VARCHAR(4) NOT NULL,
    from_value  INT NOT NULL,
    to_value    INT NOT NULL,
    cost        INT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_player_stat_log_user ON player_stat_log(user_id, created_at DESC);

INSERT INTO schema_migrations (version) VALUES ('175') ON CONFLICT DO NOTHING;
