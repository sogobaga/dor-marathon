-- Migration 101: 城市探索「打卡/完成獎勵」系統級預設值
--
-- 背景：098 把打卡/完成的 DP/GP 區間與機率做成 explore_bosses 每點欄位，但既有 1058 個點全部預設 0，
-- 導致目前打卡實際發 0 獎勵。後台需要一個「系統級預設」，per-point 欄位繼續當覆寫（非 0 才蓋過預設），
-- 見 internal/appsettings 新增的 explore_checkin_dp_min/max 等 7 個 key，讀取邏輯見 internal/explore.go。
--
-- ON CONFLICT DO NOTHING：不覆蓋任何已存在的值（若後台已手動調過就維持原值）。
INSERT INTO app_settings (key, value) VALUES
  ('explore_checkin_dp_min', '1'),      -- 每次打卡 DP 下限
  ('explore_checkin_dp_max', '3'),      -- 每次打卡 DP 上限
  ('explore_checkin_gp_min', '1'),      -- 每次打卡 GP 下限
  ('explore_checkin_gp_max', '2'),      -- 每次打卡 GP 上限
  ('explore_complete_gp_min', '5'),     -- 關主完成 GP 下限
  ('explore_complete_gp_max', '10'),    -- 關主完成 GP 上限
  ('explore_complete_gp_chance', '30')  -- 關主完成給 GP 的機率(%)
ON CONFLICT (key) DO NOTHING;

-- 順帶把每日上限/冷卻也 seed 進 DB（規則本身 097 沿用程式內建預設運作已久，這裡只是讓後台「系統設定」頁
-- 顯示真實現值而非空白；ON CONFLICT DO NOTHING 不影響已存在的值）。
INSERT INTO app_settings (key, value) VALUES
  ('explore_checkin_daily_cap_normal', '3'),
  ('explore_checkin_daily_cap_vip', '5'),
  ('explore_checkin_cooldown_hours', '24')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('101') ON CONFLICT DO NOTHING;
