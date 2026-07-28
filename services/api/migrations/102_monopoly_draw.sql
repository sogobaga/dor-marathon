-- Migration 102: 環台大富翁「機會/命運」抽卡地基（Phase 2 A1）
-- 依賴：096_gp_wallet.sql（users.gp / internal/wallet.AwardGP/SpendGP）、099_monopoly_board.sql（機會/命運格判定）
--
-- 產品設計：
--   - 兩個獨立獎勵池：chance（機會→跑者訓練知識卡）、destiny（命運→跑者照護知識卡）。全正獎勵無懲罰。
--   - 知識卡（knowledge_card_defs）依主題分 training/care，來源為 400 篇跑者知識庫 xlsx（見 103）。
--   - 重複抽到已持有的卡片 → 依稀有度轉 GP（monopoly_dup_gp 設定），不堆疊卡片本體（但 obtained_count 累加供統計）。
--   - sticker_pieces/user_stickers：完賽公仔九宮格收集，機制先建、頁面之後做（Phase 之後）。
--   - monopoly_pool_entries：加權獎勵池（後台可調權重/新增獎項），抽獎邏輯依 pool 撈 is_active 項目做加權隨機。
--   - monopoly_redemption_codes：LINE POINT／優惠券兌換碼庫存池，按 batch_key 領用；耗盡時 fallback 發 GP
--     （monopoly_redeem_fallback_gp 設定）。
--   - monopoly_draws：抽卡流水，供對帳/客服回溯與前端「最近抽到」展示。

CREATE TABLE IF NOT EXISTS knowledge_card_defs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code              VARCHAR(16) UNIQUE NOT NULL,       -- xlsx 的 ID，如 CH-001／FT-001
    theme             VARCHAR(12) NOT NULL,               -- 'training' | 'care'
    trigger_type      VARCHAR(8) NOT NULL DEFAULT '',     -- 機會 | 命運
    goal              VARCHAR(120) NOT NULL DEFAULT '',   -- 主題目標
    main_category     VARCHAR(60) NOT NULL DEFAULT '',    -- 主分類
    subtopic          VARCHAR(120) NOT NULL DEFAULT '',   -- 子主題
    title             VARCHAR(120) NOT NULL,              -- 卡片標題
    body              TEXT NOT NULL,                       -- 卡片正文
    player_action     TEXT NOT NULL DEFAULT '',           -- 玩家行動
    timing            VARCHAR(60) NOT NULL DEFAULT '',    -- 適用時機
    importance        VARCHAR(8) NOT NULL DEFAULT '',     -- 重要程度：高／中／低
    handling_level    VARCHAR(12) NOT NULL DEFAULT '',    -- 處置層級：一般／緊急／留意／停止運動
    game_effect_hint  VARCHAR(120) NOT NULL DEFAULT '',   -- 建議遊戲效果（純展示 flavor，不驅動任何邏輯）
    risk_note         TEXT NOT NULL DEFAULT '',           -- 風險提醒
    source_org        VARCHAR(120) NOT NULL DEFAULT '',   -- 來源機構
    source_doc        VARCHAR(200) NOT NULL DEFAULT '',   -- 來源文件
    source_url        VARCHAR(400) NOT NULL DEFAULT '',   -- 來源網址
    rarity            VARCHAR(12) NOT NULL DEFAULT 'common', -- 'common' | 'rare'
    image_url         VARCHAR(400) NOT NULL DEFAULT '',
    sort_order        INT NOT NULL DEFAULT 0,
    is_active         BOOL NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_knowledge_card_defs_theme_sort ON knowledge_card_defs(theme, sort_order);
CREATE INDEX IF NOT EXISTS idx_knowledge_card_defs_rarity ON knowledge_card_defs(rarity);

-- 使用者持有的知識卡（複數張同一卡只累加 obtained_count，不重複插列）
CREATE TABLE IF NOT EXISTS user_knowledge_cards (
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    card_id           UUID NOT NULL REFERENCES knowledge_card_defs(id) ON DELETE CASCADE,
    obtained_count    INT NOT NULL DEFAULT 1,
    first_obtained_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_obtained_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, card_id)
);
CREATE INDEX IF NOT EXISTS idx_user_knowledge_cards_user ON user_knowledge_cards(user_id);

-- 完賽公仔九宮格拼圖（機制先建，頁面之後做）
CREATE TABLE IF NOT EXISTS sticker_pieces (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    set_key    VARCHAR(24) NOT NULL DEFAULT 'finisher',
    position   SMALLINT NOT NULL,          -- 1..9
    title      VARCHAR(80) NOT NULL DEFAULT '',
    image_url  VARCHAR(400) NOT NULL DEFAULT '',
    rarity     VARCHAR(12) NOT NULL DEFAULT 'common',
    is_active  BOOL NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (set_key, position)
);

CREATE TABLE IF NOT EXISTS user_stickers (
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    piece_id          UUID NOT NULL REFERENCES sticker_pieces(id) ON DELETE CASCADE,
    obtained_count    INT NOT NULL DEFAULT 1,
    first_obtained_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_obtained_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, piece_id)
);

-- 加權獎勵池（後台可調權重/新增獎項；is_active=false 的項目不參與抽獎但保留歷史）
CREATE TABLE IF NOT EXISTS monopoly_pool_entries (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pool                  VARCHAR(12) NOT NULL,           -- 'chance' | 'destiny'
    reward_type           VARCHAR(20) NOT NULL,           -- 'gp' | 'dp' | 'vip_days' | 'knowledge_card' | 'sticker' | 'redemption_code'
    weight                INT NOT NULL DEFAULT 1 CHECK (weight >= 0),
    amount                INT NOT NULL DEFAULT 0,         -- gp/dp/vip_days 的數量；knowledge_card/sticker/redemption_code 不用
    redemption_batch_key  VARCHAR(40) NOT NULL DEFAULT '', -- reward_type=redemption_code 時對應的兌換碼批次
    note                  VARCHAR(120) NOT NULL DEFAULT '',
    is_active             BOOL NOT NULL DEFAULT true,
    sort_order            INT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_monopoly_pool_entries_pool_active ON monopoly_pool_entries(pool, is_active);

-- 兌換碼庫存池（LINE POINT／優惠券），依 batch_key 領用；同批次內 code 不重複
CREATE TABLE IF NOT EXISTS monopoly_redemption_codes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_key  VARCHAR(40) NOT NULL,
    kind       VARCHAR(20) NOT NULL DEFAULT 'coupon',     -- 'line_point' | 'coupon'
    label      VARCHAR(120) NOT NULL DEFAULT '',
    code       VARCHAR(120) NOT NULL,
    is_used    BOOL NOT NULL DEFAULT false,
    used_by    UUID REFERENCES users(id),
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_monopoly_redemption_codes_batch_used ON monopoly_redemption_codes(batch_key, is_used);
CREATE UNIQUE INDEX IF NOT EXISTS uq_monopoly_redemption_codes_batch_code ON monopoly_redemption_codes(batch_key, code);

-- 抽卡流水（純對帳/展示用，權威狀態一律以 user_knowledge_cards/user_stickers/users.gp/users.dp 為準）
CREATE TABLE IF NOT EXISTS monopoly_draws (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pool             VARCHAR(12) NOT NULL,
    reward_type      VARCHAR(20) NOT NULL,
    reward_ref_type  VARCHAR(20) NOT NULL DEFAULT '',     -- 'knowledge_card' | 'sticker' | 'redemption_code' | ''
    reward_ref_id    UUID,
    amount           INT NOT NULL DEFAULT 0,
    is_duplicate     BOOL NOT NULL DEFAULT false,
    converted_gp     INT NOT NULL DEFAULT 0,
    meta             JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_monopoly_draws_user_created ON monopoly_draws(user_id, created_at);

-- 知識卡入口可見性（比照 100_monopoly_entry_settings 的白名單模式，測試中僅 sogobaga@gmail.com 可見）
INSERT INTO app_settings (key, value) VALUES
  ('knowledge_entry_state', 'whitelist'),
  ('knowledge_entry_whitelist', 'sogobaga@gmail.com'),
  ('monopoly_dup_gp', '{"common":5,"rare":20}'),
  ('monopoly_redeem_fallback_gp', '10')
ON CONFLICT (key) DO NOTHING;

-- 預設池：讓上線後停在機會/命運格立刻有東西可抽（sticker/redemption_code 先不放，之後由後台加）
INSERT INTO monopoly_pool_entries (pool, reward_type, weight, amount, note) VALUES
  ('chance',  'knowledge_card', 70, 0,  '跑者訓練知識卡'),
  ('chance',  'gp',             20, 5,  'GP 小獎'),
  ('chance',  'gp',             7,  10, 'GP 中獎'),
  ('chance',  'vip_days',       3,  1,  'VIP 體驗 1 天'),
  ('destiny', 'knowledge_card', 70, 0,  '跑者照護知識卡'),
  ('destiny', 'gp',             20, 5,  'GP 小獎'),
  ('destiny', 'dp',             7,  3,  'DP 獎勵'),
  ('destiny', 'vip_days',       3,  1,  'VIP 體驗 1 天')
ON CONFLICT DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('102') ON CONFLICT DO NOTHING;
