# RO 仙境傳說 Pre-Renewal（Classic，約 2010–2011）運算規則總整理

供 DORPG（後端 `services/api/internal/rpg`、前端 `apps/web/src/lib/dorpg`）角色數值設計參考。本文整合五份獨立研究（`stats.md`／`element.md`／`classes.md`／`monsters.md`／`progression.md`，皆已完成交叉驗證，見各自 `.verify.md`），並對照 DORPG 現行實作、列出待拍板決策點。

---

## 0. 文件說明

### 0.1 版本判定

rAthena 原始碼以 `#ifdef RENEWAL` / `RENEWAL_STAT` / `RENEWAL_EXP` / `RENEWAL_CAST` / `RENEWAL_ASPD` / `RENEWAL_LVDMG` / `RENEWAL_DROP` 等巨集區分版本，這些巨集全部包在同一個編譯期開關（`PRERE` 建置時全部不存在）。**本文只採用這些巨集的 `#else` / 未定義分支**，對應約 **2010–2011 年台版（韓版 2011 年底才推出 Renewal）** 的「經典版」規則。`db/pre-re/` 目錄下的資料表（`job_exp.yml`／`statpoint.yml`／`job_stats.yml`／`attr_fix.yml`／`size_fix.yml`／`mob_db.yml` 等）本身即為 pre-Renewal 專用表。iRO Wiki `irowiki.org/classic/...` 系列頁面定位即為 pre-Renewal 文件，用於交叉驗證。

### 0.2 可信度標示規則

| 標示 | 意義 |
|---|---|
| **已確認** | rAthena 原始碼／資料表 + iRO Wiki Classic（或兩份獨立原始碼）數字/邏輯互相印證 |
| **單一來源** | 僅一處語料佐證，但來源具權威性（多半是 rAthena 原始碼或資料表本身） |
| **存疑／衝突** | 兩來源矛盾，或本機語料查無資料；文中會並列數字並說明採用理由（衝突預設優先 rAthena pre-re 原始碼/資料表——機器可讀、且被多年正式服/私服實測） |

本文件五份底稿皆已各自經過一輪獨立 VERIFIER 逐行/逐格重新核對原始碼與資料表（詳見各自 `.verify.md`），本文在此基礎上二次整合，**未重新查證**，僅在整合時發現的矛盾另行裁決（見文末「整合裁決」附錄）。

### 0.3 來源總表

| 類別 | 檔案/頁面 | 用途 |
|---|---|---|
| rAthena 原始碼 | `status.cpp`（素質→衍生數值核心）、`battle.cpp`（傷害管線）、`skill.cpp`（詠唱/技能特例）、`pc.cpp`（加點/負重/技能點）、`mob.cpp`／`mob.hpp`（怪物 AI 旗標） | 權威、機器可讀、逐年被伺服器實測 |
| rAthena 資料表 | `db/statpoint.yml`、`job_stats.yml`、`job_basepoints.yml`、`job_exp.yml`、`skill_db.yml`、`skill_tree.yml`、`attr_fix.yml`、`size_fix.yml`、`mob_db.yml`、`refine.yml` | 權威數值來源 |
| rAthena 設定檔 | `battle.conf`、`player.conf`、`skill.conf`、`exp.conf` | 伺服器預設值（多數為官方出廠預設） |
| iRO Wiki Classic | `Stats.txt`／`DEF.txt`／`ASPD.txt`／`Size.txt`／`Race.txt`／`Elements.txt`／各職業頁／各技能頁／`Base_EXP.txt`／`Job_EXP.txt`／`Weight.txt` 等 | 文件化說明、交叉驗證、補充原始碼沒寫的「設計意圖」 |
| 補抓頁面 | `map.hpp`／`script_constants.hpp`（enum 定義）、`Two-Handed_Sword_Mastery` 頁 | 交叉驗證階段用 curl 額外取得 |

原始底稿位置：`scratchpad/ro_classic/{stats,element,classes,monsters,progression}.md`（+ 對應 `.verify.md`、`monsters_raw.csv`）。

---

## 1. 六素質與衍生數值

### 1.1 六素質效果總表

| 素質 | 每 1 點 | 每 N 點（門檻） | 可信度 |
|---|---|---|---|
| **STR** | 近戰武器 ATK 最小/最大值 +1；基礎負重 +30 | 每 5 點：遠程武器傷害+1（`floor(STR/5)`，因遠程主副屬性對調） | 已確認 |
| **AGI** | FLEE +1；ASPD 提升（`amotion -= amotion×(4AGI+DEX)/1000`） | — | 已確認 |
| **VIT** | MaxHP +1%（相乘）；軟防輸入值（`def2 += VIT`）；治療道具效果 +2% | 每 5 點：HP 自然回復 +1 | 已確認 |
| **INT** | MaxSP +1%（相乘）；MDEF 軟防貢獻 `INT+floor(VIT/2)` 的 INT 部份 | 每 6 點：SP 自然回復 +1；MATK 依 `INT+floor(INT/7)²`（下限）/`INT+floor(INT/5)²`（上限）非線性成長 | 已確認 |
| **DEX** | HIT +1；近戰武器 ATK 最小值提升（依武器等級 ×1.0~1.6）；詠唱時間 −1/150 | 每 5 點：近戰傷害額外 +1 | 已確認 |
| **LUK** | 暴擊率 +0.3%（內部 `LUK×10/3`‰）；完美迴避 +0.1% | 每 5 點：ATK+1；降低敵方對己暴擊率（己方 LUK×0.2%） | 已確認 |

**玩家沒有「攻擊屬性」，防禦屬性一律為無屬性 1 級**（`base_status->ele_lv=1` 明確賦值；`def_ele` 靠 struct 清零 + `ELE_NEUTRAL=0` 疊加而來，非明確賦值，屬單一來源＋推論）。攻擊屬性完全來自武器/技能/附魔（見第 3 節）。

### 1.2 ATK（狀態 ATK ＋ 武器 ATK）公式

```
主/副屬性判定：
  弓/樂器/鞭/左輪/步槍/火神炮/霰彈槍/榴彈槍 → 主屬性=DEX，副屬性=STR
  其餘近戰武器                              → 主屬性=STR，副屬性=DEX

batk（狀態ATK） = 主屬性 + floor(主屬性/10)² + floor(副屬性/5) + floor(LUK/5)
```
（已確認：`status.cpp:2424-2496` `status_base_atk`，與 irowiki `Stats.txt` 三條敘述拆解後完全對應）

武器擲骰（`battle_calc_base_damage`，`battle.cpp:2515-2582`）：函式本身原始碼註解寫明適用範圍為「*This applies to pre-renewal and non-sd in renewal*」（`battle.cpp:2499`），即：**pre-Renewal 版本的所有單位**＋**Renewal 版本下的「非玩家」單位（non-sd＝non-session-data，指怪物/寵物/傭兵等 NPC 戰鬥單位）**。本文（pre-RE）範圍內對玩家與怪物一律適用，先前版本稱其為「pre-RE 專用函式」是簡化說法，正確語意見上述引註。
```
atkmax = 武器基礎ATK（不含精煉）
atkmin = DEX × (80 + 武器等級×20) / 100      // Lv1~4 係數 1.0/1.2/1.4/1.6
atkmin = min(atkmin, atkmax)
（若為弓箭攻擊：atkmin = atkmin×atkmax/100；若 atkmin>atkmax 則墊高 atkmax）

非暴擊：damage = rnd(0, atkmax-atkmin-1) + atkmin     // 值域 [atkmin, atkmax-1]，擲不到 atkmax 本身
暴擊  ：damage = atkmax                                // 弓箭暴擊也不擲骰，不排除弓箭
damage += 弓箭固定加成（暴擊全額／非暴擊 rnd(0,值-1)）
damage ×= 體型修正 atkmods[武器類型][目標體型] / 100    // 只對玩家生效，MATK 不吃
damage += batk
damage += 超精煉隨機加成（扣防前）
```
已確認（`battle.cpp:2557-2582`，交叉驗證修正過方向與值域邊界）。

精煉「每級固定加成」（`refine.yml Bonus/100`）在**扣防之後**才加回傷害（即使主傷害被 DEF 打到 0 仍全額命中）；超精煉隨機加成（`RandomBonus`）則在**扣防之前**的擲骰階段就加入，會被 DEF 影響。已確認（`battle.cpp:4894-4915` vs `battle.cpp:2402-2424`）。

### 1.3 MATK

```
MATK_min = INT + floor(INT/7)²
MATK_max = INT + floor(INT/5)²
```
已確認（`status.cpp:2511-2516`），每次施放技能在 `[MATK_min, MATK_max]` 重新擲骰一次（不是先固定再乘機率）。

### 1.4 DEF / MDEF（硬防 + 軟防）

**硬防**：`damage = damage × (100-DEF)/100`，DEF 上限鎖 100（免傷 100%）。`mob_db.Defense`／`MagicDefense` 就是直接的百分比數值。已確認。

**物理軟防（vit_def）**：依「被打的一方」是玩家還是怪物，公式不同（此為交叉驗證修正過的重點，見附錄）：
```
被打的是玩家（Sd vit-eq）：
  D = def2（VIT+技能加成）
  vit_def = floor(0.3D) + rnd(0, max(0, floor(D²/150)-floor(0.3D)-1)) + floor(D/2)

被打的是怪物/寵物（Mob-Pet vit-eq）——玩家打怪走這條：
  R = floor(VIT/20)²
  vit_def = VIT + (R>0 ? rnd(0,R-1) : 0)
```
已確認（`battle.cpp:4806`/`:4825`）。低等怪 VIT 通常 <20，`floor(VIT/20)=0`，故 `vit_def≈VIT`，幾乎無隨機波動。

**魔法防禦**：
```
硬 MDEF：damage = damage × (100-MDEF)/100 - MDEF2     // magic_defense_type=0（預設）
軟 MDEF2 = INT + floor(VIT/2)
```
已確認（`status.cpp:2717-2718`；`battle.cpp:6107-6114`）。

### 1.5 HIT / FLEE / 暴擊 / 完美迴避

```
HIT  = BaseLv + DEX             （不含LUK，LUK/3是Renewal才加）
FLEE = BaseLv + AGI             （不含LUK，LUK/5是Renewal才加）
命中率 = clamp(80 + 攻擊方HIT - 防禦方FLEE, 5, 100)
是否命中 = rnd(0,99) < 命中率

完美迴避(%) = 1 + 防禦方LUK×0.1   // 判定順序在所有其他判定之前，一旦觸發直接算閃避成功
                                  // 不吃多目標懲罰，只對一般武器攻擊生效（技能/陷阱/魔法無效）

暴擊率(‰) = 10 + floor(攻擊方LUK×10/3)
  若「攻擊方非玩家 且 防禦方是玩家」：暴擊率 -= 防禦方LUK×3   // 怪打玩家的舊版相容公式
  其餘情況（含怪打怪）：             暴擊率 -= 防禦方LUK×2   // 每5點LUK扣1%
  若裝備Katar：暴擊率 ×= 2（機率翻倍，非傷害翻倍）
```
已確認。暴擊必中、無視硬防與軟防、無視 FLEE，**Pre-Renewal 暴擊沒有固定傷害倍率**（效果純粹是「直接取武器上限、無視防禦」），Renewal 才有的 ×1.4 倍在此不適用。

### 1.6 ASPD

**`aspd_base`（職業×武器基礎延遲）表**（`db/job_aspd.yml`，權威，已確認；單位與下方公式的 `amotion` 同尺度，即 `AMOTION_ZERO_ASPD=2000` 對應顯示 ASPD 0）：

| 職業 | Fist | Dagger | 1hSword | 2hSword | 1hSpear | 2hSpear | 1hAxe | 2hAxe | Mace | 2hMace | Staff | 2hStaff | Bow |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 初心者 Novice | 500 | 650 | 700 | — | — | — | 800 | — | 700 | 700 | 650 | 650 | — |
| 劍士 Swordman | 400 | 500 | 550 | 600 | 650 | 700 | 700 | 750 | 650 | 700 | — | — | — |
| 魔法師 Mage | 500 | 600 | — | — | — | — | — | — | — | — | 700 | 700 | — |
| 服事 Acolyte | 400 | — | — | — | — | — | — | — | 600 | 600 | 600 | 600 | — |
| 弓箭手 Archer | 400 | 600 | — | — | — | — | — | — | — | — | — | — | 700 |
| 盜賊 Thief | 400 | 500 | 650 | — | — | — | 800 | — | — | — | — | — | 800 |
| 商人 Merchant | 400 | 600 | 700 | — | — | — | 700 | 750 | 700 | 700 | — | — | — |

（`—`＝該職業不可裝備此武器種類，`job_aspd.yml` 未定義；數值越**大**＝出手越**慢**，這是「基礎延遲」不是「基礎ASPD」）。六職業之外的二轉/三轉/Baby 系另有各自區塊，本文僅列一轉六職＋Novice。

**公式**（逐行對照 `status_base_amotion_pc` 的 `#else`／pre-RE 分支，`status.cpp:2401-2412`）：
```
// status.cpp:2403-2405（單一武器；雙持則改用兩武器aspd_base加權，本文不展開）
amotion = aspd_base[職業][武器]

// status.cpp:2408（4×AGI+DEX 每千分比降低；C++整數除法=無條件捨去）
amotion -= floor(amotion × (4×AGI + DEX) / 1000)

// status.cpp:2411（裝備/技能的原始 ms 加成，如高級服飾卡等；預設0）
amotion += aspd_add

// status.cpp:4620、pc_maxaspd（conf/player.conf）：amotion 夾在 [下限, 上限] 之間
amotion = clamp(amotion, floor_by_class, 4000)
  floor_by_class：
    一般職業（max_aspd=190）  → 100   // (2000-190×10)×2/2，見 battle.cpp:8965 + status.cpp:4620
    三轉/延伸職業（=193）      → 70
    上限 4000 來自 MIN_ASPD(8000)/2，實務上打不到（=顯示ASPD -200，無意義）

顯示值 ASPD = 200 - amotion/10        // status.cpp:4620 + pc.cpp:10266 SP_ASPD
真實攻擊間隔 adelay(ms) = 2 × amotion  // status.cpp:4637，AMOTION_DIVIDER_PC=2
```
已確認（`status.cpp:2355-2412`／`:4609-4637`；`conf/player.conf:80-89`；`db/job_aspd.yml`）。`amotion` 與「顯示 ASPD」用的是同一個內部尺度（0–2000），**不等於真實 ms**——真實攻速間隔 `adelay` 是 `amotion` 的兩倍，兩者換算關係已在上式列出，供需要真實出手間隔（例如前端動畫節奏）時使用。

### 1.7 HP / SP 上限與自然回復

```
MaxHP = floor(BaseHP[職業][BaseLv] × (1+VIT×0.01) × TRANS)
MaxSP = floor(BaseSP[職業][BaseLv] × (1+INT×0.01) × TRANS)
TRANS = 1.25（轉生）、否則 1

HP regen（每6秒一次，坐下3秒）= floor(VIT/5) + max(1, floor(MaxHP/200))
SP regen（每8秒一次，坐下4秒）= 1 + floor(INT/6) + floor(MaxSP/100)（INT≥120 再加 floor((INT-120)/2)+4）
負重 ≥50%：停止自然回復；≥90%：無法攻擊/使用技能
```
已確認（`status.cpp:3511-3604`／`:5276-5292`；`job_basepoints.yml` 693+594 格窮舉核對 0 誤差）。

### 1.8 負重、詠唱時間

```
內部單位：MaxWeight(內部) = job_stats.yml的MaxWeight欄位(整數，如Swordman=28000) + STR×300
顯示單位：MaxWeight(顯示，即玩家看到的負重上限) = MaxWeight(內部) / 10
          → 換算後即「職業基礎值(顯示值2000起) + STR×30」
詠唱時間 = 技能基礎詠唱 × (150-DEX)/150     // DEX≥150瞬發
施法後硬直 = 技能基礎延遲 × (1-Σ道具/技能修正%)   // 預設不受DEX/AGI影響
```
已確認（`status.cpp:3663`：`sd->max_weight = job_db.get_maxWeight(...) + sd->status.str * 300`；`db/job_stats.yml`：Swordman `MaxWeight: 28000`，換算顯示值2800與本文§4.1一致）。**RO 內部把重量／負重都以「顯示值×10」儲存**（物品重量欄位同理），STR 對負重上限的貢獻因此是內部 300／顯示 30，並非兩套不同公式。

### 1.9 加點成本

```
從 BaseLv=x 升到 x+1 得到的素質點 = floor(x/5) + 3        （1~99 累計 1225，含創角48點共1273）
把某素質從 x 加到 x+1 所需點數     = floor((x-1)/10) + 2   （1→99 共需 628 點）
單一素質上限 99（一轉/二轉/初心者皆同）
```
已確認（`pc.cpp` `PC_STATUS_POINT_COST`；`statpoint.yml` 275 筆全量核對 0 誤差）。

### 1.10 範例一：劍士物理攻擊完整結算

**設定**：Base Lv 30／Job Lv 30 劍士，STR40 AGI20 VIT20 INT1 DEX30 LUK10，裝備單手劍（武器基礎 ATK 100，精煉+0，武器等級2）。目標：**大黃蜂 Hornet**（E 級，Lv8，Small，Insect，風屬性1級，DEF5/MDEF5，STR6 AGI20 VIT8 INT10 DEX17 **LUK5**，HIT25/FLEE28）。

> Hornet 的 LUK 經 `db/mob_db.yml:213`（`Id: 1004 AegisName: HORNET`）直接核對為 **5**，非舊稿誤植的 10；下表與暴擊/完美迴避數字已改用正確值重算（見文末「修訂紀錄」F8）。

步驟順序已依 `battle_calc_weapon_attack`（pre-RE 分支）實際呼叫順序排列——**屬性修正在扣防與精煉/熟練度之後才發生**（`battle.cpp:5625` 扣防 → `:5627` 精煉/熟練度 → `:5643` 屬性修正，三者依序、非文件舊稿的「屬性修正在扣防前」，見「修訂紀錄」F2）：

| 步驟 | 計算 | 結果 |
|---|---|---|
| ① batk | `40 + floor(40/10)² + floor(30/5) + floor(10/5)` = `40+16+6+2` | **64** |
| ② 武器擲骰 atkmin | `30 × (80+20×2)/100 = 30×1.2` | 36 |
| ③ 擲骰值域 | `rnd(0, 100-36-1) + 36` | [36, 99] |
| ④ 體型修正（1H劍 vs Small=75%） | `[36,99]×0.75` | [27, 74] |
| ⑤ + batk | `+64` | [91, 138] |
| ⑥ 技能倍率（普攻100%） | 不變 | [91, 138] |
| ⑦ 硬防 −5%（Defense=5） | `×0.95` | [86, 131] |
| ⑧ 軟防（Hornet VIT8→R=0→vit_def=8） | `−8` | [78, 123] |
| ⑨ 精煉加成(+0)／熟練度(無) | 不變 | [78, 123] |
| ⑩ 屬性修正（無屬性 vs 風1＝100%，**放在扣防之後才套用**） | 不變 | **最終：78~123 點／擊** |

本例最終數字與舊稿一致（78~123），因為屬性倍率恰好是 100%，重排序不影響結果；§1.11 的魔法範例則因倍率≠100% 而數字確實不同（見該節與「修訂紀錄」F2）。

**暴擊**（機率 `(10+floor(10×10/3)) - 5×2 = 43-10 = 33‰ = 3.3%`；係數用「其餘情況（含怪打怪）」那條，因為攻擊方是玩家）：無視防禦、不擲骰，`100(atkmax)×0.75(體型)+64(batk) = 139`，暴擊固定 **139 點**。

**命中率**：`HIT=BaseLv+DEX=30+30=60`；對 Hornet `FLEE=28`：`命中率=80+(60-28)=112→clamp 100%`（Hornet 另有 `1+LUK×0.1=1+5×0.1=1.5%` 完美迴避獨立判定，不受命中率影響；舊稿因 LUK 誤植為10而寫成2%）。

**ASPD**：劍士單手劍 `aspd_base=550`（`db/job_aspd.yml`，見§1.6新表）。
```
amotion = 550 - floor(550×(4×20+30)/1000) = 550 - floor(550×110/1000) = 550 - 60 = 490
490 落在該職業下限100～上限4000之間，不觸底
顯示 ASPD = 200 - 490/10 = 151
真實攻擊間隔 adelay = 2×490 = 980ms/次
```
**顯示 ASPD = 151，每次攻擊間隔約 980ms**（第 9 節原「存疑」項已因 `job_aspd.yml` 補齊而解除，見「修訂紀錄」F1）。

**HP**：劍士 Lv30 `BaseHP=511`；`MaxHP=floor(511×1.2)=613`。HP 自然回復：`floor(20/5)+max(1,floor(613/200))=4+3=7`/6秒。

### 1.11 範例二：魔法師 Cold Bolt 完整結算

**設定**（為說明計算方法另外假設的配置，非題目指定）：Base Lv 30／Job Lv 30 魔法師，STR1 AGI10 VIT10 INT80 DEX20 LUK10。施放 **Cold Bolt（冰箭）Lv5**。目標：**小雞 Picky**（F 級，Lv3，Small，Brute，**火屬性1級**，DEF0/MDEF0，STR1 AGI3 VIT3 INT5 DEX10 LUK30）。

步驟順序已依 `battle_calc_magic_attack`（pre-RE 分支）實際呼叫順序排列——**MDEF 先扣、屬性修正最後才套用**（`battle.cpp:6114` 硬MDEF%+軟MDEF2同一行 → `:6209` 最小傷害夾1 → `:6238` 屬性修正，見「修訂紀錄」F2；舊稿順序相反，數字需重算）：

| 步驟 | 計算 | 結果 |
|---|---|---|
| ① MATK 區間 | `80+floor(80/7)²=80+121=201` ／ `80+floor(80/5)²=80+256=336`；實際擲骰 `201 + rnd()%(336-201)`，**擲不到上限 336 本身**（與 §1.10 物理擲骰同一慣例，`battle.cpp:5970`） | [201, 335] |
| ② 命中段數 | Cold Bolt 命中數 = SkillLv = 5（多段合併結算，每段各自擲骰） | 5 段 |
| ③ 每段技能倍率（100%MATK） | 不變 | 每段 [201,335] |
| ④ 硬MDEF(0%)＋軟MDEF2同一行公式 `dmg×(100-MDEF)/100 - MDEF2`（Picky：`INT5+floor(VIT3/2)=5+1=6`） | `×1.00 - 6` | 每段 [195, 329] |
| ⑤ 屬性修正（水 vs 火1＝150%，見第3節Lv1表；**在MDEF之後套用**） | `floor(×1.5)` | 每段 [292, 493] |
| ⑥ 5段加總（各自獨立擲骰） | `Σ` | **總傷害約 1460~2465**（期望值≈1968） |

（`floor(195×1.5)=292`、`floor(329×1.5)=493`；若沿用舊稿「屬性修正在前、MDEF在後」的錯誤順序會得到每段[295,496]／總傷害≈1475~2480，兩者相差約1%，此處以實際原始碼順序為準。）

**詠唱與消耗**：`詠唱=700×5×(150-20)/150=3500×0.867≈3033ms`；施放後硬直 `800+200×5=1800ms`（不受DEX/AGI影響）；SP消耗 `10+2×5=20`。**命中率**：**魔法不判定命中，必中**——`battle_calc_magic_attack`（pre-RE 分支）整個函式內沒有任何 HIT/FLEE/hitrate 判斷，也不會觸發完美迴避（完美迴避只在一般武器攻擊路徑 `battle_calc_weapon_attack` 判定，見 §1.5）；魔法只會被詠唱中斷、Safety Wall／Pneuma 類技能或屬性 0 倍（immune）擋下。已確認（原始碼逐函式檢查；irowiki Flee 頁同樣只描述物理攻擊）。因此本範例不需代入命中率。

**MaxSP 參考**：魔法師 Lv30 `BaseSP=190`，`MaxSP=floor(190×1.8)=342`，Cold Bolt Lv5 每次耗20 SP。

---

## 2. 傷害結算管線

### 2.1 物理傷害（普通攻擊／近戰技能）

依 `battle_calc_weapon_attack`（pre-RE／`#else` 分支，`battle.cpp:5387-5646`）**實際函式呼叫順序**逐行整理，可直接轉譯：

```
1.  Lucky Dodge：防禦方 flee2 觸發 rnd()%1000<flee2 → 直接判定閃避，後續全跳過（連暴擊都不算）      // :5423-5427
2.  多重攻擊/連擊判定（Double Attack 等）battle_calc_multi_attack()                                  // :5433
3.  暴擊判定 is_attack_critical() → 暴擊必中，wd.type=DMG_CRITICAL                                    // :5436-5443
4.  取得 nk 旗標（技能的屬性/防禦相關特殊旗標）battle_skill_get_damage_properties()                    // :5447
5.  命中判定 is_attack_hitting()：未命中 → dmg_lv=ATK_FLEE，傷害歸零                                   // :5450
6.  若非無限防禦(植物型 infdef)：
    a. battle_calc_skill_base_damage(wd,src,target,0,0)      // :5456　先算「普攻基準」
       skill_id!=0 時再算一次 (...,skill_id,skill_lv)         // :5461　兩者皆呼叫 battle_calc_base_damage()：
                                                                武器擲骰(含體型修正)＋batk（見§1.2）
    b. 技能倍率 ATK_RATE(damage, battle_calc_attack_skill_ratio(...))                                 // :5465
    c. 技能固定加成 ATK_ADD(damage, battle_calc_skill_constant_addition(...))                          // :5468
    d. 狀態/藥水/Buff 加成 battle_attack_sc_bonus()                                                    // :5613-5614
    e. 防禦扣減 battle_calc_defense_reduction()（PA_SHIELDCHAIN/CR_SHIELDBOOMERANG 例外先扣過不重扣）    // :5621-5625
         - 硬防：damage ×= attack_ignores_def()?100:(100-DEF)  /100                                    // :4884
         - 軟防：damage -= attack_ignores_def()?0:vit_def                                              // :4886-4888
         - attack_ignores_def() 在 pre-RE 對「命中判定為暴擊」直接 return true                          // :3335-3336
           ⇒ 暴擊在這一步「硬防×100%、軟防不扣」，等同無視防禦；非暴擊才吃 (100-DEF)% 與 -vit_def
    f. 防禦後加成 battle_calc_attack_post_defense()                                                    // :5627
         - 精煉固定加成 rhw.atk2（無條件加，不受是否暴擊影響）                                            // :4903-4906
         - 最小傷害先夾 1                                                                              // :4911
         - 武器熟練度 Mastery battle_calc_attack_masteries()（同樣無條件套用）                            // :4913
         - 最小傷害再夾 1                                                                              // :4928
7.  屬性修正 battle_calc_element_damage()：查四級相剋表，floor(damage×表值/100)                          // :5643
       —— 呼叫本身**在整個 if(wd.damage) 區塊之外、無條件執行**，暴擊也不例外；
          `skill_id=0` 且來源是 mob/pet/hom（`attack_attr_none` 旗標）則整段跳過、100%直傷
8.  卡片/種族/體型/狀態百分比加成（card fix，未在此展開）
9.  左右手加權、連擊(div_)除法
10. 最終最小傷害保底（通常≥1）
```

**與本文舊版的關鍵差異**（見文末「修訂紀錄」F2）：**屬性修正（步驟7）發生在扣防與精煉/熟練度（步驟6e/6f）之後**，不是之前；暴擊只跳過步驟6e的「硬防+軟防」兩項，精煉、熟練度、屬性修正三者對暴擊一樣套用（§1.10範例因屬性倍率恰為100%，重排序不改變最終數字，但§1.11的魔法範例數字確實不同）。

### 2.2 魔法傷害

依 `battle_calc_magic_attack`（pre-RE／`#else`分支，`battle.cpp:5801-6260`）：

```
0. 無命中判定：魔法不查 HIT/FLEE、不觸發完美迴避（本函式內無 hitrate/flee 判斷；物理才有）。
1. 基礎傷害：依技能各自 case 決定；一般單體傷害技能(default分支)為
   damage = MATK_min + rnd() % (MATK_max - MATK_min)          // :5967-5972，每次施放重新擲骰一次
2. 技能倍率 MATK_RATE(skillratio)                               // :5993（skillratio 預設100，各技能可加減）
3. （卡片/道具% 加成，未在此展開）
4. MDEF：硬%與軟值**同一行公式**一次扣完，無先後之分
   damage = damage × (100-MDEF)/100 - MDEF2                    // :6112-6114（magic_defense_type=0 預設分支）
5. 最小傷害夾 1：if (damage<1) damage=1;                        // :6209
6. 屬性修正（在 MDEF 與夾1「之後」）：
   damage = battle_attr_fix(damage, 攻擊屬性, 防禦方屬性, 防禦方等級)   // :6238
7. 卡片加成 battle_calc_cardfix()（pre-RE 限定，在屬性修正之後）        // :6241
```

**與本文舊版的關鍵差異**：**MDEF 扣減在前、屬性修正在後**——舊版寫成「屬性修正→硬MDEF→軟MDEF2」，順序相反；§1.11 範例已按本節重算（總傷害從≈1982降為≈1968，差異約1%）。

負值傷害會在**加總後**被夾到 0（不會幫怪物加血），但個別疊加分量（如武器熟練度加成）在拆分呼叫時可能各自被單獨夾到 0——這是實作細節，不影響最終結果方向。

---

## 3. 屬性相剋、體型、種族

### 3.1 十種屬性

無(Neutral)／水(Water)／地(Earth)／火(Fire)／風(Wind)／毒(Poison)／聖(Holy)／暗(Dark)／念(Ghost)／不死(Undead)，各有 1~4 級（僅**防禦方**有等級，攻擊方沒有等級概念）。已確認。

**攻擊屬性決定優先序**（`battle_get_weapon_element`，已確認）：
1. 技能固定屬性（如 Bolt 系）→ 不看武器
2. 普通攻擊/`Element:Weapon`技能 → 武器附魔屬性 → 消耗箭矢時箭矢屬性覆蓋（僅右手）→ 忍者精靈符石覆蓋 → `SC_ENCHANTARMS` 覆蓋（優先權最高）
3. `Element:Endowed`技能 → 用附魔buff屬性，不看武器
4. `Element:Random`技能 → 每次隨機
5. 少數技能（`MC_CARTREVOLUTION`等）→ 傷害吃武器屬性，但相剋判定強制用無屬性

**玩家防禦屬性固定為無屬性1級**（`ele_lv=1` 明確賦值；`def_ele=ELE_NEUTRAL` 靠 struct 清零推論，單一來源）。屬性等級只出現在怪物（`mob_db.yml` 每隻都有 `Element`+`ElementLevel`，全 1004 筆分布：Lv1 395／Lv2 253／Lv3 222／Lv4 134）與部分裝備附魔效果（一律視為 Lv1）。

**普通攻擊特殊排除**：`attack_attr_none: 14`（=`BL_MOB|BL_PET|BL_HOM`）——寵物/家將/一般怪物的普通攻擊完全跳過相剋表（永遠100%），但**玩家**普攻不在此列，仍吃相剋表（`BL_PC`不在bitmask內）。這是「鬼靈骸打玩家全額傷害、玩家打鬼靈骸只有25%」現象的根源。

**不死系判定**：`SC_STONE`/`SC_FREEZE`等異常，`undead_detect_type=0`（預設）時「種族=Undead 或 屬性=Undead，任一成立即免疫」；`battle_check_undead()`本身在其他呼叫點（如治療轉傷害判定）則只看屬性。已確認。`AL_HEAL`/`ALL_RESURRECTION`/`PR_ASPERSIO`/`AB_HIGHNESSHEAL`對不死系敵對目標會轉為造成傷害（吃聖屬性相剋）。

### 3.2 屬性相剋四級完整表（`attr_fix.yml`，權威，已確認，400格程式化比對0誤差）

用法：`最終傷害 = floor(原始傷害 × 表值 / 100)`。

**Level 1**

| 攻擊\防禦 | Neutral | Water | Earth | Fire | Wind | Poison | Holy | Dark | Ghost | Undead |
|---|---|---|---|---|---|---|---|---|---|---|
| Neutral | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 25 | 100 |
| Water | 100 | 25 | 100 | 150 | 50 | 100 | 75 | 100 | 100 | 100 |
| Earth | 100 | 100 | 100 | 50 | 150 | 100 | 75 | 100 | 100 | 100 |
| Fire | 100 | 50 | 150 | 25 | 100 | 100 | 75 | 100 | 100 | 125 |
| Wind | 100 | 175 | 50 | 100 | 25 | 100 | 75 | 100 | 100 | 100 |
| Poison | 100 | 100 | 125 | 125 | 125 | 0 | 75 | 50 | 100 | -25 |
| Holy | 100 | 100 | 100 | 100 | 100 | 100 | 0 | 125 | 100 | 150 |
| Dark | 100 | 100 | 100 | 100 | 100 | 50 | 125 | 0 | 100 | -25 |
| Ghost | 25 | 100 | 100 | 100 | 100 | 100 | 75 | 75 | 125 | 100 |
| Undead | 100 | 100 | 100 | 100 | 100 | 50 | 100 | 0 | 100 | 0 |

**Level 2**

| 攻擊\防禦 | Neutral | Water | Earth | Fire | Wind | Poison | Holy | Dark | Ghost | Undead |
|---|---|---|---|---|---|---|---|---|---|---|
| Neutral | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 25 | 100 |
| Water | 100 | 0 | 100 | 175 | 25 | 100 | 50 | 75 | 100 | 100 |
| Earth | 100 | 100 | 50 | 25 | 175 | 100 | 50 | 75 | 100 | 100 |
| Fire | 100 | 25 | 175 | 0 | 100 | 100 | 50 | 75 | 100 | 150 |
| Wind | 100 | 175 | 25 | 100 | 0 | 100 | 50 | 75 | 100 | 100 |
| Poison | 100 | 75 | 125 | 125 | 125 | 0 | 50 | 25 | 75 | -50 |
| Holy | 100 | 100 | 100 | 100 | 100 | 100 | -25 | 150 | 100 | 175 |
| Dark | 100 | 100 | 100 | 100 | 100 | 25 | 150 | -25 | 100 | -50 |
| Ghost | 0 | 75 | 75 | 75 | 75 | 75 | 50 | 50 | 150 | 125 |
| Undead | 100 | 75 | 75 | 75 | 75 | 25 | 125 | 0 | 100 | 0 |

**Level 3**

| 攻擊\防禦 | Neutral | Water | Earth | Fire | Wind | Poison | Holy | Dark | Ghost | Undead |
|---|---|---|---|---|---|---|---|---|---|---|
| Neutral | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 0 | 100 |
| Water | 100 | -25 | 100 | 200 | 0 | 100 | 25 | 50 | 100 | 125 |
| Earth | 100 | 100 | 0 | 0 | 200 | 100 | 25 | 50 | 100 | 75 |
| Fire | 100 | 0 | 200 | -25 | 100 | 100 | 25 | 50 | 100 | 175 |
| Wind | 100 | 200 | 0 | 100 | -25 | 100 | 25 | 50 | 100 | 100 |
| Poison | 100 | 50 | 100 | 100 | 100 | 0 | 25 | 0 | 50 | -75 |
| Holy | 100 | 100 | 100 | 100 | 100 | 125 | -50 | 175 | 100 | 200 |
| Dark | 100 | 100 | 100 | 100 | 100 | 0 | 175 | -50 | 100 | -75 |
| Ghost | 0 | 50 | 50 | 50 | 50 | 50 | 25 | 25 | 175 | 150 |
| Undead | 100 | 50 | 50 | 50 | 50 | 0 | 150 | 0 | 100 | 0 |

**Level 4（MVP/高等怪常見）**

| 攻擊\防禦 | Neutral | Water | Earth | Fire | Wind | Poison | Holy | Dark | Ghost | Undead |
|---|---|---|---|---|---|---|---|---|---|---|
| Neutral | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 0 | 100 |
| Water | 100 | -50 | 100 | 200 | 0 | 75 | 0 | 25 | 100 | 150 |
| Earth | 100 | 100 | -25 | 0 | 200 | 75 | 0 | 25 | 100 | 50 |
| Fire | 100 | 0 | 200 | -50 | 100 | 75 | 0 | 25 | 100 | 200 |
| Wind | 100 | 200 | 0 | 100 | -50 | 75 | 0 | 25 | 100 | 100 |
| Poison | 100 | 25 | 75 | 75 | 75 | 0 | 0 | -25 | 25 | -100 |
| Holy | 100 | 75 | 75 | 75 | 75 | 125 | -100 | 200 | 100 | 200 |
| Dark | 100 | 75 | 75 | 75 | 75 | -25 | 200 | -100 | 100 | -100 |
| Ghost | 0 | 25 | 25 | 25 | 25 | 25 | 0 | 0 | 200 | 175 |
| Undead | 100 | 25 | 25 | 25 | 25 | -25 | 175 | 0 | 100 | 0 |

觀察：Pre-RE 在 Lv3/4 出現大量**負值**（Renewal 版多半改成0%，未逐格核對）；Earth 對 Earth 在 Lv1 例外維持100%（其餘三系自打自 Lv1 就已 25%），到 Lv2 才降到 50%。

### 3.3 體型修正

三種體型：Small／Medium／Large（`enum e_size`）。玩家預設 Medium；Baby系固定 Small；騎乘（非Baby）+1級（Baby騎乘也有對稱+1）。體型修正**只套用在玩家的武器基礎傷害擲骰段**，MATK 完全不吃，怪物/寵物/家將自己出手也不吃。

`size_fix.yml`（rAthena pre-re 官方資料表，全文僅 5 行資料）**只明確定義 3 格數字**：`Weapon: Knuckle {Medium:75, Large:50}`、`Weapon: Whip {Large:50}`；檔案表頭註解明講其餘所有欄位（含 Knuckle/Whip 未列出的 Small）皆為 `Default: 100`（`db/size_fix.yml:26-28`）。也就是說，**rAthena 官方對 24 種武器×3 體型共 72 格的立場是「只有那 3 格不是 100，其餘全部 100」**。但 iRO Wiki Classic `Size.txt` 記載遠比此豐富、多數武器都給出非 100 的懲罰值。element.md 判定「官方是否曾把大部分武器體型懲罰移除」**時間點無法確認**，依規則優先 rAthena；但 stats.md 明確建議 **DORPG 設計參考應採用 iRO Wiki 擴充表**（因其呈現「不同武器對付不同體型有取捨」的完整設計語言，教學/平衡意義更大）。本文整合後**採用 iRO Wiki 擴充表**作為 DORPG 設計依據，但依 F9 審閱意見重新核對每一格：

> **可信度判讀規則**：一格若等於 100，且該武器未被 `size_fix.yml` 特別列出，視為「rAthena 隱含預設值＋iRO Wiki 皆同意 100」＝雙來源一致＝**已確認**。一格若不等於 100，且不是上述 3 格明確定義之一，代表**只有 iRO Wiki 這一個來源**這樣說，且**與 rAthena 檔案自己聲明的隱含預設值 100 衝突**——下表以 `†` 標出這類格子。

| 武器 | 大型 Large | 中型 Medium | 小型 Small | 可信度 |
|---|---:|---:|---:|---|
| 拳擊/徒手 Fist | 100 | 100 | 100 | 已確認（三格皆為預設值，兩來源一致） |
| 短劍 Dagger | 50† | 75† | 100 | Large/Medium 單一來源且與預設100衝突；Small已確認 |
| 單手劍 1H Sword | 75† | 100 | 75† | Large/Small 單一來源且衝突；Medium已確認 |
| 雙手劍 2H Sword | 100 | 75† | 75† | Large已確認；Medium/Small 單一來源且衝突 |
| 槍(步行) Spear | 100 | 75† | 75† | Large已確認；Medium/Small 單一來源且衝突 |
| 槍(騎乘) Spear+Peco | 100 | 100 | 75† | Large/Medium已確認；Small 單一來源且衝突 |
| 斧 Axe | 100 | 75† | 50† | Large已確認；Medium/Small 單一來源且衝突 |
| 槌 Mace | 100 | 100 | 75† | Large/Medium已確認；Small 單一來源且衝突 |
| 杖 Rod | 100 | 100 | 100 | 已確認（三格皆為預設值） |
| 弓 Bow | 75† | 100 | 100 | Large 單一來源且衝突；Medium/Small已確認 |
| 拳刃 Katar | 75† | 100 | 75† | Large/Small 單一來源且衝突；Medium已確認 |
| 書 Book | 50† | 100 | 100 | Large 單一來源且衝突；Medium/Small已確認 |
| 拳套 Knuckle | **50** | **75** | 100 | **全部已確認**（Large/Medium為`size_fix.yml`明文定義，Small=預設值，與iRO Wiki三格皆一致） |
| 樂器 Instrument | 75† | 100 | 75† | Large/Small 單一來源且衝突；Medium已確認 |
| 鞭 Whip | **50** | 100 | 75† | Large為`size_fix.yml`明文定義＝已確認；Medium=預設值＝已確認；**Small=75 僅 iRO Wiki 一處來源，與 `size_fix.yml` 隱含預設值 100 衝突**（原稿誤標整列「已確認」，見「修訂紀錄」F9） |
| 槍械 Gun | 100 | 100 | 100 | 已確認（三格皆為預設值） |
| 飛鏢 Huuma Shuriken | 100 | 100 | 100 | 已確認（三格皆為預設值） |

rAthena 完整武器類型清單共 **24 種**（`W_FIST`~`W_2HSTAFF`，`MAX_WEAPON_TYPE=24`）。上表 51 格中僅 5 格（Knuckle 全部3格＋Whip 的 Large/Medium）是 `size_fix.yml` 真正逐格核對過的數字，其餘所有非 100 的格子（含此前誤標「已確認」的 Whip-Small）都只是 iRO Wiki 單方說法，設計時請視為「教學參考用的擴充表」而非「還原官方行為」。

### 3.4 種族（僅供卡片加成判定）

無形 Formless／不死 Undead／動物 Brute／植物 Plant／昆蟲 Insect／魚貝 Fish／惡魔 Demon／人型 Demi-Human／天使 Angel／龍 Dragon（十種，怪物專用）。玩家種族實際存的是獨立常數 `RC_PLAYER_HUMAN`（非 `RC_DEMIHUMAN`，兩者為不同數值的獨立常數，但 iRO Wiki 明講「玩家永遠算 Demi-Human 種族」——物品腳本層級是否兩者都寫仍存疑）。已確認種族清單本身；玩家種族細節單一來源+推論。

---

## 4. 六職業規範

初心者（Novice）Job Lv10 轉一轉，一轉每個一轉職業 Job Lv1~50 皆固定拿到 **18 點**職業被動素質加成（分配位置因職業而異）。一轉技能點固定 **49 點**（Job Lv1→50，1點/級；Novice 額外 9 點）。六個一轉職業共用同一張 Job EXP 表（差異只在武器與技能，不在經驗需求）。

### 4.1 劍士 Swordman

**定位**：近戰坦/爆發輸出起點；Bash 打樁、Provoke 拉怪削防；二轉分裂騎士/十字軍。

**Job Lv 加成累計**：STR+7／VIT+4／DEX+3／LUK+2／AGI+2／INT+0。

**HP/SP**：`HpFactor=70`／`SpIncrease=200`；Lv1 HP40/SP12，Lv30 HP511/SP70，Lv50 HP1179/SP110，Lv99 HP3999/SP208。`MaxWeight=2800`（顯示值）。可用武器：單手劍/槍/雙手劍。

**技能表**（10個，一轉全）：

| 技能 | MaxLv | SP | 前置 | 效果 | 可信度 |
|---|---|---|---|---|---|
| 劍術精通 Sword Mastery | 10 | 被動 | 無 | 單手劍/匕首 ATK+4×Lv | 單一來源 |
| 雙手劍術精通 2H Sword Mastery | 10 | 被動 | 劍術精通Lv1 | 雙手劍 ATK+4×Lv，無視防禦 | 已確認 |
| 增加HP自然回復 | 10 | 被動 | 無 | 站立每10秒回`5×Lv+0.2×Lv%MaxHP` | 單一來源 |
| 強打 Bash | 10 | 8~15 | 無 | ATK%=100+30×Lv | 單一來源(比例) |
| 挑釁 Provoke | 10 | 3+Lv | 無 | 對方ATK+(2+3×Lv)%／DEF−(5+5×Lv)% | 已確認 |
| 咆哮斬 Magnum Break | 10 | 30 | Bash Lv5 | 5×5火屬性範圍，ATK%=100+20×Lv | 已確認 |
| 忍耐 Endure | 10 | 10 | Provoke Lv5 | 免疫硬直，持續(7+3×Lv)秒 | 已確認 |

**慣例配點**：力量流（STR主VIT副，Bash爆發）／敏捷流（AGI/LUK高，雙手劍爆擊）。

### 4.2 魔法師 Magician

**定位**：脆皮遠程元素輸出；二轉分裂法師/賢者。

**Job Lv 加成累計**：INT+8／AGI+4／DEX+3／LUK+3。

**HP/SP**：`HpFactor=30`／`SpIncrease=600`（六職最高）；Lv1 HP40/SP16，Lv30 HP326/SP190，Lv99 HP2020/SP604。`MaxWeight=2200`（六職最低）。可用武器：杖 Rod。

**技能表**（14個）：

| 技能 | MaxLv | SP | 前置 | 效果 | 可信度 |
|---|---|---|---|---|---|
| 增加SP自然回復 | 10 | 被動 | 無 | 站立每10秒回`3×Lv`+`0.2×Lv%MaxSP` | 單一來源 |
| 念力攻擊 Napalm Beat | 10 | 9~18 | 無 | 鬼屬性，MATK%=70+10×Lv | 已確認 |
| 冰箭 Cold Bolt | 10 | 10+2Lv | 無 | 水屬性，命中數=Lv | 已確認 |
| 火焰箭 Fire Bolt | 10 | 10+2Lv | 無 | 火屬性，機制同冰箭 | 已確認 |
| 雷擊 Lightning Bolt | 10 | 10+2Lv | 無 | 風屬性，機制同冰箭 | 已確認 |
| 安全牆 Safety Wall | 10 | 30~40 | Napalm Beat7+Soul Strike5 | 擋擊次數=Lv+1 | 已確認 |
| 精神強化 Soul Strike | 10 | 18~42 | Napalm Beat4 | 鬼屬性，命中數=ceil(Lv/2) | 已確認 |
| 冰結 Frost Diver | 10 | 26−Lv | Cold Bolt5 | 水屬性，MATK%=100+10×Lv+機率凍結 | 已確認 |
| 石化 Stone Curse | 10 | 26−Lv+紅寶石 | 無 | 地屬性，無傷害石化 | 單一來源 |
| 火球 Fire Ball | 10 | 25 | Fire Bolt4 | 火屬性5×5，MATK%=70+10×Lv | 已確認 |
| 火牆 Fire Wall | 10 | 40 | Fire Ball5+Sight1 | 1×3阻擋+燒傷 | 已確認 |
| 暴風雨 Thunder Storm | 10 | 24+5Lv | Lightning Bolt4 | 風屬性5×5，命中數=Lv，80%MATK/段 | 已確認 |

**慣例配點**：純INT主、DEX副（降詠唱提命中）。

### 4.3 服事（聖職者）Acolyte

**定位**：治療/增益核心，對不死惡魔專精；二轉分裂神官/武僧。

**Job Lv 加成累計**：LUK+4／VIT+3／INT+3／DEX+3／AGI+2／STR+3。

**HP/SP**：`HpFactor=40`／`SpIncrease=500`；Lv1 HP40/SP15，Lv30 HP371/SP160，Lv99 HP2510/SP505。`MaxWeight=2400`。可用武器：鎚/杖。**唯一有部分BaseASPD數字的職業**：徒手160.1、鎚/杖140.2（單一來源，該iRO Wiki頁本身未完工）。

**技能表**（15個，重點列出）：

| 技能 | MaxLv | SP | 前置 | 效果 | 可信度 |
|---|---|---|---|---|---|
| 神之守護 Divine Protection | 10 | 被動 | 無 | 對不死/惡魔怪物額外DEF：`floor((BaseLv/25+3)×Lv+0.5)` | 已確認 |
| 冥魔剋星 Demon Bane | 10 | 被動 | DP Lv3 | 對不死/惡魔怪物額外ATK：`Lv×(BaseLv/20+3)` | 已確認 |
| 治療術 Heal | 10 | 10+3Lv | 無 | `HP=floor((BaseLv+INT)/8)×(4+8×Lv)`；對不死系轉為聖屬性攻擊 | 已確認 |
| 亞煞蘭之光 Increase AGI | 10 | 15+3Lv | Heal3 | AGI+(2+Lv)，持續(40+20Lv)秒 | 已確認 |
| 減益光環 Decrease AGI | 10 | 13+2Lv | IncAGI1 | AGI−(2+Lv) | 已確認 |
| 十字聖印 Signum Crucis | 10 | 35 | Demon Bane3 | 不死/惡魔DEF−(10+4Lv)%，成功率(23+4Lv)% | 已確認 |
| 天使的守護 Angelus | 10 | 20+3Lv | DP3 | 隊伍DEF2+`VIT/2×(5Lv)/100` | 已確認 |
| 祝福 Blessing | 10 | 24+4Lv | DP5 | STR/INT/DEX各+Lv，命中+2Lv | 已確認 |

**Heal 對不死屬性的特殊規則**：施法者是玩家＋目標為不死屬性時，`AL_HEAL`/`ALL_RESURRECTION`/`PR_ASPERSIO`/`AB_HIGHNESSHEAL` 直接轉為造成傷害（聖屬性）。已確認。

**慣例配點**：補師流（VIT/INT均衡）／武僧預備流（STR/AGI優先）。

### 4.4 弓箭手 Archer

**定位**：遠程物理輸出，DEX/AGI疊命中攻速；二轉分裂獵人/吟遊詩人。

**Job Lv 加成累計**：DEX+7／STR+3／AGI+3／LUK+2／INT+2／VIT+1。

**HP/SP**：`HpFactor=50`／`SpIncrease=200`；Lv1 HP40/SP12，Lv30 HP424/SP70，Lv99 HP3029/SP208。`MaxWeight=2600`。可用武器：弓+箭。

**技能表**（7個）：

| 技能 | MaxLv | SP | 前置 | 效果 | 可信度 |
|---|---|---|---|---|---|
| 貓頭鷹之眼 Owl's Eye | 10 | 被動 | 無 | DEX+Lv | 單一來源 |
| 鷲鷹之眼 Vulture's Eye | 10 | 被動 | Owl's Eye3 | 射程+Lv、命中+Lv | 單一來源 |
| 集中力提升 Improve Concentration | 10 | 20+5Lv | Vulture's Eye1 | ATK+5Lv、命中+10Lv、硬直防禦−5Lv% | 已確認 |
| 雙重射擊 Double Strafe | 10 | 12 | 無 | 每擊`(100+10(Lv−1))%`武器ATK×2下 | 單一來源 |
| 亂射 Arrow Shower | 10 | 15 | Double Strafe5 | 5×5，ATK%=75+5Lv，擊退2格 | 已確認 |
| 製作箭矢 Arrow Crafting | 1 | 10 | 無 | 製作箭矢 | 已確認 |
| 弓箭反擊 Arrow Repel | 1 | 15 | Job Lv35 | ATK%=150%，擊退6格 | 已確認 |

**慣例配點**：DEX主、AGI副。

### 4.5 盜賊 Thief

**定位**：高攻速/高迴避偷襲型；Double Attack機率追加、Steal偷道具、Hiding隱身；二轉分裂刺客/流氓。

**Job Lv 加成累計**：AGI+4／STR+4／DEX+4／VIT+2／LUK+3／INT+1。

**HP/SP**：與Archer完全相同（`HpFactor=50`／`SpIncrease=200`）。`MaxWeight=2400`。可用武器：匕首/弓。

**技能表**（10個）：

| 技能 | MaxLv | SP | 前置 | 效果 | 可信度 |
|---|---|---|---|---|---|
| 二段攻擊 Double Attack | 10 | 被動 | 無 | 匕首普攻`5×Lv%`機率追加等量傷害 | 已確認 |
| 迴避提升 Improve Dodge | 10 | 被動 | 無 | FLEE+3×Lv（二轉盜賊系+4×Lv） | 已確認 |
| 偷竊 Steal | 10 | 10 | 無 | 成功率%=`(己DEX−怪DEX)/2+6×Lv+4` | 已確認 |
| 隱身 Hiding | 10 | 起始10 | Steal5 | 隱身，持續30×Lv秒 | 已確認 |
| 塗毒 Envenom | 10 | 12 | 無 | 中毒率`10+4Lv%`，固定ATK`+15×Lv` | 已確認 |
| 解毒 Detoxify | 1 | 10 | Envenom3 | 治癒中毒 | 已確認 |

**慣例配點**：AGI/DEX雙高。

### 4.6 商人 Merchant

**定位**：經濟/肉盾混合，推車機動戰法；二轉分裂鐵匠/煉金術士。

**Job Lv 加成累計**：VIT+4／DEX+5／STR+5／INT+1／AGI+1／LUK+2。

**HP/SP**：`HpFactor=40`／`SpIncrease=300`；Lv1 HP40/SP13，Lv30 HP371/SP100，Lv99 HP2510/SP307。`MaxWeight=2800`（六職最高，與劍士並列）。可用武器：斧/匕首/鎚/單手劍。推車額外提供8000重量，**不**增加角色本身負重上限。

**技能表**：

| 技能 | MaxLv | SP | 前置 | 效果 | 可信度 |
|---|---|---|---|---|---|
| 擴大負重 Enlarge Weight Limit | 10 | 被動 | 無 | 負重+200×Lv | 已確認 |
| 殺價 Discount | 10 | 被動 | EWL3 | 折扣完整表7~24%（Lv10封頂24%） | 已確認 |
| 抬價 Overcharge | 10 | 被動 | Discount3 | 同曲線，售價額外+7~24% | 已確認 |
| 手推車 Pushcart | 10 | 被動 | EWL5 | 移速`(50+5Lv)%` | 單一來源 |
| 擺攤 Vending | 10 | 30 | Pushcart3 | 開個人商店 | 已確認 |
| 貪婪之心 Mammonite | 10 | 5+Zeny100Lv | 無 | ATK%=100+50×Lv | 已確認 |

**慣例配點**：戰商流（STR/VIT）／經濟流（INT/DEX）。

### 4.7 六職共同對照

| 職業 | HpFactor | SpIncrease | MaxWeight | 一轉技能數 |
|---|---:|---:|---:|---:|
| Novice | 0 | 100 | 2000 | 3 |
| Swordman | 70 | 200 | 2800 | 10 |
| Mage | 30 | 600 | 2200 | 14 |
| Acolyte | 40 | 500 | 2400 | 15 |
| Archer | 50 | 200 | 2600 | 7 |
| Thief | 50 | 200 | 2400 | 10 |
| Merchant | 40 | 300 | 2800 | 10 |

---

## 5. 怪物資料（F／E 級）

### 5.1 欄位讀法

- **`Attack`/`Attack2`＝物理傷害的最終隨機下限/上限**（怪物**不吃**STR/DEX/LUK加成——`enable_baseatk=0x9`不含`BL_MOB`，`status_base_atk()`對怪物直接return 0），未套用目標DEF/元素/體型修正。
- **`Defense`/`MagicDefense`＝直接的硬防百分比數值**（非加值，非Renewal公式），物理DEF上限鎖100，MDEF未見顯式上限。
- **HIT/FLEE 不在資料表裡**，即時算：`HIT=Lv+DEX`、`FLEE=Lv+AGI`，對PC/MOB一視同仁。
- **`AttackDelay`才是真正攻速**（`AttackMotion`只是動畫前搖，`adelay=max(adelay,amotion)`），每秒攻擊次數≈`1000/AttackDelay`。
- **`Ai`欄位**是`e_aegis_monstertype`旗標組合的別名，真正「主動/被動」判準是是否含`AGGRESSIVE(0x04)`位元（非「Ai數字本身」）。
- **怪物 MATK 用玩家同一條公式，逐字對怪物也通用**：`status_base_matk_min/max(const status_data* status)` 這個「單參數版本」在 pre-RE（`#ifndef RENEWAL`）分支中**不分 bl 型別**，`status_calc_misc()` 對所有單位（玩家/怪物/寵物/傭兵）一律呼叫同一個函式（`status.cpp:2510-2515` 定義；`:2702-2703` 呼叫，無 `bl->type` 分支）：
  ```
  MATK_min = INT + floor(INT/7)²
  MATK_max = INT + floor(INT/5)²
  ```
  即怪物表裡若有 INT 欄位（見§5.5六圍表），套此式即得該怪的 MATK 區間；Renewal 版才會依 `bl->type` 分流出怪物專用的 `int_+level+rhw.matk×70~130%`公式，pre-RE 完全不採用。**怪物技能傷害走的也是同一條 `battle_calc_magic_attack()` 管線**（見§2.2）——函式簽章吃的是通用 `block_list* src`，怪物施法時 `src` 只是換成該怪物的 block，MDEF/屬性修正/夾1的順序與玩家施法完全相同，沒有另一套怪物專屬管線。§5.3 老樹精（Elder Willow）主動施放 Fire Bolt 即依此路徑結算（見下方 F7 修正）。

### 5.2 F 級（建議 Lv 1–5，13隻）

| 中文 | English | Lv | HP | ATK | DEF/MDEF | 屬性 | 體型 | 種族 | BaseExp/JobExp | Hit/Flee | 攻速 | 主動/被動 |
|---|---|---:|---:|---|---|---|---|---|---|---|---:|---|
| 波利 | Poring | 1 | 50 | 7~10 | 0/5 | 水1 | Medium | Plant | 2/1 | 7/2 | 0.53 | 被動+撿物 |
| 滴滴 | Drops | 3 | 55 | 10~13 | 0/0 | 火1 | Medium | Plant | 4/3 | 15/6 | 0.73 | 被動+撿物 |
| 蛹 | Pupa | 2 | 427 | 1~2 | 0/20 | 地1 | Small | Insect | 2/4 | 3/3 | 1.00 | 完全靜止+看隱形 |
| 綠毛蟲 | Fabre | 2 | 63 | 8~11 | 0/0 | 地1 | Small | Insect | 3/2 | 9/4 | 0.60 | 被動,看隱形 |
| 大嘴鳥蛋 | Peco Peco Egg | 3 | 420 | 1~2 | 20/20 | 中3 | Small | Formless | 4/4 | 4/4 | 1.00 | 完全靜止 |
| 盜賊蟲卵 | Thief Bug Egg | 4 | 48 | 13~17 | 20/0 | 暗1 | Small | Insect | 8/4 | 18/10 | 1.43 | 完全靜止+看隱形 |
| 蒼蠅 | Chonchon | 4 | 67 | 10~13 | 10/0 | 風1 | Small | Insect | 5/4 | 16/14 | 0.93 | 被動,看隱形 |
| 樹精 | Willow | 4 | 95 | 9~12 | 5/15 | 地1 | Medium | Plant | 5/4 | 13/8 | 0.60 | 被動 |
| 小雞 | Picky | 3 | 80 | 9~12 | 0/0 | 火1 | Small | Brute | 4/3 | 13/6 | 1.01 | 被動 |
| 小雞(殼) | Picky(Shell) | 4 | 83 | 8~11 | 20/0 | 火1 | Small | Brute | 5/4 | 15/7 | 1.01 | 被動 |
| 魯納迪克 | Lunatic | 3 | 60 | 9~12 | 0/20 | 中3 | Small | Brute | 6/2 | 11/6 | 0.69 | 被動 |
| 青蛙 | Roda Frog | 5 | 133 | 11~14 | 0/5 | 水1 | Medium | Fish | 6/5 | 15/10 | 0.50 | 被動 |
| 禿鷹 | Condor | 5 | 92 | 11~14 | 0/0 | 風1 | Medium | Brute | 6/5 | 18/18 | 0.87 | 被動+支援 |

F級區間：HP48~427（中位80）、ATK下限1~13/上限2~17、DEF0~20（多數0）、BaseExp2~8。**F級全資料庫沒有Large體型**（最低Large怪Lv13）。無任何主動攻擊怪。

### 5.3 E 級（建議 Lv 6–22，36隻）

| 中文 | English | Lv | HP | ATK | DEF/MDEF | 屬性 | 體型 | 種族 | BaseExp/JobExp | Hit/Flee | 攻速 | 主動/被動 |
|---|---|---:|---:|---|---|---|---|---|---|---|---:|---|
| 盜賊蟲 | Thief Bug | 6 | 126 | 18~24 | 5/0 | 中3 | Small | Insect | 17/5 | 17/12 | 0.78 | 被動+支援+看隱形 |
| 小山豬 | Savage Babe | 7 | 182 | 20~25 | 0/0 | 地1 | Small | Brute | 14/12 | 19/14 | 0.62 | 被動 |
| 大黃蜂 | Hornet | 8 | 169 | 22~27 | 5/5 | 風1 | Small | Insect | 19/15 | 25/28 | 0.77 | 被動+支援+看隱形 |
| 蝙蝠 | Familiar | 8 | 155 | 20~28 | 0/0 | 暗1 | Small | Brute | 28/15 | 36/20 | 0.78 | **主動** |
| 蚱蜢 | Rocker | 9 | 198 | 24~29 | 5/10 | 地1 | Medium | Insect | 20/16 | 23/18 | 0.54 | 被動,看隱形 |
| 小沙漠狼 | Baby Desert Wolf | 9 | 164 | 30~36 | 0/0 | 火1 | Small | Brute | 20/16 | 30/18 | 0.63 | 被動+支援 |
| 浮游生物 | Plankton | 10 | 354 | 26~31 | 0/5 | 水3 | Small | Plant | 23/18 | 25/20 | 0.45 | 被動 |
| 骷髏 | Skeleton | 10 | 234 | 39~47 | 10/10 | 不死1 | Medium | Undead | 18/14 | 22/15 | 0.45 | 被動,主動施法感應 |
| 盜賊蟲(雌) | Thief Bug Female | 10 | 170 | 33~40 | 5/5 | 暗1 | Medium | Insect | 35/18 | 33/25 | 1.01 | 被動+支援+看隱形 |
| 蝦 | Kukre | 11 | 507 | 28~37 | 15/0 | 水1 | Small | Fish | 38/28 | 27/22 | 0.56 | 被動+撿物 |
| 老鼠 | Tarou | 11 | 284 | 34~45 | 0/0 | 暗1 | Small | Brute | 57/28 | 35/31 | 0.57 | 被動,主動施法感應 |
| 曼陀羅 | Mandragora | 12 | 405 | 26~35 | 0/25 | 地3 | Medium | Plant | 45/32 | 48/24 | 0.57 | **主動(定點)**射程4 |
| 蝸牛 | Ambernite | 13 | 495 | 39~46 | 30/0 | 水1 | **Large** | Insect | 57/38 | 31/26 | 0.49 | 被動,主動施法感應 |
| 海葵 | Hydra | 14 | 660 | 22~28 | 0/40 | 水2 | Small | Plant | 59/40 | 54/28 | 1.25 | **主動(定點)**射程7 |
| 尾蟲 | Wormtail | 14 | 426 | 42~51 | 5/0 | 地1 | Medium | Plant | 59/40 | 60/28 | 0.95 | 被動,主動施法感應 |
| 波波利 | Poporing | 14 | 344 | 59~72 | 0/10 | 毒1 | Medium | Plant | 81/44 | 33/28 | 0.60 | 被動+撿物 |
| 殭屍 | Zombie | 15 | 534 | 67~79 | 0/10 | 不死1 | Medium | Undead | 50/33 | 30/23 | 0.38 | **主動** |
| 蛇 | Boa | 15 | 471 | 46~55 | 0/0 | 地1 | Medium | Brute | 72/48 | 50/30 | 0.63 | 被動 |
| 蘑菇 | Spore | 16 | 510 | 24~48 | 0/5 | 水1 | Medium | Plant | 66/108 | 35/28 | 0.53 | 被動 |
| 蝴蝶 | Creamy | 16 | 595 | 53~64 | 0/30 | 風1 | Small | Insect | 105/70 | 32/56 | 0.88 | 被動,看隱形 |
| 甲蟲 | Stainer | 16 | 538 | 53~64 | 10/0 | 風1 | Small | Insect | 105/70 | 46/56 | 0.59 | 被動,主動施法感應 |
| 仙人掌 | Muka | 17 | 610 | 40~49 | 5/5 | 地1 | **Large** | Plant | 273/120 | 37/32 | 0.51 | 被動 |
| 松鼠 | Coco | 17 | 817 | 56~67 | 0/0 | 地1 | Small | Brute | 120/78 | 41/34 | 0.54 | 被動,主動施法感應 |
| 螞蟻(Andre) | Andre | 17 | 688 | 60~71 | 10/0 | 地1 | Small | Insect | 109/71 | 43/34 | 0.78 | 被動+支援+看隱形 |
| 螞蟻(Piere) | Piere | 18 | 733 | 64~75 | 15/0 | 地1 | Small | Insect | 122/78 | 45/36 | 0.78 | 被動+支援+看隱形 |
| 浣熊 | Smokie | 18 | 641 | 61~72 | 0/10 | 地1 | Small | Brute | 134/86 | 44/36 | 0.63 | 被動,主動施法感應 |
| 大嘴鳥 | Peco Peco | 19 | 531 | 50~64 | 0/0 | 火1 | **Large** | Brute | 159/72 | 46/32 | 0.64 | 被動+支援 |
| 螃蟹 | Vadon | 19 | 1017 | 74~85 | 20/0 | 水1 | Small | Fish | 135/85 | 55/38 | 0.61 | 被動,主動施法感應 |
| 螞蟻(Deniro) | Deniro | 19 | 760 | 68~79 | 15/0 | 地1 | Small | Insect | 135/85 | 62/38 | 0.78 | 被動+支援+看隱形 |
| 毒蘑菇 | Poison Spore | 19 | 665 | 89~101 | 0/0 | 毒1 | Medium | Plant | 186/93 | 43/38 | 0.60 | **主動** |
| 盜賊蟲(雄) | Thief Bug Male | 19 | 583 | 76~88 | 15/5 | 暗1 | Medium | Insect | 223/93 | 55/48 | 1.01 | **主動**+支援+看隱形 |
| 蜜蜂 | Vitata | 20 | 894 | 69~80 | 15/20 | 地1 | Small | Insect | 163/101 | 60/40 | 0.57 | 被動,主動施法感應 |
| 老樹精 | Elder Willow | 20 | 693 | 58~70 | 10/30 | 火2 | Medium | Plant | 163/101 | 58/40 | 0.73 | **主動**（會用火焰箭 MG_FIREBOLT Lv3） |
| 猴子 | Yoyo | 21 | 879 | 71~82 | 0/0 | 地1 | Small | Brute | 280/111 | 53/45 | 0.95 | 被動+支援+撿物 |
| 水母 | Marina | 21 | 2087 | 84~106 | 0/5 | 水2 | Small | Plant | 218/140 | 57/42 | 0.44 | 被動 |
| 蟬 | Metaller | 22 | 926 | 131~159 | 15/30 | 火1 | Medium | Insect | 241/152 | 71/44 | 0.59 | 被動+支援+看隱形 |

E級區間：HP126~2087（中位533，水母離群值）、ATK下限18~131/上限24~159、DEF0~30（中位2.5）、BaseExp14~280（中位約77）。體型3種齊全（Ambernite/Muka/Peco Peco為Large）。帶`AGGRESSIVE`旗標共7隻（≈19%）：蝙蝠/殭屍/毒蘑菇/盜賊蟲(雄)/老樹精（可移動追擊）+海葵/曼陀羅（定點遠攻）。

### 5.4 分布統計

| 分級 | Lv範圍 | 隻數 | HP(中位) | ATK(中位) | DEF(中位) | BaseExp(中位) | 屬性種類 | 體型種類 | 種族種類 |
|---|---|---:|---|---|---|---|---:|---:|---:|
| F | 1–5 | 13 | 48–427(80) | 1~2–13~17(9~12) | 0–20(0) | 2–8(5) | 6 | 2(無Large) | 5 |
| E | 6–22 | 36 | 126–2087(533) | 18~24–131~159(≈48~60) | 0–30(2.5) | 14–280(≈77) | 8 | 3 | 5 |

**設計建議**（源自monsters.md）：F級可分「一般小怪」（低HP低ATK）與「蛋/蛹型高血低攻」（HP400+，ATK1~2，撿物/看隱形）兩種子型態；F級缺Large是官方真實限制，建議DORPG規定「Large體型從E級開始」；E級主動怪集中Lv15+（殭屍起），Lv6–14維持被動貼近新手安全期設計。

### 5.5 怪物六圍表（STR/AGI/VIT/INT/DEX/LUK）

原§5.2/§5.3主表只列 HP/ATK/DEF/屬性/體型/種族等「已經算好」的欄位，未列六素質本身；但下列公式會直接讀六素質、且對難度曲線/相剋設計有影響，因此另表列出（`db/mob_db.yml` 逐格讀取，49隻與該檔案比對0誤差，見文末「修訂紀錄」F5自檢紀錄）：

- **VIT** → 物理軟防 `vit_def`（§1.4「Mob-Pet vit-eq」公式：`R=floor(VIT/20)²`，`vit_def=VIT+(R>0?rnd(0,R-1):0)`）。F/E級怪VIT多在1~30之間，`floor(VIT/20)`幾乎恆為0，即軟防幾乎等於VIT本身、無隨機波動；VIT≥20才開始出現隨機加成（如Andre/Piere/Smokie/Yoyo/Metaller等VIT22~36的E級怪）。
- **LUK** → 玩家對牠的暴擊率扣減（§1.5：`暴擊率‰ -= 防禦方LUK×2`，玩家對玩家或怪對怪同一係數）；牠對玩家的完美迴避判定跟牠自己的LUK無關（完美迴避看「防禦方」，怪物被攻擊時「防禦方」是怪物自己，故牠的LUK仍是決定「玩家能否被完美迴避」時的計算輸入，但此處是「玩家攻擊、怪物防禦」情境，即§1.10 Hornet範例用法）。
- **INT** → 怪物 MATK（§5.1新增說明：`INT+floor(INT/7)²`~`INT+floor(INT/5)²`，與玩家公式相同，只在會主動施法的怪物身上有意義，如老樹精INT35）。
- **DEX/AGI** → HIT/FLEE（`HIT=Lv+DEX`、`FLEE=Lv+AGI`，見§5.1）。
- **STR** → 怪物**不**用STR算物理ATK（§5.1已述`status_base_atk()`對怪物return 0，`Attack/Attack2`直接來自資料表），故下表STR欄位在pre-RE規則下對戰鬥數值無直接作用，僅供設計參考／未來若切換戰鬥模式時使用。

| 中文 | English | STR | AGI | VIT | INT | DEX | LUK |
|---|---|---:|---:|---:|---:|---:|---:|
| 波利 | Poring | 1 | 1 | 1 | 0 | 6 | 30 |
| 滴滴 | Drops | 1 | 3 | 3 | 0 | 12 | 15 |
| 蛹 | Pupa | 1 | 1 | 1 | 0 | 1 | 20 |
| 綠毛蟲 | Fabre | 1 | 2 | 4 | 0 | 7 | 5 |
| 大嘴鳥蛋 | Peco Peco Egg | 1 | 1 | 1 | 0 | 1 | 20 |
| 盜賊蟲卵 | Thief Bug Egg | 1 | 6 | 4 | 0 | 14 | 20 |
| 蒼蠅 | Chonchon | 1 | 10 | 4 | 5 | 12 | 2 |
| 樹精 | Willow | 1 | 4 | 8 | 30 | 9 | 10 |
| 小雞 | Picky | 1 | 3 | 3 | 5 | 10 | 30 |
| 小雞(殼) | Picky(Shell) | 1 | 3 | 3 | 10 | 11 | 20 |
| 魯納迪克 | Lunatic | 1 | 3 | 3 | 10 | 8 | 60 |
| 青蛙 | Roda Frog | 1 | 5 | 5 | 5 | 10 | 5 |
| 禿鷹 | Condor | 1 | 13 | 5 | 0 | 13 | 10 |
| 盜賊蟲 | Thief Bug | 1 | 6 | 6 | 0 | 11 | 0 |
| 小山豬 | Savage Babe | 1 | 7 | 14 | 5 | 12 | 35 |
| 大黃蜂 | Hornet | 6 | 20 | 8 | 10 | 17 | 5 |
| 蝙蝠 | Familiar | 1 | 12 | 8 | 5 | 28 | 0 |
| 蚱蜢 | Rocker | 1 | 9 | 18 | 10 | 14 | 15 |
| 小沙漠狼 | Baby Desert Wolf | 1 | 9 | 9 | 5 | 21 | 40 |
| 浮游生物 | Plankton | 1 | 10 | 10 | 0 | 15 | 0 |
| 骷髏 | Skeleton | 1 | 5 | 10 | 0 | 12 | 0 |
| 盜賊蟲(雌) | Thief Bug Female | 1 | 15 | 10 | 5 | 23 | 5 |
| 蝦 | Kukre | 1 | 11 | 11 | 5 | 16 | 2 |
| 老鼠 | Tarou | 1 | 20 | 11 | 10 | 24 | 5 |
| 曼陀羅 | Mandragora | 1 | 12 | 24 | 0 | 36 | 15 |
| 蝸牛 | Ambernite | 1 | 13 | 13 | 5 | 18 | 5 |
| 海葵 | Hydra | 1 | 14 | 14 | 0 | 40 | 2 |
| 尾蟲 | Wormtail | 1 | 14 | 28 | 5 | 46 | 5 |
| 波波利 | Poporing | 1 | 14 | 14 | 0 | 19 | 15 |
| 殭屍 | Zombie | 1 | 8 | 7 | 0 | 15 | 0 |
| 蛇 | Boa | 1 | 15 | 15 | 10 | 35 | 5 |
| 蘑菇 | Spore | 1 | 12 | 12 | 5 | 19 | 8 |
| 蝴蝶 | Creamy | 1 | 40 | 16 | 15 | 16 | 55 |
| 甲蟲 | Stainer | 1 | 40 | 16 | 5 | 30 | 5 |
| 仙人掌 | Muka | 15 | 15 | 30 | 5 | 20 | 10 |
| 松鼠 | Coco | 24 | 17 | 34 | 20 | 24 | 10 |
| 螞蟻(Andre) | Andre | 1 | 17 | 24 | 20 | 26 | 20 |
| 螞蟻(Piere) | Piere | 1 | 18 | 26 | 20 | 27 | 15 |
| 浣熊 | Smokie | 1 | 18 | 36 | 25 | 26 | 35 |
| 大嘴鳥 | Peco Peco | 1 | 13 | 13 | 25 | 27 | 9 |
| 螃蟹 | Vadon | 1 | 19 | 16 | 10 | 36 | 15 |
| 螞蟻(Deniro) | Deniro | 1 | 19 | 30 | 20 | 43 | 10 |
| 毒蘑菇 | Poison Spore | 1 | 19 | 25 | 0 | 24 | 0 |
| 盜賊蟲(雄) | Thief Bug Male | 1 | 29 | 16 | 5 | 36 | 0 |
| 蜜蜂 | Vitata | 1 | 20 | 25 | 65 | 40 | 70 |
| 老樹精 | Elder Willow | 1 | 20 | 25 | 35 | 38 | 30 |
| 猴子 | Yoyo | 1 | 24 | 30 | 35 | 32 | 55 |
| 水母 | Marina | 1 | 21 | 21 | 0 | 36 | 10 |
| 蟬 | Metaller | 1 | 22 | 22 | 20 | 49 | 50 |

（F級13隻＋E級36隻＝49隻，順序與§5.2/§5.3主表一致；產生與核對腳本見「修訂紀錄」F5。）

---

## 6. 成長制度

### 6.1 Base Lv 經驗表（全職業共用，不分職業）

| Lv | 升下一級所需 | 累計 | Lv | 升下一級所需 | 累計 |
|---:|---:|---:|---:|---:|---:|
| 1 | 9 | 0 | 20 | 1,620 | 8,961 |
| 5 | 77 | 86 | 25 | 2,950 | 19,175 |
| 10 | 320 | 881 | 30 | 7,995 | 40,848 |
| 15 | 830 | 3,361 | 40 | 34,212 | 199,603 |
| 50 | 115,254 | 784,124 | 70 | 1,473,746 | 8,543,332 |
| 90 | 9,738,720 | 85,546,129 | **99** | 99,999,999(哨兵,不會扣) | **405,234,427** |

Lv98→99 所需的 `99,999,998` 是**真實會被扣除**的極端練功門檻（非哨兵），只有Lv99自己的`Exp:99999999`才是真哨兵（無Lv100）。**Pre-RE沒有等級差經驗修正**（`RENEWAL_EXP`專屬機制），打同一隻怪不論角色等級固定同樣經驗。

### 6.2 一轉 Job Lv 經驗表（六職共用，1–50）

| JobLv | 升下一級 | 累計 | JobLv | 升下一級 | 累計 |
|---:|---:|---:|---:|---:|---:|
| 1 | 30 | 0 | 30 | 28,359 | 132,312 |
| 10 | 520 | 1,331 | 40 | 125,915 | 755,215 |
| 20 | 3,988 | 14,900 | 49 | 509,596 | 3,244,025 |
| — | — | — | **50** | 999,999,999(哨兵) | **3,753,621** |

Novice Job Lv 1–10：Lv9累計811，Lv10封頂（`999,999,999`哨兵），練滿Novice共1,151經驗。

### 6.3 素質點

```
每級獲得 = floor(BaseLv/5) + 3；創角另給48點；1~99累計1273點
加點成本 = floor((x-1)/10) + 2；1→99總成本628點；素質上限99
```

### 6.4 技能點

初心者9點（JobLv1→10）；一轉49點（JobLv1→50，1點/級，無JobLv40+加碼——三處獨立程式碼互相印證，iRO Wiki「40級後加碼」一句視為存疑不採信）。

### 6.5 死亡懲罰

```
預設扣「目前等級升下一級所需經驗」的1%（Base與Job都各扣1%，非只扣Base）
Novice完全免疫死亡懲罰（硬編碼排除）
死亡重生：未轉職初心者回50%MaxHP；已轉職角色（含六個一轉職業）在引擎預設值下只回1HP/1SP
（restart_hp_rate/restart_sp_rate預設皆為0，需伺服器另外調高才會回更多）
```

### 6.6 HP/SP自然回復、負重

```
HP regen(每6秒) = floor(VIT/5) + max(1, floor(MaxHP/200))
SP regen(每8秒) = 1 + floor(INT/6) + floor(MaxSP/100)（INT≥120再加floor((INT-120)/2)+4）
坐下：間隔減半（頻率加倍）
負重≥50%：取消自然回復；≥90%：無法攻擊/技能
角色實際負重上限 = 職業基礎MaxWeight + STR×300（內部單位，顯示值STR×30）
```

### 6.7 組隊經驗分享

隊伍上限12人；Even Share模式下`(可分配經驗/隊員數)+1`，僅Base Lv差距≤10級的在線隊員互相均分；多打一加成`exp_bonus_attacker=25%`/每多一人，上限`exp_bonus_max_attacker=12`（最高+275%，iRO Wiki另有580%一說，判定為特定伺服器可調值非引擎鐵律）。

---

## 7. DORPG 現況對照

| RO 規則 | DORPG 現況（欄位/預設值） | 對照結果 |
|---|---|---|
| 六素質 STR/AGI/VIT/INT/DEX/LUK | `Stats{Str,Agi,Vit,Dex,Int,Luk}`，`compute.go` 逐一對應加成 | **一致**（六素質種類/角色完全對齊） |
| 初始1點、創角48點 | `InitialStat=1`／`InitialFreePoints=40` | **近似**（初始值一致，可配點數40≠RO的48，屬有意調整） |
| 加點成本 `floor((x-1)/10)+2` | `pointCost = (n-1)/CostStepEvery + CostBase`，預設 `CostBase=2`／`CostStepEvery=10` | **一致**（公式與RO完全同構，Base/Step皆對齊RO係數） |
| 素質上限99 | `MaxStat=99` | **一致** |
| batk：`STR+floor(STR/10)²+floor(DEX/5)+floor(LUK/5)`（近戰） | `atk = STR×StrMeleeAtk + floorDiv(DEX,5) + floorDiv(LUK,3) + floorDiv(BaseLv,4)` | **缺**（無RO的`floor(STR/10)²`階梯放大項；改用等級線性項`LvAtkPer`取代，且LUK除數3≠RO的5） |
| 遠程武器主副屬性對調（DEX為主STR為副） | `DefaultWeaponType`系統設定二選一（melee/ranged），非依裝備判斷 | **近似**（有遠程/近戰分支但係數簡化、且是全域開關非逐角色判斷） |
| MATK：`INT+floor(INT/7)²`~`INT+floor(INT/5)²`非線性 | `matk = INT×1.5 + floorDiv(DEX,5) + floorDiv(LUK,3) + floorDiv(BaseLv,4)` | **缺**（RO是INT的平方階梯非線性成長，DORPG是純線性`INT×1.5`，中後期INT爆發力RO遠高於DORPG） |
| 硬防%減傷 `dmg×(100-DEF)/100`，軟防VIT隨機值另扣 | `Def = floorDiv(AGI,5)+floorDiv(VIT,2)+floorDiv(BaseLv,2)`，前端`computeDamage`用 `max(1,raw-def)`線性減法 | **衝突**（RO是乘法百分比+隨機軟防兩段式；DORPG是單純數值減法，無百分比免傷概念，機制根本不同） |
| MDEF：`INT+floor(VIT/2)`為軟防，另有硬MDEF% | `Mdef = floorDiv(VIT,5)+floorDiv(DEX,5)+INT×1` | **近似**（同樣INT/VIT混合，但係數與RO不同，且DORPG沒有硬/軟兩段） |
| HIT=`BaseLv+DEX`，FLEE=`BaseLv+AGI`（不含LUK） | `Hit = BaseLv×1 + DEX×1 + floorDiv(LUK,3)`；`Flee`同構+LUK項，且`Flee`封頂`FleeCapPct=95` | **近似**（多了LUK項且Flee有上限，RO兩者皆無LUK、FLEE無理論上限） |
| 完美迴避（LUK獨立判定，無上限） | `PerfectDodge = floorDiv(LUK,10)`，前端目前**未見到**完全迴避判定邏輯介入miss計算 | **缺**（後端算出數值但戰鬥引擎`missChance`未使用它，等同目前無效欄位） |
| 暴擊率`10+floor(LUK×10/3)`‰，敵方LUK×2或×3減免 | `CritPct = LUK×0.3`（%），`critChance = attacker.critPct + critRate×100 - defender.critShield`，`critShield = floorDiv(LUK,5)` | **近似**（結構相似：己方LUK加成-對方LUK類減免，但額外疊加全域`battle_crit_rate`基礎值8%，RO沒有這個「全員保底暴擊」概念） |
| Pre-RE暴擊無倍率、無視防禦 | `critMultiplier=2.0`固定倍傷，且暴擊傷害仍套用`elementMul`與`chargeMul`，**未見無視防禦**（`computeRawDamage`最後仍`-def`） | **衝突**（DORPG暴擊＝固定2倍傷害且仍扣防禦；RO暴擊＝不變傷害但完全無視防禦，機制方向相反） |
| ASPD：`200-amotion/10`，AGI+DEX共同降低攻速間隔 | `Aspd = AspdBase(150) + AGI×0.25 + DEX×0.1`，`aspd_cap=193`；前端`attackCooldownFor`用`(200-aspd)/(200-150)`換算冷卻 | **一致**（P3新增後，公式結構、參考值150、上限193皆刻意對齊RO） |
| 詠唱縮減 `(150-DEX)/150` | `CastReductionPct`：`DEX×1 + INT×0.5`單位數×`0.5%`每單位，封頂50% | **近似**（RO只有DEX、DORPG加了INT×0.5且封頂50%非RO的0%下限，方向類似但係數/上限均不同，且RO無封頂概念——DEX150即瞬發） |
| HP=`floor(BaseHP[職業][Lv]×(1+VIT%)×TRANS)` | `MaxHP = (BaseHP+HPPerBaseLevel×BaseLv)×(1+VIT×1%)` | **近似**（結構一致：等級項+VIT%乘幅；DORPG無職業別BaseHP表，用單一線性公式取代RO的逐職業查表） |
| MP同上（INT%） | `MaxMP`同構，`IntMPPct=1` | **近似**（同上） |
| HP/SP自然回復（VIT/5、INT/6門檻） | `HPRegen = floorDiv(VIT,5)+floorDivF(MaxHP,200)`；`MPRegen`含INT≥120特殊段 | **一致**（除數與RO完全對齊：5/200、6/4-at120/2/100） |
| 負重 `職業基礎+STR×30` | `Weight = WeightBase(2000)+STR×30` | **一致**（無職業別差異，但係數/基礎值對齊RO單一職業水準） |
| 怪物用資料表絕對值（Attack/Defense/HP直接讀表，不吃STR加成） | `battle_scale_mode="power"`：怪物數值完全由玩家戰力動態推算（`ScaleMonster`：`mobDef=playerAtk×比例`，`mobHp=hitDamage×倍數`），無怪物自身素質/絕對值系統 | **衝突**（RO怪物是設計者填好的絕對數值+屬性/種族/體型固定搭配；DORPG目前怪物只有`hp_mult/atk_mult/def_mult`乘數，數值完全跟隨玩家戰力縮放，架構完全不同——`battle_scale_mode="level"/"fixed"`是預留但未實作的分支） |
| 屬性相剋10×10×4級表（含負值、Ghost/Undead極端值） | `BattleElementChart`：怪物屬性(中文)→技能element(英文)→倍率，**無等級概念**，目前僅4種怪物屬性(金木土闇)各配1~3個技能元素 | **缺**（RO是10屬性×4防禦等級的完整矩陣；DORPG僅有稀疏的部分表、無等級分層、無負值傷害設計） |
| 攻擊方無等級，防禦方1~4級決定倍率極端程度 | 無等級欄位 | **缺**（尚無「防禦等級」概念，之後若擴充元素系統需決定要不要引入） |
| 玩家防禦屬性固定無屬性1級，攻擊屬性依武器/技能 | 玩家在`elementMultiplier`查表中不帶attribute（只有怪物`enemy.attribute`），技能帶`element` | **一致**（玩家沒有防禦屬性、只有技能/攻擊帶屬性，方向與RO相同） |
| 體型修正（武器×3體型） | 無體型/Size欄位進入傷害公式（`ScaledMonster`無size相關乘數，`MonsterRow.Size`只是展示欄位） | **缺**（DB有`size`欄位但目前完全不影響戰鬥數值） |
| 種族相剋（10種族卡片加成） | `MonsterRow.Race`只是展示欄位，不影響戰鬥計算 | **缺** |
| 五段結算：完美迴避→連擊→暴擊→命中→傷害 | `resolveAttackOrDamageSkill`：miss→immune(屬性0倍)→crit→net≤0(immune)→normal | **近似**（順序類似但語意不同：RO的完美迴避是獨立於命中率之外的「防禦方額外一次判定」，DORPG的miss已經把命中率與完美迴避效果合併成單一機率，且DORPG多了「net≤0視為immune」這個RO沒有的機制——RO的最小傷害通常保底≥1，DORPG故意不保底藉此做出「完全格擋」演出） |
| 六職業（各自HP/SP係數、武器限制、專屬技能樹） | 無職業系統，只有`DefaultWeaponType`全域二選一 | **缺**（DORPG目前是單一角色模板+可調武器分支，無職業選擇/技能樹/職業限定裝備） |
| 怪物Ai旗標（主動/被動、AGGRESSIVE等） | `BattleEnemyActMinMs/MaxMs`固定隨機行動間隔，無主動/被動之分（所有怪物同一套AI節奏） | **缺**（RO怪物有豐富的AI行為差異；DORPG怪物目前只有攻速`speed_mult`與威脅度`Threat`兩個維度） |
| 成長經驗表（Base/Job分離、非線性遞增） | DORPG的Base Lv沿用「既有DOR等級（`users.exp`換算）」，非RO式雙軌經驗制 | **缺/不適用**（DORPG的等級來源是既有跑步App機制，非RO的打怪練功制，此項屬架構性差異非數值對齊問題） |
| 死亡懲罰（扣經驗%，Novice免疫） | 無對應機制（P2尚無伺服器判定勝負後的懲罰系統，契約D5明講「沒有伺服器判定也沒有獎勵發放」） | **缺（尚未實作，非本輪範圍）** |

---

## 8. 待拍板清單

| # | 決策點 | 選項與建議 | 影響範圍 |
|---|---|---|---|
| 1 | **怪物數值：絕對值 vs 戰力縮放** | A) 保留現行`power`模式（怪物隨玩家戰力動態縮放，適合無等級落差的休閒設計）／B) 改採RO式怪物絕對數值表（HP/ATK/DEF/屬性/種族/體型都是設計者填死，可重現「同一隻怪永遠一樣強」的手感，但需要為每隻怪物設計並維護數值，且需搭配「玩家Lv vs 怪Lv」落差的難度曲線）。**建議**：維持A（DORPG無練功爬塔設計，B需要大量美術/數值人力且與契約D1精神衝突），但可從RO的F/E級怪物數值**比例關係**（如HP:ATK:DEF的相對比例、屬性分布佔比）取材，讓`hp_mult/atk_mult/def_mult`的手動輸入有參考基準。 | `scaling.go`、`rpg_monsters`資料表設計 |
| 2 | **硬防% vs 現行減算** | RO是`dmg×(100-DEF)/100`（百分比免傷，高DEF可趨近免傷但有100%上限）+VIT隨機軟防；DORPG是`max(1,raw-def)`純數值減法（DEF越高，扣的「量」固定不變，對高傷害技能而言等於變相降低DEF的相對重要性）。**建議**：若想讓「配VIT/防禦向道具」在後期依然有感，可考慮引入百分比減傷（哪怕只是`raw×(1-defPct)`再減固定值的簡化版），但需要重新平衡`BattleMobDefRatio`等現有已調校過的數值（見`BALANCE.md`），改動成本不小，屬於**中大型平衡重構**，建議列入P4再評估。 | `formulas.ts computeDamage/computeRawDamage`、`scaling.go ScaleMonster` |
| 3 | **MATK 是否要走RO式平方階梯非線性** | RO的`INT+floor(INT/7)²`讓INT在高數值時MATK爆發式成長（INT80時MATK上限比純線性`INT×1.5`高出許多）；DORPG目前純線性。**建議**：若INT流角色在現行測試中後期輸出乏力，可考慮加一項`floor(INT/N)²`的門檻加成係數（`rpg_config`已是JSON可調架構，加欄位不算破壞性變更），先用BALANCE模擬驗證是否值得。 | `compute.go Compute()`、`config.go` 新增欄位 |
| 4 | **暴擊機制方向：固定倍率+扣防 vs RO式無倍率+無視防禦** | 兩者是完全不同的設計哲學：RO暴擊的爽感來自「打穿防禦」，DORPG暴擊的爽感來自「傷害翻倍」。**建議**：不需要改成RO那一套（DORPG現行`critMultiplier=2.0`已是刻意設計且已上線兩輪修正，改動屬於推翻已驗證的平衡），但可以考慮讓暴擊**額外**無視一部分防禦（例如`def×(1-critDefIgnorePct)`）作為向RO精神致敬的微調，非必要。 | `combat.ts resolveAttackOrDamageSkill`、`config.go` |
| 5 | **屬性系統要不要做滿4級** | RO的10屬性×4防禦等級矩陣（含負值、Ghost/Undead極端值）是深度設計，DORPG目前只有稀疏、無等級的簡化表。**建議**：若元素系統要作為中期玩法擴充重點，可以參考本文§3.2的完整表格結構（尤其Ghost/Undead的25%↔0%↔-100%的等級化梯度，是RO元素系統最有記憶點的部分），但**不需要照抄10×10×4=400格全表**，可以先挑3~4種怪物屬性各自的克制/被克關係做成2級（一般/精英），成本遠低於全套移植。 | `config.go BattleElementChart`、`rpg_monsters.attribute`資料設計 |
| 6 | **體型表要不要進武器系統** | RO的體型修正（大/中/小×武器）是核心手感之一（例如「弓對大型友善、對拳套系不友善」）。DORPG目前完全沒有武器種類系統（只有`WeaponKind`四種：sword/staff/bow/greatsword，且不影響戰鬥數值，只影響特效）。**建議**：若之後要做裝備/武器選擇系統，體型修正是低成本高手感回報的機制，可以直接借用本文§3.3的擴充表（17武器×3體型），但目前`MonsterRow.Size`欄位已存在（僅供展示），是很好的起點——**優先度：中，等武器系統立項時一併考慮**。 | 需要先有「武器種類」系統作為前置 |
| 7 | **HP/SP職業係數：要不要做職業別差異** | RO六職HP係數差距達2倍以上（劍士0.7 vs 魔法師0.3），是「坦克/脆皮」定位的數值根基。DORPG目前是全角色共用同一套`BaseHP/HPPerBaseLevel`，沒有職業差異化。**建議**：若DORPG之後要做「職業/流派」選擇玩法，可比照RO的HpFactor/SpFactor概念（每個流派一組簡單的乘數係數即可，不需要對應RO的具體數字），現有`Config`架構加一層「職業係數表」不算破壞性改動。 | 需要先有「職業/流派」系統作為前置；`config.go` |
| 8 | **一轉技能：挑哪些先做** | 若DORPG要做技能系統，RO一轉六職技能中最容易移植且最具代表性的：劍士Bash（ATK%固定倍率單體）、魔法師三色Bolt（元素單體+多段合併結算）、服事Heal/Blessing（治療+屬性增益）、弓箭手Double Strafe（雙重攻擊）、盜賊Double Attack（被動機率追擊）、商人Mammonite（高倍率但耗資源）。**建議**：優先做「單體倍率技能」（Bash/Bolt系/Mammonite同構，公式最簡單：`ATK或MATK×百分比`），DORPG現有`Skill.coefficient/flat`架構已完全支援這種形式，幾乎零改動成本；被動技能（Double Attack機率觸發、Sword Mastery固定加成）次之；範圍/控場技能（Safety Wall、Frost Diver的異常狀態）成本最高，列最後。 | `rpg_skills`資料表內容設計，不需改動引擎架構 |
| 9 | **物攻/魔攻分開後，STR/INT的語意要不要更貼近RO** | RO是「STR→物理ATK主力，INT→魔法ATK主力＋MDEF」，DORPG現行`Compute()`已經是這個方向（`StrMeleeAtk`/`IntMatk`各自獨立），本質已對齊，此項**其實不算矛盾**，只是MATK非線性成長（決策點3）與STR的`floor(STR/10)²`階梯加成（RO獨有，DORPG完全沒有此類「整十階梯」設計）兩處細節可以再考慮要不要引入，增加「配到整十的滿足感」。 | `compute.go`，屬於決策點3的延伸 |

---

## 附錄：整合時發現的矛盾與裁決

整合五份文件時，發現以下需要裁決的地方（各自底稿內部已無矛盾，矛盾出現在**跨文件的設計建議層級**，非事實層級）：

1. **體型修正表該用哪個版本**：`element.md`（研究範圍：屬性/體型/種族）的結論是「衝突時優先rAthena `size_fix.yml`」，該表官方只有Knuckle/Whip兩個例外；但`stats.md`（研究範圍：六素質/衍生數值）在§13直接把iRO Wiki的17武器擴充表**當作正文內容**列出，並言明「本文採用iRO Wiki表作為DORPG設計參考基準」。兩份文件對「哪個是rAthena的官方事實」沒有矛盾（都同意`size_fix.yml`本身只有兩個例外），但對「DORPG應該參考哪一個」給出不同建議。**裁決**：本文第3.3節採用stats.md的立場（iRO Wiki擴充表），因為本文件的目的是「供設計參考」而非「重現rAthena確切行為」，擴充表的設計語言更完整，並在表格中逐格標註可信度（第1輪審閱F9修正後：`size_fix.yml`實際只明文定義Knuckle全部3格＋Whip的Large 1格，其餘所有非100數值——包含原稿誤標「已確認」的Whip-Small=75——都只是iRO Wiki單一來源，且與該檔案自己聲明的隱含預設值100衝突，詳見§3.3表格與文末「修訂紀錄」F9）。
2. **暴擊率減免係數的怪物VIT/LUK數字量級**：`stats.md`與`monsters.md`都各自獨立核對了各自範圍內的公式（前者是暴擊公式本身，後者是怪物軟防公式），兩者沒有直接的數字矛盾，但整合時發現用`monsters_raw.csv`實際怪物LUK代入`stats.md`的暴擊公式，會出現「E級怪蟬(Metaller) LUK=50」這種遠高於同級距其他怪物（多數個位數~20左右）的離群值，導致「玩家對蟬暴擊率被壓到接近0」的极端結果。這不是文件錯誤（兩邊公式都已交叉驗證），而是官方資料本身的怪物設計差異，本文在§1.10選用Hornet（`mob_db.yml`實際LUK5，第1輪審閱F8修正前的原稿曾誤植為LUK10，較具代表性）而非蟬做為範例，避免範例本身失真。
3. **一轉技能點「JobLv40+是否加碼」**：`classes.md`與`progression.md`都獨立處理過這個問題（前者在§9存疑清單、後者在未確認清單第1項），兩者**結論一致**（三處rAthena程式碼互相印證「固定49點無加碼」，iRO Wiki單一頁面的「加碼」說法不採信），沒有需要裁決的矛盾，此處僅說明兩份文件在同一問題上各自獨立得出一致結論，可視為交叉驗證的加強證據。
4. **魔法是否判定命中率（FLEE）**：五份研究底稿都沒處理；編排者在第 1 輪審閱後直接對 `battle_calc_magic_attack`（pre-RE）逐函式檢查，確認函式內完全沒有 HIT/FLEE/hitrate 判斷 → **魔法必中**，已寫進 §1.11 與 §2.2 步驟 0（已確認）。

---

## 修訂紀錄（第 1 輪審閱）

COMPLETENESS CRITIC 提出 F1–F9 共 9 項，逐條處理如下。數值類項目（F1/F5/F7/F8）一律用 Bash+Python 直接讀語料產生後貼入，腳本存於 `scratchpad/ro_classic/fixer/`（`gen_aspd.py`／`gen_stats_table.py`／`gen_example1.py`／`gen_example2.py`／`verify_mobdb.py`）。

- **F1（ASPD 表與範例，已修，§1.6／§1.10）**：新語料 `db/job_aspd.yml` 提供七職業(Novice/Swordman/Mage/Acolyte/Archer/Thief/Merchant)×各武器BaseASPD，已解析為完整表格貼入§1.6。公式逐行對照 `status_base_amotion_pc` pre-RE 分支（`src/status.cpp:2401-2412`）重寫，含 dual-wield 但本文範例未用到；下限來源追到 `conf/player.conf:80-89`（`max_aspd=190`／`max_third_aspd=193`）與換算式 `battle.cpp:8965`＋`status.cpp:4620`，算出一般職業 amotion 下限=100、三轉/延伸=70。§1.10 劍士範例（AGI20/DEX30，1H Sword，`aspd_base=550`）代入：`amotion=550-floor(550×110/1000)=550-60=490`，未觸底，**顯示 ASPD=200-490/10=151**，真實攻擊間隔 `adelay=2×490=980ms`。第9節原「存疑」標記已解除。
- **F2（傷害管線改虛擬碼，已修，§2.1／§2.2／§1.10／§1.11）**：直接讀 `battle.cpp` 的 `battle_calc_weapon_attack`（`:5387-5646`）與 `battle_calc_magic_attack`（`:5801-6260`）逐行重寫成可轉譯虛擬碼，並發現一個先前被誤述的順序：**pre-RE 屬性修正在扣防（含精煉/熟練度）之後才套用**，不是之前——物理管線 `battle_calc_defense_reduction`(`:5625`)＋`battle_calc_attack_post_defense`(`:5627`) 先執行，`battle_calc_element_damage`(`:5643`) 在整個 `if` 區塊之外無條件最後呼叫；魔法管線同理，MDEF（硬%+軟值同一行，`:6112-6114`）與最小傷害夾1（`:6209`）在前，`battle_attr_fix`(`:6238`) 在後。另確認暴擊只跳過「硬防+軟防」兩項（`attack_ignores_def()` 對 pre-RE 暴擊直接return true，`:3335-3336`），精煉/熟練度/屬性修正對暴擊照樣套用。§1.10（Hornet，屬性倍率100%）數字因此不變，但§1.11（Cold Bolt，屬性倍率150%）重算後從「每段[295,498]／總傷害≈1475~2490（期望≈1982）」修正為「每段[292,495]／總傷害≈1460~2475（期望≈1968）」，差異約1%，已在文中列出新舊對照。
- **F3（負重內部/顯示單位，已修，§1.8）**：`status.cpp:3663` 確認內部負重 `= job_stats.yml的MaxWeight欄位 + STR×300`，顯示值除以10；`db/job_stats.yml` Swordman `MaxWeight:28000` 換算顯示2800，與§4.1既有數字一致。§1.8已加註內部/顯示雙欄公式與該處引註。
- **F4（"pre-RE專用函式"措辭，已修，§1.2）**：`battle_calc_base_damage` 函式頭原始碼註解原文為「*This applies to pre-renewal and non-sd in renewal*」（`battle.cpp:2499`），已改寫為「pre-Renewal版本＋Renewal版本下的非玩家(non-sd)單位」，不再用簡化說法。
- **F5（怪物六圍表，已修，新增§5.5）**：`gen_stats_table.py` 從 `monsters_raw.csv` 按§5.2/§5.3現有列序（AegisName主鍵join，含Level消歧義斷言）產生49隻×6素質表，貼入新增的§5.5，並附「哪些公式用到哪個素質」的說明（VIT→軟防、LUK→暴擊防禦、INT→怪物MATK、DEX/AGI→HIT/FLEE、STR→pre-RE怪物物理ATK不吃此值）。自檢：`verify_mobdb.py` 直接重新解析 `mob_db.yml`（不透過csv中介），49隻×6欄＝294格逐格比對，**0 mismatch**。
- **F6（怪物MATK公式，已修，§5.1）**：確認 `status_base_matk_min/max(const status_data*)` 單參數版本在pre-RE（`#ifndef RENEWAL`）分支對所有`bl`型別通用、無`BL_PC`分支（`status.cpp:2510-2515`定義，`:2702-2703`無條件呼叫），與玩家公式完全相同（`INT+floor(INT/7)²`~`INT+floor(INT/5)²`）；並說明怪物技能傷害同樣走`battle_calc_magic_attack()`同一條管線（`src`换成怪物block即可），沒有另一套怪物專屬管線。已於§5.1新增條目。
- **F7（老樹精技能名，已修，§5.3）**：`db/mob_skill_db.txt:142-143`（`1033,Elder Willow@MG_FIREBOLT,attack/chase,19,3,...`）確認為技能ID 19＝`MG_FIREBOLT`（`db/skill_db.yml:976-977` `Id:19 Name:MG_FIREBOLT Description:Fire Bolt`），MaxLv用到Lv3，與老樹精本身「火屬性2級」一致（原稿誤寫「冰咆哮」）。已核對§5.2/§5.3全表，僅此一列有技能名稱標註，無其他列需要同步修正。
- **F8（Hornet LUK訂正，已修，§1.10）**：`db/mob_db.yml:213`（`Id:1004 AegisName:HORNET`）直接讀出`Luk:5`，非原稿的10。已更正設定敘述，並用`gen_example1.py`重算全部下游數字：暴擊率 `43-5×2=33‰=3.3%`（原稿23‰/2.3%）、完美迴避 `1+5×0.1=1.5%`（原稿2%）；batk/擲骰/體型/硬防/軟防/HIT-FLEE/暴擊傷害/ASPD/HP等其餘數字皆與LUK無關，逐行程式重算後與原稿一致（[78,123]、暴擊139、命中100%、HP613/回復7）。
- **F9（Whip體型表可信度，已修，§3.3）**：直接讀`db/size_fix.yml`確認全檔僅5行資料、只明文定義3格（Knuckle Medium75/Large50、Whip Large50），檔頭註解明講其餘一律預設100（`size_fix.yml:26-28`）。據此重新稽核整張17武器×3體型表：**Whip的Small=75原稿誤標「已確認」，實為單一來源(iRO Wiki)且與rAthena隱含預設值100衝突**，已改標並加註；同時系統性稽核其餘16列，凡「非100且不在size_fix.yml明文3格內」的儲存格一律加`†`並改標「單一來源／與預設100衝突」（Dagger/1H Sword/2H Sword/Spear系/Axe/Mace/Bow/Katar/Book/Instrument共10種武器、合計逾20格受影響），凡「＝100或屬明文3格」則維持/改標「已確認」（Fist/Rod/Gun/Huuma Shuriken全為預設值100，Knuckle三格皆明文定義）。表格前新增可信度判讀規則說明。

**無法處理的項目**：無。F1–F9 全數在本輪語料內可完整核對並修正，沒有需要另外請示或標記「待補語料」的殘留項。

---

**產出檔案**：`docs/dorpg/RO_CLASSIC_RULES.md`（本檔）。原始五份研究底稿與交叉驗證報告位於 `scratchpad/ro_classic/`，供追溯逐條依據（行號/檔名）之用；第1輪審閱的產生/自檢腳本位於 `scratchpad/ro_classic/fixer/`。
