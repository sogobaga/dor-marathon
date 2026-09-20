# DORPG P11 契約：怪物強度九級（F～特S）＋強度挑戰對戰列表（每級 1／3／5 隻）＋高階怪召喚 — 2026-09-20（校準後定稿 v2）

單一真相。P5–P10 契約仍有效，本檔只加不改。校準依據：`scratchpad/dorpg_p11/RANK_TABLE.md`（真引擎、同級參考玩家、三職業中位、30–60 種子）。

## 0. 使用者需求（原話濃縮）
怪物等級＝玩家等級的 1 對 1 定義：F 裸裝無技能無相剋輕鬆勝；E 靠技能輕鬆勝；D 技能＋裝備輕鬆勝；C 靠屬性相剋輕鬆勝；B 要有技巧才能勝；A 組隊 3 人才輕鬆勝；特A 組隊 5 人；S 裝備技能相剋技巧道具＋滿隊，努力 5–10 分鐘才可能勝；特S 同上 20–30 分鐘。特A／S／特S 會召喚 A～F 級怪物（未來可設定叫哪些）。對戰列表新增每種強度的組合（1 隻），另有 3 隻、5 隻的組合。

## 1. 決策（編排者定案）
- **強度＝資料表 `rpg_monster_ranks`**（九列，倍率相對「同級參考玩家 Ref(N)」為絕對值；後台可調）。**只有 `scaling_mode='rank'` 的遭遇**用它：`hp = floor(Ref(N).hp × rank.hp_mult × monster.hp_mult × power_scale × slotScale)`（atk/def/mdef 同型，不再乘 battle_lvl_*_ratio）。既有六場（劇情場景）維持 `scaling_mode='legacy'`＝現行公式（battle_lvl 3/3/0.4/0.4 × 怪物既有 mult × ps 1.0），**手感零改動**——校準發現 A 級向量若套到台北 101 首領（A 怪＋四隻）會幾乎不可勝，所以不把新分級回灌到舊場景。
- **九隻分級怪物**（新列，mult 全 1.0，rank 對應；沿用內容包五張圖）：F 荊棘幼蛾（E 圖）、E 荊棘毒蛾·躁（E 圖）、D 灰白獸人·壯（D 圖）、C 沙塵骷髏騎士·銳（C 圖）、B 鋼鐵巨鉗蟹·甲（B 圖）、A 幽暗食人花（A 圖，非首領旗標）、特A 鋼鐵巨鉗蟹王（B 圖）、S 幽暗食人花魔王（A 圖）、特S 深淵食人花始祖（A 圖）；屬性依相剋玩法配置（F/E 木、D 無、C 土、B 金、A 闇、特A 金、S 闇、特S 闇；weak_elements 對應）。既有五隻不動（rank 欄只是標籤）。
- **初版校準倍率**（相對 Ref(N)；def_mult＝mdef_mult）：

  | rank | 中文 | hp | atk | def/mdef | 原型 | 校準錨點與已知缺口 |
  |---|---|---|---|---|---|---|
  | F | F級 | 1.8 | 1.8 | 0.24 | 均衡 | Lv10/30/50 全過 |
  | E | E級 | 0.7 | 8.75 | 0.093 | 爆發（血少攻高，技能差距才顯現） | Lv10 過；Lv30 需 ×1.10；Lv50 未解 |
  | D | D級 | 6.6 | 2.2 | 0.29 | 磨耗 | Lv10/30 過；Lv50 裸裝仍能勝 |
  | C | C級 | 1.8 | 9.0 | 0.24 | 爆發 | Lv30 過；Lv10 太難、Lv50 太易 |
  | B | B級 | 6.0 | 6.0 | 0.80 | 均衡 | Lv30 過；Lv10 ×0.79、Lv50 ×1.29 |
  | A | A級 | 11.5 | 11.5 | 1.53 | 均衡 | Lv20–40 過（傭兵資料限制）；Lv50 失效 |
  | SA | 特A級 | 14.2 | 14.2 | 1.89 | 均衡 | Lv20–40 過 |
  | S | S級 | 20 | 10 | 2.66 | 磨耗 | Lv30 過（60%／392s）；Lv40 太易 |
  | SS | 特S級 | 190 | 6.5 | 0.87 | 磨耗 | Lv30/40 過（47–50%／26 分鐘）；Lv20 不過 |

  hp／atk 各自不單調是三種原型混用的結果（爆發型 E/C 血少攻高；磨耗型 D/S/SS 血多攻低）；`hp×atk` 總壓力指數九級遞增。**等級漂移**（Lv10 與 Lv50 需要不同倍率）九份報告指向同一根因：`Ref(N)` 參考玩家表沒有裝備與技能成長，與真實玩家的差距隨等級擴大且依職業不同——本輪先接受「倍率以 Lv30 為錨、後台可調」，並在表中保留 `level_curve JSONB`（每級修正係數，預設 `{}`）供之後填；**根治＝重算帶裝備的參考玩家表**，列為下一輪（P13）。
- **強度挑戰對戰列表**：新增 20 場（`scaling_mode='rank'`、`level_mode='player'`＝怪物等級跟隨玩家有效等級）：F～B 各 ×1／×3／×5（同級同怪），A ×1／×3，特A／S／特S ×1；場景輪用六個既有場景；F～A 可逃跑（S／特S 不可）；對戰選單分兩組「劇情場景」與「強度挑戰」（徽章＋隻數＋「Lv.＝你的等級」）。×3／×5 **已校準（2026-09-20 sim3）**：同倍率下 F×3 只剩 13% 勝、E／C／B 的 ×3／×5 幾乎全滅，故遭遇 `power_scale` ×3 設 0.4、×5 設 0.3（C／B 級為瓶頸取全域保守值；F／D 單獨可到 0.75／0.6），後台可單場微調。
- **召喚（A 以上）**：`rpg_monster_ranks.summon` JSONB `{ "waves": [ { "at_hp_pct": 60, "rank": "E", "count": 2, "monster_ids": [], "power_scale": 1.0 } ] }`（monster_ids 空＝該級分級怪；未來後台可指定）；預設：A 60%→2×E；特A 70%→2×D、35%→1×C；S 75%→3×D、50%→2×B、25%→1×A；特S 80%→2×C、60%→2×B、40%→1×A、20%→2×B＋1×A。場上敵人上限 5（含召喚者），無空位略過該波；召喚怪等級＝召喚者等級、強度＝其 rank 向量；bootstrap 先算好 `summonPool`，引擎在召喚者 HP% 跨門檻時放進空槽（事件 `summon`，浮字「召喚！」）；召喚後的難度另輪校準。**修正（2026-09-20）**：模擬證實召喚怪直接套用完整 rank 向量太強（A 級 60% 門檻召喚 2×E，讓 3 人隊 270 場只贏 1 場）——`waves[].power_scale`（缺省 1.0，值域 0.05–2.0）是獨立於 `rank.*_mult` 之外的折減旋鈕，套用在該波召喚怪的 encounterScale 上（`enc.power_scale × wave.power_scale`），讓後續模擬可以只調召喚怪強度、不動一般敵人的 rank 向量；本輪只接通機制，實際折減數值由模擬決定後寫回 migration 189 seed。　**2026-09-20 sim3 校準**：召喚怪用完整 rank 向量太強（A 級 3 人隊 270 場只贏 1 場、S／特S 全敗），故每波 `power_scale`＝A 0.15（≥90% 達標）、S 0.25（47% 勝／約 5.3 分）、特S 0.05（40% 勝／約 27.8 分）；⚠️**特A 無可行解**——0.05–2.0 全區間滿隊中位最高僅 80–83%（重騎士 100%、輕騎士／法師 73–83%），暫取 0.10 並列為已知缺口，需結構調整（減少召喚隻數／提高觸發門檻／補職業手段）。
- `battle_lvl_*_ratio` 程式預設**維持** 3/3/0.4/0.4（legacy 用）。

## 2. 資料模型（migration 189；P12 用 188）
- `rpg_monster_ranks`（rank TEXT PK、label、sort_order、hp_mult/atk_mult/def_mult/mdef_mult NUMERIC、level_curve JSONB DEFAULT '{}'、summon JSONB DEFAULT '{}'、badge_color、description、calibrated_note、timestamps）＋九列 seed（§1 表）。
- `rpg_monsters`：`rank` 加 FK（既有 A–E 值合法）；新增九隻分級怪（id `DOR-MON-R-<rank>`）。
- `rpg_encounters` ADD `scaling_mode TEXT NOT NULL DEFAULT 'legacy'` CHECK (legacy|rank)、`level_mode TEXT NOT NULL DEFAULT 'fixed'` CHECK (fixed|player)、`rank TEXT NULL REFERENCES rpg_monster_ranks`、`monster_count INT NULL`；20 場 seed（code `rank_<R>_x<n>`）與槽位（×1 front_center；×3 front 三格；×5 全五格）。

## 3. 規則
- bootstrap：`level_mode='player'` → N＝玩家有效等級；`scaling_mode='rank'` → 用 rank 向量（× level_curve[N] 若有）；敵人 wire 加 `rank`、`rankLabel`；`summonPool`。TS fixture 同步公式（Go／TS 逐位元）。
- 引擎：召喚觸發／槽位／目標選擇對新敵人生效（focus_fire、守護狀態照常）；勝利＝場上所有敵人死亡。
- 後台「怪物強度」分頁：九級倍率、level_curve、summon JSON、說明；遭遇頁 scaling_mode／level_mode／rank／monster_count；怪物頁 rank 下拉。

## 4. 畫面
- 對戰選單兩組；強度挑戰卡片「F 級・單挑／三隻／五隻」＋徽章色＋「Lv.＝你的等級」；S／特S 標示「不可逃跑」。
- 戰鬥：敵人名旁徽章；召喚浮字與新敵人進場。

## 5. 驗證
- Go：rank 公式（與 TS 逐位元）、legacy 六場數值零改動（回歸測試鎖定現值）、summonPool、level_mode=player、20 場 seed 槽位數；引擎：召喚觸發／上限／事件／目標；模擬：rank_runner 重跑九級定義（含召喚後 A+）與 ×3／×5 回報；Neon：189 冪等、九級、九隻、20 場、legacy 不變；E2E：對戰選單分組與徽章、F×1 裸裝勝、S 召喚畫面；審查。
- 待辦（P13）：重算帶裝備的參考玩家表以消除等級漂移，再重新校準九級。
