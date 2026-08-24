-- Migration 145: IP/流量每日聚合——供每日營運報告「流量安全」區塊與異常流量巡檢使用。
-- day 用台灣日（UTC+8 手算，由 internal/middleware.IPDailyAggregate 的 taiwanDay() 產生，比照全站
-- 「不用 time.LoadLocation，distroless 無 tzdata」的慣例，見 internal/ops/selfcheck.go taiwanNow）。
--
-- 資料來源：internal/middleware.IPDailyAggregate 中介層（比照 FiveXXAlert 掛法），每請求 in-memory
-- 聚合真實 IP（internal/reqip.ClientIP）＋ CF-IPCountry 標頭（無則 "??"），背景 goroutine 每 5 分鐘
-- 批次 UPSERT（ON CONFLICT 累加）進本表，flush 後清空 map；健康檢查等高頻探測路徑不計入。
-- 保留 30 天，同一背景迴圈每天順手 DELETE 過期資料（見 IPDailyAggregate.cleanupOldDays）。
--
-- requests：該 IP 當天總請求數；auth_fails：401/403 回應數（登入/授權失敗，異常飆高可能是暴力破解/
-- 掃描）；not_found：404 回應數（異常飆高可能是路徑掃描）。
CREATE TABLE IF NOT EXISTS ops_ip_daily (
    day         DATE NOT NULL,
    ip          VARCHAR(45) NOT NULL,           -- IPv4/IPv6 皆可能（比照 user_login_logs.ip 欄寬）
    country     VARCHAR(4) NOT NULL DEFAULT '??', -- CF-IPCountry（ISO 2 碼；"??"=標頭缺失，"T1"=Tor 等 CF 特殊值）
    requests    INT NOT NULL DEFAULT 0,
    auth_fails  INT NOT NULL DEFAULT 0,
    not_found   INT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, ip)
);
CREATE INDEX IF NOT EXISTS idx_ops_ip_daily_day ON ops_ip_daily(day);

INSERT INTO schema_migrations (version) VALUES ('145') ON CONFLICT DO NOTHING;
