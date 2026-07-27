-- Migration 099: 環台大富翁 Phase 1 —— 盤面遊戲（棋子位置 + 擲骰紀錄）
-- 依賴：096_gp_wallet.sql（users.gp / internal/wallet.SpendGP/AwardGP）
--
-- 盤面固定 46 個位置（position 0=START，1..45 為格子，繞一圈=46 格），由前端 BOARD_COORDS
-- 常數決定座標，這裡只存「目前在哪一格」與「累積繞了幾圈」。機會格(6,15,25,35)／命運格(10,18,30,39)
-- 的判定是純函式（見 internal/monopoly landedOnFor），不落地成表。

CREATE TABLE IF NOT EXISTS monopoly_player_state (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    position       INT NOT NULL DEFAULT 0 CHECK (position BETWEEN 0 AND 45),
    laps_completed INT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 擲骰紀錄（純流水，供除錯/對帳；不參與任何業務判斷，權威狀態一律以 monopoly_player_state 為準）
CREATE TABLE IF NOT EXISTS monopoly_rolls (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    roll_value    INT NOT NULL CHECK (roll_value BETWEEN 1 AND 6),
    from_position INT,
    to_position   INT,
    gp_cost       INT,
    laps_gained   INT NOT NULL DEFAULT 0,
    landed_on     VARCHAR(10) NOT NULL DEFAULT 'normal', -- normal | chance | destiny
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_monopoly_rolls_user_created ON monopoly_rolls(user_id, created_at);

INSERT INTO schema_migrations (version) VALUES ('099') ON CONFLICT DO NOTHING;
