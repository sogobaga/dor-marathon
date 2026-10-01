-- Migration 197: COROS MCP 第二階段（匯入、型號標示、自動同步），見 docs/integration/COROS_MCP_STAGE2_CONTRACT.md
-- 依賴：196_coros_mcp.sql（user_integrations.issuer 等）、014_integrations.sql（activities 外部來源欄位）
-- 皆 ADD COLUMN IF NOT EXISTS，可重複套用；DB 必須先於程式套用（程式的 SELECT/INSERT 會讀寫這兩欄）。

-- 活動的資料來源裝置型號（例「COROS PACE 4」）。NULL＝未知；目前只有 COROS MCP 匯入會寫。
-- COROS 活動資料本身不含型號，來源是同步時 queryDevices 的第一支綁定裝置。
ALTER TABLE activities
    ADD COLUMN IF NOT EXISTS device_name VARCHAR(60);

-- MCP 連線（provider='coros_mcp'）最後一次「成功同步」時間，供前台顯示「上次同步」。
ALTER TABLE user_integrations
    ADD COLUMN IF NOT EXISTS last_synced_at TIMESTAMPTZ;

INSERT INTO schema_migrations (version) VALUES ('197') ON CONFLICT DO NOTHING;
