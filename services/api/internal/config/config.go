package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env            string
	Port           string
	DatabaseURL    string
	RedisURL       string
	JWTSecret      string
	AccessTTL      time.Duration
	RefreshTTL     time.Duration
	CORSOrigins    []string
	GoogleClientID string // 選用：空字串 = Google 登入未啟用

	// 綠界 ECPay — 全域環境開關 + 共用回呼設定
	ECPayEnv           string // stage | prod（僅此為 prod 且結帳來源 origin 列在 ECPayProdOrigins 才會用正式特店，見下）
	ECPayReturnURL     string // server 對 server 付款結果通知（須公開可達）
	ECPayClientBackURL string // 付款後返回商店網址（預設 fallback）

	// 綠界 ECPay — 正式特店。UAT(dor.hero-mi.com) 與正式(www.dor.tw) 目前共用同一後端 process，
	// 為避免 UAT 測試不慎刷到真錢，正式特店僅在 ECPayEnv=prod 且前端結帳時帶來的 origin
	// （window.location.origin，見 Checkout body 的 client_back_url；不是 HTTP Host／
	// X-Forwarded-Host——前台是 Next.js 伺服器端代理，這兩個 header 在代理這一跳會被換成 API 自己的
	// Railway 網域，反映不出瀏覽器真實來源）明確列在 ECPayProdOrigins 時才會被選用
	// （見 internal/payment.MultiConfig.ResolveByOrigin，故障安全：不在任何白名單內一律 fail closed）。
	// 預設留空——正式帳號開通前不會有值，任何誤判都只會打到空字串的 MerchantID 而失敗，不會誤刷真錢。
	ECPayProdMerchantID string
	ECPayProdHashKey    string
	ECPayProdHashIV     string
	ECPayProdOrigins    []string // 允許使用正式特店的完整 origin（逗號分隔，預設 https://www.dor.tw,https://dor.tw）

	// 綠界 ECPay — 測試特店（預設值＝官方公開測試特店帳號，部署即可用）。
	// 若舊版 ECPAY_MERCHANT_ID/ECPAY_HASH_KEY/ECPAY_HASH_IV 已在 Railway 設定，會自動當作 fallback 沿用。
	ECPayStageMerchantID string
	ECPayStageHashKey    string
	ECPayStageHashIV     string

	// Strava 運動數據整合（選用：空 ClientID = 未啟用）
	StravaClientID           string
	StravaClientSecret       string
	StravaRedirectURI        string // 須與 Strava app 設定的 Authorization Callback Domain 相符
	StravaWebhookVerifyToken string
	FrontendURL              string // OAuth 完成後導回前台
}

func Load() *Config {
	return &Config{
		Env:            getEnv("ENV", "development"),
		Port:           getEnv("PORT", "8080"),
		DatabaseURL:    mustEnv("DATABASE_URL"),
		RedisURL:       mustEnv("REDIS_URL"),
		JWTSecret:      mustEnv("JWT_SECRET"),
		AccessTTL:      parseDuration(getEnv("JWT_ACCESS_TTL", "60m")),
		RefreshTTL:     parseDuration(getEnv("JWT_REFRESH_TTL", "720h")), // 30 天；每次登入/refresh 重置（滑動視窗）
		CORSOrigins:    strings.Split(getEnv("CORS_ORIGINS", "http://localhost:3000"), ","),
		GoogleClientID: getEnv("GOOGLE_CLIENT_ID", ""),

		ECPayEnv:           getEnv("ECPAY_ENV", "stage"),
		ECPayReturnURL:     getEnv("ECPAY_RETURN_URL", "https://dor-marathon-production.up.railway.app/api/v1/payments/ecpay/notify"),
		ECPayClientBackURL: getEnv("ECPAY_CLIENT_BACK_URL", "https://dor.hero-mi.com"),

		ECPayProdMerchantID: getEnv("ECPAY_PROD_MERCHANT_ID", ""),
		ECPayProdHashKey:    getEnv("ECPAY_PROD_HASH_KEY", ""),
		ECPayProdHashIV:     getEnv("ECPAY_PROD_HASH_IV", ""),
		ECPayProdOrigins:    splitCSV(getEnv("ECPAY_PROD_ORIGINS", "https://www.dor.tw,https://dor.tw")),

		// fallback 到舊版 ECPAY_MERCHANT_ID 等舊變數名，相容既有 Railway 環境設定
		ECPayStageMerchantID: getEnv("ECPAY_STAGE_MERCHANT_ID", getEnv("ECPAY_MERCHANT_ID", "2000132")),
		ECPayStageHashKey:    getEnv("ECPAY_STAGE_HASH_KEY", getEnv("ECPAY_HASH_KEY", "5294y06JbISpM5x9")),
		ECPayStageHashIV:     getEnv("ECPAY_STAGE_HASH_IV", getEnv("ECPAY_HASH_IV", "v77hoKGq4kWxNNIS")),

		StravaClientID:           getEnv("STRAVA_CLIENT_ID", ""),
		StravaClientSecret:       getEnv("STRAVA_CLIENT_SECRET", ""),
		StravaRedirectURI:        getEnv("STRAVA_REDIRECT_URI", "https://dor-marathon-production.up.railway.app/api/v1/integrations/strava/callback"),
		StravaWebhookVerifyToken: getEnv("STRAVA_WEBHOOK_VERIFY_TOKEN", "dor-strava-webhook"),
		FrontendURL:              getEnv("FRONTEND_URL", "https://dor.hero-mi.com"),
	}
}

func (c *Config) IsDev() bool { return c.Env == "development" }

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// splitCSV 依逗號切分並去除空白/空字串（用於網域白名單等清單型 env var）
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic("required env var not set: " + key)
	}
	return v
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic("invalid duration: " + s)
	}
	return d
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
