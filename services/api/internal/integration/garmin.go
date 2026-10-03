package integration

// Garmin 直連（Activity API，推送模式）— 公開 API 與 handler 骨架。
//
// 流程概述：Garmin 把使用者的活動摘要 POST 到我們的 webhook（路徑 token 驗證）→ 先把「最小化」事件寫入
// integration_events（落地）→ 回精確 200 → 在背景就地處理（匯入活動、撤銷、權限異動）。部署重啟或處理失敗時，
// 事件表是唯一事實來源：啟動後 60 秒掃一次、下一筆推送順手掃（5 分鐘節流）、每日報告窗口再掃一次——
// 全程沒有任何 ticker／週期性 DB 迴圈（Neon 必須能睡）。
//
// 本檔提供 S7（cmd/api/main.go）接線需要的全部公開介面：
//
//	NewGarminHandler(cfg GarminConfig, deps GarminDeps) *GarminHandler
//	(*GarminHandler).Router() http.Handler        // 掛在 /api/v1/integrations/garmin（公開群組，路由內自帶登入）
//	(*GarminHandler).StartupSweep(ctx)            // 啟動後延遲 60 秒掃一次（只跑一次）
//	(*GarminHandler).Drain(ctx)                   // 優雅關閉：等處理中的事件、釋放租約
//	(*GarminHandler).Name() string                // "garmin"（日報「直連手錶」段落）
//	(*GarminHandler).DirectStats(ctx)             // 純 DB 統計（只含人數）
//	(*GarminHandler).MaintainDaily(ctx)           // 每日維護：事件保存期限、補掃到期事件、token 保活
//
// 設定全部從環境變數讀（GarminConfigFromEnv），不動 config.Config。

import (
	"context"
	"crypto/sha256"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/integration/entrygate"
	"github.com/dor/api/internal/notify"
)

const (
	garminDefaultRedirectURI = "https://www.dor.tw/api/v1/integrations/garmin/callback"
	garminDefaultAuthBase    = "https://connect.garmin.com"
	garminDefaultTokenURL    = "https://diauth.garmin.com/di-oauth2-service/oauth/token"
	garminDefaultAPIBase     = "https://apis.garmin.com"

	// 推送 body 上限：Production 審核要求能收 ≥10MB，預設 16MB（超過回 413）。
	garminDefaultMaxBytes int64 = 16 << 20
	garminMinMaxBytes     int64 = 10 << 20 // 環境變數設得比這小就拉回這個值（避免設定錯誤導致審核失敗）
	// gzip 請求解壓後上限（防 zip bomb）。
	garminInflateMax int64 = 32 << 20

	// 路徑 token 最短長度（太短視為未設定，整個 webhook 回 503）。
	garminWebhookTokenMin = 32

	garminEntryStateKey    = "garmin_entry_state"
	garminWhitelistKey     = "garmin_whitelist"
	garminDefaultWhitelist = "" // 缺鍵＝空白：超管恆可用；測試帳號由後台白名單加入（repo 公開，程式內不寫 email）

	// 同時處理的上限（每個處理中的使用者最多占 2 條 DB 連線：鎖交易＋工作）。
	garminProcConcurrency  = 4
	garminParseConcurrency = 4
	garminParseWait        = 5 * time.Second
	garminProcessBudget    = 25 * time.Second
	garminStartupDelay     = 60 * time.Second
	garminSweepThrottle    = 5 * time.Minute
	garminMaxTimers        = 1000
)

// GarminConfig 由環境變數注入（GarminConfigFromEnv）。
type GarminConfig struct {
	ClientID         string   // GARMIN_CLIENT_ID
	ClientSecret     string   // GARMIN_CLIENT_SECRET
	RedirectURI      string   // GARMIN_REDIRECT_URI（預設 https://www.dor.tw/api/v1/integrations/garmin/callback）
	TokenAuth        string   // GARMIN_TOKEN_AUTH：basic（預設）| body
	AuthBase         string   // GARMIN_AUTH_BASE（僅測試覆寫）
	TokenURL         string   // GARMIN_TOKEN_URL（僅測試覆寫）
	APIBase          string   // GARMIN_API_BASE（僅測試覆寫）
	WebhookToken     string   // GARMIN_WEBHOOK_TOKEN（≥32 字元）
	WebhookTokenPrev string   // GARMIN_WEBHOOK_TOKEN_PREV（輪替重疊期，選用）
	WebhookMaxBytes  int64    // GARMIN_WEBHOOK_MAX_BYTES（預設 16777216）
	ConsentVersions  []string // GARMIN_CONSENT_VERSIONS（逗號分隔，預設 v1）：/connect 的 consent_v 必須在其中
}

// GarminConfigFromEnv 讀取 GARMIN_* 環境變數（空值套用預設）。
func GarminConfigFromEnv() GarminConfig {
	c := GarminConfig{
		ClientID:         strings.TrimSpace(os.Getenv("GARMIN_CLIENT_ID")),
		ClientSecret:     strings.TrimSpace(os.Getenv("GARMIN_CLIENT_SECRET")),
		RedirectURI:      strings.TrimSpace(os.Getenv("GARMIN_REDIRECT_URI")),
		TokenAuth:        strings.ToLower(strings.TrimSpace(os.Getenv("GARMIN_TOKEN_AUTH"))),
		AuthBase:         strings.TrimSpace(os.Getenv("GARMIN_AUTH_BASE")),
		TokenURL:         strings.TrimSpace(os.Getenv("GARMIN_TOKEN_URL")),
		APIBase:          strings.TrimSpace(os.Getenv("GARMIN_API_BASE")),
		WebhookToken:     strings.TrimSpace(os.Getenv("GARMIN_WEBHOOK_TOKEN")),
		WebhookTokenPrev: strings.TrimSpace(os.Getenv("GARMIN_WEBHOOK_TOKEN_PREV")),
	}
	if raw := strings.TrimSpace(os.Getenv("GARMIN_WEBHOOK_MAX_BYTES")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			c.WebhookMaxBytes = n
			if c.WebhookMaxBytes < garminMinMaxBytes {
				c.WebhookMaxBytes = garminMinMaxBytes
			}
		}
	}
	for _, v := range strings.Split(os.Getenv("GARMIN_CONSENT_VERSIONS"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			c.ConsentVersions = append(c.ConsentVersions, v)
		}
	}
	return c.withDefaults()
}

func (c GarminConfig) withDefaults() GarminConfig {
	if c.RedirectURI == "" {
		c.RedirectURI = garminDefaultRedirectURI
	}
	if c.TokenAuth != "body" {
		c.TokenAuth = "basic"
	}
	if c.AuthBase == "" {
		c.AuthBase = garminDefaultAuthBase
	}
	if c.TokenURL == "" {
		c.TokenURL = garminDefaultTokenURL
	}
	if c.APIBase == "" {
		c.APIBase = garminDefaultAPIBase
	}
	if c.WebhookMaxBytes <= 0 {
		c.WebhookMaxBytes = garminDefaultMaxBytes
	}
	if len(c.ConsentVersions) == 0 {
		c.ConsentVersions = []string{"v1"}
	}
	return c
}

// GarminDeps 由 main.go 注入的外部依賴。
type GarminDeps struct {
	DB          *pgxpool.Pool
	Redis       *redis.Client // 可為 nil（本機／測試：退記憶體）
	RequireAuth func(http.Handler) http.Handler
	// RateLimit：main.go 以 middleware.RateLimit(rdb, action, limit, window, middleware.UserOrIP) 包裝後注入；
	// 為 nil 時不限流。限流一律掛在 RequireAuth 之後（使用者維度才有值）。
	RateLimit   func(action string, limit int, window time.Duration) func(http.Handler) http.Handler
	HTTPClient  *http.Client // 選用；測試注入以 host 分流的 in-process RoundTripper
	FrontendURL string       // callback 完成後導回的前台網址（config.FrontendURL）
	JWTSecret   string       // state 簽章金鑰的來源（config.JWTSecret；內部再衍生用途專屬金鑰）
	Mailer      GarminMailer // 站內信（撤銷／暫停通知）；為 nil 時不發信
}

// garminStore：handler 用到的資料存取（*Repository 實作；單元測試以記憶體假物件取代）。
type garminStore interface {
	// 事件表
	KnownGarminUsers(ctx context.Context, ids []string) (map[string]string, error)
	InsertGarminEvents(ctx context.Context, evs []garminEventIn) ([]garminEvent, error)
	ClaimDueGarminEvents(ctx context.Context, limit int) ([]garminEvent, error)
	ClaimGarminEventByID(ctx context.Context, id string) (*garminEvent, error)
	MarkGarminEventDone(ctx context.Context, id, result string) error
	MarkGarminEventError(ctx context.Context, id, errCode string, next time.Time, maxAttempts int) (int, bool, error)
	MarkGarminEventDead(ctx context.Context, id, reason string) error
	DeferGarminEvent(ctx context.Context, id, note string, next time.Time) error
	ReleaseGarminLeases(ctx context.Context, ids []string) error
	PurgeGarminEvents(ctx context.Context) (int64, error)
	WithGarminUserLock(ctx context.Context, key string, fn func(ctx context.Context) error) error
	GarminEventStats(ctx context.Context) (garminEventStats, error)

	// 連線列
	GetGarminByUser(ctx context.Context, userID string, withTokens bool) (*garminConn, error)
	GetGarminDirectByUserID(ctx context.Context, garminUserID string, withTokens bool) (*garminConn, error)
	GarminBlocked(ctx context.Context, userID, garminUserID string) (bool, error)
	AddGarminBlock(ctx context.Context, userID, garminUserID, reason, createdBy string) error
	RemoveGarminBlock(ctx context.Context, userID string) (int64, error)
	SaveGarmin(ctx context.Context, in garminSaveInput) (garminSaveResult, error)
	UpdateGarminTokens(ctx context.Context, id, access, refresh string, expiresAt, refreshExpiresAt time.Time) error
	MarkGarminReauth(ctx context.Context, id string) error
	SetGarminScope(ctx context.Context, id, scope string) error
	TouchGarminSynced(ctx context.Context, id string) error
	LatestGarminDeviceName(ctx context.Context, userID string) (string, error)
	PurgeGarminUser(ctx context.Context, userID, garminUserID string) (GarminPurgeResult, error)
	GarminConnStats(ctx context.Context, generation string) (garminConnStats, error)
	GarminKeepaliveCandidates(ctx context.Context, generation string, within time.Duration, limit int) ([]*garminConn, error)

	// 匯入
	LegacyTwinExists(ctx context.Context, userID, bareID string, startUnix int64) (bool, error)
	ImportActivity(ctx context.Context, a *NormalizedActivity) (ImportResult, error)
	AfterImport(ctx context.Context, na *NormalizedActivity, res ImportResult, opt TailOptions)
}

// GarminMailer 站內信發送（*mail.Handler 滿足；main.go 傳入與其他模組相同的 mail handler）。
type GarminMailer interface {
	InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error)
}

var _ garminStore = (*Repository)(nil)

// GarminHandler 是 Garmin 直連的 HTTP handler 與事件處理器。
type GarminHandler struct {
	cfg         GarminConfig
	deps        GarminDeps
	repo        *Repository // 可為 nil（單元測試只用 store）
	store       garminStore
	db          *pgxpool.Pool
	kv          *garminKV
	hc          *http.Client
	requireAuth func(http.Handler) http.Handler
	rateLimit   func(action string, limit int, window time.Duration) func(http.Handler) http.Handler

	tokDigest     [32]byte
	tokPrevDigest [32]byte
	hasPrev       bool

	// 可注入（測試）：預設為真實實作
	alertFn      func(kind, title, detail string)
	emergency    func(ctx context.Context) bool                           // 入口 hidden（緊急關閉）？
	entryFor     func(ctx context.Context, userID string) (string, error) // 使用者的入口狀態（"shown"／"hidden"）；nil＝entrygate.Load
	handlers     garminEventHandlers
	startupDelay time.Duration
	refreshWait  time.Duration // 拿不到跨副本刷新鎖時最多輪詢多久（預設 5 秒）
	purgeWait    time.Duration // PurgeUser 拿不到跨副本使用者鎖時最多輪詢多久（預設 8 秒）
	now          func() time.Time

	mailer         GarminMailer
	disabledReason string // 非空＝設定有問題（例如端點網址不合規），連接功能停用

	// 處理框架狀態
	procSem    chan struct{}
	parseSem   chan struct{}
	userMu     garminKeyedMutex
	workMu     sync.Mutex // 保護 workN／workClosed／workIdle（背景工作追蹤，Drain 用）
	workN      int
	workClosed bool
	workIdle   chan struct{}
	draining   atomic.Bool
	inflightM  sync.Mutex
	inflight   map[string]struct{}
	timersM    sync.Mutex
	timers     map[string]*time.Timer
}

// garminEventHandlers：各事件類型的處理函式（回 result 字串；回 error 代表暫時性失敗，走退避重試）。
type garminEventHandlers struct {
	activity   func(ctx context.Context, ev garminEvent) (string, error)
	deregister func(ctx context.Context, ev garminEvent) (string, error)
	permission func(ctx context.Context, ev garminEvent) (string, error)
}

// NewGarminHandler 建構 handler（Repository 由 deps.DB 建立）。
func NewGarminHandler(cfg GarminConfig, deps GarminDeps) *GarminHandler {
	repo := NewRepository(deps.DB)
	h := newGarminHandlerWithStore(cfg, deps, repo)
	h.repo = repo
	h.db = deps.DB
	return h
}

func newGarminHandlerWithStore(cfg GarminConfig, deps GarminDeps, store garminStore) *GarminHandler {
	cfg = cfg.withDefaults()
	hc := deps.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Timeout: 15 * time.Second,
			// 不跟隨導向：Garmin API 不會導向；避免被導去他處
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	h := &GarminHandler{
		cfg:          cfg,
		deps:         deps,
		store:        store,
		db:           deps.DB,
		kv:           newGarminKV(deps.Redis),
		hc:           hc,
		requireAuth:  deps.RequireAuth,
		rateLimit:    deps.RateLimit,
		alertFn:      notify.Alert,
		startupDelay: garminStartupDelay,
		refreshWait:  garminRefreshWait,
		purgeWait:    8 * time.Second,
		now:          time.Now,
		procSem:      make(chan struct{}, garminProcConcurrency),
		parseSem:     make(chan struct{}, garminParseConcurrency),
		inflight:     map[string]struct{}{},
		timers:       map[string]*time.Timer{},
	}
	if h.requireAuth == nil {
		// fail-closed：沒有注入認證中介層就一律 401（不可讓 connect／status／disconnect 無保護）
		h.requireAuth = func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				respondErr(w, http.StatusUnauthorized, "login required")
			})
		}
	}
	h.tokDigest = sha256.Sum256([]byte(cfg.WebhookToken))
	if len(cfg.WebhookTokenPrev) >= garminWebhookTokenMin {
		h.tokPrevDigest = sha256.Sum256([]byte(cfg.WebhookTokenPrev))
		h.hasPrev = true
	}
	h.mailer = deps.Mailer
	// 端點網址驗證：production 只允許官方 garmin.com 網域的 https 網址（防設定被改成導向他處）。
	for name, u := range map[string]string{"GARMIN_AUTH_BASE": cfg.AuthBase, "GARMIN_TOKEN_URL": cfg.TokenURL, "GARMIN_API_BASE": cfg.APIBase} {
		if !garminHostOK(u) {
			h.disabledReason = "invalid_endpoint_" + strings.ToLower(name)
			log.Error().Str("setting", name).Msg("garmin: endpoint is not an official https garmin.com host, garmin connect disabled")
		}
	}
	h.emergency = h.defaultEmergency
	h.handlers = garminEventHandlers{
		activity:   h.processActivityEvent,
		deregister: h.processDeregEvent,
		permission: h.processPermissionEvent,
	}
	return h
}

// Name 供日報「直連手錶」段落辨識供應商。
func (h *GarminHandler) Name() string { return garminProvider }

// limit 回傳掛在 RequireAuth 之後的限流中介層（未注入 RateLimit 時為 no-op）。
func (h *GarminHandler) limit(action string, n int, window time.Duration) func(http.Handler) http.Handler {
	if h.rateLimit == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return h.rateLimit(action, n, window)
}

// Router 掛在 /api/v1/integrations/garmin。webhook 與 callback 公開（各自驗 token／state）；
// connect／status／disconnect 需登入（路由內自帶，不依賴外層）。status／disconnect 不受入口閘限制。
func (h *GarminHandler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/webhook/{token}/activities", h.WebhookActivities)
	r.Post("/webhook/{token}/deregistrations", h.WebhookDeregistrations)
	r.Post("/webhook/{token}/permissions", h.WebhookPermissions)
	r.Get("/callback", h.Callback)
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)
		r.With(h.limit("garmin_connect", 10, time.Minute)).Post("/connect", h.Connect)
		r.With(h.limit("garmin_status", 60, time.Minute)).Get("/status", h.Status)
		r.With(h.limit("garmin_disconnect", 10, time.Minute)).Post("/disconnect", h.Disconnect)
	})
	return r
}

// webhookConfigured：路徑 token 已設定且夠長。
func (h *GarminHandler) webhookConfigured() bool {
	return len(h.cfg.WebhookToken) >= garminWebhookTokenMin
}

// defaultEmergency：入口狀態為 hidden（緊急全關）時回 true。比照 entrygate 語意：缺鍵＝whitelist（不是緊急）。
func (h *GarminHandler) defaultEmergency(ctx context.Context) bool {
	if h.db == nil {
		return false
	}
	return entrygate.Emergency(ctx, h.db, garminEntryStateKey)
}

// alert 送 Telegram 告警（per-kind 30 分鐘節流）。⚠️ 內容不得含使用者資料、路徑、token。
func (h *GarminHandler) alert(kind, title, detail string) {
	if h.alertFn != nil {
		h.alertFn(kind, title, detail)
	}
}

// count 計數器（Redis，失敗無害）；回傳新值（失敗回 0）。
func (h *GarminHandler) count(ctx context.Context, key string, n int64, ttl time.Duration) int64 {
	v, err := h.kv.IncrBy(ctx, key, n, ttl)
	if err != nil {
		log.Debug().Err(err).Msg("garmin counter incr failed")
		return 0
	}
	return v
}

// hourBucket／tenMinBucket：計數器鍵的時間桶（UTC）。
func (h *GarminHandler) hourBucket() string { return h.now().UTC().Format("2006010215") }
func (h *GarminHandler) tenMinBucket() string {
	t := h.now().UTC()
	return t.Format("20060102") + "-" + strconv.Itoa(t.Hour()*6+t.Minute()/10)
}
func (h *GarminHandler) dayBucket() string { return h.now().UTC().Format("20060102") }
