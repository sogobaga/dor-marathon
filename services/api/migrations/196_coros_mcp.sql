-- Migration 196: COROS MCP 第一階段（連接＋讀取測試），見 docs/integration/COROS_MCP_STAGE1_CONTRACT.md
-- 依賴：014_integrations.sql（user_integrations）、042_app_settings.sql（app_settings）
--
-- 連線沿用既有 user_integrations（provider 唯一鍵），但 provider 用獨立值 'coros_mcp'，
-- 絕不覆寫或影響既有 provider='coros' 的 Terra／Partner-API 直連列（契約第 6 點）。
-- 既有欄位放不下的部分（issuer、最後一次 probe 時間）用 ALTER TABLE 補欄位。
ALTER TABLE user_integrations
    ADD COLUMN IF NOT EXISTS issuer        TEXT,         -- 區域閘道 issuer（如 https://mcpus.coros.com），token 區域綁定
    ADD COLUMN IF NOT EXISTS last_probe_at TIMESTAMPTZ;  -- 最近一次「讀取測試」時間，供 /status 回傳

-- DCR（動態用戶端註冊）結果，每個 issuer 只註冊一次；client_secret 經既有 token 加密管線加密後存入
-- （confidential client 才有 secret；若最終走 public+PKCE fallback，此欄位為 NULL）。
-- raw_response 保存註冊回應原文，但寫入前必須先移除 secret 欄位（契約第 3 點：任何 log／存檔不得出現 secret）。
CREATE TABLE IF NOT EXISTS coros_mcp_clients (
    issuer                    TEXT PRIMARY KEY,
    client_id                 TEXT NOT NULL,
    client_secret             TEXT,              -- 加密後存（"enc:" 前綴，比照 user_integrations token），無則 NULL
    token_endpoint_auth_method VARCHAR(20) NOT NULL DEFAULT 'client_secret_basic',
    raw_response              JSONB,             -- 註冊回應原文，已移除 secret
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 讀取測試（probe）逐步紀錄：tools/list、queryDevices、querySportRecords、getActivityDetail、
-- queryActivityLapData 各一列；request/response 存原文供除錯，30 天保存期限（併入既有每日報告
-- 清理排程，不新增排程，見契約第 8 點）。
CREATE TABLE IF NOT EXISTS coros_mcp_probe_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tool        TEXT NOT NULL,
    request     JSONB,
    response    JSONB,
    status      TEXT NOT NULL,   -- ok | error
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 供保存期限排程（DELETE ... WHERE created_at < now() - interval '30 days'）掃描，
-- 也是後台／status 端點「取某使用者最新一次 probe」的查詢路徑。
CREATE INDEX IF NOT EXISTS idx_coros_mcp_probe_logs_user_created ON coros_mcp_probe_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_coros_mcp_probe_logs_created ON coros_mcp_probe_logs(created_at);

-- 入口白名單（比照 gps_raw_log_whitelist 格式：逗號分隔 email，缺鍵＝擁有者），
-- 預設只有擁有者帳號，超管不自動放行（契約第 1 點）。
INSERT INTO app_settings (key, value) VALUES
    ('coros_mcp_whitelist', 'sogobaga@gmail.com')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('196') ON CONFLICT DO NOTHING;
