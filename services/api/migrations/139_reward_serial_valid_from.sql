-- Migration 139: 活動序號組使用期限加「開始時間」——使用期限從單一截止時間變成時間區段（開始~截止）
-- 設計見 memory activity-reward-system。valid_from 為選填：NULL=即刻可用；有值時需早於 valid_until
-- （見 internal/rewardserial/service.go validateGroupInput）。user_rewards.valid_from 比照既有
-- valid_until 的 denormalize 慣例，於發放序號當下自序號組帶入（見 internal/activityreward/roll.go
-- claimSerialsFromGroup），即使序號組事後被改期也不影響已發出獎勵的顯示與判定。

ALTER TABLE reward_serial_groups ADD COLUMN IF NOT EXISTS valid_from TIMESTAMPTZ;  -- 開始時間；NULL=即刻可用
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS valid_from TIMESTAMPTZ;          -- 發放時自序號組 denormalize

INSERT INTO schema_migrations (version) VALUES ('139') ON CONFLICT DO NOTHING;
