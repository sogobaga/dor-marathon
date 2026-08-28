-- Migration 149：序號組合包（固定組合、中獎後全發，取代「加權隨機抽一個」——用於單張面額有上限
-- （如 LINE POINTS 單張最高 1000）但要送出更大額（如 3500=1000×3+500×1）的情境）＋序號組結構化面額。
-- 設計見 memory activity-reward-system；語意契約見 internal/activityreward（roll.go grantSerialBundle）。
--
-- face_value：序號組的結構化面額（如 1000/500），取代原本靠 name 字串解析（reward_preview.go
-- largestNumberIn／後台 denomValueOf）——組合包 bundle_total = Σ(face_value × count) 需要一個「保證
-- 是數字」的欄位，不能繼續賭 name 字串裡一定能解析出正確數字。0 = 未設（既有 group 用下方 regexp 回填；
-- 回填不到數字的維持 0，後台須手動補）。
ALTER TABLE reward_serial_groups ADD COLUMN IF NOT EXISTS face_value INT NOT NULL DEFAULT 0;

-- 既有序號組回填：從 name 抓「最大的連續數字」當面額（與 reward_preview.go largestNumberIn 同一種解析
-- 口徑：抓字串中所有連續數字片段，取數值最大者）。限制單一片段最多 10 位數字（'\d{1,10}'）避免超長數字
-- 片段 cast bigint 溢位炸掉整支 migration；再限制 BETWEEN 1 AND 2000000000 避免超出 face_value INT 欄位
-- 範圍（INT 上限 2147483647，抓一個安全值）。只回填 face_value 仍是預設值 0 的既有列，不覆寫。
UPDATE reward_serial_groups g
SET face_value = sub.val
FROM (
    SELECT rsg.id, MAX((m[1])::bigint) AS val
    FROM reward_serial_groups rsg
    CROSS JOIN LATERAL regexp_matches(rsg.name, '\d{1,10}', 'g') AS m
    GROUP BY rsg.id
) sub
WHERE g.id = sub.id
  AND g.face_value = 0
  AND sub.val BETWEEN 1 AND 2000000000;

-- 組合包發放欄位：同一次組合包發放的多張序號（roll.go grantSerialBundle 逐 entry 各自 INSERT 一列
-- user_rewards）共用同一 bundle_id，前台錢包 group by bundle_id 併成一張卡片顯示。非組合包（單張序號
-- 獎勵，既有 grantSerialTwoLayer 路徑）bundle_id 維持 NULL，行為不變。
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS bundle_id UUID;
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS bundle_label VARCHAR(120);
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS bundle_total INT;
CREATE INDEX IF NOT EXISTS idx_user_rewards_bundle ON user_rewards(bundle_id) WHERE bundle_id IS NOT NULL;

INSERT INTO schema_migrations (version) VALUES ('149') ON CONFLICT DO NOTHING;
