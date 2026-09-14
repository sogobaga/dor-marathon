-- 176_rpg_battle_content.sql
-- DORPG P2：戰鬥「內容資料層」——場景/怪物/技能/道具/隊友/遭遇編組全部進 DB＋後台可改，
-- 玩家可以連打很多場才摸得出手感（契約 CONTRACT.md 開頭原話）。此檔只建表＋灌草案 seed，
-- 不含任何遊戲判定邏輯（判定在前端 engine，P1 已凍結；伺服器權威留給 P3）。
--
-- ⚠️ 使用者手動套用到 Neon，不得自行套到正式庫；本檔全部 CREATE TABLE IF NOT EXISTS /
-- ADD COLUMN IF NOT EXISTS、seed 全部 ON CONFLICT DO NOTHING，可安全重複執行。
--
-- D1（動態縮放，預設 power 模式）＋D2（用「目標時間」反推數值，不用魔術倍率）是本檔怪物數值
-- 設計的唯一理由：擁有者目前角色 STR3/AGI2/其餘1、34 點未配置，若怪物存絕對 HP/ATK，任何角色
-- 都會遇到「打不動」或「一下就死」——所以 rpg_monsters 只存「相對倍率」（hp_mult/atk_mult/
-- def_mult/speed_mult），實際 HP/ATK/DEF 在戰鬥開始當下依「當前玩家戰力」現算（見
-- internal/rpg/scaling.go 的 ScaleMonster，公式鏡像在 apps/web/src/lib/dorpg/fixture.ts）。
-- rpg_companions 同理，只存「玩家數值的倍率」，不存絕對值（見 D3）。
--
-- 資料來源：seed 的中文名稱/rank/attribute/size/race/場景 slots 逐字取自內容包
-- source/ui/08_DORPG_Content_Pack_v1/assets/{monsters,scenes}/**/*.json（本檔作者已逐檔核對，
-- 非憑記憶）；attribute/size/race 刻意存中文原文（闇/金/土/無/木、大型/中型/小型、植物/魚貝/
-- 不死/人形/昆蟲等），不轉寫成 DDL 註解列出的 metal|wood|... 英文代碼——理由：(1) 契約 §2 對
-- MIGRATION 的指示明講這些欄位要「逐字取自內容包」；(2) 現有前端 apps/web/src/lib/dorpg/
-- sampleBattle.ts 的 Enemy.attribute 已經是這樣存中文字串、BattleScreen 只是把它當純顯示文字
-- 印出，不做屬性克制運算——存中文才不會跟既有畫面邏輯脫節。英文 enum 註解只是「這欄位語意上
-- 對應哪些屬性」的參考，不代表儲存格式；之後如果要做屬性克制計算，需要另建中英對照表，不在
-- P2 範圍內。
--
-- 圖片 URL：讀過 apps/web/src/lib/dorpg/{cdn.ts,assets.ts} 與 scripts/dorpg-upload/manifest.json
-- 確認——R2（img.dor.tw/dorpg/...）目前只上傳了 P1 的「戰鬥用大圖集」（mon/<id>/<action>.webp
-- 四動作 sprite、bgm、fx 特效/音效），manifest.json 裡完全沒有 mon/<id>.webp（poster）或
-- scene/<id>.webp 這兩種 key；P0 的靜態小圖（poster 用第 0 格、scene 用手機版縮圖）本來就是放在
-- apps/web/public/ui/dorpg/{mon,scene}/ 這個本機資料夾（已用 ls 核對 5 張怪物 poster 與 6 張場景
-- 圖都確實存在），並且 assets.ts 的 monsterPoster()/sceneImage() 正是回傳 /ui/dorpg/{mon,scene}/
-- <id>.webp。所以本檔 poster_url/image_url 一律填本機路徑（跟現有 sampleBattle.ts 用法一致），
-- 不是 R2 網址——前端實際載得到的路徑以此為準。

-- ---------------------------------------------------------------------------
-- 建表（依相依順序：scenes/monsters/skills/items/companions 互相獨立→encounters 依賴 scenes→
-- encounter_monsters 依賴 encounters 與 monsters→battle_logs 只依賴 users→最後才動既有表）。
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rpg_scenes (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, image_url TEXT NOT NULL DEFAULT '',
  slots JSONB NOT NULL,                         -- [{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"}, ...] 五個
  location_note TEXT NOT NULL DEFAULT '',
  is_active BOOLEAN NOT NULL DEFAULT TRUE, sort_order INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rpg_monsters (
  id          TEXT PRIMARY KEY,                 -- 內容包 monsterId，例 'DOR-MON-A-67000200001'
  name        TEXT NOT NULL,
  rank        TEXT NOT NULL DEFAULT '',         -- 災害級（顯示用：特S/S/特A/A~F）
  attribute   TEXT NOT NULL DEFAULT 'neutral',  -- metal|wood|water|fire|earth|light|dark|neutral
  size        TEXT NOT NULL DEFAULT 'medium',   -- small|medium|large
  race        TEXT NOT NULL DEFAULT '',
  sprite_id   TEXT NOT NULL DEFAULT '',         -- R2 圖集資料夾（空＝用 id）
  poster_url  TEXT NOT NULL DEFAULT '',
  hp_mult     NUMERIC(7,3) NOT NULL DEFAULT 1,
  atk_mult    NUMERIC(7,3) NOT NULL DEFAULT 1,
  def_mult    NUMERIC(7,3) NOT NULL DEFAULT 1,
  speed_mult  NUMERIC(7,3) NOT NULL DEFAULT 1,  -- 行動間隔倍率（<1 更頻繁）
  threat      INT NOT NULL DEFAULT 0,           -- 初始目標挑選用
  is_boss     BOOLEAN NOT NULL DEFAULT FALSE,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  sort_order  INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rpg_skills (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  icon_id     TEXT NOT NULL,                    -- kit 素材 id，例 'icon_skill_slash'
  kind        TEXT NOT NULL DEFAULT 'damage',   -- damage|heal|shield
  target      TEXT NOT NULL DEFAULT 'enemy',    -- enemy|ally|self|allAllies
  weapon      TEXT NOT NULL DEFAULT 'sword',    -- sword|staff|bow|greatsword（特效/音效組）
  element     TEXT NOT NULL DEFAULT 'neutral',
  mp_cost     INT NOT NULL DEFAULT 0,
  cooldown_ms INT NOT NULL DEFAULT 4000,
  coefficient NUMERIC(7,3) NOT NULL DEFAULT 1,
  -- flat：kind=heal/shield 時的絕對回復/護盾量，語意是「參考玩家」（HPMax=rpg_config.
  -- battle_reference_hp，預設 300）身上的絕對值——後端 ScaleSkill()/前端 fixture.ts 的鏡像
  -- 會依實際玩家 HPMax ÷ battle_reference_hp 的比例（clamp 0.25~8 倍）等比例縮放後才送進戰鬥，
  -- 不是任何角色都通用的絕對值（P2 修正第 1 輪根因：怪物數值有隨玩家戰力縮放，這裡原本沒有，
  -- 高等級玩家會出現治療佔血量比例大幅下降的問題）。kind=damage 時 flat 不縮放，加在玩家 ATK
  -- 上，維持絕對值語意。
  flat        INT NOT NULL DEFAULT 0,
  cast_ms     INT NOT NULL DEFAULT 300,
  is_default  BOOLEAN NOT NULL DEFAULT FALSE,   -- 未設定 loadout 時預設帶入
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  sort_order  INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rpg_items (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, icon_id TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'hp',              -- hp|mp|revive
  -- amount：kind=hp/mp 時的絕對回復量，語意同 rpg_skills.flat——「參考玩家」（HPMax=
  -- battle_reference_hp/MPMax=battle_reference_mp）身上的絕對值，實際使用時依玩家真實
  -- HPMax/MPMax 的比例縮放（P2 修正第 1 輪，見 rpg_skills.flat 註解）。kind=revive 則是
  -- 「復活後 HP 佔 hpMax 的百分比」，本來就是相對值，不套用這個縮放。
  amount INT NOT NULL DEFAULT 0,
  default_quantity INT NOT NULL DEFAULT 0,      -- 每場戰鬥預設帶幾個
  is_active BOOLEAN NOT NULL DEFAULT TRUE, sort_order INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rpg_companions (
  id TEXT PRIMARY KEY,                          -- 內容包 charId，例 'char_xiaomi'
  name TEXT NOT NULL, portrait_id TEXT NOT NULL, role TEXT NOT NULL DEFAULT '',
  weapon TEXT NOT NULL DEFAULT 'sword',
  level_offset INT NOT NULL DEFAULT 0,
  hp_mult NUMERIC(7,3) NOT NULL DEFAULT 1, mp_mult NUMERIC(7,3) NOT NULL DEFAULT 1,
  atk_mult NUMERIC(7,3) NOT NULL DEFAULT 1, matk_mult NUMERIC(7,3) NOT NULL DEFAULT 1,
  def_mult NUMERIC(7,3) NOT NULL DEFAULT 1, mdef_mult NUMERIC(7,3) NOT NULL DEFAULT 1,
  act_interval_mult NUMERIC(7,3) NOT NULL DEFAULT 1,
  skill_ids TEXT[] NOT NULL DEFAULT '{}',       -- AI 可用技能（目前引擎只用 heal）
  is_player_portrait BOOLEAN NOT NULL DEFAULT FALSE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE, sort_order INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- 只允許一位當玩家頭像
CREATE UNIQUE INDEX IF NOT EXISTS idx_rpg_companions_player_portrait ON rpg_companions ((is_player_portrait)) WHERE is_player_portrait;

CREATE TABLE IF NOT EXISTS rpg_encounters (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT NOT NULL UNIQUE,                    -- 對外識別（API 用 code，不用 UUID）
  title TEXT NOT NULL, subtitle TEXT NOT NULL DEFAULT '',
  scene_id TEXT NOT NULL REFERENCES rpg_scenes(id),
  scene_kind TEXT NOT NULL DEFAULT 'normal',    -- normal|boss（BGM 選曲）
  difficulty INT NOT NULL DEFAULT 1,            -- 1~5（顯示星數）
  power_scale NUMERIC(7,3) NOT NULL DEFAULT 1,  -- 整場相對玩家戰力的倍率
  escape_chance NUMERIC(4,3) NOT NULL DEFAULT 0.350,
  can_escape BOOLEAN NOT NULL DEFAULT TRUE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE, sort_order INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rpg_encounter_monsters (
  encounter_id UUID NOT NULL REFERENCES rpg_encounters(id) ON DELETE CASCADE,
  slot TEXT NOT NULL,                           -- rear_left|rear_right|front_left|front_center|front_right
  monster_id TEXT NOT NULL REFERENCES rpg_monsters(id),
  power_scale NUMERIC(7,3) NOT NULL DEFAULT 1,  -- 個別再乘（同場想放一隻硬的就調這裡）
  PRIMARY KEY (encounter_id, slot)
);

CREATE TABLE IF NOT EXISTS rpg_battle_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  encounter_code TEXT NOT NULL,
  outcome TEXT NOT NULL,                        -- victory|defeat|draw|escaped|abandoned
  duration_ms INT NOT NULL DEFAULT 0,
  damage_dealt INT NOT NULL DEFAULT 0, damage_taken INT NOT NULL DEFAULT 0,
  enemies_defeated INT NOT NULL DEFAULT 0,
  attacks INT NOT NULL DEFAULT 0, charged_attacks INT NOT NULL DEFAULT 0,
  skills_used INT NOT NULL DEFAULT 0, items_used INT NOT NULL DEFAULT 0, guard_ms INT NOT NULL DEFAULT 0,
  player_level INT NOT NULL DEFAULT 0, player_power INT NOT NULL DEFAULT 0,
  client_version TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rpg_battle_logs_user ON rpg_battle_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rpg_battle_logs_code ON rpg_battle_logs(encounter_code, created_at DESC);

-- 玩家目前裝備的技能欄（8 格，存 skill id；空陣列＝沿用 is_default 技能）。
ALTER TABLE player_characters ADD COLUMN IF NOT EXISTS loadout TEXT[] NOT NULL DEFAULT '{}';

-- ---------------------------------------------------------------------------
-- Seed：數值先放草案，全部 ON CONFLICT DO NOTHING（可重複執行），由 TUNE 依 BALANCE 報告改。
-- ---------------------------------------------------------------------------

-- rpg_scenes：六張場景目前共用同一組 monsterSlots（逐字取自各 scene.json，六份內容完全相同）；
-- row 由 y<0.7 判定 rear、否則 front（契約 §2 明講的規則）。location_note 取 scene.json 的 region。
INSERT INTO rpg_scenes (id, name, image_url, slots, location_note, sort_order) VALUES
  ('scene_taipei_101', '台北101', '/ui/dorpg/scene/scene_taipei_101.webp',
    '[{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"},{"id":"rear_right","x":0.68,"y":0.54,"scale":0.38,"row":"rear"},{"id":"front_left","x":0.18,"y":0.88,"scale":0.33,"row":"front"},{"id":"front_center","x":0.5,"y":0.89,"scale":0.36,"row":"front"},{"id":"front_right","x":0.82,"y":0.88,"scale":0.33,"row":"front"}]'::jsonb,
    '臺北市信義區', 1),
  ('scene_fuhe_bridge', '福和橋', '/ui/dorpg/scene/scene_fuhe_bridge.webp',
    '[{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"},{"id":"rear_right","x":0.68,"y":0.54,"scale":0.38,"row":"rear"},{"id":"front_left","x":0.18,"y":0.88,"scale":0.33,"row":"front"},{"id":"front_center","x":0.5,"y":0.89,"scale":0.36,"row":"front"},{"id":"front_right","x":0.82,"y":0.88,"scale":0.33,"row":"front"}]'::jsonb,
    '臺北市／新北市永和區', 2),
  ('scene_taipei_stadium', '台北田徑場', '/ui/dorpg/scene/scene_taipei_stadium.webp',
    '[{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"},{"id":"rear_right","x":0.68,"y":0.54,"scale":0.38,"row":"rear"},{"id":"front_left","x":0.18,"y":0.88,"scale":0.33,"row":"front"},{"id":"front_center","x":0.5,"y":0.89,"scale":0.36,"row":"front"},{"id":"front_right","x":0.82,"y":0.88,"scale":0.33,"row":"front"}]'::jsonb,
    '臺北市松山區', 3),
  ('scene_ximending', '西門町', '/ui/dorpg/scene/scene_ximending.webp',
    '[{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"},{"id":"rear_right","x":0.68,"y":0.54,"scale":0.38,"row":"rear"},{"id":"front_left","x":0.18,"y":0.88,"scale":0.33,"row":"front"},{"id":"front_center","x":0.5,"y":0.89,"scale":0.36,"row":"front"},{"id":"front_right","x":0.82,"y":0.88,"scale":0.33,"row":"front"}]'::jsonb,
    '臺北市萬華區', 4),
  ('scene_tamsui_estuary', '淡水河口', '/ui/dorpg/scene/scene_tamsui_estuary.webp',
    '[{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"},{"id":"rear_right","x":0.68,"y":0.54,"scale":0.38,"row":"rear"},{"id":"front_left","x":0.18,"y":0.88,"scale":0.33,"row":"front"},{"id":"front_center","x":0.5,"y":0.89,"scale":0.36,"row":"front"},{"id":"front_right","x":0.82,"y":0.88,"scale":0.33,"row":"front"}]'::jsonb,
    '新北市淡水區', 5),
  ('scene_jiannan_mountain', '劍南山', '/ui/dorpg/scene/scene_jiannan_mountain.webp',
    '[{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"},{"id":"rear_right","x":0.68,"y":0.54,"scale":0.38,"row":"rear"},{"id":"front_left","x":0.18,"y":0.88,"scale":0.33,"row":"front"},{"id":"front_center","x":0.5,"y":0.89,"scale":0.36,"row":"front"},{"id":"front_right","x":0.82,"y":0.88,"scale":0.33,"row":"front"}]'::jsonb,
    '臺北市劍南山一帶', 6)
ON CONFLICT (id) DO NOTHING;

-- rpg_monsters：id/name/rank/attribute/size/race 逐字取自各 monster.json；倍率為契約 §2 給的草案。
-- poster_url 見檔頭說明（本機 /ui/dorpg/mon/，非 R2）。sprite_id 留空＝戰鬥動畫用 id 本身找 R2 資料夾
-- （R2 上 dorpg/mon/<id>/{idle,attack,hit,death}.webp 的資料夾名稱本來就等於這裡的 id）。
INSERT INTO rpg_monsters (id, name, rank, attribute, size, race, poster_url, hp_mult, atk_mult, def_mult, speed_mult, threat, is_boss, sort_order) VALUES
  ('DOR-MON-A-67000200001', '幽暗食人花首領', 'A', '闇', '大型', '植物', '/ui/dorpg/mon/DOR-MON-A-67000200001.webp', 7.0, 1.6, 1.5, 1.1, 100, TRUE, 1),
  ('DOR-MON-B-0089', '鋼鐵巨鉗蟹', 'B', '金', '大型', '魚貝', '/ui/dorpg/mon/DOR-MON-B-0089.webp', 1.8, 1.25, 1.35, 1.15, 40, FALSE, 2),
  ('DOR-MON-C-0229', '沙塵骷髏騎士', 'C', '土', '中型', '不死', '/ui/dorpg/mon/DOR-MON-C-0229.webp', 1.3, 1.15, 1.2, 1.05, 30, FALSE, 3),
  ('DOR-MON-D-0182', '灰白獸人', 'D', '無', '中型', '人形', '/ui/dorpg/mon/DOR-MON-D-0182.webp', 0.9, 1.0, 1.0, 1.0, 20, FALSE, 4),
  ('DOR-MON-E-0052', '荊棘毒蛾', 'E', '木', '小型', '昆蟲', '/ui/dorpg/mon/DOR-MON-E-0052.webp', 0.6, 0.8, 0.8, 0.9, 10, FALSE, 5)
ON CONFLICT (id) DO NOTHING;

-- rpg_skills：欄位值全部沿用 apps/web/src/lib/dorpg/sampleBattle.ts 既有的 5 個技能（is_default=TRUE，
-- 未設定 loadout 時原封不動補進 8 格技能欄，畫面手感與 P0/P1 展示一致）。
INSERT INTO rpg_skills (id, name, icon_id, kind, target, weapon, element, mp_cost, cooldown_ms, coefficient, flat, cast_ms, is_default, sort_order) VALUES
  ('slash', '斬擊', 'icon_skill_slash', 'damage', 'enemy', 'sword', 'neutral', 5, 4000, 1.6, 20, 300, TRUE, 1),
  ('fireball', '火球', 'icon_skill_fireball', 'damage', 'enemy', 'staff', 'fire', 25, 12000, 2.4, 60, 600, TRUE, 2),
  ('heal', '治療', 'icon_skill_heal', 'heal', 'ally', 'staff', 'light', 20, 8000, 2.0, 80, 500, TRUE, 3),
  ('ice_lance', '冰槍', 'icon_skill_ice_lance', 'damage', 'enemy', 'staff', 'water', 15, 6000, 2.0, 30, 400, TRUE, 4),
  ('shield', '護盾', 'icon_skill_shield', 'shield', 'self', 'staff', 'light', 15, 10000, 1.5, 60, 300, TRUE, 5)
ON CONFLICT (id) DO NOTHING;

-- rpg_items：amount 由 TUNE 依 BALANCE.md §5 調整——hp_potion 300→150、mp_potion 120→80。
-- hp_potion 的 150 有被「蓄氣+防禦+補血」策略實際模擬驗證過（難度 1~4 場次 100% 勝率、BOSS 場
-- 死亡率明顯低於「只點普攻」，回復量確實發揮作用）；mp_potion 的 80 未被本輪任一策略消耗 MP
-- 的路徑驗證過（三種契約指定策略都不使用技能格），是依「玩家 MPMax 76~160 之間，一瓶大致補滿
-- 到半滿」的比例估算值，BALANCE.md §8-5 建議之後補一個「用技能」策略重新驗證。default_quantity
-- 維持契約 §2 給定值不變。
INSERT INTO rpg_items (id, name, icon_id, kind, amount, default_quantity, sort_order) VALUES
  ('hp_potion', '紅藥水', 'icon_item_hp_potion', 'hp', 150, 3, 1),
  ('mp_potion', '藍藥水', 'icon_item_mp_potion', 'mp', 80, 2, 2),
  ('revive_feather', '復甦羽毛', 'icon_item_revive_feather', 'revive', 50, 1, 3)
ON CONFLICT (id) DO NOTHING;

-- rpg_companions：char_xiaojing 是玩家頭像（D4：is_player_portrait=TRUE 那一列，本身不列入隊友
-- 清單），其餘四位是隊友，倍率依契約 §2 給定；role 欄位契約未逐字指定文字，這裡依武器類型給
-- 合理的顯示用預設（後台可自由改），不影響任何數值公式。
INSERT INTO rpg_companions (id, name, portrait_id, role, weapon, level_offset, hp_mult, mp_mult, atk_mult, matk_mult, def_mult, mdef_mult, act_interval_mult, skill_ids, is_player_portrait, sort_order) VALUES
  ('char_xiaojing', '小井', 'char_xiaojing', '', 'sword', 0, 1, 1, 1, 1, 1, 1, 1, '{}', TRUE, 0),
  ('char_xiaomi', '小咪', 'char_xiaomi', '治療', 'staff', 0, 0.7, 1, 1, 1.2, 1, 1, 1, '{heal}', FALSE, 1),
  ('char_xiaoyou', '小優', 'char_xiaoyou', '游擊', 'bow', 0, 1, 1, 1, 1, 1, 1, 1, '{}', FALSE, 2),
  ('char_aguang', '阿光', 'char_aguang', '劍士', 'sword', 0, 1, 1, 1, 1, 1, 1, 1, '{}', FALSE, 3),
  ('char_ashen', '阿深', 'char_ashen', '重裝', 'greatsword', 0, 1.3, 1, 1, 1, 1, 1, 1.25, '{}', FALSE, 4)
ON CONFLICT (id) DO NOTHING;

-- rpg_encounters：code 是外部識別（id 每次重跑都會是新的 gen_random_uuid()，所以衝突判斷一定要用
-- code，不能用 id——這點很容易寫錯導致重跑就長出重複遭遇，特別在此註記）。
-- 第 1/2/3/6 場的怪物編組逐字取自契約 §2；第 4（淡水河口）/第 5（劍南山步道）場契約只給了
-- 「難度＋power_scale＋隻數」沒有逐字編組，編組草案見下方 rpg_encounter_monsters 區塊註解。
--
-- power_scale 由 TUNE 依 BALANCE.md §2-3 全面下修（草案的 0.55→1.6 等比級距是用「玩家單打獨鬥」
-- 估算，套用整隊校準後的 battle_mob_hits/battle_enemy_dps_ratio 會讓難度 4~5 場「5 分鐘打不死＋
-- 確定團滅」同時發生——power_scale 同時乘進 mobHp 與 mobAtk，怪物編組本身已經很陡的難度斜率會被
-- 二次放大，見 BALANCE.md §7 迭代記錄）。ximen_night/tamsui_dusk/taipei101_boss 三場 TUNE 又用
-- scratchpad/dorpg_p2/sim_lib.mjs 重跑並微調過一輪（ximen_night 0.72→0.73：BALANCE 原值「只點
-- 普攻」勝率 92% 超出契約 60~85% 上限；tamsui_dusk/taipei101_boss 測過鄰近值後發現勝率會在極窄
-- 範圍內斷崖式翻轉（見引擎敵人命中/爆擊無隨機性的既有限制，BALANCE.md §6-6），沒有比 BALANCE
-- 原值更好的點，維持 0.55/0.46 不變——實測數據見 BALANCE.md 與本檔頭引用的 TUNE 驗證紀錄）。
INSERT INTO rpg_encounters (code, title, subtitle, scene_id, scene_kind, difficulty, power_scale, escape_chance, can_escape, sort_order) VALUES
  ('training_ground', '訓練場', '入門教學．熟悉操作手感', 'scene_taipei_stadium', 'normal', 1, 0.60, 0.350, TRUE, 1),
  ('ximen_night', '西門町夜巡', '夜巡邊界．小怪成群', 'scene_ximending', 'normal', 2, 0.73, 0.350, TRUE, 2),
  ('fuhe_bridge', '福和橋下', '橋下盤據．小心巨鉗', 'scene_fuhe_bridge', 'normal', 2, 0.60, 0.350, TRUE, 3),
  ('tamsui_dusk', '淡水河口', '河口起霧．敵勢漸強', 'scene_tamsui_estuary', 'normal', 3, 0.55, 0.350, TRUE, 4),
  ('jiannan_trail', '劍南山步道', '登山惡鬥．狹路難退', 'scene_jiannan_mountain', 'normal', 4, 0.52, 0.350, TRUE, 5),
  ('taipei101_boss', '台北101首領戰', '首領現身．無法逃跑', 'scene_taipei_101', 'boss', 5, 0.46, 0.000, FALSE, 6)
ON CONFLICT (code) DO NOTHING;

-- rpg_encounter_monsters：用 code 查回 encounter_id 再插入，power_scale 全部先留草案值 1（個別
-- 微調留給 TUNE）。PK 是 (encounter_id, slot)，同一場同一槽位只會有一隻怪。

-- 1. training_ground（難度1）：延續契約 §2 給的 3 隻編組。
INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale)
SELECT e.id, v.slot, v.monster_id, v.power_scale
FROM rpg_encounters e
JOIN (VALUES
  ('front_left', 'DOR-MON-D-0182', 1.000),
  ('front_center', 'DOR-MON-E-0052', 1.000),
  ('front_right', 'DOR-MON-E-0052', 1.000)
) AS v(slot, monster_id, power_scale) ON TRUE
WHERE e.code = 'training_ground'
ON CONFLICT (encounter_id, slot) DO NOTHING;

-- 2. ximen_night（難度2）：契約 §2 給的 4 隻編組。
INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale)
SELECT e.id, v.slot, v.monster_id, v.power_scale
FROM rpg_encounters e
JOIN (VALUES
  ('rear_left', 'DOR-MON-E-0052', 1.000),
  ('front_left', 'DOR-MON-D-0182', 1.000),
  ('front_center', 'DOR-MON-D-0182', 1.000),
  ('front_right', 'DOR-MON-C-0229', 1.000)
) AS v(slot, monster_id, power_scale) ON TRUE
WHERE e.code = 'ximen_night'
ON CONFLICT (encounter_id, slot) DO NOTHING;

-- 3. fuhe_bridge（難度2）：契約 §2 給的 4 隻編組。
INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale)
SELECT e.id, v.slot, v.monster_id, v.power_scale
FROM rpg_encounters e
JOIN (VALUES
  ('rear_right', 'DOR-MON-C-0229', 1.000),
  ('front_left', 'DOR-MON-D-0182', 1.000),
  ('front_center', 'DOR-MON-B-0089', 1.000),
  ('front_right', 'DOR-MON-D-0182', 1.000)
) AS v(slot, monster_id, power_scale) ON TRUE
WHERE e.code = 'fuhe_bridge'
ON CONFLICT (encounter_id, slot) DO NOTHING;

-- 4. tamsui_dusk（難度3）：契約未逐字給編組，草案原本是 2C+2B+1D；TUNE 依 BALANCE.md 的模擬
--    編組改為 2C+2D+1B（front_left 從 B 換成 D）——這是實際被模擬驗證過落在契約目標區間的編組
--    （見 scratchpad/dorpg_p2/sim_lib.mjs ENCOUNTER_DEFS.tamsui_dusk），不是隨意調整；換成別的
--    編組前，記得同步改 apps/web/src/lib/dorpg/fixture.ts 的 tamsui_dusk 並重跑模擬。
INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale)
SELECT e.id, v.slot, v.monster_id, v.power_scale
FROM rpg_encounters e
JOIN (VALUES
  ('rear_left', 'DOR-MON-C-0229', 1.000),
  ('rear_right', 'DOR-MON-C-0229', 1.000),
  ('front_left', 'DOR-MON-D-0182', 1.000),
  ('front_center', 'DOR-MON-B-0089', 1.000),
  ('front_right', 'DOR-MON-D-0182', 1.000)
) AS v(slot, monster_id, power_scale) ON TRUE
WHERE e.code = 'tamsui_dusk'
ON CONFLICT (encounter_id, slot) DO NOTHING;

-- 5. jiannan_trail（難度4）：契約未逐字給編組，草案原本是 3B+2C；TUNE 依 BALANCE.md 的模擬編組
--    改為 2B+2C+1D（front_left 從 B 換成 C、front_right 從 C 換成 D）——同上，這是實際被模擬
--    驗證過的編組（見 sim_lib.mjs ENCOUNTER_DEFS.jiannan_trail），登山小徑遇到成群中高階怪、
--    退路窄的情境，緊接在 boss 戰之前墊一階。
INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale)
SELECT e.id, v.slot, v.monster_id, v.power_scale
FROM rpg_encounters e
JOIN (VALUES
  ('rear_left', 'DOR-MON-B-0089', 1.000),
  ('rear_right', 'DOR-MON-B-0089', 1.000),
  ('front_left', 'DOR-MON-C-0229', 1.000),
  ('front_center', 'DOR-MON-C-0229', 1.000),
  ('front_right', 'DOR-MON-D-0182', 1.000)
) AS v(slot, monster_id, power_scale) ON TRUE
WHERE e.code = 'jiannan_trail'
ON CONFLICT (encounter_id, slot) DO NOTHING;

-- 6. taipei101_boss（難度5，can_escape=FALSE）：契約 §2 明講 rear_right 放 boss（A），其餘
--    B/C/D/E 各一隻，槽位由本檔指定。
INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale)
SELECT e.id, v.slot, v.monster_id, v.power_scale
FROM rpg_encounters e
JOIN (VALUES
  ('rear_left', 'DOR-MON-E-0052', 1.000),
  ('rear_right', 'DOR-MON-A-67000200001', 1.000),
  ('front_left', 'DOR-MON-D-0182', 1.000),
  ('front_center', 'DOR-MON-C-0229', 1.000),
  ('front_right', 'DOR-MON-B-0089', 1.000)
) AS v(slot, monster_id, power_scale) ON TRUE
WHERE e.code = 'taipei101_boss'
ON CONFLICT (encounter_id, slot) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('176') ON CONFLICT DO NOTHING;
