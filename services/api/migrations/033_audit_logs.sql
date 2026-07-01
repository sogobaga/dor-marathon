-- Migration 033: 後台操作紀錄（沿用 001_init 既有的 audit_logs 表，不另建）
-- 既有 audit_logs：id BIGSERIAL, user_id, action, resource, resource_id, meta JSONB, ip, created_at。
-- 後台中介層寫入：action=人類可讀動作、resource=模組、resource_id=目標、
--   meta={method,path,status,login,name}（login/name 為操作者快照）。此處補 resource 索引供篩選。

CREATE INDEX IF NOT EXISTS idx_audit_logs_resource ON audit_logs(resource);

INSERT INTO schema_migrations (version) VALUES ('033') ON CONFLICT DO NOTHING;
