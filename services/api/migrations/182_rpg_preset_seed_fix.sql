-- 182_rpg_preset_seed_fix.sql
-- DORPG P6 資料修正：migration 181 的系統預設腳本「阿深」(id=00000000-0000-0000-0000-000000000104,
-- companion_id=char_ashen, heavy_knight) 的 skill_levels 違規——hk_b3（堅守姿態）被寫成 Lv6，
-- 但 migration 180 的技能 seed（180_rpg_jobs_skills.sql 第 135 行）把 hk_b3 的 max_level 定義為 5：
--   ('hk_b3', '堅守姿態', ..., 'heavy_knight', 'b', 3, 5, ...)  -- 第 19 欄 max_level=5，不是 10
-- Lv6 超出上限 1 級，是一筆不合法的系統預設 seed。
--
-- 根因（給下一個人看，避免同類錯誤再發生）：internal/rpg/presets_test.go 的
-- heavyKnightPathBSkills() 測試 fixture 把 hk_b3 的 MaxLevel 抄成 10（照抄 hk_b1/hk_b2 的
-- max_level，沒有逐筆對照 migration 180 的真實值），使得 TestSystemPresetSeeds_AreValidAtLevel25
-- 用了錯誤的上限做驗證，讓 {"hk_b1":7,"hk_b2":6,"hk_b3":6,"hk_b4":5} 這筆本該被擋下的違規配置
-- 「通過」了測試。本檔只修資料本身；同一次修補也把 presets_test.go 的 fixture 改成與 migration
-- 180 逐值相同，並改用本檔套用後的最終值，讓測試之後真的能攔到這類問題。
--
-- 使用者已將 migration 181 套用到正式庫（Neon），不得回頭改寫 181 的 INSERT——依規範另開一份
-- migration UPDATE 既有列，並確保可重複執行（IS DISTINCT FROM）。
--
-- 新配置（總技能點仍 24＝Lv25 的 TotalSkillPoints；前置鏈逐一核對 migration 180 的
-- max_level／prereq_skill_id／prereq_level）：
--   hk_b1 盾甲衝撞　Lv8（max_level=10，無前置）
--   hk_b2 鋼鐵之軀　Lv6（max_level=10，前置 hk_b1>=3，8>=3 成立）
--   hk_b3 堅守姿態　Lv5（max_level=5，已達上限，前置 hk_b2>=3，6>=3 成立）
--   hk_b4 鐵壁護盾　Lv5（max_level=5，已達上限，前置 hk_b3>=3，5>=3 成立）
--   合計 8+6+5+5 = 24。
--
-- ⚠️ 冪等：WHERE 帶 IS DISTINCT FROM 目標值，可安全重複套用；只 UPDATE 既有一列資料，不建表/
-- 不加欄。

UPDATE rpg_companion_presets
SET skill_levels = '{"hk_b1":8,"hk_b2":6,"hk_b3":5,"hk_b4":5}'::jsonb,
    updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000104'
  AND user_id IS NULL
  AND skill_levels IS DISTINCT FROM '{"hk_b1":8,"hk_b2":6,"hk_b3":5,"hk_b4":5}'::jsonb;

INSERT INTO schema_migrations (version) VALUES ('182') ON CONFLICT DO NOTHING;
