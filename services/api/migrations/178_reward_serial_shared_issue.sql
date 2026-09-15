-- Migration 178：序號組「共用碼可發給多位得主」語意修正。
--
-- 根因（已用正式庫唯讀查證，見 memory activity-reward-system）：後台 use_limit_type=repeat/unlimited
-- 一直被管理員理解成「這一列序號可以發給 N 位不同得主」（例如一組共用折扣碼要發給前 10 位完成者），
-- 但後端發放路徑（internal/activityreward/roll.go claimSerialsFromGroup）完全不讀這個欄位——實際語意
-- 是「一列序號＝一位得主」（UPDATE ... SET status='issued' WHERE status='available' ... LIMIT 1），
-- use_limit_type/use_limit_count 只在後台被驗證與顯示、從未影響配發次數。結果是共用碼組別只要 1 列
-- 序號，發給第一位得主後 status 就變成 issued、後面的人全部拿不到，觸發「庫存吃緊」告警——告警本身沒錯，
-- 錯的是配發邏輯沒有實作管理員設定的意圖。
--
-- 語意改變（本 migration 之後，use_limit_type 的意思）：
--   single    ：一列序號＝一位得主（既有行為，完全不變）。
--   repeat(N) ：同一列序號可以發給 N 位不同得主（issue_count 累加到 N 才變 issued）。
--   unlimited ：同一列序號可以發給任意人數（issue_count 只增不減，狀態恆為 available，永不耗盡）。
--
-- 新增 issue_count：這一列序號「已經發給幾位不同得主」，取代「status 是否已改 issued」作為 repeat/
-- unlimited 型的耗盡判斷依據（single 型不使用這個欄位遞增，維持既有 status 判斷）。
--
-- 回填 issue_count：以 user_rewards 為準——每發給一位得主，claimSerialsFromGroup／grantSerialBundle 就會
-- 各自 INSERT 一筆 user_rewards(serial_id=該序號)，所以「這列序號目前已發給幾位」＝
-- COUNT(user_rewards WHERE serial_id=該序號)，不論當初是靠哪個 use_limit_type 發出的都成立（single 型
-- 正常情況下每列最多 1 筆，回填後 issue_count=1，不影響其既有行為，因為 single 路徑永遠不讀寫這個欄位）。
--
-- 回填 status：共用碼組別（repeat 未到上限 / unlimited）過去因為配發邏輯錯誤被誤標成 issued 的列，
-- 只要「回填後的 issue_count 顯示還有剩餘額度」就放回 available，讓下一次配發能繼續發給下一位得主
-- （這是本次修復唯一會改動既有資料狀態的地方，且只朝「讓本該可再發的序號恢復可發」這個方向修正，
-- 不會把任何原本 available 的序號改成別的狀態，符合「發獎邏輯寧可少發不可多發」的保守原則——這裡只是
-- 撥亂反正，找回原本就該存在、只是因程式錯誤而卡住沒發出去的名額）。
--
-- 冪等：ADD COLUMN IF NOT EXISTS／INSERT ... ON CONFLICT DO NOTHING／回填 WHERE 條件本身具冪等性
-- （issue_count 已等於目標值時 IS DISTINCT FROM 為 false，不會重複疊加；status 已是 available 的不會
-- 被再次觸碰），可重複執行。

ALTER TABLE reward_serials ADD COLUMN IF NOT EXISTS issue_count INT NOT NULL DEFAULT 0;

-- 回填 issue_count：以 user_rewards.serial_id 的實際發放筆數為準。
UPDATE reward_serials s
SET issue_count = sub.n
FROM (
    SELECT serial_id, COUNT(*) AS n
    FROM user_rewards
    WHERE serial_id IS NOT NULL
    GROUP BY serial_id
) sub
WHERE sub.serial_id = s.id
  AND s.issue_count IS DISTINCT FROM sub.n;

-- 回填 status：共用碼組別（repeat/unlimited）裡「回填後仍有剩餘額度」但被誤標 issued 的列，放回
-- available 讓後續配發能繼續發給下一位得主。single 型與已用滿的 repeat 型不受影響。
UPDATE reward_serials s
SET status = 'available'
FROM reward_serial_groups g
WHERE g.id = s.group_id
  AND s.status = 'issued'
  AND (
        g.use_limit_type = 'unlimited'
        OR (g.use_limit_type = 'repeat' AND s.issue_count < COALESCE(g.use_limit_count, 0))
      );

-- 部分索引（2026-09-15 對抗審查 PLAUSIBLE-5）：形狀比照共用碼實際的 claim SQL（見
-- activityreward.claimSerialsFromGroup 的 sharedIssue 分支）——
--   WHERE group_id=$2 AND status<>'void' AND issue_count<$3 ORDER BY issue_count, created_at
-- 舊索引 (group_id, status, issue_count) 的等值前綴只吃得到 group_id 一欄（status 是 <> 不等於、issue_count
-- 是 < 範圍），status 那一欄反而卡在 issue_count 與 created_at 之間、對這個查詢完全沒用還佔位；且該索引
-- 涵蓋了 status='void' 的列（claim 查詢永遠排除），白白放大索引卻幫不上忙。改成排除 void 的部分索引、
-- 鍵序對齊「先按 issue_count 排、同 issue_count 內再按 created_at 排」，讓 ORDER BY 可以直接吃索引順序、
-- WHERE issue_count<$3 也能用範圍掃描。索引名稱不變，沿用既有 migration 檔內既有引用習慣。
CREATE INDEX IF NOT EXISTS idx_reward_serials_group_issue
    ON reward_serials(group_id, issue_count, created_at)
    WHERE status <> 'void';

-- 既有資料診斷（2026-09-15 對抗審查 PLAUSIBLE-4，唯讀通知、不改資料）：組合型序號組（migration 150）的
-- CRUD 驗證（rewardserial.validateBundleChildMeta）已要求子面額組必須是 use_limit_type='single'，但這條
-- 規則是後來才加上的——如果正式庫在此規則生效前，已經有組合包子面額組被設成 repeat/unlimited，這裡先
-- RAISE NOTICE 列出來讓人工檢視（該子面額組即使繼續留著也不會被 grantSerialBundle 用共用碼語意配發，
-- 只是設定跟 CRUD 現在允許的狀態不一致，需要人工決定是否要改回 single），不在 migration 內自動改動這批
-- 資料——發獎邏輯屬帳本類程式，寧可留著讓人工判斷，也不要 migration 自己動手可能改壞已經在正常運作的
-- 組合包設定。
DO $$
DECLARE
    r RECORD;
    violation_count INT := 0;
BEGIN
    FOR r IN
        SELECT i.parent_group_id, i.child_group_id, g.use_limit_type
        FROM reward_serial_group_items i
        JOIN reward_serial_groups g ON g.id = i.child_group_id
        WHERE g.use_limit_type <> 'single'
    LOOP
        violation_count := violation_count + 1;
        RAISE NOTICE '組合包子面額組違反「子項須為 single」規則：parent_group_id=%, child_group_id=%, use_limit_type=%',
            r.parent_group_id, r.child_group_id, r.use_limit_type;
    END LOOP;
    IF violation_count = 0 THEN
        RAISE NOTICE 'ok：沒有組合包子面額組使用 repeat/unlimited，無需人工檢視';
    END IF;
END $$;

INSERT INTO schema_migrations (version) VALUES ('178') ON CONFLICT DO NOTHING;
