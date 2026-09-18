# DORPG P6 契約：怪物等級制／酒館與隊伍／傭兵腳本／HP 整數 — 2026-09-18

單一真相。P5 契約（`docs/dorpg/P5_CONTRACT.md`／`P5_WIRE.md`）仍有效，本檔只加不改。

## 0. 使用者需求（原話濃縮）
1. 戰鬥中 HP 出現小數，**務必整數**。
2. 五名角色（玩家＋四位隊友）可**個別設定數值**，讓職業特性明確，供測試。
3. 六個關卡的怪物等級分別設為 **Lv.10／20／30／40／50／60**，做出等級差異。
4. 一個頁面設定隊伍成員；預設隊伍只有 **小咪**；其餘四位（小咪也算）是「**雇傭兵**」，放在「**酒館**」頁；未來組隊要花 DP 雇用（本輪不收費，只顯示文案）。
5. 每位傭兵可設定多組「**腳本**」：設定等級 → 取得該等級的配點數與技能點 → 配置六圍與技能 → 儲存並命名 → 之後可切換使用。

## 1. HP／MP 整數（引擎）
- 引擎內 `hp`、`mp`、`hpMax`、`mpMax`、護盾值一律整數：所有加減（傷害、治療、護盾吸收、hp_regen、passive 的 `hp_max_pct`/`mp_max_pct`、`damage_taken_pct`、multi-hit 各段）在**套用前 `Math.floor`**（傷害向下取整、治療向下取整、上限向下取整），並在 createBattle 時把 wire 進來的數值也 floor。
- 後端 Compute 的 HPMax/MPMax 回傳整數（passive pct 乘完 floor）。
- UI 顯示以整數為準（不再依賴 toFixed）。verify 腳本加斷言：任意 buff/debuff/heal/regen 序列後所有 actor 的 hp/mp 皆為整數。

## 2. 怪物等級制（battle_scale_mode = "level"，本輪成為預設）
- `rpg_encounters.monster_level INT NOT NULL DEFAULT 10`；六場依序 10/20/30/40/50/60（training_ground 10、ximen_night 20、fuhe_bridge 30、tamsui_dusk 40、jiannan_trail 50、taipei101_boss 60）。後台遭遇分頁可編輯；EncounterPicker 顯示「怪物 Lv.N」。
- **參考玩家 RefPlayer(N)**（Go `RefPlayerStats(cfg, N)`，純函式）：等級 N、六圍＝把 `TotalStatPoints(N)` 以「輪流 +1 給目前最低的素質、成本負擔得起且未達 cap=min(MaxStat,N)」的確定性演算法配完（六圍起始 InitialStat）；職業 nil、無 passive；用 P5 的 Compute 得到 HPMax／MPMax／ATK／MATK／DEF／MDEF／Hit／Flee／Aspd。
- **怪物數值**：`hp = floor(Ref.HPMax × hp_mult × battle_lvl_hp_ratio × power_scale × slotScale)`、`atk = floor(Ref.ATK × atk_mult × battle_lvl_atk_ratio × power_scale)`、`def = floor(Ref.DEF × def_mult × battle_lvl_def_ratio × power_scale)`、`mdef = floor(Ref.MDEF × mdef_mult…)`（新 config：`battle_lvl_hp_ratio 1.0、battle_lvl_atk_ratio 1.0、battle_lvl_def_ratio 1.0、battle_lvl_mdef_ratio 1.0`，後台可調）；`matk`（怪物施法用）= Ref.MATK × atk_mult；hit/flee 沿用「等級基線」公式但改用 **怪物自己的等級 N**（不再用玩家等級）；行動間隔沿用 speed_mult。`slotScale`（前後排）沿用 power 模式的既有邏輯。怪物 `level` 欄位送前端顯示 Lv.N。
- `battle_scale_mode` 預設改 `"level"`；`"power"` 路徑完整保留可切回；`"fixed"` 仍為預留。
- **TS 鏡像**：不移植 Compute；改為**資料鏡像**——Go 產生 `apps/web/src/lib/dorpg/refPlayerTable.json`（N=1..99 的 Ref 衍生值）＋ Go 測試 `TestRefPlayerTable_MatchesJSON` 保證同步（改公式必須重生 JSON，測試會擋）；fixture.ts 的離線預覽用該表算怪物，與 Go 逐位元一致。
- 難度預期（供驗證，非硬性）：Lv27 玩家（186 點、26 技能點、有小咪預設腳本）打 Lv10 應輕鬆、Lv30 均勢、Lv60 幾乎必敗；用真引擎模擬（`scratchpad/dorpg_p2/sim_lib.mjs` 改造或新寫）跑 6 場各 ≥50 種子給勝率與時長表，**不強求落在特定區間**，但要回報並在 config 預設值（四個 ratio）上給一次合理調整讓曲線單調。

## 3. 傭兵、腳本、隊伍
### 3.1 資料
- `rpg_companions.job_id TEXT NULL REFERENCES rpg_jobs(id)`：小咪 cleric、小優 archer、阿光 light_knight、阿深 heavy_knight；小井（is_player_portrait）NULL、不是傭兵。既有 hp_mult…act_interval_mult 保留，乘在腳本算出的衍生值之上。
- `rpg_companion_presets`（腳本）：`id UUID PK, user_id UUID NULL REFERENCES users(id) ON DELETE CASCADE`（NULL＝系統預設、唯讀）, `companion_id TEXT REFERENCES rpg_companions(id)`, `name TEXT`, `level INT CHECK 1..99`, `stats JSONB`（{str,agi,vit,dex,int,luk}）, `skill_levels JSONB`（{skill_id: level}）, `created_at, updated_at`；索引 (user_id, companion_id)。
- **系統預設腳本**（seed，user_id NULL，名稱「預設」）：四位各一組，等級 25（170 配點、24 技能點，cap 25），配點與技能依職業定位（小咪：INT/DEX 高＋巷口包紮 Lv5＋運動貼紮術＋補給站陣線；小優：AGI/DEX/LUK＋河濱速射／鎖定訊標；阿光：STR/AGI/LUK＋街角連斬／劍羽迴身／疾風步伐；阿深：STR/VIT＋盾甲衝撞／鋼鐵之軀／堅守姿態／鐵壁護盾）——由實作者依規則算出合法配置寫進 seed，並用同一套驗證函式在測試裡驗證合法。
- `player_party`：`user_id UUID REFERENCES users(id) ON DELETE CASCADE, slot INT CHECK 1..4, companion_id TEXT REFERENCES rpg_companions(id), preset_id UUID NULL REFERENCES rpg_companion_presets(id) ON DELETE SET NULL, PRIMARY KEY(user_id, slot), UNIQUE(user_id, companion_id)`。沒有任何列＝預設隊伍＝小咪＋其系統預設腳本。preset_id NULL＝用該傭兵的系統預設腳本。
### 3.2 規則
- 腳本驗證＝與玩家相同：配點總數 `TotalStatPoints(level)`、成本 `floor((n−1)/10)+2`、每項 ≤ min(MaxStat, level)、技能點 `TotalSkillPoints(level)`、前置鏈、max_level、只能配該傭兵職業的技能。存檔時整份驗證，不合法 400 帶欄位錯誤。
- 系統預設腳本唯讀；「另存新腳本」可從任何腳本複製。刪除使用者腳本時若被 player_party 引用 → preset_id 設 NULL（退回系統預設）。
- 隊伍最多 4 位傭兵、同一傭兵不可重複；玩家固定為隊長（不佔 slot）。
- 戰鬥 bootstrap：隊伍成員＝player_party（或預設）；每位＝Compute(cfg, level, stats, job, passives from skill_levels) × 該傭兵 hp_mult 等倍率 → 自己的 HP/MP/ATK/MATK/DEF/MDEF 與**自己的 CombatRating**（取代現行沿用玩家評級）；技能＝skill_levels 中 level≥1 且非 passive、`implemented=true` 的技能（ExpandEffect 展開）。
- 隊友 AI（引擎）：每次行動依序判定：①有 heal 技能可用且有隊友 HP < 50%（含自己）→ 治療最低者（allAllies 版本則全體）；②有 shield／buff 技能可用且目標身上沒有同 stat 效果 → 施放（buff 優先給玩家或自己依 target）；③有 damage 技能可用且 MP 足夠 → 用 tier 最高的；④有 debuff 可用且敵人身上沒有 → 施放；⑤否則普攻。冷卻與 MP 與玩家同規則。small rng 抖動避免全隊同步。
### 3.3 介面（WIRE 見 `WIRE.md`）
- `GET /rpg/tavern`、`PUT /rpg/party`、`POST /rpg/presets`、`PUT /rpg/presets/{id}`、`DELETE /rpg/presets/{id}`、`POST /rpg/presets/validate`（草稿 → 預算、衍生值、錯誤，不存）。
### 3.4 酒館畫面（新 screen「酒館」，入口與角色／戰鬥同層）
- 上段「隊伍」：隊長（玩家＋職業）＋ 4 個格子（傭兵名＋腳本名／Lv，或「空」）；格子可「移出」。
- 下段「傭兵」：四張卡（頭像、名字、職業、定位一句），每張：腳本下拉（系統預設＋自己的）、「加入隊伍」／「已在隊伍」、「編輯腳本」；文案「未來需花費 DP 雇用（測試階段免費）」。
- 腳本編輯器（同頁展開或子畫面）：名稱、等級（1–99）、六圍（+1／Max／還原，顯示 可配／總點與 cap）、技能（該職業 10 個，−／＋／Max／全部重置，技能點 X／Y、前置文字）、即時衍生值預覽（HP/MP/ATK/MATK/DEF/MDEF，由 validate 端點回傳）、「儲存」「另存新腳本」「刪除」。等級改動時自動清空不合法的配置並提示（或直接拒存並標紅）。
- 手機寬度優先；沿用 CharacterScreen 的元件與樣式語言；金底白字規則；不新增 fixed 覆蓋層。

## 4. 不做／邊界
- 不收 DP、不做雇用交易；不做二轉；怪物 `fixed` 模式仍預留；玩家自己的設定沿用 P5（不做玩家腳本）。
- 既有 `rpg_companions.skill_ids`（舊 AI 用）保留但被 skill_levels 取代；小井不進酒館。
- Migration **181**（一份）：encounters.monster_level＋六場 UPDATE、companions.job_id＋四筆 UPDATE、presets 表＋4 筆系統預設、player_party 表。**先套 DB 再 push**。

## 5. 驗證
- Go：RefPlayerStats 確定性與 cap／預算不變式（N=1,10,27,50,99）、refPlayerTable.json 同步測試、level 模式怪物公式、腳本驗證（合法／超預算／超 cap／前置／非本職技能／系統預設唯讀）、party 規則、bootstrap 隊友衍生值與技能。
- 引擎：HP/MP 整數不變式、隊友 AI 五段優先序（各一個斷言）、level 模式 fixture 與 Go 表一致。
- 模擬：Lv27 參考玩家＋預設隊伍 vs 六場勝率／時長表。
- Neon 分支：套 181、tavern/party/presets 流程、bootstrap 隊伍。
- E2E：酒館頁截圖（隊伍／傭兵／編輯器）、戰鬥中 HP 皆整數、EncounterPicker 顯示 Lv。
