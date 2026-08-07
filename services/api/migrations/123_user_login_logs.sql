-- 123_user_login_logs.sql
-- 用戶登入紀錄 log：與 audit_logs（僅記後台管理者操作）分開，這張只記玩家端登入
-- （password/google/register 三種），供後台檢查「有沒有人天天登入」。
-- 刻意不記 Refresh（高頻自動行為，會灌爆這張表、加重 DB 負擔）；見 internal/auth handler.go 的呼叫點。
CREATE TABLE IF NOT EXISTS user_login_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id),
    method      VARCHAR(20) NOT NULL,       -- password | google | register
    ip          VARCHAR(45),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_login_logs_user_created ON user_login_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_login_logs_created ON user_login_logs(created_at DESC);

-- 會員列表「上次登入」欄位：每次成功登入寫 log 的同時更新此欄，免去列表頁另外 JOIN 聚合查詢。
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;
