-- 2026-09-08 資安稽核 finding 3：session 撤銷缺口修補。
-- tokens_not_before：IssuedAt 早於這個時間戳的 access/refresh token 一律視為失效，不分角色
-- （含 admin，與 session_epoch 只對非 admin 生效不同——見 internal/auth/service.go
-- ValidateAccessToken／Refresh 的檢查）。NULL＝從未被設定過，代表「沒有下限」，不擋任何 token
-- （既有帳號升級後預設行為不變，零回歸）。
--
-- 使用時機：
--   - 後台管理者密碼變更（internal/adminacct.Handler.Update 的密碼段落）：改密碼後把這個帳號
--     「現有全部」session 立即失效，不用等 token 自然過期。
--   - POST /admin/accounts/{id}/revoke-sessions（僅超級管理員，見
--     internal/auth/handler.go AdminRevokeSessions）：懷疑帳號外洩時手動強制登出全部裝置。
ALTER TABLE users ADD COLUMN IF NOT EXISTS tokens_not_before TIMESTAMPTZ;

INSERT INTO schema_migrations (version) VALUES ('171') ON CONFLICT DO NOTHING;
