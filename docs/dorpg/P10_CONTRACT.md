# DORPG P10 契約：傭兵換職業＋重騎士「守護」系列（仇恨／守護狀態）＋重騎士防禦拉高 — 2026-09-20

單一真相。P5–P9 契約仍有效（`docs/dorpg/P5_*.md`…`P9_*.md`），本檔只加不改。

## 0. 使用者需求（原話）
- 「小調整：小咪-魔法師、小優-聖職者、阿光-弓箭手」
- 「重騎士增加幾個系列技能和調整：1. 守護系列：吸引怪物的攻擊，在守護狀態下，會增加對怪物的仇恨值，讓怪物只會攻擊重騎士 2. 重騎士的防禦力可以再高一些，目前沒有更明顯的差異」

## 1. 決策（編排者定案）
- **傭兵換職業只改資料**（migration 187）：`rpg_companions.job_id`／`role`／`weapon`（視覺）；四筆系統預設腳本重配配點、技能、裝備、策略；阿深維持重騎士。正式庫使用者自訂腳本 0 筆、`player_party` 4 筆只存 companion_id，不受影響。
- **守護系列＝重騎士專屬第三條技能路線 `c`「守護」**（`rpg_jobs` 新增 `path_c_id/path_c_name/path_c_desc`，只有 heavy_knight 有值；其他職業維持兩條）。四個技能 `hk_c1`～`hk_c4`。
- **引擎新增「守護狀態」與仇恨規則**：怪物選目標時，若場上有存活角色處於守護狀態，**只會攻擊該角色**（多人時取守護狀態剩餘時間最長者）；沒有人守護時沿用現行加權隨機。守護狀態來源：(a) 挑釁／守護姿態技能（新技能 kind `taunt`）；(b) 學了被動「守護本能」的重騎士**按防禦（GUARD）期間**。挑釁另外把「正在蓄力鎖定別人」的怪物立刻改鎖自己。
- **重騎士防禦拉高用兩個槓桿**：(1) 防具 DEF 職業係數 1.3 → **1.7**（重生 50 件重騎士防具的 `profile.def`，VIT／HP% 不動；角色頁 DEF 數字明顯拉開）；(2) 職業天生特性 `rpg_jobs.traits`（JSONB），重騎士 `{"damage_taken_pct": -15}`——在線性減防（怪物 ATK 已 ×3）下，flat DEF 幾點的差異只有 4% 承傷，百分比減傷才是有感的槓桿。特性套用到玩家與同職業傭兵的戰鬥 `equipmentEffects.damageTakenPct`（相加，仍受 −60 夾限）並顯示在角色頁。
- 隊友（AI）目前不能按防禦，所以「守護本能」的防禦即守護只對玩家重騎士生效；AI 傭兵（阿深）靠挑釁／守護姿態達成同樣效果。護盾（shield）沿用現行無過期機制，本輪不改。
- 第三路線的 UI（酒館腳本技能區、角色頁技能頁、後台技能表）以「路線陣列」通用渲染，有 `path_c` 才顯示。

## 2. 資料模型（migration 187，使用者手動套用；可重複執行）
- `rpg_jobs` ADD COLUMN IF NOT EXISTS `path_c_id TEXT NULL`、`path_c_name TEXT NULL`、`path_c_desc TEXT NULL`、`traits JSONB NOT NULL DEFAULT '{}'`；UPDATE heavy_knight：`path_c_id='heavy_knight_guardian_wall'`、`path_c_name='守護'`、`path_c_desc='吸引怪物仇恨、把攻擊全部攬在自己身上的守護者路線。'`、`traits='{"damage_taken_pct":-15}'`。
- `rpg_skills` 新增四筆（job_id=heavy_knight，path='c'，kind 如下；`INSERT … ON CONFLICT (id) DO UPDATE`）：
  | id | 名稱 | tier | kind | target | max | prereq | MP | CD | effect JSON |
  |---|---|---|---|---|---|---|---|---|---|
  | hk_c1 | 挑釁 | 1 | taunt | self | 10 | — | 8 | 6000 | `{"duration_base_ms":4000,"duration_per_level_ms":500,"retarget":true}` |
  | hk_c2 | 守護本能 | 2 | passive | self | 10 | hk_c1≥3 | 0 | 0 | `{"stat":"def_pct","value_base":3,"value_per_level":0.7,"guard_taunt":true}` |
  | hk_c3 | 守護姿態 | 3 | taunt | self | 5 | hk_c2≥3 | 18 | 12000 | `{"duration_base_ms":10000,"duration_per_level_ms":1000,"damage_taken_pct_base":-10,"damage_taken_pct_per_level":-2,"retarget":false}` |
  | hk_c4 | 守護誓約 | 4 | buff | allies | 5 | hk_c3≥3 | 22 | 15000 | `{"stat":"damage_taken_pct","target":"allAllies","value_base":-8,"value_per_level":-2,"duration_ms":10000}`（守護狀態下隊友的保險：對濺射／群攻） |
  display_text 一句話各寫清楚；`implemented=TRUE`；sort_order 接在 hk_b5 之後。
- `rpg_companions` UPDATE：小咪 `job_id='mage'`, `role='法師'`, `weapon='staff'`, `skill_ids='{}'`；小優 `job_id='cleric'`, `role='治療'`, `weapon='staff'`, `skill_ids='{heal}'`, `matk_mult=1.1`；阿光 `job_id='archer'`, `role='游擊'`, `weapon='bow'`；阿深不動。
- 系統預設腳本（user_id IS NULL，Lv25）UPDATE：
  - 小咪（mage）：stats 維持 `{str1,agi1,vit10,dex20,int25,luk5}`；skill_levels 走魔法師 a 路線（傷害線），24 點且通過 ValidatePreset（前置鏈）；equipment `mg_staff_t3`＋`mg_*_t3` 五部位＋`acc_mp_t2`／`acc_mpregen_t2`；strategy `element_advantage`。
  - 小優（cleric）：stats 改成 `{str1,agi1,vit10,dex20,int25,luk5}`；skill_levels `{cl_a1:5, cl_a2:5, cl_a3:10}`（原小咪配置）；equipment `cl_staff_t3`＋`cl_*_t3`＋`acc_mpregen_t2`／`acc_hp_t2`；strategy `protect_allies`。
  - 阿光（archer）：stats 改成 `{str1,agi25,vit1,dex20,int1,luk15}`；skill_levels `{ar_a1:10, ar_a2:3, ar_a3:5}`（原小優配置）；equipment `ar_longbow_t3`＋`ar_*_t3`＋`acc_crit_t2`／`acc_agi_t2`；strategy `focus_fire`。
  - 阿深（heavy_knight）：stats 不動；skill_levels 改 `{hk_b1:5, hk_b2:6, hk_b3:3, hk_c1:3, hk_c2:3, hk_c3:4}`（合計 24，前置：b2←b1≥3、b3←b2≥3、c2←c1≥3、c3←c2≥3）；equipment、strategy 不動（protect_allies）。
- `rpg_armor_items`：重騎士 50 件 `profile.def` 依 P8 公式以係數 1.7 重生（`jsonb_set(profile,'{def}', …)` 逐件 UPDATE；其餘欄位不動）；docs 的 `DORPG_防具表_v1.xlsx`／`ARMOR_v1.md` 同步更新重騎士數值與係數表（同步 Downloads）。
- `presets_test.go` 的 `systemPresetSeeds()` 常數與 migration 同步（單一真相靠人工，測試名 TestSystemPresetSeeds_AreValidAtLevel25 要繼續有效，且新增 hk_c 技能後前置鏈正確）。

## 3. 規則（引擎與後端）
- **技能 kind `taunt`**（Go `validSkillKinds` 與 `ExpandEffect` 新增；TS `SkillKind` 新增）：展開後 `{ durationMs, damageTakenPct, retarget }`（duration＝base＋per_level×(lv−1)；damageTakenPct 同式，缺省 0）。
- **守護狀態**：`PartyActor.tauntUntil: number`（戰鬥時鐘毫秒，0＝無）。`inGuardianState(actor, now) = alive && (tauntUntil > now || (actor.action==='guarding' && actor.guardTaunt))`；`guardTaunt` 由被動 `guard_taunt:true`（守護本能學到 ≥1 級）在 bootstrap 展開成 party member 欄位 `guardTaunt: boolean`。
- **施放 taunt 技能**：設 `tauntUntil = max(tauntUntil, now + durationMs)`；若 `damageTakenPct ≠ 0` 套一個 `ActiveEffect{stat:'damage_taken_pct', value, expiresAt: now+durationMs, sourceSkillId}`（沿用既有 buff 機制，同來源刷新不疊加）；若 `retarget`：所有存活敵人（含 windup 中）目標改為施放者，發事件 `taunt { actorId, enemyIds }`（浮字「挑釁！」）；不 retarget 的只影響之後的選目標。
- **敵人選目標** `pickEnemyTarget(ctx, rng)`：先找守護狀態中的存活角色（多人取 tauntUntil 最大；同值取玩家）→ 有就回傳；否則沿用 `pickWeightedAliveTarget`。`advanceEnemyAI` 的 idle 鎖定與「目標已死」fallback 都改走它。
- **玩家防禦**：GUARD_BEGIN 時若 `guardTaunt` → 進入守護狀態（由 inGuardianState 直接判定，不需寫 tauntUntil）；GUARD_END 即解除（taunt 技能的 tauntUntil 不受影響）。
- **職業特性** `traits.damage_taken_pct`：bootstrap 把它加進該成員 `equipmentEffects.damageTakenPct`（P8 夾限 ≥ −60 仍適用）；`/rpg/me` 與酒館 PresetDTO 的 `equip_bonus` 不混入（特性另欄 `job.traits` 回傳），角色頁顯示「職業特性：受到傷害 −15%」。
- **AI 策略對 taunt 技能**（decideAction，所有策略）：在 buff 階段前新增「守護」判斷：有可用 taunt 技能且自己不在守護狀態 → 施放（優先 retarget=false 的守護姿態，其次挑釁）；`protect_allies` 額外：任一隊友正被 ≥1 個 windup 中的敵人鎖定且挑釁可用 → 立即挑釁；`mp_conserve` 受 MP 門檻限制（緊急治療例外不含挑釁）；`balanced` 對沒有 taunt 技能的角色零改動（既有斷言不變）。玩家自動戰鬥同一函式。
- **HP/MP 整數不變式、冷卻／詠唱／MP 檢查不繞過**。

## 4. 介面（WIRE.md）
- JobDTO 新增 `paths: [{ id, key: 'a'|'b'|'c', name, desc }]`（保留既有 path_a_*/path_b_* 欄位相容）與 `traits: object`。SkillDTO.kind 可為 `taunt`；`path` 可為 `c`。
- bootstrap party member 新增 `guardTaunt: boolean`、`jobTraits: { damageTakenPct }`（已合併進 equipmentEffects，另送供除錯／顯示）；wire skill 新增 kind `taunt` 與展開欄位 `taunt: { durationMs, damageTakenPct, retarget }`。
- 事件新增 `taunt`。

## 5. 畫面
- 酒館腳本技能區與角色頁技能頁：路線通用渲染（第三條「守護」出現在重騎士）；kind 標籤新增「守護」（`taunt`）。
- 戰鬥：PartyCard 狀態標籤加「守護」（守護狀態中，含防禦＋守護本能）；挑釁浮字「挑釁！」；敵人被拉過來時沿用 targetChanged 視覺（若有）。
- 角色頁：職業卡加一行「職業特性：受到傷害 −15%」（有 traits 才顯示）；酒館傭兵卡顯示新職業名。
- 後台：技能表 kind 選單加 `taunt`、path 選單加 `c`；職業表可編輯 path_c 三欄與 traits JSON。

## 6. 驗證
- Go：taunt 展開（等級 1／max）、kind／path 合法值、四筆系統預設通過 ValidatePreset（含 hk_c 前置鏈與 24 點）、JobDTO paths／traits、bootstrap 合併 traits 進 damageTakenPct（夾限）、hk 防具 50 件 def 重生值＝公式（測試讀 187 SQL 對照 gen 結果）。
- 引擎（verify-dorpg-engine／strategies 新增斷言）：守護狀態時敵人 100% 選該角色；到期後回到加權；挑釁 retarget 把 windup 中敵人目標改掉並發事件；守護本能＋GUARD 進入守護狀態、GUARD_END 解除；守護姿態減傷生效與到期；AI（balanced／protect_allies／mp_conserve）對 taunt 技能的施放時機；沒有 taunt 技能的角色與既有斷言零改動；整數不變式。
- 模擬（真引擎、滿隊、Lv27 三職業 vs Lv20/30/40、50 種子）：(a) 阿深有守護技能 vs 無：敵人攻擊落在阿深的比例、玩家被打次數、勝率／時長；(b) 重騎士玩家全套（DEF 1.7＋特性 −15%）vs 輕騎士全套：每次被打 %HP 差距（目標 ≥ 25% 差）；(c) 換職業後四傭兵的表現（小咪法師輸出、小優治療次數）；回報難度變化不改比例。
- Neon：187 套用與冪等、傭兵職業與預設腳本、hk 防具 def、jobs paths／traits、酒館／bootstrap 反映新職業技能、taunt 技能可放進腳本並通過驗證。E2E：酒館傭兵卡新職業與阿深第三路線截圖、戰鬥中阿深挑釁後敵人全打阿深（PartyCard 守護標籤、敵人目標）、玩家重騎士防禦時守護標籤、角色頁職業特性、四種視窗、0 console error。審查。
