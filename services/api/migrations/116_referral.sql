-- 116_referral.sql
-- 推薦/推廣連結系統：total_km >= 10 的帳號可產生專屬推薦碼；透過 ?ref=<code> 連結註冊的新帳號，
-- 一旦自己也跨過 10km 門檻 → 推薦人 +N 天 VIP、新朋友 +M 天 VIP（雙向、一次性、無上限）。
-- 見 internal/referral（BindReferrer 綁定推薦關係、Reward 達標發獎、GetOrCreateCode 產生推薦碼）。

ALTER TABLE users ADD COLUMN IF NOT EXISTS referral_code VARCHAR(16) UNIQUE;

-- referred_user_id 為 PK：一個帳號只能被一個人推薦過（以首次成功綁定為準，
-- 見 referral.BindReferrer 的 ON CONFLICT (referred_user_id) DO NOTHING）。
CREATE TABLE IF NOT EXISTS referrals (
  referred_user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  referrer_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_used VARCHAR(16) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  rewarded_at TIMESTAMPTZ -- 非 NULL = 已發過雙向 VIP 獎勵（CAS 閘門，見 referral.Reward，一次性）
);
CREATE INDEX IF NOT EXISTS idx_referrals_referrer ON referrals(referrer_user_id);

-- 預設獎勵天數（後台「系統設定」可調；見 internal/appsettings specs 新增的
-- referral_reward_referrer_days / referral_reward_referred_days 兩個 key）。
-- ON CONFLICT DO NOTHING：不覆蓋任何已存在的值（若後台已手動調過就維持原值）。
INSERT INTO app_settings (key, value) VALUES
  ('referral_reward_referrer_days', '1'), -- 推薦人（老朋友）獎勵天數
  ('referral_reward_referred_days', '3')  -- 被推薦人（新朋友）獎勵天數
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('116') ON CONFLICT DO NOTHING;
