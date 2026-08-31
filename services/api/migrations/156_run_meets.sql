-- Migration 156: 團練邀請（run meets）
-- 依賴：155_gps_calib_notify.sql
--
-- ⚠️ 命名：資料表/API/套件一律 run_meet_*（英文語意＝「跑步聚會」），禁用 team/group/group_key。
--    「跑團分組」(races.allow_team_groups / race_groups.group_key / users.can_create_team_group，
--    見 migration 011/012) 是既有的「賽事內分組」功能，與本功能完全無關；
--    .claude/workflows/security-audit.js 亦已把 group_key 列為不得外洩欄位，撞名會造成誤判。
--    ⚠️ 中文顯示文案一律用「團練」（不得出現「跑團」二字，避免與賽事的「跑團分組」混淆）。
--
-- ⚠️ 時區：本功能的每月配額用 Asia/Taipei 計（台北 1 日 00:00 重置），
--    與既有 activity_coupon_month（to_char(NOW(),'YYYY-MM') 走 DB session TZ=UTC，
--    實際重置點是台北 08:00）刻意不一致。coupon 那條是既有缺口，本次不順手改
--    （會變更持券者行為），若日後統一另開單。本檔與 explore 的台北日界一致。
--
-- ⚠️ 密碼：join_password_hash 存 bcrypt。與既有 race_groups.group_key（明碼 VARCHAR）
--    刻意不同——那是主辦方發放的分享碼，這是使用者自設、可能與外站重用的密碼。
--
-- ⚠️ 地點三層揭露（使用者決定，優先於原規格的「只做 lat/lng 只給團員」）：
--    公開層 region / place_label       → 所有人（列表與詳情皆可見）
--    成員層 lat / lng / meeting_detail  → 發起人、已加入成員(status='joined')、後台
--    團練地點常在住家附近，精確位置對未加入者是隱私風險。API 層用不同 DTO 真的不吐這三欄
--    （不是靠前端隱藏，也不是回零值），見 internal/runmeet/model.go 的 CardView / DetailView。
--
-- ⚠️ 本 migration 只寫檔，未套用到任何資料庫。
--    部署順序：先套 migration 再推程式。runmeet_entry_state 查無值時 resolveEntry 預設回
--    hidden（fail-closed），順序錯只會功能靜默消失，不會壞資料。

-- ─────────────────────────────────────────────────────────────
-- 1) run_meets 主表
-- ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS run_meets (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- 需求 1(a) 基本資料
    title              VARCHAR(40)  NOT NULL,
    meet_at            TIMESTAMPTZ  NOT NULL,               -- 預計時間（必填）

    -- ── 地點・公開層（所有過入口閘門的登入者可見）──
    region             VARCHAR(30)  NOT NULL,               -- 縣市・行政區，例「臺北市・大安區」；供列表篩選
    place_label        VARCHAR(60)  NOT NULL,               -- 地標名，例「大安森林公園」

    -- ── 地點・成員層（僅 owner / status='joined' / 後台）──
    lat                DOUBLE PRECISION,                    -- 精確座標（選填）
    lng                DOUBLE PRECISION,
    meeting_detail     VARCHAR(200) NOT NULL DEFAULT '',    -- 集合細節，例「2 號出口涼亭旁，我穿黃色風衣」

    capacity           SMALLINT     NOT NULL,               -- 人數上限（含發起人）
    description        TEXT         NOT NULL DEFAULT '',    -- 純文字，非 HTML，不走 htmlsafe

    -- 需求 1(b) 圖片
    image_urls         TEXT[]       NOT NULL DEFAULT '{}',
    -- image_limit 是「建立當下的 VIP 權益快照」(1 或 4)。刻意與配額（不快照、每次即時算）不同：
    -- VIP 用 4 張建團後到期，若編輯時用即時判定會 400 擋死「只想改人數上限」的正常操作。
    image_limit        SMALLINT     NOT NULL DEFAULT 1,

    -- 需求 1(d) / 2(b)
    approval_required  BOOLEAN      NOT NULL DEFAULT FALSE, -- FALSE=自由加入 / TRUE=需要審核
    join_password_hash TEXT,                                -- NULL=公開團；非 NULL=私密團（bcrypt）

    -- 名額計數器（權威值）。member_count 只計 status='joined'（含發起人）；pending 不占名額。
    member_count       SMALLINT     NOT NULL DEFAULT 1,
    pending_count      SMALLINT     NOT NULL DEFAULT 0,

    -- 生命週期（四個獨立概念，刻意不合併成一個欄位）
    status             VARCHAR(10)  NOT NULL DEFAULT 'open',
    closed_at          TIMESTAMPTZ,
    closed_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    hidden_by_admin    BOOLEAN      NOT NULL DEFAULT FALSE, -- 後台下架，與發起人刪除分離
    hidden_reason      VARCHAR(100) NOT NULL DEFAULT '',
    deleted_at         TIMESTAMPTZ,                         -- 發起人軟刪除

    -- 反正規化計數（列表排序用）
    comment_count      INT NOT NULL DEFAULT 0,
    reaction_count     INT NOT NULL DEFAULT 0,

    -- 配額對帳與冪等
    quota_month        VARCHAR(7)   NOT NULL,   -- 建立當下扣的是哪個台北月，供後台對帳
    client_token       VARCHAR(64),             -- 前端 crypto.randomUUID()，防連點/網路重試重複建立

    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT run_meets_status_chk    CHECK (status IN ('open','closed','cancelled')),
    CONSTRAINT run_meets_capacity_chk  CHECK (capacity BETWEEN 2 AND 500),
    CONSTRAINT run_meets_counts_chk    CHECK (member_count >= 0 AND pending_count >= 0),
    CONSTRAINT run_meets_images_chk    CHECK (cardinality(image_urls) <= 4),
    CONSTRAINT run_meets_latlng_chk    CHECK ((lat IS NULL) = (lng IS NULL)),
    CONSTRAINT run_meets_lat_range_chk CHECK (lat IS NULL OR (lat BETWEEN -90 AND 90)),
    CONSTRAINT run_meets_lng_range_chk CHECK (lng IS NULL OR (lng BETWEEN -180 AND 180))
);
-- 刻意不加 CHECK (member_count <= capacity)：發起人調降上限時會炸掉整筆 UPDATE，
-- 且舊資料一旦違反就無法再寫入。改由應用層在 FOR UPDATE 交易內擋（見規格 1.5）。

CREATE INDEX IF NOT EXISTS idx_run_meets_browse ON run_meets (meet_at)
    WHERE deleted_at IS NULL AND hidden_by_admin = FALSE AND status = 'open';
CREATE INDEX IF NOT EXISTS idx_run_meets_owner  ON run_meets (owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_run_meets_quota  ON run_meets (owner_id, quota_month);
CREATE INDEX IF NOT EXISTS idx_run_meets_region ON run_meets (region) WHERE deleted_at IS NULL;
-- 「搜尋附近的團練」：先用 lat/lng bounding box 粗篩（此索引），再用 haversine 精算距離排序。
-- 刻意不引入 PostGIS（未安裝，且這個資料量用不上）。回應只給分級距離（distance_band），
-- 不回精確公尺數——回精確距離可讓攻擊者換多組座標查詢、三角定位反推出精確地點，
-- 等於繞過上面整套地點分層設計。使用者位置只當查詢參數，不寫入 DB、不進 log。
CREATE INDEX IF NOT EXISTS idx_run_meets_geo ON run_meets (lat, lng)
    WHERE deleted_at IS NULL AND hidden_by_admin = FALSE AND lat IS NOT NULL;
-- 部分唯一索引：NULL 不進索引，未帶 token 的舊/外部呼叫不受影響
CREATE UNIQUE INDEX IF NOT EXISTS uq_run_meets_client_token
    ON run_meets (owner_id, client_token) WHERE client_token IS NOT NULL;

-- ─────────────────────────────────────────────────────────────
-- 2) run_meet_members 成員與審核（複合 PK 天然擋重複申請，形狀比照 follows/018）
-- ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS run_meet_members (
    meet_id     UUID NOT NULL REFERENCES run_meets(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    role        VARCHAR(8)   NOT NULL DEFAULT 'member',
    status      VARCHAR(10)  NOT NULL,
    apply_note  VARCHAR(60)  NOT NULL DEFAULT '',   -- 申請附言（選填）
    applied_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    joined_at   TIMESTAMPTZ,
    decided_at  TIMESTAMPTZ,                        -- 同意/婉拒/剔除/退出時間（婉拒冷卻讀這欄）
    decided_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (meet_id, user_id),
    CONSTRAINT rmm_role_chk   CHECK (role IN ('owner','member')),
    CONSTRAINT rmm_status_chk CHECK (status IN ('pending','joined','rejected','kicked','left'))
);
CREATE INDEX IF NOT EXISTS idx_rmm_meet ON run_meet_members (meet_id, status);
CREATE INDEX IF NOT EXISTS idx_rmm_user ON run_meet_members (user_id, status);

-- ─────────────────────────────────────────────────────────────
-- 3) run_meet_comments 留言（純文字，軟刪保留供爭議追溯）
-- ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS run_meet_comments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meet_id    UUID NOT NULL REFERENCES run_meets(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    body       VARCHAR(200) NOT NULL,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rmc_meet ON run_meet_comments (meet_id, created_at)
    WHERE deleted_at IS NULL;
-- ⚠️ 以 user_id 起頭的索引是必要的，不是最佳化：CreateComment 的兩道節流
--    （3 秒間隔 MAX(created_at) 與每日上限 COUNT(*)）都是 WHERE user_id=$1，
--    掛在「每則留言 POST」的同步寫入熱路徑上跑兩次。沒有這個索引就是兩次 Seq Scan，
--    成本隨全站留言總量線性成長（本專案對 Neon compute 成本有明確立場，見規格 Part 10 第 12 條）。
--    不加部分索引條件（deleted_at IS NULL）：節流本來就要把已軟刪的留言算進去。
CREATE INDEX IF NOT EXISTS idx_rmc_user ON run_meet_comments (user_id, created_at DESC);

-- ─────────────────────────────────────────────────────────────
-- 4) run_meet_reactions 按讚／心情（PK 保證一人一團一種 → 天然防洗榜）
-- ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS run_meet_reactions (
    meet_id    UUID NOT NULL REFERENCES run_meets(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    kind       VARCHAR(10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (meet_id, user_id),
    CONSTRAINT rmr_kind_chk CHECK (kind IN ('like','fire','muscle','pray','heart'))
);
CREATE INDEX IF NOT EXISTS idx_rmr_meet ON run_meet_reactions (meet_id);

-- ─────────────────────────────────────────────────────────────
-- 5) run_meet_access 私密團解鎖票證（一次解鎖、跨裝置有效；重設密碼時整團清空）
--    ⚠️ 通過密碼 ≠ 成為成員：解鎖只是「進入詳情頁的入場券」，精確地點（lat/lng/meeting_detail）
--       仍需 status='joined' 才給（見 internal/runmeet/model.go canSeePreciseLocation）。
-- ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS run_meet_access (
    meet_id    UUID NOT NULL REFERENCES run_meets(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (meet_id, user_id)
);

-- ─────────────────────────────────────────────────────────────
-- 6) run_meet_reports 檢舉（全站第一個 UGC，上線即需下架管道）
--    骨架比照 095_registration_cancel.sql 的 status/reviewed_by/reviewed_at/review_note
-- ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS run_meet_reports (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    meet_id      UUID NOT NULL REFERENCES run_meets(id) ON DELETE CASCADE,
    comment_id   UUID REFERENCES run_meet_comments(id) ON DELETE CASCADE, -- NULL=檢舉整個團練
    reporter_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason       VARCHAR(300) NOT NULL DEFAULT '',
    status       VARCHAR(10)  NOT NULL DEFAULT 'pending',
    reviewed_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at  TIMESTAMPTZ,
    review_note  VARCHAR(200) NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT rmrep_status_chk CHECK (status IN ('pending','handled','dismissed'))
);
-- 每人每個對象只能檢舉一次（comment_id NULL 與非 NULL 分開算）
CREATE UNIQUE INDEX IF NOT EXISTS uq_rmrep_meet
    ON run_meet_reports (meet_id, reporter_id) WHERE comment_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_rmrep_comment
    ON run_meet_reports (comment_id, reporter_id) WHERE comment_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rmrep_pending ON run_meet_reports (created_at DESC)
    WHERE status = 'pending';

-- ─────────────────────────────────────────────────────────────
-- 7) users 每月配額計數器
--    語意是「用量計數」不是「餘額補滿」：上限在每次建立時依 VIP 即時算，
--    月中升級 VIP 立刻從 1 變 10（補滿制會因「本月已補過」而不生效 → 必客訴）。
--    ⚠️ 產品規則：「開啟後關閉，一樣消耗一次」。close/cancel/delete 一律不得回補 run_meet_used，
--       唯一返還管道是後台人工 POST /admin/run-meets/quota/{userId}/adjust（走 Audit 留痕）。
-- ─────────────────────────────────────────────────────────────
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS run_meet_month VARCHAR(7),
    ADD COLUMN IF NOT EXISTS run_meet_used  SMALLINT NOT NULL DEFAULT 0;

-- ─────────────────────────────────────────────────────────────
-- 8) images 加 owner/purpose：授權刪除、配額歸屬、孤兒 GC 的前提
--    既有列全為 NULL（nullable ADD COLUMN，無鎖表風險、不影響既有流程）
--    ⚠️ 這是專案第一個會 DELETE images 列的機制（cmd/compressimages/main.go 檔頭明寫
--       「永不刪除資料」）。GC 只刪 purpose='runmeet' 且建立超過 24 小時、無任何 run_meets
--       引用者，走後台按鈕 POST /admin/run-meets/images/gc，不開排程。
-- ─────────────────────────────────────────────────────────────
ALTER TABLE images
    ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS purpose       VARCHAR(20);
CREATE INDEX IF NOT EXISTS idx_images_owner ON images (owner_user_id, created_at DESC)
    WHERE owner_user_id IS NOT NULL;

-- ─────────────────────────────────────────────────────────────
-- 9) 系統設定種子
-- ─────────────────────────────────────────────────────────────
INSERT INTO app_settings (key, value) VALUES
  ('runmeet_entry_state',            'whitelist'),
  ('runmeet_entry_whitelist',        'sogobaga@gmail.com'),
  ('runmeet_create_requires_vip',    '0'),
  ('runmeet_quota_normal',           '1'),
  ('runmeet_quota_vip',              '10'),
  ('runmeet_images_normal',          '1'),
  ('runmeet_images_vip',             '4'),
  ('runmeet_capacity_max',           '50'),
  ('runmeet_pending_max',            '50'),
  ('runmeet_comment_daily_cap',      '100'),
  ('runmeet_reject_cooldown_hours',  '24'),
  ('runmeet_ended_visible_days',     '90')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('156') ON CONFLICT DO NOTHING;
