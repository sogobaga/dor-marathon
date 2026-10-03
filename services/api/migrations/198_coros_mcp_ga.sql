-- Migration 198: COROS MCP 全員開放（GA）＋直連手錶共用地基，見 docs/integration/COROS_MCP_GA_CONTRACT.md §2.3／§2.6／§6
-- 依賴：014_integrations.sql（user_integrations）、042_app_settings.sql、165（via）、196／197（coros_mcp 欄位）。
--
-- 內容：
--   1) user_integrations.reauth_required_at：需要使用者重新授權的標記（refresh 失敗時寫入、換到新 token 或重新授權時清空）。
--   2) (provider, provider_user_id) 部分唯一索引：同一個廠商帳號（COROS id_token sub、Garmin userId…）只能連到一個
--      DOR 帳號；provider_user_id 為空字串（尚未綁定識別的列）不受約束。
--   3) 預設設定列（缺鍵時程式也有預設，這裡只為後台可見）。
-- 全部 IF NOT EXISTS／ON CONFLICT：可重複套用。Garmin 直連（199）以 IF NOT EXISTS 重述共用 DDL，與本檔無順序相依。
--
-- ⚠️ 程式先讀新欄位（getConnection 的 reauth_required_at）：本 migration 必須「先於」COROS GA 程式推送套用（db-before-code-push）。
--
-- ⚠️ 套用前先唯讀確認（第 2 項的唯一索引會在重複資料上建立失敗，整個 migration 隨交易回滾）：
--   SELECT provider, provider_user_id, count(*) FROM user_integrations
--    WHERE provider_user_id <> '' GROUP BY 1, 2 HAVING count(*) > 1;
--   結果必須為空。注意索引涵蓋「所有 provider」，不只 COROS：同一個 Strava athlete／COROS openId／Terra user
--   若曾被兩個 DOR 帳號各連一次，這裡會查得到——先處理掉（或請擁有者決定保留哪一邊）再套用。
--   套用後的副作用：Strava／COROS Partner／Terra 的連接若撞到已被他人綁定的廠商帳號，會在寫入時被唯一索引擋下
--   （strava.go／terra.go 目前把它當一般儲存錯誤處理）。

ALTER TABLE user_integrations ADD COLUMN IF NOT EXISTS reauth_required_at TIMESTAMPTZ NULL;

CREATE UNIQUE INDEX IF NOT EXISTS user_integrations_provider_account_uniq
  ON user_integrations (provider, provider_user_id) WHERE provider_user_id <> '';

-- 預設設定（缺鍵時程式也有預設，這裡只為後台可見）
INSERT INTO app_settings (key, value, updated_at) VALUES
  ('coros_mcp_entry_state','whitelist',NOW()), ('coros_mcp_autosync_enabled','1',NOW())
  ON CONFLICT (key) DO NOTHING;

-- 一次性清除：讀取測試（probe）舊紀錄含 COROS 原始回應；GA 起只存摘要，這裡把開放前（僅擁有者測試）的舊紀錄清掉，
-- 與隱私權政策「不保存原始回應」一致。可重複執行（之後新紀錄只有摘要）。
DELETE FROM coros_mcp_probe_logs WHERE created_at < '2026-10-04';

INSERT INTO schema_migrations (version) VALUES ('198') ON CONFLICT DO NOTHING;
