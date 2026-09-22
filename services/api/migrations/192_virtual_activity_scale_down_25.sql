-- Migration 192: 虛擬選手活動倍率再降 25%（原雲端 session 編為 187，與 main 的 187_rpg_p10_guardian 撞號，2026-09-22 改編 192；
--   若沿用 187 守門，正式庫 schema_migrations 已有 187 會整段靜默略過、值不會降）
--
-- 使用者原話（2026-09-21）：「虛擬選手的頻率和距離再降25%」。
--
-- 語意：沿用 migration 179 的單一旋鈕 app_settings.virtual_activity_scale（同時乘在
-- internal/virtualrunner/generator.go 的「出勤機率」與「單次距離」上），把「現值」再乘 0.75：
--   0.75（migration 179 的正式站值）× 0.75 = 0.5625 → 四捨五入到後台顆粒度 0.01 = 0.56。
-- 頻率與距離各自約降到現行的 75%，月里程期望值約降到現行的 56%（兩者相乘，同 179 的說明）。
--
-- 刻意用「現值 × 0.75」而不是硬寫 0.56：若使用者在 179 之後曾透過後台「系統設定」改過這個值，
-- 本檔仍是相對於那個值再降 25%，符合「再降」的語意。若 key 不存在（理論上 179 已建），先以 179
-- 的 0.75 補上再乘。下限夾在 0.1（appsettings isFloatRange(0.1,3.0) 的合法下限）。
--
-- 四捨五入到 0.01：後台系統設定頁儲存時會把數值捨到欄位 step 的整數倍（apps/web/src/app/admin/system/
-- page.tsx roundToStep），本版同步把該欄位 step 從 0.05 改成 0.01，DB 值與後台顆粒度一致，之後在後台
-- 存檔不會偷偷把值再挪動。
--
-- ⚠️ 使用者手動套用到 Neon。整段以 schema_migrations 187 是否存在做守門，重複執行不會再乘一次 0.75。守門改看 192。
--    程式碼本身不依賴本檔（GetFloat 查無 key 回退 1.0），先後順序無所謂，但要「降」就必須套用本檔。

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '192') THEN
    INSERT INTO app_settings (key, value) VALUES ('virtual_activity_scale', '0.75')
    ON CONFLICT (key) DO NOTHING;

    UPDATE app_settings
       SET value      = round(GREATEST(value::numeric * 0.75, 0.1), 2)::text,
           updated_at = NOW()
     WHERE key = 'virtual_activity_scale';

    INSERT INTO schema_migrations (version) VALUES ('192');
  END IF;
END $$;
