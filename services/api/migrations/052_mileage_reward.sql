-- Migration 052: 里程獎勵改「單趟每滿 1km 發、單趟上限、配速防造假」
-- 依賴：020/031（exp_rules 的 per_km / dp_per_km）
-- mileage_cap_km：單趟里程獎勵的整公里上限（避免一趟灌爆）。
-- mileage_min_pace_s：防 GPS 造假的「最快合理配速」（秒/公里）。此趟時間內、以此配速最多只能跑
--   duration/min_pace 公里，超過的距離不列入發獎（擋「短時間灌大距離」的假資料）。預設 120＝2:00/km，
--   與前台 /track 的 MAX_SPEED 人體極限一致。
ALTER TABLE exp_rules
    ADD COLUMN IF NOT EXISTS mileage_cap_km      INT NOT NULL DEFAULT 21,
    ADD COLUMN IF NOT EXISTS mileage_min_pace_s  INT NOT NULL DEFAULT 120;

INSERT INTO schema_migrations (version) VALUES ('052') ON CONFLICT DO NOTHING;
