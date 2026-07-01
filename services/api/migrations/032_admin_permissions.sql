-- Migration 032: 後台管理者權限（各模組獨立勾選 + 超級管理員）
-- 管理者沿用 users(role='admin')。權限存在 users 上：
--   is_super_admin   → 跳過所有權限檢查、且唯一能管理其他管理者
--   admin_permissions→ 各功能模組權限鍵陣列（races/members/orders/... 見 internal/adminacct）

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS is_super_admin    BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS admin_permissions TEXT[]  NOT NULL DEFAULT '{}';

-- 既有 admin 一律升為超級管理員，避免加上權限後被鎖死
UPDATE users SET is_super_admin = TRUE WHERE role = 'admin';

INSERT INTO schema_migrations (version) VALUES ('032') ON CONFLICT DO NOTHING;
