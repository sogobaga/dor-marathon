-- 173_pet_registration.sql
-- 寵物雲端馬拉松（2026-09-08 owner request，本輪只做「報名」部分：狗狗賽事／貓貓賽事＋報名時登記寵物，
-- 計分邏輯留待之後——這裡先把資料模型準備好）。
--
-- races：新增 pet_kind（''=非寵物賽事｜dog｜cat，一場賽事的物種只會是其一）、
--        pet_max_per_reg（每筆報名最多可登記幾隻寵物，含基本名額，1..20）、
--        pet_base_slots（基本名額，目前固定寫 1，但存成欄位而非寫死常數，供未來調整）。
ALTER TABLE races
  ADD COLUMN IF NOT EXISTS pet_kind VARCHAR(8) NOT NULL DEFAULT '' CHECK (pet_kind IN ('', 'dog', 'cat')),
  ADD COLUMN IF NOT EXISTS pet_max_per_reg INT NOT NULL DEFAULT 1 CHECK (pet_max_per_reg BETWEEN 1 AND 20),
  ADD COLUMN IF NOT EXISTS pet_base_slots INT NOT NULL DEFAULT 1;

-- race_groups（賽事分組/組別）：飼主／寵物兩個勾選——是否納入飼主成績／寵物成績（供之後的計分邏輯用），
-- 預設兩者皆勾（TRUE），對既有分組/非寵物賽事零影響。
ALTER TABLE race_groups
  ADD COLUMN IF NOT EXISTS for_owner BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS for_pet   BOOLEAN NOT NULL DEFAULT TRUE;

-- race_addons（加購項目）：kind 區分一般品項（item，預設，向下相容既有資料）與「加購寵物參賽名額」
-- （pet_slot，每份 +1 隻寵物）。同一賽事至多一個 pet_slot 加購由 app 層（normalizeRequest）擋，
-- 不在 DB 加唯一索引（比照既有 gender_limit/supply kind 等 enum 欄位的驗證慣例，全部落在應用層）。
ALTER TABLE race_addons
  ADD COLUMN IF NOT EXISTS kind VARCHAR(16) NOT NULL DEFAULT 'item' CHECK (kind IN ('item','pet_slot'));

-- registration_pets：每筆報名底下登記的寵物名單（一筆報名可有 1..pet_max_per_reg 筆）。
-- platform/platform_ref 保留給未來串接第三方寵物資料平台（例如晶片註冊平台）用，本輪不使用（皆為空字串）。
CREATE TABLE IF NOT EXISTS registration_pets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    registration_id UUID NOT NULL REFERENCES registrations(id) ON DELETE CASCADE,
    race_id         UUID NOT NULL,
    user_id         UUID NOT NULL,
    seq             INT NOT NULL,
    species         VARCHAR(8) NOT NULL,        -- 'dog' | 'cat'（＝報名當下 race.pet_kind）
    name            VARCHAR(40) NOT NULL,
    chip_id         VARCHAR(32) NOT NULL DEFAULT '',
    platform        VARCHAR(32) NOT NULL DEFAULT '',
    platform_ref    VARCHAR(64) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (registration_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_registration_pets_registration ON registration_pets(registration_id);

INSERT INTO schema_migrations (version) VALUES ('173') ON CONFLICT DO NOTHING;
