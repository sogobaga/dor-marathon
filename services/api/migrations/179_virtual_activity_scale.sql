-- Migration 179: 虛擬選手全域活動倍率
--
-- 使用者原話：「先降低虛擬選手的頻率和距離，如果現在是 1 的話請改成 0.75，未來我可以透過這個數字來
-- 調整虛擬選手的能力。」
--
-- 語意：app_settings key = virtual_activity_scale，一個倍率同時乘在生成引擎
-- （internal/virtualrunner/generator.go）的「出勤機率」與「單次距離」上——不必分別調兩個參數。
-- 程式內建預設 1.0＝改動前的現行行為（本表若查無此 key，GetFloat 回退到 1.0，不受本檔影響）；
-- 合法範圍 0.1~3.0 且允許小數（見 internal/appsettings.specs 的 isFloatRange(0.1,3.0)）。
--
-- ⚠️ 兩個維度是相乘關係，月里程期望值約是 scale²：0.75 → 約現行 56%（0.75×0.75），不是單純的 75%。
--    這是刻意設計（詳見 generator.go RunnerParams.ActivityScale 註解），讓使用者「一個數字」就能
--    同時壓低頻率與距離兩個維度，之後想再調整（拉高／壓低）直接在後台「系統設定」頁改這個值即可，
--    不需要重新部署。
--
-- 本檔把正式站的初始值調成 0.75（使用者本次要求的降低幅度）；ON CONFLICT DO NOTHING 保留之後任何
-- 透過後台 API 手動調整過的值，重複套用本 migration 不會覆蓋回 0.75。

INSERT INTO app_settings (key, value) VALUES
  ('virtual_activity_scale', '0.75')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('179') ON CONFLICT DO NOTHING;
