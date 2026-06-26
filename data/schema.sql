-- ════════════════════════════════════════════════════════════════
-- DOR 雲端馬拉松 — Database Schema  v1.0
-- Engine  : MySQL 8.0+ / MariaDB 10.6+
-- Charset : utf8mb4_unicode_ci
-- ────────────────────────────────────────────────────────────────
-- 載入順序：
--   1. 執行此檔建立所有 table
--   2. 依 seeds/ 資料夾的數字前綴順序匯入 JSON seed 檔
--
-- SQLite 注意事項：
--   - 將 ENUM(...) 改為 TEXT + CHECK 約束
--   - 移除 ON UPDATE CURRENT_TIMESTAMP（SQLite 不支援）
--   - TINYINT(1) 改為 INTEGER
-- ════════════════════════════════════════════════════════════════

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ────────────────────────────────────────
-- 1. 使用者與認證 (Authentication & Users)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS users (
  user_id       VARCHAR(50)   NOT NULL                   COMMENT '主鍵；生產環境使用 UUID v4',
  email         VARCHAR(255)  NOT NULL,
  password_hash VARCHAR(255)  NOT NULL                   COMMENT 'bcrypt hash，不儲存明碼',
  handle        VARCHAR(50)   NOT NULL                   COMMENT '公開 @username',
  status        ENUM('active','suspended','deleted')
                NOT NULL DEFAULT 'active',
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP
                ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  UNIQUE KEY uq_users_email  (email),
  UNIQUE KEY uq_users_handle (handle)
) COMMENT '使用者帳號與認證資料';

-- 個人資料獨立成表：降低資料洩漏時的影響範圍
CREATE TABLE IF NOT EXISTS user_profiles (
  user_id       VARCHAR(50)   NOT NULL,
  display_name  VARCHAR(100)  NOT NULL                   COMMENT '暱稱（公開）',
  real_name     VARCHAR(100)                             COMMENT '本名（私密；僅供物流核對）',
  gender        ENUM('m','f','x','n')                    COMMENT 'm=男 f=女 x=其他 n=不公開',
  birthday      DATE,
  phone         VARCHAR(20),
  address       VARCHAR(255),
  id_type       ENUM('tw','foreign')                     COMMENT 'tw=身分證 foreign=護照',
  id_number     VARCHAR(100)                             COMMENT '加密儲存；AES-256-GCM',
  avatar_url    VARCHAR(500),
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP
                ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
) COMMENT '使用者個人資料（與帳號分離）';

-- 等級與累積統計：允許與 activity_logs 加總有些許差異（快取值）
CREATE TABLE IF NOT EXISTS user_levels (
  user_id           VARCHAR(50)   NOT NULL,
  current_level     TINYINT       NOT NULL DEFAULT 1,
  total_exp         INT           NOT NULL DEFAULT 0,
  total_races       SMALLINT      NOT NULL DEFAULT 0,
  total_km          DECIMAL(10,2) NOT NULL DEFAULT 0.00  COMMENT '歷史累積完賽里程（快取）',
  total_rescues     INT           NOT NULL DEFAULT 0,
  current_streak    SMALLINT      NOT NULL DEFAULT 0     COMMENT '目前連續出勤天數',
  longest_streak    SMALLINT      NOT NULL DEFAULT 0,
  spins_available   SMALLINT      NOT NULL DEFAULT 0     COMMENT '尚未使用的轉盤次數',
  updated_at        DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP
                    ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
) COMMENT '使用者等級、EXP 與累積統計快取';

-- 登入 Session：登出時設 revoked_at，保留審計軌跡
CREATE TABLE IF NOT EXISTS auth_sessions (
  session_id    VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  token_hash    VARCHAR(255)  NOT NULL,
  device_info   VARCHAR(255),
  ip_address    VARCHAR(45),
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at    DATETIME      NOT NULL,
  revoked_at    DATETIME                                 COMMENT 'NULL = 尚有效',
  PRIMARY KEY (session_id),
  KEY idx_sessions_user  (user_id),
  KEY idx_sessions_token (token_hash),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
) COMMENT '登入 Session 紀錄';

-- ────────────────────────────────────────
-- 2. 賽事 (Races)
-- ────────────────────────────────────────

-- race_id 使用人類可讀字串（如 hunt2026），方便 URL 與 log 辨識
CREATE TABLE IF NOT EXISTS races (
  race_id       VARCHAR(50)   NOT NULL                   COMMENT '人類可讀 ID，如 hunt2026',
  title         VARCHAR(200)  NOT NULL,
  sub_title     VARCHAR(200)                             COMMENT '英文標語，如 HUNTERS NIGHT',
  world_theme   VARCHAR(100)  NOT NULL                   COMMENT '世界觀，如 獵人 vs 逃亡者',
  group_type    ENUM('distance','faction','club')
                NOT NULL DEFAULT 'distance'              COMMENT '決定下方哪個子表有效',
  group_mode    ENUM('self','random')
                NOT NULL DEFAULT 'self',
  status        ENUM('draft','open','soon','live','done','cancelled')
                NOT NULL DEFAULT 'draft',
  days          TINYINT       NOT NULL,
  start_date    DATE          NOT NULL,
  end_date      DATE          NOT NULL,
  blurb         TEXT,
  hero_url      VARCHAR(500),
  max_capacity  INT                                      COMMENT 'NULL = 不限名額',
  created_by    VARCHAR(50)                              COMMENT 'FK → users.user_id（管理員）',
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP
                ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (race_id),
  KEY idx_races_status (status),
  FOREIGN KEY (created_by) REFERENCES users(user_id) ON DELETE SET NULL
) COMMENT '賽事主表';

-- 每個賽事的距離組別與費用（無論 group_type 為何，距離選項皆在此）
CREATE TABLE IF NOT EXISTS race_distance_groups (
  group_id      VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  distance_km   DECIMAL(6,2)  NOT NULL                  COMMENT '如 10.00, 21.10, 42.20',
  entry_fee     DECIMAL(10,2) NOT NULL,
  quota         INT                                     COMMENT 'NULL = 不限',
  sort_order    TINYINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (group_id),
  UNIQUE KEY uq_rdg_race_dist (race_id, distance_km),
  KEY idx_rdg_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '各賽事可報名的距離與費用';

-- 陣營定義：僅 group_type = 'faction' 的賽事使用
CREATE TABLE IF NOT EXISTS race_factions (
  faction_id    VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  faction_code  VARCHAR(50)   NOT NULL                  COMMENT '程式碼，如 fugitive / hunter',
  name          VARCHAR(100)  NOT NULL,
  color         VARCHAR(20)   NOT NULL                  COMMENT 'UI 主題色，如 fug / hunt / violet',
  target_ratio  DECIMAL(5,2)                            COMMENT '目標佔比 %，如 55.00',
  sort_order    TINYINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (faction_id),
  UNIQUE KEY uq_rf_race_code (race_id, faction_code),
  KEY idx_rf_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '陣營定義；1 對多對應 races';

-- 陣營玩法參數：與 race_factions 分開，避免重複（每賽事一筆）
CREATE TABLE IF NOT EXISTS race_faction_settings (
  race_id              VARCHAR(50)  NOT NULL,
  fugitive_target_pct  DECIMAL(5,2) NOT NULL DEFAULT 55.00 COMMENT '逃亡者目標佔比',
  rescue_multiplier    DECIMAL(4,2) NOT NULL DEFAULT 2.00  COMMENT '救援里程倍率',
  report_time          TIME         NOT NULL DEFAULT '20:00:00',
  rescue_km_per_person DECIMAL(4,2) NOT NULL DEFAULT 1.00  COMMENT '多跑 N km 救出 1 人',
  PRIMARY KEY (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '陣營競賽玩法參數（1 對 1 對應 races）';

-- 跑團定義：僅 group_type = 'club' 的賽事使用
CREATE TABLE IF NOT EXISTS race_clubs (
  club_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  name          VARCHAR(100)  NOT NULL,
  sort_order    TINYINT       NOT NULL DEFAULT 0,
  PRIMARY KEY (club_id),
  KEY idx_rc_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '公會/跑團定義；1 對多對應 races';

-- 里程銀行規則：一個賽事可設定多條規則
CREATE TABLE IF NOT EXISTS race_mileage_rules (
  rule_id             VARCHAR(50)  NOT NULL,
  race_id             VARCHAR(50)  NOT NULL,
  rule_type           ENUM('wheel','cert','share','accum')
                      NOT NULL                           COMMENT 'wheel=轉盤門檻 cert=完賽證 share=分享獎',
  wheel_km_per_spin   DECIMAL(6,2)                       COMMENT '每 N km 獲得 1 次轉盤（wheel 類型）',
  mileage_source      ENUM('goal','actual','both')
                      NOT NULL DEFAULT 'actual',
  description         VARCHAR(255),
  sort_order          TINYINT      NOT NULL DEFAULT 0,
  PRIMARY KEY (rule_id),
  KEY idx_rmr_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '里程銀行計算規則';

-- ────────────────────────────────────────
-- 3. 跑團 / 公會 (Guilds)
-- ────────────────────────────────────────

-- Guild 是跨賽事的社群實體，不屬於某個賽事
CREATE TABLE IF NOT EXISTS guilds (
  guild_id      VARCHAR(50)   NOT NULL,
  name          VARCHAR(100)  NOT NULL,
  city          VARCHAR(50),
  captain_id    VARCHAR(50)                             COMMENT 'FK → users.user_id',
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (guild_id),
  FOREIGN KEY (captain_id) REFERENCES users(user_id) ON DELETE SET NULL
) COMMENT '跑團/公會（跨賽事社群實體）';

CREATE TABLE IF NOT EXISTS guild_members (
  guild_id      VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  role          ENUM('captain','member') NOT NULL DEFAULT 'member',
  joined_at     DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (guild_id, user_id),
  KEY idx_gm_user (user_id),
  FOREIGN KEY (guild_id) REFERENCES guilds(guild_id)  ON DELETE CASCADE,
  FOREIGN KEY (user_id)  REFERENCES users(user_id)    ON DELETE CASCADE
) COMMENT '跑團成員關聯表（多對多）';

-- ────────────────────────────────────────
-- 4. 報名與訂單 (Registrations & Orders)
-- ────────────────────────────────────────

-- ship_* 欄位使用 snapshot 模式：報名時快照，日後會員改地址不影響舊訂單
CREATE TABLE IF NOT EXISTS registrations (
  reg_id        VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  group_id      VARCHAR(50)                             COMMENT 'FK → race_distance_groups.group_id',
  status        ENUM('pending','confirmed','cancelled','refunded')
                NOT NULL DEFAULT 'pending',
  ship_name     VARCHAR(100),
  ship_email    VARCHAR(255),
  ship_phone    VARCHAR(20),
  ship_address  VARCHAR(255),
  ship_gender   ENUM('m','f','x','n'),
  registered_at DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (reg_id),
  UNIQUE KEY uq_reg_user_race (user_id, race_id)      COMMENT '每人每賽事只能報名一次',
  KEY idx_reg_race   (race_id),
  KEY idx_reg_status (status),
  FOREIGN KEY (user_id)  REFERENCES users(user_id)                 ON DELETE RESTRICT,
  FOREIGN KEY (race_id)  REFERENCES races(race_id)                 ON DELETE RESTRICT,
  FOREIGN KEY (group_id) REFERENCES race_distance_groups(group_id) ON DELETE SET NULL
) COMMENT '報名紀錄；含寄件資料快照';

-- 付款紀錄：與 registrations 一對一；分開以支援未來付款失敗重試
CREATE TABLE IF NOT EXISTS orders (
  order_id       VARCHAR(50)  NOT NULL,
  reg_id         VARCHAR(50)  NOT NULL,
  user_id        VARCHAR(50)  NOT NULL,
  amount         DECIMAL(10,2) NOT NULL,
  currency       CHAR(3)      NOT NULL DEFAULT 'TWD',
  payment_method ENUM('credit_card','line_pay','atm','invoice') NOT NULL,
  payment_status ENUM('pending','paid','failed','refunded')     NOT NULL DEFAULT 'pending',
  invoice_no     VARCHAR(50)                                    COMMENT '電子發票號碼',
  paid_at        DATETIME,
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (order_id),
  UNIQUE KEY uq_orders_reg (reg_id),
  KEY idx_orders_user   (user_id),
  KEY idx_orders_status (payment_status),
  FOREIGN KEY (reg_id)  REFERENCES registrations(reg_id) ON DELETE RESTRICT,
  FOREIGN KEY (user_id) REFERENCES users(user_id)        ON DELETE RESTRICT
) COMMENT '付款訂單（1 對 1 對應 registrations）';

-- ────────────────────────────────────────
-- 5. 分組指派 (Group Assignments)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS user_group_assignments (
  assignment_id VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  faction_id    VARCHAR(50)                             COMMENT 'faction 賽事填入',
  club_id       VARCHAR(50)                             COMMENT 'club 賽事填入',
  guild_id      VARCHAR(50)                             COMMENT '所屬跑團（可選）',
  assigned_at   DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  assigned_by   ENUM('self','system_random','admin')
                NOT NULL DEFAULT 'self',
  PRIMARY KEY (assignment_id),
  UNIQUE KEY uq_uga_user_race (user_id, race_id),
  KEY idx_uga_race    (race_id),
  KEY idx_uga_faction (faction_id),
  FOREIGN KEY (user_id)    REFERENCES users(user_id)                 ON DELETE CASCADE,
  FOREIGN KEY (race_id)    REFERENCES races(race_id)                 ON DELETE CASCADE,
  FOREIGN KEY (faction_id) REFERENCES race_factions(faction_id)      ON DELETE SET NULL,
  FOREIGN KEY (club_id)    REFERENCES race_clubs(club_id)            ON DELETE SET NULL,
  FOREIGN KEY (guild_id)   REFERENCES guilds(guild_id)               ON DELETE SET NULL
) COMMENT '使用者在各賽事的陣營/公會指派結果';

-- ────────────────────────────────────────
-- 6. 每日任務 (Missions)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS mission_templates (
  template_id       VARCHAR(50)  NOT NULL,
  race_id           VARCHAR(50)  NOT NULL,
  day_no            TINYINT      NOT NULL                COMMENT '第幾天，從 1 開始',
  title             VARCHAR(200) NOT NULL,
  tag               VARCHAR(50)  NOT NULL                COMMENT '如 IDENTITY / STEALTH / CHASE',
  mission_type      ENUM('base','pace','rescue') NOT NULL,
  icon              VARCHAR(50),
  base_km           DECIMAL(5,2) NOT NULL,
  pace_lo           VARCHAR(10)                          COMMENT '最慢配速 mm:ss/km',
  pace_hi           VARCHAR(10)                          COMMENT '最快配速 mm:ss/km',
  rescue_multiplier DECIMAL(4,2) NOT NULL DEFAULT 1.00  COMMENT '救援類：多 N km 救 1 人',
  push_notification ENUM('auto','manual','off') NOT NULL DEFAULT 'auto',
  description       TEXT,
  sort_order        TINYINT      NOT NULL DEFAULT 0,
  PRIMARY KEY (template_id),
  UNIQUE KEY uq_mt_race_day (race_id, day_no),
  KEY idx_mt_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '每日任務模板定義（每個賽事每天一筆）';

CREATE TABLE IF NOT EXISTS user_mission_progress (
  progress_id   VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  template_id   VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL                  COMMENT '冗餘欄位；避免 JOIN 提升查詢效率',
  status        ENUM('pending','in_progress','done','failed')
                NOT NULL DEFAULT 'pending',
  km_done       DECIMAL(6,2)  NOT NULL DEFAULT 0.00,
  rescued_count SMALLINT      NOT NULL DEFAULT 0,
  completed_at  DATETIME,
  PRIMARY KEY (progress_id),
  UNIQUE KEY uq_ump_user_template (user_id, template_id),
  KEY idx_ump_user (user_id),
  KEY idx_ump_race (race_id),
  FOREIGN KEY (user_id)     REFERENCES users(user_id)                ON DELETE CASCADE,
  FOREIGN KEY (template_id) REFERENCES mission_templates(template_id) ON DELETE CASCADE
) COMMENT '使用者每日任務完成進度';

-- ────────────────────────────────────────
-- 7. 活動記錄與里程累積 (Activities & Mileage)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS activity_logs (
  activity_id   VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)                             COMMENT 'NULL = 非賽事期間活動',
  source        ENUM('manual','apple_health','google_fit','samsung_health',
                     'garmin','coros','polar','suunto','wahoo',
                     'fitbit','strava','nike') NOT NULL,
  activity_type ENUM('run','walk','hike','bike','other') NOT NULL DEFAULT 'run',
  distance_km   DECIMAL(6,2)  NOT NULL,
  duration_sec  INT                                     COMMENT '總時間（秒）',
  avg_pace_sec  INT                                     COMMENT '平均配速（秒/km）',
  started_at    DATETIME      NOT NULL,
  ended_at      DATETIME,
  is_verified   TINYINT(1)    NOT NULL DEFAULT 0        COMMENT '是否通過資料源驗證',
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (activity_id),
  KEY idx_al_user    (user_id),
  KEY idx_al_race    (race_id),
  KEY idx_al_started (started_at),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE SET NULL
) COMMENT '個人每次跑步活動紀錄';

-- 每人每賽事里程小計（避免每次都 SUM activity_logs）
CREATE TABLE IF NOT EXISTS race_mileage_accumulation (
  accum_id      VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  total_km      DECIMAL(8,2)  NOT NULL DEFAULT 0.00,
  goal_km       DECIMAL(6,2)  NOT NULL                  COMMENT '報名時選擇的目標距離',
  spins_earned  SMALLINT      NOT NULL DEFAULT 0,
  spins_used    SMALLINT      NOT NULL DEFAULT 0,
  is_finished   TINYINT(1)    NOT NULL DEFAULT 0,
  finished_at   DATETIME,
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP
                ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (accum_id),
  UNIQUE KEY uq_rma_user_race (user_id, race_id),
  KEY idx_rma_race (race_id),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '每人每賽事里程累積與轉盤狀態（快取表）';

-- 每日陣營戰況快照：供晚間戰報與後台圖表使用
CREATE TABLE IF NOT EXISTS faction_daily_standings (
  standing_id    VARCHAR(50)  NOT NULL,
  race_id        VARCHAR(50)  NOT NULL,
  faction_id     VARCHAR(50)  NOT NULL,
  standing_date  DATE         NOT NULL,
  percentage     DECIMAL(5,2) NOT NULL DEFAULT 0.00     COMMENT '陣營佔比 %',
  escaped_count  INT          NOT NULL DEFAULT 0,
  captured_count INT          NOT NULL DEFAULT 0,
  member_count   INT          NOT NULL DEFAULT 0,
  active_count   INT          NOT NULL DEFAULT 0        COMMENT '當日出勤人數',
  total_km       DECIMAL(10,2) NOT NULL DEFAULT 0.00,
  PRIMARY KEY (standing_id),
  UNIQUE KEY uq_fds_race_faction_date (race_id, faction_id, standing_date),
  KEY idx_fds_race (race_id),
  FOREIGN KEY (race_id)    REFERENCES races(race_id)            ON DELETE CASCADE,
  FOREIGN KEY (faction_id) REFERENCES race_factions(faction_id) ON DELETE CASCADE
) COMMENT '每日陣營戰況快照';

-- 資料同步來源授權（健康 App / GPS 手錶）
CREATE TABLE IF NOT EXISTS data_sync_connections (
  conn_id         VARCHAR(50)  NOT NULL,
  user_id         VARCHAR(50)  NOT NULL,
  provider        VARCHAR(50)  NOT NULL                  COMMENT '如 apple_health / garmin / strava',
  is_connected    TINYINT(1)   NOT NULL DEFAULT 0,
  connected_at    DATETIME,
  disconnected_at DATETIME,
  PRIMARY KEY (conn_id),
  UNIQUE KEY uq_dsc_user_provider (user_id, provider),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
) COMMENT '使用者健康平台連接狀態';

-- ────────────────────────────────────────
-- 8. 門市打卡 (Stores & Check-ins)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS stores (
  store_id      VARCHAR(20)   NOT NULL,
  name          VARCHAR(200)  NOT NULL,
  address       VARCHAR(255)  NOT NULL,
  city          VARCHAR(50)   NOT NULL,
  latitude      DECIMAL(10,7),
  longitude     DECIMAL(10,7),
  hours         VARCHAR(100),
  is_active     TINYINT(1)    NOT NULL DEFAULT 1,
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (store_id)
) COMMENT '實體門市/補給站';

-- 多對多：一個賽事可有多個門市，一個門市可參與多個賽事
CREATE TABLE IF NOT EXISTS race_stores (
  race_id        VARCHAR(50)  NOT NULL,
  store_id       VARCHAR(20)  NOT NULL,
  task_desc      TEXT                                    COMMENT '打卡任務說明',
  rescue_reward  INT          NOT NULL DEFAULT 0         COMMENT '打卡獲得的救援值',
  is_stamp_spot  TINYINT(1)   NOT NULL DEFAULT 0         COMMENT '可獲得集點貼紙',
  PRIMARY KEY (race_id, store_id),
  FOREIGN KEY (race_id)  REFERENCES races(race_id)   ON DELETE CASCADE,
  FOREIGN KEY (store_id) REFERENCES stores(store_id)  ON DELETE CASCADE
) COMMENT '賽事與門市的參與關聯（多對多）';

CREATE TABLE IF NOT EXISTS checkins (
  checkin_id     VARCHAR(50)  NOT NULL,
  user_id        VARCHAR(50)  NOT NULL,
  race_id        VARCHAR(50)  NOT NULL,
  store_id       VARCHAR(20)  NOT NULL,
  checkin_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  latitude       DECIMAL(10,7),
  longitude      DECIMAL(10,7),
  reward_granted TINYINT(1)   NOT NULL DEFAULT 0,
  PRIMARY KEY (checkin_id),
  UNIQUE KEY uq_ci_user_race_store (user_id, race_id, store_id)
             COMMENT '每賽事每門市只能打卡一次',
  KEY idx_ci_user (user_id),
  KEY idx_ci_race (race_id),
  FOREIGN KEY (user_id)  REFERENCES users(user_id)    ON DELETE CASCADE,
  FOREIGN KEY (race_id)  REFERENCES races(race_id)    ON DELETE CASCADE,
  FOREIGN KEY (store_id) REFERENCES stores(store_id)  ON DELETE RESTRICT
) COMMENT '使用者門市打卡紀錄';

-- ────────────────────────────────────────
-- 9. 轉盤獎勵 (Wheel)
-- ────────────────────────────────────────

-- weight 為相對權重，實際機率 = weight / SUM(weight)
CREATE TABLE IF NOT EXISTS wheel_pools (
  pool_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  prize_code    VARCHAR(50)   NOT NULL                  COMMENT '識別碼，如 lp50 / card / again',
  prize_kind    ENUM('line','sticker','again','miss')   NOT NULL,
  prize_label   VARCHAR(100)  NOT NULL,
  prize_amount  SMALLINT      NOT NULL DEFAULT 0        COMMENT '點數/張數等數量',
  weight        SMALLINT      NOT NULL DEFAULT 1,
  color         VARCHAR(20)                             COMMENT 'UI 主題色',
  is_active     TINYINT(1)    NOT NULL DEFAULT 1,
  PRIMARY KEY (pool_id),
  UNIQUE KEY uq_wp_race_code (race_id, prize_code),
  KEY idx_wp_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '轉盤獎項設定（每賽事獨立配置）';

CREATE TABLE IF NOT EXISTS wheel_spins (
  spin_id       VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  pool_id       VARCHAR(50)   NOT NULL,
  spun_at       DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (spin_id),
  KEY idx_ws_user (user_id),
  KEY idx_ws_race (race_id),
  FOREIGN KEY (user_id) REFERENCES users(user_id)     ON DELETE CASCADE,
  FOREIGN KEY (race_id) REFERENCES races(race_id)     ON DELETE CASCADE,
  FOREIGN KEY (pool_id) REFERENCES wheel_pools(pool_id) ON DELETE RESTRICT
) COMMENT '使用者轉盤記錄；獎項詳情透過 pool_id JOIN 取得';

-- ────────────────────────────────────────
-- 10. 集點卡與公仔 (Stickers & Prize Redemption)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS sticker_templates (
  sticker_id    VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  slot_no       TINYINT       NOT NULL                  COMMENT '九宮格位置 1-9',
  name          VARCHAR(100)  NOT NULL,
  how_to_earn   VARCHAR(255),
  PRIMARY KEY (sticker_id),
  UNIQUE KEY uq_st_race_slot (race_id, slot_no),
  KEY idx_st_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '九宮格集點卡每格定義';

CREATE TABLE IF NOT EXISTS user_stickers (
  user_sticker_id VARCHAR(50)  NOT NULL,
  user_id         VARCHAR(50)  NOT NULL,
  sticker_id      VARCHAR(50)  NOT NULL,
  race_id         VARCHAR(50)  NOT NULL                 COMMENT '冗餘欄位；方便過濾',
  earned_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_sticker_id),
  UNIQUE KEY uq_us_user_sticker (user_id, sticker_id),
  KEY idx_us_user (user_id),
  FOREIGN KEY (user_id)    REFERENCES users(user_id)                ON DELETE CASCADE,
  FOREIGN KEY (sticker_id) REFERENCES sticker_templates(sticker_id) ON DELETE CASCADE
) COMMENT '使用者已收集的集點貼紙';

CREATE TABLE IF NOT EXISTS prize_redemptions (
  redemption_id VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  prize_type    ENUM('figurine','coupon','medal','other') NOT NULL,
  prize_name    VARCHAR(200)  NOT NULL,
  status        ENUM('pending','approved','shipped','delivered')
                NOT NULL DEFAULT 'pending',
  ship_name     VARCHAR(100),
  ship_address  VARCHAR(255),
  ship_phone    VARCHAR(20),
  tracking_no   VARCHAR(100),
  redeemed_at   DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  shipped_at    DATETIME,
  delivered_at  DATETIME,
  PRIMARY KEY (redemption_id),
  KEY idx_pr_user   (user_id),
  KEY idx_pr_race   (race_id),
  KEY idx_pr_status (status),
  FOREIGN KEY (user_id) REFERENCES users(user_id)  ON DELETE RESTRICT,
  FOREIGN KEY (race_id) REFERENCES races(race_id)  ON DELETE RESTRICT
) COMMENT '完賽公仔與實體獎品兌換出貨紀錄';

-- ────────────────────────────────────────
-- 11. 完賽紀錄 (Race Results)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS race_results (
  result_id     VARCHAR(50)   NOT NULL,
  user_id       VARCHAR(50)   NOT NULL,
  race_id       VARCHAR(50)   NOT NULL,
  reg_id        VARCHAR(50)   NOT NULL,
  faction_id    VARCHAR(50)                             COMMENT '完賽時所在陣營（可 NULL）',
  distance_km   DECIMAL(6,2)  NOT NULL,
  finish_time   TIME                                   COMMENT '累積運動時間 HH:MM:SS',
  rank_title    VARCHAR(100)                           COMMENT '稱號，如 逆轉者 / 捕獲王',
  medal_color   VARCHAR(20)                            COMMENT '十六進位色碼，如 #FFC24B',
  total_rescues SMALLINT      NOT NULL DEFAULT 0,
  cert_url      VARCHAR(500),
  finished_at   DATETIME,
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (result_id),
  UNIQUE KEY uq_rr_user_race (user_id, race_id),
  KEY idx_rr_race (race_id),
  FOREIGN KEY (user_id)    REFERENCES users(user_id)              ON DELETE RESTRICT,
  FOREIGN KEY (race_id)    REFERENCES races(race_id)              ON DELETE RESTRICT,
  FOREIGN KEY (reg_id)     REFERENCES registrations(reg_id)       ON DELETE RESTRICT,
  FOREIGN KEY (faction_id) REFERENCES race_factions(faction_id)   ON DELETE SET NULL
) COMMENT '使用者完賽紀錄；含稱號與獎牌';

-- ────────────────────────────────────────
-- 12. 推播通知 (Notifications)
-- ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS notification_templates (
  template_id     VARCHAR(50)  NOT NULL,
  race_id         VARCHAR(50)                           COMMENT 'NULL = 全系統通知',
  notif_type      ENUM('battle_report','mission_reminder','result','system','broadcast')
                  NOT NULL,
  title_template  VARCHAR(200) NOT NULL,
  body_template   TEXT         NOT NULL                 COMMENT '支援 {day},{captured},{escaped} 變數',
  scheduled_time  TIME                                  COMMENT '排程自動推播時間',
  is_active       TINYINT(1)   NOT NULL DEFAULT 1,
  created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (template_id),
  KEY idx_nt_race (race_id),
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE CASCADE
) COMMENT '推播通知模板與排程設定';

CREATE TABLE IF NOT EXISTS notification_logs (
  log_id          VARCHAR(50)  NOT NULL,
  template_id     VARCHAR(50),
  user_id         VARCHAR(50)                           COMMENT 'NULL = 廣播',
  race_id         VARCHAR(50),
  title           VARCHAR(200) NOT NULL,
  body            TEXT         NOT NULL,
  target_group    ENUM('all','fugitive','hunter','inactive'),
  channel         ENUM('push','line','email') NOT NULL DEFAULT 'push',
  status          ENUM('pending','sent','failed') NOT NULL DEFAULT 'pending',
  recipient_count INT,
  open_rate       DECIMAL(5,2)                          COMMENT '開啟率 %',
  sent_at         DATETIME,
  created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (log_id),
  KEY idx_nl_race (race_id),
  FOREIGN KEY (template_id) REFERENCES notification_templates(template_id) ON DELETE SET NULL,
  FOREIGN KEY (user_id)     REFERENCES users(user_id)                      ON DELETE CASCADE,
  FOREIGN KEY (race_id)     REFERENCES races(race_id)                      ON DELETE CASCADE
) COMMENT '已推播通知紀錄（含開啟率統計）';

-- ────────────────────────────────────────
-- 13. 獎勵交易帳本 (Reward Transactions)
-- ────────────────────────────────────────

-- 所有積分變動都記錄在此；amount 正=獲得 負=消耗
CREATE TABLE IF NOT EXISTS reward_transactions (
  tx_id       VARCHAR(50)   NOT NULL,
  user_id     VARCHAR(50)   NOT NULL,
  race_id     VARCHAR(50),
  tx_type     ENUM('line_points','spin_earned','spin_used','rescue',
                   'checkin','achievement','refund','admin_adjust') NOT NULL,
  amount      INT           NOT NULL,
  description VARCHAR(255),
  ref_id      VARCHAR(50)                               COMMENT '關聯記錄 ID（spin_id / checkin_id 等）',
  created_at  DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (tx_id),
  KEY idx_rt_user (user_id),
  KEY idx_rt_race (race_id),
  FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  FOREIGN KEY (race_id) REFERENCES races(race_id) ON DELETE SET NULL
) COMMENT '獎勵進出帳總帳（LINE Points、轉盤次數等）；供對帳與稽核';

SET FOREIGN_KEY_CHECKS = 1;

-- ════════════════════════════════════════
-- Table Count: 33 tables
-- ════════════════════════════════════════
