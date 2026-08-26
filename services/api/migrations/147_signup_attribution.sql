-- Migration 147：會員註冊來源歸因（新會員從哪個管道進站/註冊，供行銷成效分析）。
--
-- user_signup_attribution：1:1 掛在 users 之下（PK=user_id），僅記錄「新建帳號」當下一次性
-- 快照，不隨後續行為更新——比照 virtual_runners 的 1:1 掛法，ON DELETE CASCADE 隨帳號刪除清除。
--
-- source 值（Go 層驗證，見 internal/attribution.Classify；本表故意不加 CHECK 約束，比照全站
-- 慣例列舉欄位只在 Go 層驗證）：
--   referral（推廣連結，ref_user_id 才會非空）／facebook／instagram／line／google（自然搜尋或
--   廣告）／threads／tiktok／other（其他外部網域或無法辨識的 utm_source）／direct（無來源，
--   直接輸入網址或書籤進站）。
--
-- ref_user_id：僅 source='referral' 時填入推薦人 user_id（解析自既有 referrals 表，見
-- internal/attribution.Record）；推薦人帳號被刪除時 SET NULL（不倒扣 source，維持這筆列史仍
-- 標記為「透過推廣連結而來」，只是推薦人身分已不可考）。
--
-- utm：{"source":"...","medium":"...","campaign":"..."} 僅存有值的欄位；非 utm_source 分流時
-- 也可能有值（單純記錄 landing_url 帶的 utm_medium/campaign，不影響 source 判斷）。
--
-- landing_url/referrer_url：前端 lib/acquisition.ts 於 App 首次進站（localStorage 尚無
-- 'dor:acq:v1'）擷取，first-touch 不覆寫；註冊/Google 登入時隨 acq 選填欄位送出，後端截斷至
-- 500 字元（見 internal/attribution.truncateURL）。
CREATE TABLE IF NOT EXISTS user_signup_attribution (
    user_id      UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    source       VARCHAR(20) NOT NULL,
    ref_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    utm          JSONB,
    landing_url  TEXT,
    referrer_url TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_signup_attribution_source ON user_signup_attribution(source);

INSERT INTO schema_migrations (version) VALUES ('147') ON CONFLICT DO NOTHING;
