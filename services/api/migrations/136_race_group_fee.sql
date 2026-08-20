-- Migration 136: 報名費支援「各分組獨立定價」。
-- fee_mode='uniform'（預設，現行行為）：全場統一用 races.entry_fee。
-- fee_mode='per_group'：各分組可各自設定 race_groups.entry_fee_cents；該欄位 NULL＝沿用
-- races.entry_fee（此時 entry_fee 語意＝「預設報名費」，未獨立設定的組別、前台跑團成員新增的組別皆適用）。
-- uniform 模式下 race_groups.entry_fee_cents 一律忽略。
-- 有效組價的單一事實來源見後端 race.EffectiveGroupFee／前端 lib/api.ts effectiveGroupFee。

ALTER TABLE races ADD COLUMN IF NOT EXISTS fee_mode VARCHAR(12) NOT NULL DEFAULT 'uniform';
ALTER TABLE race_groups ADD COLUMN IF NOT EXISTS entry_fee_cents INT;  -- NULL=沿用 races.entry_fee

INSERT INTO schema_migrations (version) VALUES ('136') ON CONFLICT DO NOTHING;
