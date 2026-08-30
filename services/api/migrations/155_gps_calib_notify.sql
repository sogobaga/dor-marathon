-- Migration 155: GPS 距離校正——站內信通知白名單（gps_calib_notify_whitelist）
--
-- ⚠️ 部署順序：先套這支 migration，再推後端程式。閘門是 fail-closed——app_settings 查無這個 key
--    時 appsettings.GetString 回空字串，whitelisted("") 一律 false，於是「一封都不發」（連
--    sogobaga@gmail.com 也收不到）。反過來的順序不會壞資料，只會讓通知靜默消失一段時間；
--    程式端在這種情況會留一行 warn（見 internal/gpscalib service.go Recompute 尾端）。
--
-- 與 migration 154 種下的 gps_calib_entry_whitelist（決定「校正對誰生效」）**刻意分開**：入口設定
-- gps_calib_entry_state 日後改成 'open'（正式全站開放）時，校正會對全體生效，但「GPS 距離校正已啟用／
-- 暫停中」站內信仍然只發給這份名單裡的帳號。空字串＝一封都不發（fail-closed，見
-- internal/gpscalib.notifyAllowed）——與 entry_whitelist「空值＋open＝全放行」的語意相反，因為通知是
-- 打擾使用者的動作，預設值取最保守的一邊。
-- 格式同其他白名單：換行/逗號/分號/空白分隔，可填帳號編碼（# 可省）或註冊 Email，大小寫不敏感。
INSERT INTO app_settings (key, value) VALUES
  ('gps_calib_notify_whitelist', 'sogobaga@gmail.com')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('155') ON CONFLICT DO NOTHING;
