# DORPG × RO Classic 實裝計畫

角色：PLANNER。本文件整合 ARCHITECT 三條路線規劃（A/B/C）＋ JUDGE 兩份評分（產品／工程）＋ SURVEY 兩份唯讀盤點（程式／平衡）的全部結論，寫成**唯一一份**可執行計畫。依據文件：`RO_CLASSIC_RULES.md`（901行，已逐格驗證）、`scratchpad/ro_plan/{survey_code,survey_balance,route_minimal,route_faithful,route_hybrid,judge_product,judge_eng}.md`。

本文件是**規劃**，不是實作：未修改 `services/`／`apps/` 任何檔案、未建立實體 migration 檔（下列 SQL 一律標示「草案」）、未做 git 操作、未寫入正式資料庫、未動版號檔。所有 migration 編號從 **180** 起算（git log 顯示目前最新已套用到 179），實作前必須重新對照 `schema_migrations` 正式表（本專案曾因號碼衝突/漏套翻車，見使用者記憶 `migration-apply-audit.md`）。

---

## 0. 一頁摘要

### 三條路線比較

| 路線 | 定位（3–5字） | 產品分數 | 工程分數 | 致命弱點 |
|---|---|---|---|---|
| A 最小風險增量 | 貼皮換血 | 6/10 | **9/10** | 職業機制太薄（只換HP/MP數字+武器動畫），且賭「六職業不會擴充」這個跟專案實際投入規模矛盾的前提 |
| B 忠實 RO 化 | 全套重寫 | 3/10 | 2/10 | 把「加職業＋F/E怪物」放大成「重寫整個戰鬥引擎信任模型」，作廢兩輪平衡報告、強制重置唯一真實玩家資料 |
| **C RO語意+DOR縮放** | **公式借形** | **8/10** | 6/10 | Phase 2/3 仍要解凍 engine、跑兩次全套模擬，長期總成本未必比 B 低，只是拆成兩期 |

### 建議路線：以 **C 為骨幹**，融合兩份判官報告都獨立提出的「第四種組合」調整

一句話理由：C 是唯一「玩家能一路感受到職業差異、又不會一次賭光兩輪平衡心血」的路線；但兩位判官（產品/工程）都指出 C 自己的規劃有可以截長補短的地方——本計畫把這些調整**直接吃進骨幹**，不再是「附註的第四方案」，而是本計畫唯一會執行的版本。具體調整（詳見各節）：

1. **職業資料模型全部照 C**：`rpg_jobs` 正規表，不用 A 的 CHECK 列舉＋JSON map（兩位判官一致）。
2. **職業技能從 C 排的 Phase 4 提前到 Phase 2**（緊接職業骨架之後）：六個具名招式用現有 `coefficient/flat` 架構＋只用 `damage`/`heal` 兩種既有 kind，零 engine 改動（產品判官點名「性價比最高的單一步驟」）。
3. **F/E 怪物拆成兩階段**（借 A 的解耦紀律，不像 C 把「入庫」與「上架」綁在同一 Phase）：先入庫零風險，玩家真正打得到時才驗證。
4. **屬性 2 級表／體型修正表不預設要做**：C 原本排進 Phase 2 就要解凍 `combat.ts`/`formulas.ts`；本計畫把這一步改名為 **Phase 5（選配）**，只有使用者玩過 Phase 1–4 後**明確拍板**才觸發解凍引擎＋全套 BALANCE 重跑（工程判官點名這是「戰鬥判定還在前端、伺服器權威是未來式」現實下最不浪費工的做法）。
5. **Job Lv 防弊採 A 的風險態度**：先用節流／合理性檢查上線（比照現有 `maxDailyBattleLogs` 精神），不把「伺服器逐幀重算戰鬥」當作 Job Lv 生效的技術前提（C 把它綁成 Phase 3 前提，會導致「設計完成但卡在無時程的前置工程」）。

不做（三份文件與 `survey_balance.md` 一致認定風險最高、玩家最難感知、且會惡化現有 Lv27+ 偏易問題）：**怪物絕對值、硬防%減傷、暴擊改無倍率+無視防禦**——本計畫任何階段都不做，除非日後獨立立項做「硬派 RO 模式」（那是另一個需要使用者親自拍板的產品方向題）。

### 使用者只要回答這 3 題就能開工（其餘 §1 表格全部「照建議」即可）

1. Phase 1 選定職業當下，六圍要不要**強制一次性免費重置**？（**建議：要**，理由見 §3 Phase 1）
2. Phase 4 新怪物上場時，美術用「共用既有 5 隻怪的 sprite」還是「先上資料、戰鬥中晚點才看得到，等美術到位」？（**建議：後者**，理由見 §5）
3. Phase 5（Job Lv 深度成長＋屬性/體型接入戰鬥引擎）現在要不要排進時程，還是先當「日後視情況決定要不要做」的選配？（**建議：選配，不排時程**）

---

## 1. 待拍板決策表（§8 九項 + 本計畫新增 1 項）

用法：使用者可以直接說「全部照建議」，本計畫即可依此開工；若某一項想選別的答案，只需要指名那一項的編號重新拍板，不影響其他項。

| # | 決策點 | 選項 | **建議** | 影響範圍 | 若選另一個要多付出什麼 |
|---|---|---|---|---|---|
| 1 | 怪物數值：絕對值 vs 戰力縮放 | A) 維持現行 `power` 動態縮放　B) RO 式絕對值表 | **A：維持戰力縮放**，F/E 新怪物只借 RO 表的「比例關係」換算相對倍率 | `scaling.go` 的縮放公式（`ScaleMonster`/`MonsterRating` 等）完全不動；`rpg_monsters` 表與 `scaling.go` 的 `MonsterRow` struct 仍會因 Phase 3 新增 `content_source`/`element_level` 兩個純標記欄位而有小幅異動（純附加、不影響任何縮放算式），這裡「完全不動」指的是縮放公式本身，不是這兩者字面上零異動（本輪審閱澄清，避免與 Phase 3 的修訂內容互相矛盾） | 選 B：等同把 `scaling.go`/`fixture.ts` 的縮放公式整套換掉，兩輪 BALANCE 心血作廢，且與 D1「任何角色都能上手」的休閒定位衝突（`survey_balance.md §3.10` 已證明兩套安全數值範圍不重疊，無法局部移植） |
| 2 | 硬防% vs 現行線性減算 | A) 維持現行 `max(1,raw-def)`　B) RO 式 `dmg×(100-DEF)/100` | **A：不做，維持現行** | 零改動 | 選 B：玩家 `Def=floor(BaseLv/2)` 隨等級自動漲，直接套百分比＝「練等級」變成免費被動免傷，會讓已知的 Lv27+ 偏易問題（`survey_balance.md §1`：Lv27/50 難度2以上勝率91–99%，目標30–60%）雪上加霜；且要解凍 `combat.ts`/`formulas.ts`，全套重跑 |
| 3 | MATK 平方階梯非線性 | A) 維持現行線性 `INT×1.5`　B) 換 RO 公式 `INT+floor(INT/7)²` | **A：本計畫不做**（非因為不好，是因為現在做時機不對） | 零改動 | 選 B 現在做：現行 40 點配額上限 INT 只能到 17，此時 RO 公式反而比線性弱 28%（`survey_balance.md §2.3`），對 INT 流玩家是隱性削弱；只有等 Phase 5 的 Job Lv 真的能持續加點、INT 能推過交叉點（≈27–28）之後才有意義，屬 Phase 5 選配範圍 |
| 4 | 暴擊：固定倍率+扣防 vs RO式無倍率+無視防禦 | A) 維持 `critMultiplier=2.0`　B) 換 RO 式 | **A：維持現行** | 零改動 | 選 B：`survey_balance.md §2.1` 已算出在現行 `battle_mob_def_ratio=0.25` 尺度下，這個換法讓暴擊期望傷害從 +9.8% 降到 +1.5%，幾乎無感，卻要解凍 engine——划不來 |
| 5 | 屬性系統要不要做 4 級 | A) 不做等級化，維持純展示　B) 做 2 級（一般/精英）　C) 做滿 4 級 | **B，但只在 Phase 5（選配）才真的接進傷害公式**；Phase 1–4 只做「資料標記」不做「乘進公式」 | Phase 1–4：零風險純標記；Phase 5：`config.go`/`formulas.ts` 各加一個 map | 選 C（滿 4 級）：需要開 400 格專表 `rpg_element_matrix`，且 DORPG 現有 8 屬性桶要換血成 RO 的 10 屬性英文代碼，這是文件開頭「B 忠實 RO 化」**整條路線**的工程規模（不是本列選項欄裡的 B/C——同一張表用同一個字母代表兩層不同的東西，讀的時候不要混在一起），本計畫不建議 |
| 6 | 體型表要不要進武器系統 | A) 不做，維持展示欄位　B) 做，接進傷害公式 | **A（Phase 1–4）／B 留在 Phase 5（選配）** | Phase 1–4：`size` 欄位純展示；Phase 5：新增 `battle_size_chart` 並接進 `formulas.ts` | 若現在就要 B：需要先有 Phase 1 引入的「職業武器種類」當前置（本計畫已經滿足這個前提），但仍需解凍 engine 全套重跑，建議跟 §5 一起評估，不單獨提前 |
| 7 | HP/SP 職業係數 | A) 不做　B) 做 | **B，Phase 1 立即上線** | `rpg_jobs.hp_mult/mp_mult`（見 §4） | — |
| 8 | 一轉技能先做哪些 | 見 §4/§6 職業技能表 | **六個單體 damage/heal 技能，Phase 2 上線**（比 C 原排的 Phase 4 提前） | `rpg_skills` 新增 6 列＋2 個欄位 | 若要做被動技能（如 RO 原版盜賊 Double Attack）：需要碰 `combat.ts` 新增判定分支，本計畫改用一個等效主動技能替代，避免這個成本（見 §4 附註） |
| 9 | STR/INT 整十階梯加成 | A) 不做　B) 只做 STR　C) STR+INT 都做 | **B：只做 STR，Phase 1 上線**；INT 併入決策點 3，本計畫不做 | `compute.go` 一行新增 | 選 C：INT 項牽動 MATK 公式，理由同決策點 3 |
| **10（新增）** | **Job Lv 防弊要做到什麼程度** | A) 節流＋合理性檢查（信任前端回報，加每日/每場上限與時長交叉檢查）　B) 伺服器逐幀重算戰鬥（真正防弊，等同重寫一份 Go 版 combat 引擎） | **A：先用節流上線** | `battle.go` 的 `BattleReport` handler 加檢查邏輯，比照現有 `maxDailyBattleLogs` 精神 | 選 B：這是既有 P3「伺服器權威判定」待辦的完全體，成本遠高於本計畫其他任何一步，且目前沒有時程——本計畫不把它當 Job Lv 生效的前提，避免 Phase 5 卡在「設計完成但不能安全上線」（`judge_eng.md` 對 C 路線的致命弱點診斷） |

**決策點 #9 的兩點補充說明（本輪審閱新增，理由見文末修訂紀錄 #5、#7）**：

1. **這項是全站生效、不分職業的獨立改動**，不是「選了職業才有」——STR 本來就是任何角色都能配的既有素質，RO 本身這條規則也不分職業。這跟 Phase 1「未選職業角色數值逐位元不變」的驗收條件有交集：正確的不變式是「未選職業**且 STR<10**」才逐位元不變，STR≥10 的既有角色（含 owner 現有角色，若六圍未重置）`atk` 會多 +1~4，這是本計畫刻意要的效果，不是需要修的迴歸——已回寫進 Phase 1 的目標描述與驗收條件①，避免實作者依原文字面寫出「只要選了職業就不該变」的錯誤回歸測試。
2. **只套 STR、不分近戰/遠程分支**：對照 `RO_CLASSIC_RULES.md §1.2`，RO 原意近戰職業主屬性是 STR、弓系（遠程）主屬性是 DEX，嚴格忠實的做法應該是遠程職業套 DEX 整十階梯而不是 STR。本計畫為了保住「`compute.go` 一行新增」的最低成本，選擇**全職業統一套用 STR 版本**，弓箭手（遠程分支）也一樣吃 STR 整十階梯，不额外分支處理 DEX——這是已知的簡化，不是遺漏；日後若要修正，只需要在 `compute.go:107` 那個既有的 `melee`/`ranged` 分支判斷式裡把整十階梯也拆成兩支（STR 版給 melee、DEX 版給 ranged），成本同樣很低，可以晚點再補，不影響本計畫任何一個 Phase 的其他部分。

---

## 2. 目標狀態（玩家在畫面上會看到什麼，不寫程式術語）

**Phase 1 之後**：打開角色頁，第一次會看到「選擇職業」的六選一畫面——劍士、魔法師、服事、弓箭手、盜賊、商人，各自附一句話定位說明與大致的血量/魔力預覽。選完之後（選過就不能反悔，像 RO 的轉職一樣），角色卡片上會出現職業徽章；劍士血量明顯比魔法師厚、魔法師的魔力池明顯比劍士大；進入戰鬥畫面時，自己的角色終於會揮舞跟職業對應的武器（現在所有人都是耍劍——這個既有小缺陷會一併修掉）。**取捨說明**：目前遊戲裡只有劍／杖／弓／巨劍四套揮擊視覺可用，所以劍士與盜賊會共用劍的動畫、魔法師與服事會共用杖的動畫，不是六個職業各自六套獨立動畫——這是為了不用等新美術資源到位就能上線的刻意收斂（見§4）。

**Phase 2 之後**：角色的技能欄裡多了一個屬於自己職業的專屬招式——劍士是「強打」、魔法師是「冰箭」、服事是「治療術」、弓箭手是「雙重射擊」、盜賊是「奇襲」、商人是「貪婪之心」。選好職業立刻就能用，不需要額外等待或練功。

**Phase 3 之後**（玩家暫時還看不到）：後台的怪物管理頁面多出一批新的、比現有怪物更弱的小怪，附上完整的屬性/體型/種族資料，等待美術資源與新關卡上線。

**Phase 4 之後**：遊戲裡出現一個新的「新手訓練營」關卡，用全新的、比現在最弱的怪物還要更溫和的怪物組成，讓剛選完職業、還沒配好點數的新手也能輕鬆獲勝、熟悉戰鬥操作。

**Phase 5（選配，若使用者事後拍板要做）**：戰鬥結算畫面會出現「職業經驗+N」，角色頁的 Job Lv 數字真的會往上跑，每練到一定等級可以拿到額外的配點數；戰鬥中換元素技能打對應弱點的怪物會出現「效果拔群」的明顯傷害提升，用小型武器打大型怪、大型武器打小型怪會有肉眼看得出的傷害落差。

---

## 3. 分階段計畫

> 每個階段都能獨立上線；Phase 1–4 全程**零次**全套 BALANCE 重跑、**零次**解凍 `apps/web/src/lib/dorpg/engine/{combat.ts,formulas.ts}`。只有 Phase 5（選配）需要解凍引擎與全套重跑。

### Phase 1 — 職業骨架＋角色頁差異化

**目標**：玩家可以選一次職業，職業決定 HP/MP 倍率與物理攻擊走近戰或遠程分支，戰鬥中顯示對應武器視覺。既有角色（未選職業）數值逐位元不變——**但決策點 #9（STR 整十階梯）例外**：這項改動全站生效、不分職業，STR<10 的角色不受影響，STR≥10 的角色 `atk` 會有 +1~4 的預期內變化（精確說法見下方驗收條件①）。

**資料模型變更（草案，尚未建立 migration 檔，暫編號 180）**：

```sql
-- 180_rpg_jobs.sql（草案）
CREATE TABLE IF NOT EXISTS rpg_jobs (
  id          TEXT PRIMARY KEY,             -- swordman|mage|acolyte|archer|thief|merchant
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  weapon_kind TEXT NOT NULL,                -- 收斂進現有 4 種 validWeaponKinds：sword|staff|bow|greatsword（見§4「視覺收斂」說明，content.go 不需要修改）
  atk_branch  TEXT NOT NULL DEFAULT 'melee',-- melee|ranged，決定 Compute() 物攻走哪個分支
  hp_mult     NUMERIC(6,3) NOT NULL DEFAULT 1,
  mp_mult     NUMERIC(6,3) NOT NULL DEFAULT 1,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  sort_order  INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO rpg_jobs (id, name, description, weapon_kind, atk_branch, hp_mult, mp_mult, sort_order) VALUES
  ('swordman', '劍士',   '近戰坦克型，HP最高、物理攻擊穩定。',       'sword', 'melee',  1.50, 0.60, 1),
  ('mage',     '魔法師', '魔法攻擊最強、MP池最大，但HP最脆弱。',     'staff', 'melee',  0.64, 1.80, 2),
  ('acolyte',  '服事',   '物防法防均衡，MP池次高，適合輔助。',       'staff', 'melee',  0.86, 1.50, 3),
  ('archer',   '弓箭手', '遠程物理輸出，命中與傷害皆穩定。',         'bow',   'ranged', 1.07, 0.60, 4),
  ('thief',    '盜賊',   '敏捷型，HP中等、行動最靈活。',             'sword', 'melee',  1.07, 0.60, 5),
  ('merchant', '商人',   '均衡防禦型，生存力強。',                   'greatsword','melee', 0.86, 0.90, 6)
ON CONFLICT (id) DO NOTHING;

ALTER TABLE player_characters
  ADD COLUMN IF NOT EXISTS job_class TEXT REFERENCES rpg_jobs(id);
-- 刻意不加 NOT NULL：job_class=NULL 是既有角色與新角色的合法長期狀態（見下方相容性）。

INSERT INTO schema_migrations (version) VALUES ('180') ON CONFLICT DO NOTHING;
```

`hp_mult`/`mp_mult` 換算規則（§4 有完整推導）：取 RO `HpFactor`／`SpIncrease` 除以六職平均值，正規化成「均值=1.0」的倍率——**借 RO 的相對比例，不借 RO 的絕對曲線**，這樣不需要把既有已校準的怪物 DPS 全部重新調整。

**後端改動**：
- `content.go` 的 `validWeaponKinds` **不需要修改**——六職業的 `weapon_kind` 已收斂進現有 4 種（`sword`/`staff`/`bow`/`greatsword`），見上方 SQL 與§4「視覺收斂」說明（本輪審閱修訂 #2，原文誤植成擴充到 7 種）。
- 新增 `services/api/internal/rpg/content_repo.go` 第 7 組 CRUD 函式（`scanJob`/`listJobs`/`getJobByID`/`upsertJob`/`deleteJob`），複製既有 6 組（monster/skill/item/scene/companion/encounter）同構函式的模式（本輪審閱修訂 #8，原文誤數成 5 組／第 6 組）。
- `compute.go`：`ComputeInput` 加 `Job *JobRow`（nil＝未選職業）；`compute.go:100-103`（HP/MP 基底）乘上 `Job.HPMult`/`Job.MPMult`；`compute.go:107`（近戰/遠程分支）優先讀 `Job.AtkBranch`，`Job==nil` 時退回現行 `cfg.DefaultWeaponType`；`compute.go:112` 附近新增 STR 整十階梯：`atk += floorDiv(Str, cfg.StrTierPer)² × cfg.StrTierCoef`（`config.go` 新增 `StrTierPer`/`StrTierCoef` 兩個欄位，預設 10/1）——**這項全站生效、不分職業**（決策點 #9，見§1 決策表下方新增的說明段落），`Job==nil` 的角色只要 STR≥10 一樣會吃到這行新增的效果，不受「未選職業」保護。
- `handler.go`：`character` struct 加 `JobClass *string`；新增 `POST /rpg/job {"job_class":"swordman"}`——已選過（非 NULL）一律 400；用 `WHERE job_class IS NULL` 當 CAS 條件（單一 `UPDATE` 陳述式的 `WHERE` 本身具原子性，`RowsAffected()=0` 就代表已經被搶先選過，直接回 400，不需要額外顯式鎖）。要不要重置六圍是**§0 開工前使用者一次性拍板**的產品決策（建議：要），不是玩家自己勾選的選項——落地成 `config.go` 新增一個 `ResetStatsOnJobSelect bool` 欄位（預設照拍板結果寫死，不開放後台中途切換，避免「同一批玩家有的重置有的沒重置」的不一致狀態）。`cfg.ResetStatsOnJobSelect==true` 時，同一交易內：
  1. 先 `SELECT str_pt,agi_pt,vit_pt,dex_pt,int_pt,luk_pt,free_points FROM player_characters WHERE user_id=$1 FOR UPDATE` 讀出重置前的六圍與剩餘點數；
  2. 對六個素質各算一次 `spentBetween(cfg, cfg.InitialStat, 舊值)`（現成函式，見 `compute.go`），加總得到「這次要退回多少點數」；
  3. `UPDATE player_characters SET job_class=$1, str_pt=$init, agi_pt=$init, vit_pt=$init, dex_pt=$init, int_pt=$init, luk_pt=$init, free_points=$initFreePoints, updated_at=NOW() WHERE user_id=$2 AND job_class IS NULL`（`$init`＝`cfg.InitialStat`、`$initFreePoints`＝`cfg.InitialFreePoints`）；
  4. 對六個素質各寫一筆 `INSERT INTO player_stat_log (user_id, stat, from_value, to_value, cost) VALUES ($1,$2,$3,$4,$5)`——`from_value`＝重置前的值、`to_value`＝`cfg.InitialStat`、`cost`＝**負值**＝`-(該素質重置前的 spentBetween 結果)`（比照 `player_stat_log.cost` 既有語意「這筆異動花掉的點數」，退點就是負的花費），讓稽核列同時能對得上「這次重置退了多少點數」，事後可用這 6 筆算回原始配點狀態。
  `cfg.ResetStatsOnJobSelect==false` 則只 `UPDATE job_class` 一欄，不寫 `player_stat_log`。
- `battle.go:254`：`Compute()` 呼叫補 `Job` 參數。`battle.go:533-540`（`wirePartyMember` 玩家那筆）補上 `Weapon: job.WeaponKind`——**這修掉現有的既有缺陷**：現在所有玩家在戰鬥畫面因為這裡從未填值，一律被前端 `engine/index.ts:52` 的 `pm.weapon ?? 'sword'` 退回耍劍外觀。
- `admin.go:342`：`AdminPreview` 加 `job` 查詢參數，供後台試算工具選職業預覽。

**前端改動**：
- `apps/web/src/lib/api.ts`：`RpgCharacter` 加 `job_class: string | null`；新增 `rpgApi.getJobs()`/`rpgApi.chooseJob(jobId)`。
- `apps/web/src/lib/rpgMeta.ts`：`CONFIG_GROUPS` 不需要新增（職業走專表不走 JSON，跟 A 路線的做法不同）；後台 `admin/rpg/page.tsx` 新增「職業」分頁（CRUD `rpg_jobs`），比照既有怪物/技能分頁。
- `apps/web/src/components/CharacterScreen.tsx`：`job_class===null` 時顯示一次性六選一 modal（職業名稱/簡介/預估 HP·MP），選定後呼叫 `chooseJob`；選完後角色頁頂部加職業徽章。
- **不需要動** `fixture.ts`／`engine/**`／`verify-dorpg-engine.mjs`——`Compute()` 只有 Go 一份、沒有 TS 鏡像，`Weapon` 只是填一個前端本來就選填、從未真正使用的既有欄位。

**驗收條件**：
1. 未選職業**且 STR<10** 的角色，`/rpg/me` 回傳數值與上線前逐位元相同（回歸測試，用現有擁有者帳號數字核對）；未選職業但 STR≥10 的角色，`atk` 會因決策點 #9（全站生效、不分職業）多 +1~4，這是預期內的改動，不是迴歸缺陷——寫回歸測試時要挑 STR<10 的帳號，或針對 STR≥10 的帳號改用「差值等於整十階梯公式算出的量」當斷言，不能直接斷言逐位元相同。
2. 六個職業各選一次，`max_hp`/`max_mp` 依 `hp_mult`/`mp_mult` 正確變化。
3. `POST /rpg/job` 對已選過職業的帳號回 400。
4. 戰鬥畫面六職業玩家顯示各自 `weapon_kind` 對應的武器動畫——因§4「視覺收斂」取捨，劍士／盜賊同為 `sword`、魔法師／服事同為 `staff`，兩兩共用同一套揮擊視覺是**預期內**的簡化，不是缺陷；驗收重點是「每個職業顯示的武器動畫與其 `weapon_kind` 一致」，不是「六個職業六種不同動畫」（此前恆定顯示劍士的既有缺陷仍然要修掉）。
5. `compute_test.go`/`config_test.go`（機械新增 `Job: nil` 或針對六職業的呼叫點）全數通過。

**風險與回退**：低。`Compute()` 簽章變動範圍可控（3 個呼叫點），`Job==nil` 分支保證舊行為不變（決策點 #9 的 STR 整十階梯例外，見上）。若上線後發現問題：
- 若拍板 `cfg.ResetStatsOnJobSelect=false`：`job_class`/HP·MP·武器分支這部分，`job_class` 欄位可以整批 `UPDATE ... SET job_class=NULL` 復原顯示邏輯，不影響六圍/點數資料。
- **若拍板 `cfg.ResetStatsOnJobSelect=true`：這是不可逆操作，不是「不影響其他資料」**——正式站目前只有 owner 一人的角色列，且該角色 40 點已配完，是真實資料；重置當下六圍會被覆蓋成 `cfg.InitialStat`，光是把 `job_class` 復原成 `NULL` **不會**還原六圍。前端 UI 必須在使用者按下「選擇職業」前二次確認，明白告知「這會清空目前配點，且不能復原」；後端則靠上面寫入的 6 筆 `player_stat_log`（`from_value`＝重置前的值）留下可供事後人工回復的軌跡——回復時工程師手動依這 6 筆稽核列把六圍與 `free_points` 寫回去，系統本身不提供一鍵復原重置的端點（風險太低頻，owner 只有一人一次性觸發，不值得為此開一支還原 API）。

**平衡模擬**：**輕量**，不需要全套重跑。針對六職業中 HP 最高/最低（劍士1.50× / 魔法師0.64×）與代表性等級（Lv27）各跑一次既有 `training_ground`／`taipei101_boss` 兩個極端場景（6職業×2場＝12次模擬），確認新的 HP/MP 極值沒有把既有勝率推出契約紅線（>5分鐘不死／<15秒團滅）。STR 整十階梯依 `survey_balance.md §2.4` 結論（現行點數規模下影響≤1~4點）免驗證。

---

### Phase 2 — 六職業起手技能（提前執行，性價比最高的一步）

**目標**：玩家選定職業後立即擁有一個專屬招式，不需要等待任何後續系統（Job Lv、屬性/體型）。

**為什麼提前**：兩份判官報告（產品／工程）都獨立指出，六個職業技能是「用現有 `coefficient`/`flat` 架構、零 engine 改動成本」的內容，且是玩家最想要的「養角色」體感來源——沒有理由排到 C 原本規劃的 Phase 4 才做。

**資料模型變更（草案，暫編號 181）**：

```sql
-- 181_rpg_job_skills.sql（草案）
ALTER TABLE rpg_skills
  ADD COLUMN IF NOT EXISTS job_id           TEXT REFERENCES rpg_jobs(id), -- NULL＝通用/舊技能（現有5個維持NULL）
  ADD COLUMN IF NOT EXISTS unlock_job_level INT NOT NULL DEFAULT 1;       -- 本階段全部給1（選定職業立即可用）

INSERT INTO rpg_skills (id, name, icon_id, kind, target, weapon, element, mp_cost, cooldown_ms, coefficient, flat, cast_ms, job_id, unlock_job_level, sort_order) VALUES
  ('bash',           '強打',     'icon_skill_slash',   'damage', 'enemy', 'sword',  'neutral', 12, 3000, 2.0, 0,  300,  'swordman', 1, 20),
  ('cold_bolt',      '冰箭',     'icon_skill_ice_lance','damage','enemy', 'staff',  'water',   14, 2500, 1.5, 10, 800,  'mage',     1, 21),
  ('heal',           '治療術',   'icon_skill_heal',    'heal',   'ally',  'staff',  'neutral', 15, 4000, 1.0, 20, 1000, 'acolyte',  1, 22),
  ('double_strafe',  '雙重射擊', 'icon_skill_slash',   'damage', 'enemy', 'bow',    'neutral', 12, 2800, 1.8, 0,  400,  'archer',   1, 23),
  ('surprise_strike','奇襲',     'icon_skill_slash',   'damage', 'enemy', 'sword',  'neutral', 10, 2000, 1.6, 5,  200,  'thief',    1, 24),
  ('mammonite',      '貪婪之心', 'icon_skill_slash',   'damage', 'enemy', 'greatsword','neutral', 20, 4000, 2.5, 0, 300,  'merchant', 1, 25)
ON CONFLICT (id) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('181') ON CONFLICT DO NOTHING;
```

**重要附註（決策表 #8 的落地說明）**：RO 原版盜賊的代表技能是「二段攻擊 Double Attack」（被動、機率追加傷害），這需要在 `combat.ts` 新增一個判定分支才能實作——為了保住「Phase 2 零 engine 改動」的承諾，本計畫用一個**主動**技能「奇襲」替代（傷害倍率與冷卻時間營造盜賊「快而輕」的手感），放棄 RO 原版的被動機制。若日後想補回真正的被動二段攻擊，可在 Phase 5 與屬性/體型系統一起評估。

**後端改動**（本輪審閱抓到的第一個高嚴重度缺口，原文這裡完全沒寫到重點：DB 寫了 6 個技能，沒有下面第 3 點就不會出現在任何人的技能欄——見文末修訂紀錄 #1）：

1. `content.go`：`SkillRow` struct 加 `JobID *string`（json `job_id`）/`UnlockJobLevel int`（json `unlock_job_level`）；`SkillRow.Validate()` 加一段——`JobID` 非 nil 時檢查非空字串即可，存在性交給 DB 的 `REFERENCES rpg_jobs(id)` 外鍵擋，不必應用層重查一次。
2. `content_repo.go`：`skillCols` 常數尾端加 `, job_id, unlock_job_level`；`scanSkill()` 的 `row.Scan(...)` 尾端加 `&s.JobID, &s.UnlockJobLevel`；`upsertSkill()` 的 INSERT 欄位清單／VALUES／ON CONFLICT UPDATE 三處都各加這兩欄。`listSkills`/`getSkillsByIDs`/`deleteSkill` 共用同一個 `skillCols`/`scanSkill`，不用個別修改（比照 Phase 3 對 `monsterCols` 的處理手法，見下方§3 Phase 3）。
3. **`battle.go`——真正讓技能生效的關鍵，原文漏掉的部分**：`buildSkillSlots(ctx, cfg, pbs, loadout []string)` 加一個參數 `jobClass string`（未選職業傳空字串）；呼叫端 `BattleBootstrap`（約 :613-618）改成 `h.buildSkillSlots(ctx, cfg, pbs, loadout, ch.JobClass)`（`ch` 是 Phase 1 已經在 `character`/`characterCols` 加好 `JobClass` 的角色列，直接讀現成欄位）。函式契約由「loadout 非空就用 loadout，否則取 `is_default` 的技能」改成：loadout 分支完全不動；`defaults` 分支的篩選條件從單純的 `sr.IsDefault` 改成「`sr.IsDefault && sr.JobID==nil`（既有 5 個通用技能）**或** `sr.JobID!=nil && *sr.JobID==jobClass && ch.JobLevel>=sr.UnlockJobLevel`（本職業技能）」，其餘依 `sort_order`（`listSkills` 已排序）合併取前 8 格的邏輯不變——通用技能 `sort_order` 個位數、職業技能草案給 20-25，合併後自然是「先放滿通用技能、再接職業技能」，6+1=7 格，8 格夠放。
4. `getLoadout`/loadout 非空分支維持完全不動，理由見下方前端改動。

**前端改動**：**無**（原文「技能選擇 UI（若有既有的「裝備技能」畫面）」是猜錯方向——已全 repo 核對過：`apps/web/src/lib/api.ts` 的 `rpgApi` 只有 `me`/`allocate` 兩支，找不到任何 `setLoadout`/`equipSkill` 呼叫；`player_characters.loadout` 全 repo 只有 `battle.go getLoadout()` 一個讀取點，沒有任何寫入路徑。也就是說**現在所有玩家的技能欄都必然走「預設帶入」這條路徑**，loadout 永遠是空的——本階段只要修好上面第 3 點的 `buildSkillSlots`，玩家選定職業後下一次進戰鬥就會自動看到職業技能，不需要等一個還不存在的「裝備技能」UI，也不需要新增任何前端檔案。

**驗收條件**：
1. 選定職業的角色，`GET /rpg/battle/bootstrap` 回傳的技能欄自動包含對應職業技能；未選職業或選了別的職業，技能欄裡看不到這個技能——是「自動帶入」不是「能不能手動裝備」（見上，玩家端目前沒有手動裝備技能的介面）。
2. 既有 5 個通用技能（`job_id=NULL`）任何角色的技能欄仍會照舊自動帶入，行為不變（回歸測試：未選職業角色 bootstrap 回傳的技能欄與上線前逐位元相同）。
3. 六個新技能各自過一次既有的「新增技能」BALANCE 抽查（單技能小規模模擬，非全套），且要透過修好後的 `buildSkillSlots` 實際跑一次 bootstrap 取值，不能只憑 DB 資料表算——避免「抽查了資料但沒抽查到玩家真正會用到的路徑」。

**風險與回退**：低但不是零風險——`buildSkillSlots` 是每次進戰鬥都會呼叫的既有函式，改動範圍集中在它的「預設帶入」分支（loadout 非空分支完全不動），Phase 1 已經備好 `ch.JobClass`/`ch.JobLevel` 可直接讀，不需要額外查詢；驗收條件②的回歸測試就是為了保證「未選職業角色」這條最多人會走的路徑逐位元不變。`rpg_skills` 新增兩個欄位（純 ADD COLUMN）本身零風險。

**平衡模擬**：局部——每個新技能各自跑一次既有的「新增技能/道具」驗證流程（例如檢查 `bash`/`mammonite` 的高倍率單體傷害會不會在低 MP 消耗/短冷卻下破壞現有難度曲線），不需要全套三等級×多場×多策略模擬。

---

### Phase 3 — F/E 級怪物資料入庫（後台可見，玩家暫不可見，零平衡影響）

**目標**：把 F/E 級怪物的資料存進資料庫，但**不接上任何 encounter**——先讓資料就緒、後台可核對，把驗證面限縮到最小。

**資料模型變更（草案，暫編號 182）**：

```sql
-- 182_rpg_monsters_ro_content.sql（草案）
ALTER TABLE rpg_monsters
  ADD COLUMN IF NOT EXISTS content_source TEXT NOT NULL DEFAULT 'dor', -- 'dor' | 'ro_classic'
  ADD COLUMN IF NOT EXISTS element_level  TEXT NOT NULL DEFAULT '1';   -- '1'(一般) | '2'(精英)，供 Phase 5 使用，本階段純標記
-- ⚠️ 命名警告（本輪複驗 #C）：這個 element_level 是 DORPG 自訂的「一般/精英」二元標記，**不是**
--    RO_CLASSIC_RULES.md §3 講的怪物屬性等級 1~4（mob_db.yml 的 ElementLevel）。兩者剛好有幾隻對得上
--    （例：老樹精 RO 是火2、這裡也標 2），但多數對不上（例：曼陀羅 RO 是地3、這裡標 2），因為這裡實際
--    是照「是否精英/主動」在挑 1 或 2。Phase 5 若要接屬性相剋表，RO 的表是 4 級不是 2 級，屆時要嘛
--    改成真正搬 RO 的 ElementLevel、要嘛把二元標記對應到表的哪兩級講清楚，不能直接拿這欄當 RO 等級查表。

-- 既有 5 隻（DOR-MON-A~E）content_source 維持 'dor'、element_level 維持 '1'，不受影響。

-- 新增列示範（完整清單見 §5，此處僅列格式；正式批次建議寫生成腳本讀 RO_CLASSIC_RULES.md §5.2/§5.3，不要手刻49列）
INSERT INTO rpg_monsters (id, name, rank, attribute, element_level, size, race, content_source, hp_mult, atk_mult, def_mult, speed_mult, threat, is_boss, sort_order) VALUES
  ('RO-MON-F-poring', '波利', 'F', '水', '1', '中型', '植物', 'ro_classic', 0.08, 0.31, 0.05, 0.70, 1, FALSE, 100)
  -- ……其餘列見 §5 完整表
ON CONFLICT (id) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('182') ON CONFLICT DO NOTHING;
```

**後端改動**：**小幅但不是「無」**（本輪審閱抓到的高嚴重度缺口——`content.go`/`content_repo.go` 的怪物欄位是寫死的白名單清單，不會自動長出新欄位；`MonsterRow.Validate()` 不檢查值域這件事沒錯，但那不代表新欄位不用手動加進讀寫路徑，否則 API 回傳與後台編輯都看不到 `content_source`/`element_level`，驗收條件①的篩選功能也做不出來。四處要改，手法與 Phase 2 對 `SkillRow` 的處理完全一致（同一套「加欄位＝四處同動」模式，見文末修訂紀錄 #3）：

1. `services/api/internal/rpg/scaling.go` 的 `MonsterRow` struct 加 `ContentSource string`（json `content_source`）/`ElementLevel string`（json `element_level`）。
2. `services/api/internal/rpg/content_repo.go` 的 `monsterCols` 常數尾端加 `, content_source, element_level`。
3. 同檔 `scanMonster()` 的 `row.Scan(...)` 尾端加 `&m.ContentSource, &m.ElementLevel`。
4. 同檔 `upsertMonster()` 的 INSERT 欄位清單／VALUES／ON CONFLICT UPDATE 三處都各加這兩欄。

`listMonsters`/`getMonstersByIDs`/`deleteMonster` 共用同一個 `monsterCols`/`scanMonster`，不用個別修改。`MonsterRow.Validate()`（content.go）維持不檢查這兩欄的值域——跟 `rank`/`race` 同一類「純顯示/標記」欄位，不影響任何公式。

**前端改動**：**三處，不是一句話**（本輪複驗抓到的低估，見文末修訂紀錄 #B）：

1. `apps/web/src/lib/api.ts` 的 `RpgMonster` interface 逐欄鏡像後端 `MonsterRow`，同樣要補 `content_source`／`element_level`，否則後台表單拿不到這兩個 key。
2. `apps/web/src/app/admin/rpg/page.tsx` 的 `emptyMonster()` 與 `MONSTER_FIELDS` 要補這兩欄。⚠️ 不補的後果很具體：後台按「＋新增」建立的**新**怪物列，表單不會收集這兩欄，送出的 JSON 缺 key、後端解出 Go 零值空字串，寫進 NOT NULL 欄位變成 `content_source=''`（而不是預期的 `'dor'`）。（**既有列不受影響**：`startEdit` 是 `setForm({...r})` 執行期展開後端 JSON，TypeScript 型別不會在執行期濾掉未宣告欄位，所以既有怪物被後台編輯儲存不會把這兩欄洗掉。）
3. `SimpleContentTab` 目前**完全沒有任何篩選機制**，只有寫死的 `sort_order` 排序——驗收條件①要的篩選 UI 要從零做，不是掛既有元件。若想再壓低本階段成本，可把驗收條件①降級成「後台列表能看到新列且 `content_source` 欄位顯示正確」，把篩選 UI 併進 Phase 4。

**驗收條件**：
1. 後台怪物列表能篩選/排序出 `content_source='ro_classic'` 的新列。
2. 既有 6 場 encounter 的模擬結果與上線前逐位元一致（證明零污染——因為 `rpg_encounter_monsters` 沒有任何一列引用新 id）。

**風險與回退**：接近零，但精確地說不是字面上「零後端改動」（見上）——`ALTER TABLE ADD COLUMN IF NOT EXISTS ... DEFAULT` 對既有資料列純附加、不改變任何既有值；四處欄位清單的異動也是純增量（不刪不改既有欄位順序），舊呼叫端行為不受影響。`ON CONFLICT DO NOTHING` 保證 SQL 重跑安全；有問題直接 `DELETE FROM rpg_monsters WHERE content_source='ro_classic'` 即可完全復原（連新欄位都要復原的話，`ALTER TABLE ... DROP COLUMN` 同樣是一行可逆指令）。

**平衡模擬**：**不需要**。

---

### Phase 4 — 新手訓練營上線（F 級怪物玩家真正可見）

**目標**：新增一個用 F 級怪物組成的新關卡，讓玩家真正打得到 Phase 3 入庫的新怪物。

**資料模型變更（草案，暫編號 183）**：

```sql
-- 183_rpg_ro_training_encounters.sql（草案）
INSERT INTO rpg_scenes (id, name, image_url, slots, location_note, sort_order) VALUES
  ('scene_beginner_camp', '新手訓練營', '/ui/dorpg/scene/scene_beginner_camp.webp',
    '[{"id":"rear_left","x":0.27,"y":0.53,"scale":0.25,"row":"rear"},{"id":"rear_right","x":0.68,"y":0.54,"scale":0.38,"row":"rear"},{"id":"front_left","x":0.18,"y":0.88,"scale":0.33,"row":"front"},{"id":"front_center","x":0.5,"y":0.89,"scale":0.36,"row":"front"},{"id":"front_right","x":0.82,"y":0.88,"scale":0.33,"row":"front"}]'::jsonb,
    '新手村（RO風）', 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO rpg_encounters (code, title, subtitle, scene_id, scene_kind, difficulty, power_scale, escape_chance, can_escape, sort_order) VALUES
  ('beginner_camp_f', '新手訓練營．F級', '波利與滴滴．最溫和的第一戰', 'scene_beginner_camp', 'normal', 1, 0.30, 0.50, TRUE, 0)
ON CONFLICT (code) DO NOTHING;
-- rpg_encounter_monsters 略，比照既有 6 場的寫法，slot 對應 §5 的 F 級怪物 id。

INSERT INTO schema_migrations (version) VALUES ('183') ON CONFLICT DO NOTHING;
```

`power_scale=0.30` 刻意低於現有最低難度 `training_ground`（0.60），因為 F 級是「比現有最弱怪更弱」的新手前導內容。

**後端改動**：無（`battle.go` 既有 API 已通用支援任意 encounter，`wirePartyMember` 的職業武器邏輯 Phase 1 已完成）。

**前端改動**：關卡選單顯示新場景（若有專屬美術，見驗收條件②；若無則見 §5 的美術缺口處理）。

**驗收條件**：
1. 新 encounter 用既有模擬工具跑 3 策略×≥100 種子，時長/勝率落在「比 `training_ground` 更軟」的目標區間（例如 20–40 秒、勝率 >95%）。
2. 新怪物的視覺資源確實可載入（真實圖或第 5 節指定的替代方案，非死連結）。
3. 職業武器 VFX 在既有 4 種 `WeaponKind`（`sword`/`staff`/`bow`/`greatsword`）內正確對應到職業——劍士/盜賊顯示 `sword`、魔法師/服事顯示 `staff`、弓箭手顯示 `bow`、商人顯示 `greatsword`，兩兩共用是§4「視覺收斂」的預期結果，不是缺陷。

**風險與回退**：中，風險集中在「美術資源是否到位」而非程式邏輯——若美術排不進來，Phase 4 可以延後，Phase 1–3 仍可獨立上線並交付「六職業＋起手技能＋後台新怪物資料」三項可見成果。

**平衡模擬**：只需要驗證新加的這一場（3策略×≥100種子），不需要重跑既有 6 場——既有 6 場的 `rpg_encounter_monsters` 編組完全沒變。

---

### Phase 5（選配，需使用者事後明確拍板才觸發）— Job Lv 真正成長 ＋ 屬性/體型接入戰鬥引擎

**觸發條件**：使用者玩過 Phase 1–4（職業＋起手技能＋F級怪物基本盤）後，主動表示「想要更深的機制」才啟動本階段。**不預設一定要做**，也不預先排時程——這是本計畫吸收兩份判官報告「第四種組合」建議中最關鍵的一條：C 原本把這一步排進 Phase 2/3 的必經路徑，本計畫把它改成事後才觸發的獨立選配，避免在「戰鬥判定還在前端、伺服器權威是未來式」的現實下預先燒錢做可能沒人要的深度機制。

**若觸發，範圍包含**（不在此展開逐行程式碼，只列範圍，因為觸發時機未定，過早鑿死細節設計沒有意義）：

1. **Job Lv 真正生效**：來源＝戰鬥（非跑量，理由見 §6），效果＝每 N Job Lv 解鎖 +1 額外可配點數（沿用現有 `pointCost` 曲線，不做 RO 式固定配置表），封頂約 +10 點——**不是**為了讓 MATK 公式換血這件事單獨鋪路，而是先讓「Job Lv 有感」這件事本身成立。防弊採決策表 #10 的 A 選項（節流＋合理性檢查）。
2. **屬性 2 級表接入 `formulas.ts`**：`elementMultiplier()` 新增精英表查詢分支，含「不死」新屬性桶的負值倍率處理（需要明確定義「負傷害要不要變成幫怪加血」，建議：夾在 0，不加血）。
3. **體型修正表接入 `formulas.ts`**：新增 `sizeMultiplier()`，法術類技能（`weapon==='staff'`）不吃體型修正。
4. **MATK 平方階梯公式替換**（決策點 #3，需等①的額外配點讓 INT 能推過交叉點才有意義）。
5. **暴擊額外無視部分防禦的小幅折衷**（決策點 #4 的溫和版本，非 RO 原版無倍率）。

**前置動作**：解凍 `apps/web/src/lib/dorpg/engine/{combat.ts,formulas.ts}`（P2 契約明講凍結範圍），改完必須：① 重跑 `verify-dorpg-engine.mjs` 全部斷言（含新增）；② 重跑三等級（Lv5/27/50）×六場×三策略×≥150 種子的完整 BALANCE 模擬，且需新增一個「INT 主派＋用技能」的模擬策略（既有三輪 BALANCE/REBALANCE 報告從未覆蓋過的新面向）；③ 確認新增的「不死屬性負值倍率」不變量已妥善處理。

**成本量級**：與 C 路線原規劃的 Phase 2+3 相當（約 15-20 個檔案，Go+TS 各半），這是本計畫唯一需要全套重跑平衡、且工程風險評級「中高」以上的階段——正因如此才刻意設計成「選配、事後拍板」而非預設路徑的一部分。

---

## 4. 職業規格表

換算規則：`hp_mult`／`mp_mult` 直接取 `RO_CLASSIC_RULES.md §4.7` 的 `HpFactor`／`SpIncrease`，除以六職平均值（`HpFactor` 均值 46.67、`SpIncrease` 均值 333.33），正規化成「均值=1.0」的倍率——**借用 RO 職業間的相對比例，不借用 RO 的絕對曲線**（DOR 的 `BaseHP`/`BaseMP` 基準值走的是「跑量換算等級」的線性公式，跟 RO 逐職業逐等級查表完全是不同架構，直接搬 RO 絕對數字沒有意義）。武器種類**先取 RO §4 逐職業「可用武器」欄位當參考，再收斂進 DORPG 現有 4 種 `WeaponKind`（`sword`/`staff`/`bow`/`greatsword`，理由與收斂對照見表格下方「視覺收斂」說明，本輪審閱修訂 #2）**；近戰/遠程分支不受收斂影響，直接取自 RO §4 對應欄位。`BaseASPD`／「Job Lv 固定素質分配表」這兩項 RO 原始數值**刻意不搬**（理由見 §7）。

| 職業 | RO HpFactor→hp_mult | RO SpIncrease→mp_mult | 主武器（DORPG WeaponKind） | 近戰/遠程 | Job Lv 加點效果 | 起手技能（Phase 2） | 直接用 RO 值／因 DOR 而調整 |
|---|---:|---:|---|---|---|---|---|
| 劍士 Swordman | 70 → **1.50** | 200 → **0.60** | `sword`（單手劍） | 近戰 | 統一制度：每 N Job Lv +1 額外配點（非逐職業，見§6） | 強打 Bash（damage / neutral / coef2.0） | HP/MP比例直接借RO；技能倍率沿用RO「ATK%=100+30×Lv」精神但簡化成固定係數（現有架構無「技能等級」概念） |
| 魔法師 Mage | 30 → **0.64** | 600 → **1.80** | `staff`（杖） | 近戰（施法動畫，非移動遠程分支） | 同上 | 冰箭 Cold Bolt（damage / water / coef1.5） | HP/MP比例直接借RO（六職中HP最低、MP最高，落差原樣保留）；技能沿用RO「命中數=Lv」精神但簡化成單次固定倍率 |
| 服事 Acolyte | 40 → **0.86** | 500 → **1.50** | `staff`（RO 原為鎚/杖，收斂取杖） | 近戰 | 同上 | 治療術 Heal（heal / neutral / coef1.0） | HP/MP比例直接借RO；Heal 沿用既有 `heal` 技能 kind，公式結構（MATK×係數+固定值）與 RO「`floor((BaseLv+INT)/8)×(4+8×Lv)`」精神一致但簡化 |
| 弓箭手 Archer | 50 → **1.07** | 200 → **0.60** | `bow`（弓） | **遠程** | 同上 | 雙重射擊 Double Strafe（damage / neutral / coef1.8） | HP/MP比例直接借RO；`atk_branch=ranged` 對齊RO「DEX主STR副」精神（現有 `DefaultWeaponType` 機制本身已支援這個分支，只是從全站開關收斂成職業屬性）；惟決策點 #9 的 STR 整十階梯本計畫不分近戰/遠程、全職業統一套 STR 版本（見§1 決策表下方補充說明），弓箭手嚴格說 RO 原意主屬性是 DEX，這裡是本計畫已知的簡化 |
| 盜賊 Thief | 50 → **1.07** | 200 → **0.60** | `sword`（RO 原為匕首/弓；匕首無視覺素材，弓雖是盜賊合法武器但會與弓箭手完全重複，故取單手劍當匕首的近似） | 近戰 | 同上 | 奇襲（DOR原創，替代RO被動「二段攻擊」，damage / neutral / coef1.6，見§3 Phase2附註） | HP/MP比例直接借RO（與弓箭手完全相同）；技能**因引擎限制調整**（RO原版是被動機率追擊，需要碰combat.ts，本計畫改主動技能）；武器視覺與劍士共用（見下方「視覺收斂」） |
| 商人 Merchant | 40 → **0.86** | 300 → **0.90** | `greatsword`（RO 原為斧/匕首/鎚/單手劍；單手劍雖在合法清單內，但會與劍士/盜賊視覺重複，故取巨劍表現斧的重量感） | 近戰 | 同上 | 貪婪之心 Mammonite（damage / neutral / coef2.5） | HP/MP比例直接借RO；Mammonite 倍率沿用RO「ATK%=100+50×Lv」精神但簡化成固定係數（RO原公式另有Zeny消耗機制，DOR無經濟系統，本計畫捨棄Zeny消耗、只保留高倍率單體傷害的手感） |

**武器視覺收斂（本輪審閱新增，理由見文末修訂紀錄 #2）**：DORPG 目前只有 `sword`/`staff`/`bow`/`greatsword` 四套武器揮擊 FX（`apps/web/src/lib/dorpg/types.ts` 的 `WeaponKind` 是封閉聯集、`fxManifest.ts` 的 `FxWeapon` 與 `cdn.ts` 的 `fxAtlasUrl` 同樣只認這四種、R2 上也只有四套 FX 圖集），內容包 08 更完全沒有匕首/鈍器/斧頭揮擊素材。六職業的 `weapon_kind` 因此**一律收斂進這既有 4 種**：劍士→`sword`、魔法師→`staff`、服事→`staff`（RO 原本可選鎚或杖，取杖）、弓箭手→`bow`、盜賊→`sword`、商人→`greatsword`。⚠️ 後兩者要說清楚**不是「RO 武器在 DOR 沒有對應」**（那是本輪審閱前的錯誤說法）：盜賊的合法武器是匕首與**弓**，商人的合法武器含**單手劍**，兩者在 DORPG 都有現成視覺可用；捨棄不用是因為「盜賊用弓＝與弓箭手完全重複、商人用單手劍＝與劍士/盜賊重複」，六個職業只有四套視覺時，**辨識度**優先於字面忠實。這是刻意的取捨，不是遺漏：RO 原始武器種類與 DORPG 實際顯示的武器動畫從此不再一一對應，換來的是「零美術成本、`content.go`/`types.ts`/`fxManifest.ts`/`cdn.ts` 完全不用碰」。若日後要做真正忠實的匕首/鈍器/斧視覺，需要新增 FX 素材＋擴充上述三個前端型別檔案，這是一個獨立的美術＋前端工項，**不包含在本計畫任何 Phase 內**。

**六職業共同不做的事**（詳見 §7）：逐職業 `BaseASPD`（RO 原始資料本身只有服事一職有完整數字，其餘職業缺乏可信來源，見 `RO_CLASSIC_RULES.md §4.3`「唯一有部分BaseASPD數字的職業」）、RO 式「Job Lv 加成累計」固定素質分配表（`stat_bonus_per_50`，六職各自 STR/AGI/VIT/…固定比例）——本計畫統一用「解鎖額外可配點數，玩家自己選要加哪個素質」取代，理由是現有 `Allocate` 端點本來就是玩家自主配點的體驗，沒有理由在 Job Lv 這裡改成系統強制分配。

---

## 5. F/E 怪物落地表

從 `RO_CLASSIC_RULES.md §5.2`（F級13隻）／`§5.3`（E級36隻）挑出本輪要進 DORPG 的怪物：**F級10隻、E級10隻，共20隻**（其餘F/E怪物可在後續批次比照同一換算方法補齊到49隻，不影響本計畫任何驗收條件）。已涵蓋 **7種 DOR 屬性桶**（水/火/土/木/闇/無/不死，遠超過「至少4種」的要求）與**3種體型**（小型/中型/大型，E級的蝸牛/仙人掌/大嘴鳥涵蓋大型，因為 RO 原始資料裡「F級全資料庫沒有Large體型」）。

屬性映射規則（RO→DOR）：水→水、地→土、火→火、風/毒→木（DOR沒有風/毒桶，借用木系）、中(性)→無、**不死→不死（新增第9個DOR屬性桶，不死系是F/E級最具RO記憶點的內容，不合併進「無」）**。

`hp_mult`/`atk_mult`/`def_mult` 用 min-max 正規化法換算（F級目標區間 hp[0.08,0.35]/atk[0.15,0.45]/def[0.05,0.40]，E級目標區間 hp[0.35,0.85]/atk[0.45,0.95]/def[0.30,0.95]，確保 F<E<現有D/C/B/A 不重疊）；`speed_mult` 直接取 RO「攻速」欄位值夾在 [0.7,1.3]（F）／[0.7,1.8]（E）。**以下數字是示範性換算（供核對算法用），正式批次建議寫生成腳本讀 RO 原始資料重算，不要手刻/照抄本表當最終值。**

### F 級（10隻，建議 rank='F'，content_source='ro_classic'）

| id（草案） | 中文 | DOR屬性 | 體型 | 種族 | hp_mult | atk_mult | def_mult | speed_mult | threat |
|---|---|---|---|---|---:|---:|---:|---:|---:|
| RO-MON-F-poring | 波利 | 水 | 中型 | 植物 | 0.08 | 0.31 | 0.05 | 0.70 | 1 |
| RO-MON-F-drops | 滴滴 | 火 | 中型 | 植物 | 0.09 | 0.37 | 0.05 | 0.73 | 3 |
| RO-MON-F-pupa | 蛹 | 土 | 小型 | 昆蟲 | 0.35 | 0.15 | 0.05 | 1.00 | 2 |
| RO-MON-F-thiefbugegg | 盜賊蟲卵 | 闇 | 小型 | 昆蟲 | 0.08 | 0.45 | 0.40 | 1.30 | 5 |
| RO-MON-F-chonchon | 蒼蠅 | 木 | 小型 | 昆蟲 | 0.09 | 0.37 | 0.23 | 0.93 | 3 |
| RO-MON-F-picky | 小雞 | 火 | 小型 | 獸型 | 0.10 | 0.35 | 0.05 | 1.01 | 3 |
| RO-MON-F-rodafrog | 青蛙 | 水 | 中型 | 魚貝 | 0.14 | 0.39 | 0.05 | 0.70 | 4 |
| RO-MON-F-condor | 禿鷹 | 木 | 中型 | 獸型 | 0.11 | 0.39 | 0.05 | 0.87 | 4 |
| RO-MON-F-fabre | 綠毛蟲 | 土 | 小型 | 昆蟲 | 0.09 | 0.33 | 0.05 | 0.60 | 2 |
| RO-MON-F-lunatic | 魯納迪克 | 無 | 小型 | 獸型 | 0.09 | 0.35 | 0.05 | 0.69 | 3 |

### E 級（10隻，建議 rank='E'，content_source='ro_classic'）

| id（草案） | 中文 | DOR屬性 | element_level | 體型 | 種族 | hp_mult | atk_mult | def_mult | speed_mult | threat | 備註 |
|---|---|---|---|---|---|---:|---:|---:|---:|---:|---|
| RO-MON-E-skeleton | 骷髏 | 不死 | 1 | 中型 | 不死 | 0.38 | 0.54 | 0.52 | 0.70 | 10 | |
| RO-MON-E-hornet | 大黃蜂 | 木 | 1 | 小型 | 昆蟲 | 0.36 | 0.46 | 0.41 | 0.77 | 10 | |
| RO-MON-E-familiar | 蝙蝠 | 闇 | 1 | 小型 | 獸型 | 0.36 | 0.46 | 0.30 | 0.78 | 12 | 主動怪 |
| RO-MON-E-ambernite | 蝸牛 | 水 | 1 | **大型** | 昆蟲 | 0.44 | 0.54 | 0.95 | 0.70 | 16 | |
| RO-MON-E-muka | 仙人掌 | 土 | 1 | **大型** | 植物 | 0.47 | 0.54 | 0.41 | 0.70 | 25 | |
| RO-MON-E-pecopeco | 大嘴鳥 | 火 | 1 | **大型** | 獸型 | 0.45 | 0.60 | 0.30 | 0.70 | 20 | |
| RO-MON-E-elderwillow | 老樹精 | 火 | **2** | 中型 | 植物 | 0.49 | 0.62 | 0.52 | 0.73 | 22 | 主動施法（原設定會用火焰箭） |
| RO-MON-E-poporing | 波波利 | 木 | 1 | 中型 | 植物 | 0.41 | 0.63 | 0.30 | 0.70 | 14 | |
| RO-MON-E-mandragora | 曼陀羅 | 土 | **2** | 中型 | 植物 | 0.42 | 0.49 | 0.30 | 0.70 | 13 | 主動定點遠攻 |
| RO-MON-E-zombie | 殭屍 | 不死 | 1 | 中型 | 不死 | 0.45 | 0.66 | 0.30 | 0.70 | 14 | 主動怪 |

**已知限制（明講不隱藏）**：DEF 換算只用 RO 的物理 `DEF` 欄位，忽略 `MDEF`（DORPG 現行 `def_mult` 是單一數值，沒有硬防/軟防兩段式的空間，這是路線 C「借比例不借絕對值」的必然簡化，不是算錯）；`race` 欄位新出現「不死」中文值，是純展示自由文字欄位，不影響任何公式。

### 美術缺口怎麼辦

`08_DORPG_Content_Pack_v1` 目前**只有 5 隻怪物的美術**（`assets/monsters/DOR-MON-{A,B,C,D,E}-*`，各配 idle/attack/hit/death 4 個動作），對應現有 5 隻怪物，新增的 20 隻 F/E 級怪物**完全沒有對應美術**。三個選項與本計畫的選擇：

1. **共用既有 5 隻怪的 sprite**（按體型/屬性選最接近的一隻套用）——優點是 Phase 4 可以立即讓玩家「看到」新內容；缺點是同一張圖出現在不同名字/數值的怪物身上，容易讓玩家覺得「换皮不换料」，且素材包本身的美術設計（每隻怪對應特定角色形象）不是為了共用而設計的。
2. **先上資料、戰鬥中晚點才看得到**（Phase 3 先入庫，Phase 4 延後到美術到位才真正開放新 encounter）——本計畫**採用此選項**，理由已在 §3 Phase 4 的「風險與回退」說明：Phase 1–3 已經能獨立交付「六職業＋起手技能＋後台新怪物資料」三項可見成果，不需要為了趕美術進度而犧牲美術品質。
3. **挑選對應**（用現有5隻裡「風味最接近」的一隻代打，例如既有 DOR-MON-E-0052 已知是植物系，可以暫代波利/滴滴這類植物系F級怪）——如果使用者希望 Phase 4 不要等美術，這是比選項1更講究的折衷，可以在 §3 Phase 4 觸發前另行拍板採用。

**建議**：先用選項2，若使用者希望更快看到新關卡，再改選項3（不建議選項1，換皮痕跡太明顯）。

---

## 6. Job Lv 來源定案建議

**來源：戰鬥（`rpg_battle_logs`），不是跑步里程。**

**理由**：
1. DOR 的 Base Lv 已經是「跑量→`users.exp`→等級」這條管線在扛，若 Job Lv 也吃跑量，等於同一份里程數據被算兩次升級用途，玩家會感覺「我明明沒打過怪，Job Lv 卻自己漲」，語意混亂。
2. RO 原始設計裡 Job Lv 本來就是「打怪練功」的產物，讓 DOR 的 Job Lv 對應「玩了多少場城市探索戰鬥」，正好讓兩條進度線各自對應到「你跑了多少路」與「你玩了多少次戰鬥」，語意不重疊。
3. 三條路線（A/B/C）與兩份判官報告在這一點上**完全一致**，是本計畫唯一沒有爭議、可以直接定案的項目。

**公式**（Phase 5 觸發時才實際生效，欄位 `job_level`/`job_exp` 在 migration 175 已存在，現況只是接線到底但零效果的裝飾欄位）：

```
單場戰鬥 Job EXP 獲得 = enemies_defeated × job_exp_per_kill(預設10) × encounter.difficulty(1~5)
只在 outcome='victory' 的戰報上發放。

從 Job Lv n 升到 n+1 所需 Job EXP = 200 + 40×n（JobLv 1→50 累計 58,800）
```

以「平均每天打3場、共擊殺約15隻、平均難度2」估：15×10×2=300 Job EXP/天 → 練滿 JobLv50 約需196天（約6.5個月）——長線但不誇張的次要成長曲線，符合「疊加在跑步App上的附加玩法」定位，不是主玩法。以上兩個常數（`job_exp_per_kill`、`200`/`40`）建議做成 `rpg_config` 欄位，上線後依實際打怪頻率數據再調。

**現有玩家回填方式**：Phase 5 上線當下，所有既有角色 `job_exp=0`/`job_level=1`（或沿用現況既有值，因為現況這兩個欄位雖然存在但從未被賦予真實意義，直接從 1 級重新起算不會造成任何玩家「被倒扣」的觀感）。不需要用歷史 `rpg_battle_logs` 回溯計算——因為現況這 18 筆既有紀錄全部來自前端自行回報（無伺服器判定），用來回溯發放 Job EXP 等同追認一批未經驗證的資料，不建議。

---

## 7. 不做什麼

明確列出本輪刻意不做的 RO 機制與原因：

| RO 機制 | 為什麼不做 |
|---|---|
| **怪物絕對數值表**（HP/ATK/DEF全部設計者填死） | 與 DOR「怪物隨玩家戰力縮放」的架構前提衝突，`survey_balance.md` 已證明兩套安全數值範圍完全不重疊，無法局部移植，等同整個戰鬥系統重寫（詳見決策表#1） |
| **硬防%減傷＋VIT隨機軟防** | 玩家 `Def` 隨等級自動漲，直接套百分比等於「練等級」變成免費被動免傷，會讓已知的 Lv27+ 偏易問題惡化（決策表#2） |
| **暴擊無倍率+完全無視防禦** | 現行尺度下期望傷害加成從+9.8%降到+1.5%，幾乎無感，卻要解凍engine（決策表#4） |
| **屬性四級完整400格表** | DORPG 現有內容深度撐不起這個顆粒度，本計畫做2級簡化版且留在Phase5選配（決策表#5） |
| **負重系統** | RO 的負重上限/超重減速機制在 DOR 沒有對應的「背包/裝備」系統可以掛，商人「擴大負重」技能因此也不移植 |
| **死亡懲罰（扣經驗%）** | DORPG 現行契約明講「沒有伺服器判定也沒有獎勵發放」，連「贏了給獎勵」都還沒做完整，「輸了扣經驗」更不該搶先做 |
| **Zeny（遊戲內金錢）與商店/擺攤** | DOR 沒有對應的虛擬貨幣經濟系統，商人「殺價/抬價/擺攤」技能因此不移植，Mammonite 技能也拿掉原本的Zeny消耗機制 |
| **二轉職業與二轉技能樹** | 本輪任務單明講只做一轉六職業，二轉是完全獨立的下一個里程碑，`rpg_jobs` 專表的設計已經為二轉留好擴充空間（新增列即可）但本計畫不展開 |
| **卡片系統與種族相剋** | RO 卡片是裝備插槽機制，DOR 沒有對應的裝備系統；`race` 欄位本計畫維持純展示，不接入任何公式 |
| **完整命中率/迴避率/完美迴避機制** | 這些是 RO 傷害管線的一部分，牽動 `combat.ts`，決策上與「不做硬防%/暴擊改版」同一批，留給 Phase 5 觸發時一併評估是否要做完美迴避（`PerfectDodge` 目前是後端算出但前端未使用的既有欄位） |
| **逐職業 BaseASPD** | RO 原始資料本身只有服事一職有可信數字，其餘職業缺乏來源，本計畫不用不完整的資料杜撰其餘五職的數字 |
| **RO 式 Job Lv 固定素質分配表** | 與現有玩家自主配點的 `Allocate` 端點體驗衝突，本計畫統一用「解鎖額外配點數，玩家自己選」取代（見§4/§6） |

---

## 8. 開工檢查清單

使用者拍板後，Phase 1 第一個 PR 要動的檔案清單：

**後端（Go）**：
1. `services/api/migrations/180_rpg_jobs.sql`（新檔，正式編號需重新對照 `schema_migrations`）
2. `services/api/internal/rpg/content.go`（**不需要**改 `validWeaponKinds`——六職業武器已收斂進現有 4 種，見§4「視覺收斂」；只新增 `JobRow` 型別與 `Validate()`）
3. `services/api/internal/rpg/content_repo.go`（新增第7組CRUD：`scanJob`/`listJobs`/`getJobByID`/`upsertJob`/`deleteJob`，複製既有 6 組同構函式）
4. `services/api/internal/rpg/compute.go`（`ComputeInput` 加 `Job *JobRow`；HP/MP乘幅；近戰/遠程分支；STR整十階梯）
5. `services/api/internal/rpg/config.go`（新增 `StrTierPer`/`StrTierCoef`/`ResetStatsOnJobSelect` 欄位＋`DefaultConfig()`/`Validate()`）
6. `services/api/internal/rpg/handler.go`（`character` struct/`characterCols`/`scanCharacter` 加 `JobClass`；新增 `POST /rpg/job`；`buildCharacterView` 查職業）
7. `services/api/internal/rpg/battle.go`（`loadPlayerBattleStats`/`wirePartyMember` 補 `Job`/`Weapon`）
8. `services/api/internal/rpg/admin.go`（`AdminPreview` 加 `job` 查詢參數）
9. `services/api/internal/rpg/compute_test.go`／`config_test.go`（機械新增職業相關測試呼叫點）

**前端（TS）**：
10. `apps/web/src/lib/api.ts`（`RpgCharacter` 加 `job_class`；新增 `rpgApi.getJobs()`/`chooseJob()`）
11. `apps/web/src/components/CharacterScreen.tsx`（六選一 modal＋職業徽章）
12. `apps/web/src/app/admin/rpg/page.tsx`（新增「職業」CRUD 分頁）

**不動的檔案**：`apps/web/src/lib/dorpg/engine/{combat.ts,formulas.ts,types.ts}`、`apps/web/src/lib/dorpg/fixture.ts`、`apps/web/scripts/verify-dorpg-engine.mjs`——這五個前端 TS 檔案 Phase 1–4 全程都不需要碰（本計畫唯一會動到 TS 引擎層的是 Phase 5 選配）。

**本輪審閱訂正**：原文在這份清單裡把 `services/api/internal/rpg/scaling.go` 也列成「Phase 1–4 全程不動」，這是錯的——`scaling.go` 在 **Phase 1 這個 PR**（本清單標題所指）確實不用碰，但**修訂後的 Phase 3** 會在其 `MonsterRow` struct 加 `ContentSource`/`ElementLevel` 兩個欄位（見§3 Phase 3「後端改動」），Phase 3 開工前這份清單要記得把 `scaling.go` 加回「要動的檔案」。這正是本輪審閱標題「本計畫聲稱零改動的每一處斷言」抓到的同類錯誤之一（另兩處是 Phase 3「後端改動：無」與 Phase 2 遺漏 `buildSkillSlots`，見文末修訂紀錄）。

---

## 附：本文件產生過程與截取來源

本文件為唯讀規劃產出，未修改 `services/`／`apps/` 任何檔案、未建立實體 migration 檔、未做任何 git 操作、未對正式資料庫寫入。

**截取自路線 A（最小風險增量）**：Phase 1「零 TS 鏡像」的改動紀律與範圍界定（只動HP/MP係數/近戰遠程分支/武器視覺，不碰scaling.go/combat.ts/formulas.ts）；F/E 怪物「先入庫、不接encounter」與「接新encounter才驗證」的兩段式解耦（本計畫的 Phase 3/4 拆分，C 原本把這兩件事併在同一個 Phase）；輕量模擬抽查（12次/N場）取代全套重跑的方法論。

**截取自路線 B（忠實RO化）**：六職業 HP/SP 係數的 RO 原始數值來源（`RO_CLASSIC_RULES.md §4.7`）；六個一轉技能的具體選擇與初版倍率/CD/MP數字草稿（Bash/Cold Bolt/Double Strafe/Mammonite 的 `coefficient`/`flat`/`mp_cost`/`cooldown_ms` 數字，本計畫直接沿用）；Job Lv 來源＝戰鬥而非跑量的判斷與理由。**未採用**：怪物絕對值系統、硬防%+軟防隨機值、暴擊無倍率、屬性四級400格全表、強制重置現有玩家配點——這些是B路線的核心手術，兩份判官報告與`survey_balance.md`都判定風險/成本遠高於本計畫需要的範圍。

**截取自路線 C（RO語意+DOR縮放，勝出骨幹）**：職業走 `rpg_jobs` 正規表（而非A的CHECK列舉+JSON map）的資料模型決策；F/E怪物用RO表「比例關係」換算相對倍率的min-max正規化演算法；屬性2級簡化版（一般/精英）取代4級全表的折衷方案；「不死」新增為第9個屬性桶且不合併進「無」的判斷；Job Lv效果＝解鎖額外配點數（非RO固定分配表）的統一制度設計；§8九項待拍板逐項答案的整體方向。**調整**：C原排在Phase4的職業技能提前到Phase2；C原本併在Phase2的「F/E怪物+屬性/體型系統」拆成Phase3(入庫)/Phase4(上架)/Phase5選配(接入引擎)三段；C原本把Job Lv真正生效綁定「伺服器權威判定」為前提，本計畫改採節流防弊，不預先綁定未排期的大工程。

**截取自兩份判官報告的「第四種組合」**（本計畫最終骨幹的直接來源，而非附註）：職業技能提前（產品判官）；職業資料模型用專表而非map-in-JSON（工程判官，與C原生選擇一致，等於雙重確認）；F/E怪物「先標記不接公式」的屬性/體型延後策略、Phase 5 改為使用者事後拍板才觸發的選配（工程判官）；Job Lv 防弊採節流而非強制要求伺服器權威判定（工程判官）。

---

## 本計畫最不確定的 3 個地方

1. **F/E怪物的相對倍率換算數字是「示範性」而非「最終值」**——§5表格用min-max正規化法手算了20隻怪，但完整49隻的批次生成、以及這20隻數字本身要不要因為「新手訓練營」實際模擬結果而微調，需要在Phase3/4實作時用真正的模擬工具驗證，本計畫給的是可核對算法用的起點,不是可以直接照抄上線的定案數字。
2. **一次性免費重置六圍的觸發時機是否會製造帳號安全疑慮**——目前正式站只有owner一人的角色資料，`POST /rpg/job` 用 `WHERE job_class IS NULL` 當CAS條件理論上安全，但沒有在真正的併發測試環境驗證過這個競態保護是否在所有情境下都成立（例如同一玩家用兩個分頁同時提交）。
3. **Phase 2 用主動技能「奇襲」替代RO原版被動「二段攻擊」是否會讓盜賊玩家覺得不夠忠實**——這是本計畫為了保住「零engine改動」承諾所做的妥協，如果使用者本身對盜賊職業的期待就是RO原版的被動追擊手感，這個替代方案可能需要重新評估，屆時等同把「被動技能判定」的engine改動提前到Phase2，會打破本計畫「Phase1-4全程零engine解凍」的核心賣點。

---

## 修訂紀錄（第 1 輪審閱）

角色：FIXER。依據 CRITIC 逐條核對原始碼後的審查意見（#1–#8）修訂本文件。以下 file:line 皆指本文件 `RO_IMPLEMENTATION_PLAN.md`（修訂前的原始行號僅供對照，實際內容以本次修訂後的版本為準）；修訂前另外重新讀過對應的後端/前端原始碼與 migration 檔逐項核對，來源列在各項下方。

**#1【高】Phase 2「裝備技能」機制在程式碼裡不存在**
- 核對來源：`services/api/internal/rpg/battle.go` 的 `buildSkillSlots`（:414-453）／`getLoadout`（:403-412）、`content.go` 的 `SkillRow`（:52-90）、`content_repo.go` 的 `skillCols`/`scanSkill`/`upsertSkill`（:109-170）、migration `176_rpg_battle_content.sql` 的 `rpg_skills` DDL（:69-92）；全 repo 搜尋 `loadout`/`setLoadout`/`equipSkill` 確認玩家端沒有任何「裝備技能」UI 或端點，`apps/web/src/lib/api.ts` 的 `rpgApi` 只有 `me`/`allocate` 兩支。
- 處理方式：改寫 Phase 2「後端改動」（本文件 §3 Phase 2，約 :183-188）與「前端改動」（約 :190）——新增 `buildSkillSlots` 要加 `jobClass` 參數、改寫「預設帶入」分支篩選條件的具體 Go 片段（原文完全沒提到這一步，是讓技能真正出現在玩家技能欄的關鍵）；同步確認 `rpg_skills` 欄位草案（`job_id`/`unlock_job_level`）與既有 schema 相容（型別、既有 13 欄不受影響）；改寫驗收條件①②③與風險段落，把「能不能手動裝備」的錯誤語意改成「自動帶入」。

**#2【高】dagger/mace/axe 在前端沒有型別與美術資產**
- 核對來源：`apps/web/src/lib/dorpg/types.ts:13`（`WeaponKind`）、`fxManifest.ts:20`（`FxWeapon`）、`cdn.ts:23`（`fxAtlasUrl`）、`content.go:14`（`validWeaponKinds`），四處皆只認 `sword`/`staff`/`bow`/`greatsword`。
- 處理方式：採用建議的收斂方案（服事 mace→staff、盜賊 dagger→sword、商人 axe→greatsword，其餘不變）。已改：Phase 1 SQL 草案、Phase 2 技能 SQL 的 `weapon` 欄、§4 職業規格表武器欄與換算規則說明、新增「武器視覺收斂」取捨說明段落、Phase 1 驗收條件④、Phase 4 驗收條件③、§0 目標狀態描述、§8 checklist 第 2 項。全文重新搜尋 `dagger`/`mace`/`axe`/「7 種」確認無殘留。

**#3【高】Phase 3「後端改動：無」是錯的**
- 核對來源：`content_repo.go` 的 `monsterCols`/`scanMonster`/`upsertMonster`（:32-95）、`scaling.go` 的 `MonsterRow` struct（:101-118）——欄位是寫死清單，新增欄位不會自動出現在讀寫路徑上。
- 處理方式：Phase 3「後端改動」改成「小幅但不是無」，比照 Phase 2 手法列出四個具體改動點（`MonsterRow` struct／`monsterCols`／`scanMonster`／`upsertMonster`），同步修正「風險與回退」措辭。

**#4【高】一次性重置六圍的風險被低估，且與「不影響其他資料」矛盾**
- 核對來源：migration `175_rpg_stats.sql` 的 `player_characters`/`player_stat_log` DDL、`handler.go` 的 `Allocate`（:198-271）、`compute.go` 的 `spentBetween`/`pointCost`（:59-74）。
- 處理方式：Phase 1「後端改動」handler.go 段落給出具體交易邏輯與 `player_stat_log` 稽核列 SQL 草案（`from_value`＝重置前的值、`cost`＝負值退點，比照 `spentBetween()` 反向計算）；「風險與回退」段落明確定性重置為不可逆操作，要求前端二次確認，依是否重置分成兩條回退路徑。順帶把重置開關從「玩家自己勾選的請求參數」改成「§0 開工前一次性拍板、寫死在 `cfg.ResetStatsOnJobSelect` 的產品決策」，避免同一批玩家出現「有的重置有的沒重置」的不一致狀態——這是本輪在落實 #4 時額外發現的設計缺口，一併修掉。

**#5【中】決策點 #9（STR 整十階梯）沒有 `Job != nil` 保護，與 Phase 1 不變式矛盾**
- 核對來源：`compute.go:94-116`（`Compute()` 本體，STR 攻擊力算式在 :105-116），確認若照原文寫法插入，STR 整十階梯確實不受 `Job==nil` 保護。
- 處理方式：§1 決策表後新增「決策點 #9 的兩點補充說明」，明講這是全站生效、不分職業的獨立改動；同步改寫 Phase 1 目標與驗收條件①，把「未選職業數值逐位元不變」改成「未選職業且 STR<10 才逐位元不變」，並給出 STR≥10 帳號的回歸測試寫法建議。

**#6【中】§1 決策表第 5 列重複借用 A/B/C 字母**
- 處理方式：把「這是 B 路線的規模」改成「這是文件開頭『B 忠實 RO 化』**整條路線**的工程規模」，並加註提醒同一張表的字母在兩個層次代表不同意思。

**#7【低】STR 整十階梯未依武器分支切換主屬性**
- 核對來源：`RO_CLASSIC_RULES.md §1.2`（近戰主屬性 STR、弓系主屬性 DEX）、`compute.go:107`（現有 melee/ranged 分支判斷式）。
- 處理方式：在決策點 #9 補充說明第 2 點與§4 弓箭手列的備註欄加上取捨說明——全職業統一套 STR 版本是已知簡化，並指出日後要修正時該碰 `compute.go:107` 哪個分支。

**#8【文字】content_repo.go 現有同構函式數錯（5組→6組）**
- 核對來源：`content_repo.go` 實際依序有 monster/skill/item/scene/companion/encounter 六組 CRUD。
- 處理方式：Phase 1「後端改動」與§8 checklist 都改成「新增第 7 組」「複製既有 6 組」。

### 本輪審閱期間額外發現並一併修掉的同類問題

依指示對全文「聲稱零改動／後端無改動」的每一處斷言重新自我檢查，另外抓到 3 處同類錯誤：

1. §8「不動的檔案」清單原把 `services/api/internal/rpg/scaling.go` 跟四個前端 TS 檔案並列成「Phase 1–4 全程不動」，與修訂後的 Phase 3（會改 `scaling.go` 的 `MonsterRow`）矛盾。已拆開成「五個前端 TS 檔案 Phase 1-4 不動」+ 一段訂正說明：`scaling.go` 只在 Phase 1 這個 PR 不用碰，Phase 3 開工前要記得把它加回「要動的檔案」。
2. §1 決策表第 1 列「影響範圍」欄原寫「`rpg_monsters`／`scaling.go` 完全不動」，同樣與 Phase 3 的欄位新增矛盾。已改成區分「縮放公式本身完全不動」vs「資料表/struct 因 Phase 3 有小幅純附加異動，不影響任何算式」。
3. Phase 2 技能 SQL 草案的 `weapon` 欄位對盜賊（`surprise_strike`）/商人（`mammonite`）兩個技能仍沿用 `dagger`/`axe`，是修 #2 時沒有同步套用到 Phase 2 造成的殘留，已一併改成 `sword`/`greatsword`（與§4 表格、Phase 1 SQL 的職業武器保持一致）。

### 仍然無法確定的地方

- `buildSkillSlots` 改寫後的 Go 片段（Phase 2 後端改動第 3 點）是本輪依現有程式碼風格手寫的草案，未實際編譯或跑測試，實作時仍要以當下最新版本的 `battle.go` 為準核對變數名稱與縮排。
- Phase 1「重置六圍」稽核列寫入（決策點 #4 修法）的 6 筆 `player_stat_log` INSERT，草案假設整個流程包在單一 DB 交易內，但沒有寫出 Go 的 `tx.Begin()`/`tx.Commit()` 骨架，留給實作者比照 `handler.go` 現有 `Allocate()` 的交易寫法補齊。
- Phase 2 技能合併排序（通用技能 `sort_order` 個位數 + 職業技能 20-25）只驗證了六個新技能彼此不沖突，沒有處理「後台管理者事後把某個通用技能的 `sort_order` 也改成 20 以上」這種資料治理邊界情況——判斷風險低（後台是受信任管理者），本輪未進一步處理。

---

## 修訂紀錄（第 2 輪：複驗殘留）

第 1 輪修訂後由獨立複驗者回到原始碼重查，#1、#3（Go 端）、#4–#8 全部 PASS，另抓到三項殘留，本輪一併處理：

- **#A【中】§4 武器收斂的「理由」與 `RO_CLASSIC_RULES.md` 自己的武器清單矛盾**：盜賊的合法武器是匕首與**弓**（`RO_CLASSIC_RULES.md:553`）、商人含**單手劍**（`:574`），兩者 DORPG 都有現成視覺，原文卻寫成「DOR 無對應」。**武器對應本身不變**（sword／greatsword），改的是理由：真正的原因是辨識度（盜賊用弓會與弓箭手重複、商人用單手劍會與劍士/盜賊重複），不是沒有素材。已改 §4 表格兩格與「視覺收斂」說明段落。
- **#B【中高】Phase 3 前端改動低估**：實際要動三處（`api.ts` 的 `RpgMonster` interface、`admin/rpg/page.tsx` 的 `emptyMonster()`／`MONSTER_FIELDS`、以及從零生出篩選 UI——`SimpleContentTab` 目前只有寫死的 `sort_order` 排序）。不補第 2 項的具體後果是「後台新建的怪物 `content_source` 會存成空字串」。已在 Phase 3 前端改動段落逐條列出，並給出「把篩選 UI 併進 Phase 4」的降本選項。
- **#C【低】`element_level` 命名會誤導**：它是 DORPG 自訂的「一般/精英」二元標記，不是 RO `mob_db.yml` 的屬性等級 1~4；老樹精剛好對上、曼陀羅對不上。已在 Phase 3 的 SQL 草案加警告註解，避免 Phase 5 的實作者拿這欄直接查 RO 的四級相剋表。

（另有一項純資訊性落差：`admin.go` 的 `AdminPreview` 行號引用與實際有小幅偏移，不影響內容，未逐一訂正。）
