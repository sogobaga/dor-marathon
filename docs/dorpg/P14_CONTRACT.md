# DORPG P14 契約：取消「素質上限＝等級」— 2026-09-20

單一真相。P5–P13 契約仍有效，本檔**取代** P5_CONTRACT §3 的「上限：每項素質 ≤ min(MaxStat, L)」一條。

## 0. 使用者需求（原話）
「優化：取消等級為基礎數值上限的設定」

## 1. 決策
- 單一素質上限改為**只受 `rpg_config.max_stat`（預設 99）限制**，與有效等級無關。低等級可以把點數集中投在單一素質（Lv1 起即可配點；實際能投多少仍受配點預算 `TotalStatPoints` 與遞增成本 `pointCost` 限制，所以不會爆表）。
- 新增後台開關 `rpg_config.stat_cap_by_level`（預設 `false`）：設為 `true` 可一鍵還原 P5 舊規則 `min(max_stat, 有效等級)`，兩種模式都有測試守住。
- **參考玩家表（reflevel.go）刻意不吃這個開關**，固定走等級上限（新增 `refStatCap`）。WHY：它是 `ScaleMonsterByLevel`／`ScaleMonsterByRank` 的唯一基準，P6 六場與 P11 九級強度向量都以它校準，跟著玩家規則浮動會讓所有怪物數值位移、兩輪校準作廢。實測該上限在 99 級內從未被觸發（成本遞增使六圍輪流配點遠達不到 level），因此固定它不改變任何現有數值——`TestRefPlayerTable_MatchesJSON` 守住。
- 錯誤碼不變：`stat_cap`（配點端）／`stat_over_cap`（腳本驗證）在超過 `max_stat` 時照樣回報；DTO 欄位 `stat_cap` 保留，值改為 `max_stat`。

## 2. 影響面
- 後端：`config.go`（新欄位與預設）、`compute.go StatCap`、`reflevel.go refStatCap`；`handler.go` 配點端、`presets.go` 腳本驗證沿用 `StatCap()` 不需改邏輯。
- 前端：角色頁「／N 上限」直接顯示新的 `stat_cap`（99）；酒館錯誤文案改「素質超過上限」；後台「參數設定」新增 `stat_cap_by_level` 開關。
- 不需要 migration：`rpg_config` 是 `app_settings` 的整包 JSON，正式庫沒有這個鍵，改程式預設即生效（比照 177/185 的既有做法）。

## 3. 驗證
- Go：預設模式任何等級 `StatCap==max_stat`；開關打開還原 `min(max_stat, level)`；腳本驗證在兩種模式下的 `stat_over_cap`；`TestRefPlayerTable_MatchesJSON` 通過（怪物數值零位移）；既有 P5–P13 測試全過。
- 前端：tsc、四個 verify 腳本、next build；角色頁可把單一素質加到遠高於等級（受預算限制）。
- 遊玩：低等級集中投點的手感由使用者實測回報；必要時用後台 `max_stat` 或還原開關調整。
