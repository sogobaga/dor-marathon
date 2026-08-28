-- Migration 150：組合能力從「RewardItem.bundle」（migration 149，賽事即時獎勵設定層級的臨時組合）搬到
-- 「序號組」（reward_serial_groups）層級——組合定義變成序號組本身的固定屬性，賽事即時獎勵設定端就近視為
-- 一個普通面額選項（跟其他面額一起放進加權隨機池），中獎抽到它時發放引擎自動拆解成子面額組發放。
-- 設計見 memory activity-reward-system；語意契約見 internal/rewardserial（CRUD）、
-- internal/activityreward（roll.go grantSerialBundle）。

-- is_bundle=true：這個序號組不自己存 reward_serials，改成「子面額組(child_group_id) × 數量(count)」的
-- 固定組合（如「LINE POINTS 3000」= LINE POINTS 1000 × 3）。預設 FALSE，既有序號組行為完全不變。
ALTER TABLE reward_serial_groups ADD COLUMN IF NOT EXISTS is_bundle BOOLEAN NOT NULL DEFAULT FALSE;

-- 組合定義表：parent_group_id 是 is_bundle=true 的序號組，child_group_id 是實際持有序號的面額組（須為
-- 非組合型，防巢狀，由 internal/rewardserial CRUD 驗證把關，非資料庫層級 CHECK——巢狀判斷需要查詢
-- child 當下的 is_bundle 值，CHECK constraint 做不到跨列查詢）。
-- parent_group_id 用 ON DELETE CASCADE：刪除組合型序號組本身時一併清空其組合定義。
-- child_group_id 刻意不加 CASCADE（預設 NO ACTION）：刪除一個仍被某組合引用的子面額組會被資料庫擋下，
-- 逼管理員先把該子項從組合中移除，避免留下失聯的組合定義（見 internal/rewardserial.DeleteGroup
-- 對 23503 外鍵違反的錯誤轉譯）。
CREATE TABLE IF NOT EXISTS reward_serial_group_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_group_id UUID NOT NULL REFERENCES reward_serial_groups(id) ON DELETE CASCADE,
    child_group_id UUID NOT NULL REFERENCES reward_serial_groups(id),
    count INT NOT NULL CHECK (count >= 1),
    sort_order INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_reward_serial_group_items_parent ON reward_serial_group_items(parent_group_id);

INSERT INTO schema_migrations (version) VALUES ('150') ON CONFLICT DO NOTHING;
