-- 單一登入（single-session）強制：帳號加 session_epoch，每次「登入」(Login/Register/Google)
-- 遞增，寫進 access+refresh token 的 sev claim；refresh 與 GPS 上傳皆比對此值，
-- 不符即代表 token 已被更新的登入取代 → 拒絕，藉此把舊裝置踢下線。
ALTER TABLE users ADD COLUMN IF NOT EXISTS session_epoch INTEGER NOT NULL DEFAULT 0;
