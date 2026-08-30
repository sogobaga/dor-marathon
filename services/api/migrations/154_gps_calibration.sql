-- Migration 154: App GPS 距離校正（以穿戴裝置為參考）
-- activities：NULL = 未套校正（distance_km 即原始）；有值 = distance_km 已 = raw × calib_factor
ALTER TABLE activities
  ADD COLUMN IF NOT EXISTS raw_distance_km NUMERIC(8,2),
  ADD COLUMN IF NOT EXISTS calib_factor    NUMERIC(6,4),
  ADD COLUMN IF NOT EXISTS ext_manual      BOOLEAN,      -- Strava summary.manual；本期只存不判，未來解鎖 k>1 的前提
  -- 對抗式審查修正：duration_s 對 Strava 存的是 moving_time（扣掉停等），但 gps_runs.duration_s 是
  -- GPS 端的真實經過時間（含紅綠燈停等）——gpscalib 的閘門拿兩者互相對齊/比較時，一趟只要停等超過
  -- 20-35 秒就會被 partial/edge 閘門誤拒。elapsed_s 另存「總經過時間」供時間對齊專用：Strava 存
  -- elapsed_time；COROS/Terra 語意本來就接近經過時間，維持 NULL（gpscalib 端 COALESCE 回 duration_s）。
  ADD COLUMN IF NOT EXISTS elapsed_s       INT;

-- gps_runs：distance_km / avg_pace_s 永遠是「原始值」（原始檔案表，此規則寫進註解）
ALTER TABLE gps_runs
  ADD COLUMN IF NOT EXISTS calib_factor      NUMERIC(6,4) NOT NULL DEFAULT 1.0,
  ADD COLUMN IF NOT EXISTS calib_distance_km NUMERIC(8,2),   -- = round2(distance_km × calib_factor)，與 activities.distance_km 相同
  ADD COLUMN IF NOT EXISTS client_version    TEXT,
  ADD COLUMN IF NOT EXISTS acc_p50 REAL, ADD COLUMN IF NOT EXISTS acc_p90 REAL,
  ADD COLUMN IF NOT EXISTS used_point_count  INT;            -- 量測用（環境/精度分層是未來最大變異來源），本期無邏輯讀它
COMMENT ON COLUMN gps_runs.distance_km IS '原始有效距離（永不套校正）；校正後見 calib_distance_km';

CREATE TABLE IF NOT EXISTS gps_calib_pairs (
  id              BIGSERIAL PRIMARY KEY,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  gps_run_id      UUID NOT NULL REFERENCES gps_runs(id) ON DELETE CASCADE,
  ext_activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
  ext_source      VARCHAR(16) NOT NULL,
  gps_km          NUMERIC(8,2) NOT NULL,   -- 一律 gps_runs.distance_km（原始）
  ext_km          NUMERIC(8,2) NOT NULL,
  gps_dur_s       INT NOT NULL, ext_dur_s INT NOT NULL,
  start_gap_s     INT NOT NULL, end_gap_s INT NOT NULL,   -- GPS起−外部起 / GPS迄−外部迄（秒）
  log_ratio       DOUBLE PRECISION NOT NULL,             -- ln(ext_km/gps_km)
  dist_w          REAL NOT NULL,
  accepted        BOOLEAN NOT NULL,
  reject_reason   TEXT,                                   -- flagged|ambiguous|partial|short|edge|range|other_source
  inlier_w        REAL,                                   -- 最近一次重算的 inlier 權重（離群=接近 0）
  activity_at     TIMESTAMPTZ NOT NULL,                   -- = GPS 起點；衰減/視窗/排序基準
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (gps_run_id, ext_activity_id)                    -- 重算冪等（ON CONFLICT DO UPDATE）
);
CREATE INDEX IF NOT EXISTS ix_gps_calib_pairs_user ON gps_calib_pairs(user_id, activity_at DESC);

CREATE TABLE IF NOT EXISTS user_gps_calib (
  user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  ref_source     VARCHAR(16) NOT NULL DEFAULT '',        -- 對哪個外部來源校準（見估計器 ref_source 規則）
  factor         NUMERIC(6,4) NOT NULL DEFAULT 1.0,      -- 對外係數（已過 publish）
  log_mu         DOUBLE PRECISION NOT NULL DEFAULT 0,    -- 內部估計
  sigma          DOUBLE PRECISION,
  n_pairs        INT  NOT NULL DEFAULT 0,                -- 視窗內 accepted 數
  n_eff          REAL NOT NULL DEFAULT 0,
  eff_weight     REAL NOT NULL DEFAULT 0,
  status         VARCHAR(10) NOT NULL DEFAULT 'warming', -- warming|active|unstable|stale|frozen
  enabled        BOOLEAN NOT NULL DEFAULT TRUE,          -- 使用者開關（關閉 = 只算不套）
  frozen_factor  NUMERIC(6,4),                           -- 後台釘住值（status=frozen 時生效）
  reset_at       TIMESTAMPTZ,                            -- 重設點：只用之後的配對
  last_pair_at   TIMESTAMPTZ,
  computed_at    TIMESTAMPTZ,
  version        INT NOT NULL DEFAULT 0,
  window_fingerprint TEXT,                                -- 上次 publish 時 EstimateWindow 用到的
                                                            -- accepted 配對集合指紋（sha256，見
                                                            -- gpscalib.WindowFingerprint）；同一指紋
                                                            -- 再次出現代表沒有新證據，Recompute 不會
                                                            -- 再讓 ±2% 步幅前進（對抗步幅被連續觸發繞過）
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_gps_calib_log (            -- 係數軌跡：稽核/前台歷程/後台圖表
  id            BIGSERIAL PRIMARY KEY,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  version       INT NOT NULL,
  pair_id       BIGINT REFERENCES gps_calib_pairs(id) ON DELETE SET NULL,
  factor_before NUMERIC(6,4), factor_after NUMERIC(6,4),
  status        VARCHAR(10), log_mu DOUBLE PRECISION, sigma DOUBLE PRECISION, n_eff REAL,
  reason        VARCHAR(16) NOT NULL,                     -- recompute|enable|disable|reset|admin_freeze|admin_unfreeze
  actor         TEXT NOT NULL,                            -- system|user|admin:<id>
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ix_user_gps_calib_log_user ON user_gps_calib_log(user_id, created_at DESC);

INSERT INTO app_settings (key, value) VALUES
  ('gps_calib_entry_state', 'whitelist'),
  ('gps_calib_entry_whitelist', 'sogobaga@gmail.com')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('154') ON CONFLICT DO NOTHING;
