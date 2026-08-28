package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/activity"
	"github.com/dor/api/internal/activityreward"
	"github.com/dor/api/internal/adminacct"
	"github.com/dor/api/internal/analytics"
	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/cache"
	"github.com/dor/api/internal/config"
	"github.com/dor/api/internal/db"
	"github.com/dor/api/internal/emailbroadcast"
	"github.com/dor/api/internal/event"
	"github.com/dor/api/internal/explore"
	"github.com/dor/api/internal/image"
	"github.com/dor/api/internal/integration"
	"github.com/dor/api/internal/mail"
	"github.com/dor/api/internal/mailer"
	"github.com/dor/api/internal/middleware"
	"github.com/dor/api/internal/monopoly"
	"github.com/dor/api/internal/ops"
	"github.com/dor/api/internal/organizer"
	"github.com/dor/api/internal/partner"
	"github.com/dor/api/internal/payment"
	"github.com/dor/api/internal/personaltask"
	"github.com/dor/api/internal/profile"
	"github.com/dor/api/internal/promo"
	"github.com/dor/api/internal/push"
	"github.com/dor/api/internal/race"
	"github.com/dor/api/internal/realtime"
	"github.com/dor/api/internal/reward"
	"github.com/dor/api/internal/rewardserial"
	"github.com/dor/api/internal/routing"
	"github.com/dor/api/internal/training"
	"github.com/dor/api/internal/version"
	"github.com/dor/api/internal/virtualrunner"
)

func main() {
	godotenv.Load()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	if os.Getenv("ENV") == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	cfg := config.Load()
	ctx := context.Background()

	// 連接資料庫
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer pool.Close()
	log.Info().Msg("database connected")

	// 連接 Redis
	rdb, err := cache.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to redis")
	}
	defer rdb.Close()
	log.Info().Msg("redis connected")

	// --- 模組初始化 ---

	// Auth
	authRepo := auth.NewRepository(pool)
	authSvc := auth.NewService(authRepo, rdb, cfg.JWTSecret, cfg.AccessTTL, cfg.RefreshTTL, cfg.GoogleClientID)
	authHandler := auth.NewHandler(authSvc)

	// WebSocket Manager（各模組共用）
	wsManager := realtime.NewManager(rdb)
	// 單一登入：authHandler 建立時 wsManager 尚未就緒，故用 setter 補注入，
	// 讓登入成功後可推播 session_revoked 踢除舊裝置連線。
	authHandler.SetRealtime(wsManager)

	// Promo（優惠序號）
	promoRepo := promo.NewRepository(pool)
	promoSvc := promo.NewService(promoRepo)
	promoHandler := promo.NewHandler(promoSvc)

	// Race
	raceRepo := race.NewRepository(pool)
	raceSvc := race.NewService(raceRepo, rdb, promoSvc)
	raceHandler := race.NewHandler(raceSvc, wsManager)

	// 站內信（in-app mail）：供前台鈴鐺列表 + 後台廣播 mail 頻道寫入。提前到這裡建構（原本在後面），
	// 因為 VIP 站內付2.0 BindHandler（Phase D 續約排程通知）需要在建構時就注入 MailInserter。
	mailHandler := mail.NewHandler(pool, wsManager)
	// 參賽虛擬獎勵發放通知（migration 140）：raceSvc 建構於 mailHandler 之前，兩者互相依賴的初始化順序
	// 無法互換，晚繫結注入（比照下面 raceHandler.SetRefundCreator 的 setter 模式）。
	raceSvc.SetMailInserter(mailHandler)

	// Payment（綠界 ECPay）—— 正式／測試雙特店，依結帳來源 origin 故障安全切換（見 payment.MultiConfig）
	// 啟動檢查：宣告要跑正式環境（ECPAY_ENV=prod）卻沒有配齊正式特店憑證，寧可直接拒絕啟動，
	// 也不要讓服務帶著空字串 MerchantID/HashKey/HashIV 悄悄跑起來（那樣所有正式結帳都會失敗，
	// 或更糟——萬一日後有人把測試金鑰誤填進 PROD_* 變數，此檢查至少能擋下「完全空白」的最壞情況）。
	if cfg.ECPayEnv == "prod" && (cfg.ECPayProdMerchantID == "" || cfg.ECPayProdHashKey == "" || cfg.ECPayProdHashIV == "") {
		log.Fatal().Msg("ECPAY_ENV=prod 但 ECPAY_PROD_MERCHANT_ID/HASH_KEY/HASH_IV 未配置完整，拒絕啟動")
	}
	ecpayStageCfg := &payment.Config{
		MerchantID:    cfg.ECPayStageMerchantID,
		HashKey:       cfg.ECPayStageHashKey,
		HashIV:        cfg.ECPayStageHashIV,
		Env:           "stage",
		ReturnURL:     cfg.ECPayReturnURL,
		ClientBackURL: cfg.ECPayClientBackURL,
		AllowedBacks:  cfg.CORSOrigins, // 付款返回網址白名單＝允許的前台來源（如 www.dor.tw）
	}
	ecpayProdCfg := &payment.Config{
		MerchantID:    cfg.ECPayProdMerchantID,
		HashKey:       cfg.ECPayProdHashKey,
		HashIV:        cfg.ECPayProdHashIV,
		Env:           "prod",
		ReturnURL:     cfg.ECPayReturnURL,
		ClientBackURL: cfg.ECPayClientBackURL,
		AllowedBacks:  cfg.CORSOrigins,
	}
	payCfg := &payment.MultiConfig{
		Prod:        ecpayProdCfg,
		Stage:       ecpayStageCfg,
		GlobalEnv:   cfg.ECPayEnv,
		ProdOrigins: cfg.ECPayProdOrigins,
	}
	payRepo := payment.NewRepository(pool)
	paymentHandler := payment.NewHandler(payCfg, payRepo, raceSvc)
	// 取消報名審核核准時複用退款核心（見 race.Service.ApproveCancelRequest）；晚繫結注入——
	// payment.NewHandler 建構時需要 raceSvc 當 OrderMarker，兩者互相依賴，只能等這裡都建構好再接起來。
	raceHandler.SetRefundCreator(paymentHandler.CreateRefund)

	// 站內付2.0（VIP 訂閱綁卡，VIP 訂閱 Phase C2）——獨立於上面 AIO 的 MultiConfig，只有單一組憑證
	// （見 config.ECPayBind*），故障安全設計已在 payment.NewBindClient 內：env 非 "prod" 一律用測試站。
	// raceRepo 直接滿足 payment.VipOrderCreator 介面（CreateVipOrder 簽章相同，見該介面註解），
	// 不需要額外轉接層。
	bindClient := payment.NewBindClient(cfg.ECPayBindEnv, cfg.ECPayBindMerchantID, cfg.ECPayBindHashKey, cfg.ECPayBindHashIV)
	bindHandler := payment.NewBindHandler(bindClient, payRepo, pool, raceRepo, mailHandler, cfg.ECPayBindEnv, cfg.ECPayBindReturnURL, cfg.ECPayBindResultURL, cfg.FrontendURL)

	// Ops（每日資料一致性自檢排程：orders/payment_transactions/vip_subscriptions 等金流表的一致性
	// 健檢，異常送 Telegram，比照 bindHandler.RunRenewalLoop 的排程骨架，見 internal/ops/selfcheck.go；
	// 同一個 Handler 也承載每日營運報告排程，見 internal/ops/dailyreport.go）
	opsHandler := ops.NewHandler(pool)
	// 會員活躍度分析（六大區塊每日彙整報告，台灣時間 03:00 排程，見 internal/analytics/schedule.go；
	// 與上面 opsHandler 的 08:00 自檢/營運報告排程分開時段、分開 advisory lock，互不搶跑）。
	analyticsHandler := analytics.NewHandler(pool)
	// IP/流量每日聚合中介層（migration 145，見 internal/middleware/ipdaily.go）：掛載於下方路由 r.Use
	// 區塊，背景 flush loop 於下方 bgCtx 一併啟動。
	ipDailyAgg := middleware.NewIPDailyAggregate(pool)

	// Activity
	actRepo := activity.NewRepository(pool)
	actSvc := activity.NewService(actRepo, raceSvc, rdb, wsManager)
	actHandler := activity.NewHandler(actSvc)

	// Organizer
	orgRepo := organizer.NewRepository(pool)
	orgSvc := organizer.NewService(orgRepo, raceSvc)
	orgHandler := organizer.NewHandler(orgSvc)

	// Partner（跑者充電站：合作商家目錄）
	partnerRepo := partner.NewRepository(pool)
	partnerSvc := partner.NewService(partnerRepo)
	partnerHandler := partner.NewHandler(partnerSvc)

	// Reward（轉盤 + 集點卡）
	rewardRepo := reward.NewRepository(pool)
	rewardSvc := reward.NewService(rewardRepo)
	rewardHandler := reward.NewHandler(rewardSvc)

	// 活動獎勵系統 P1：序號庫存管理（合作商家/序號組/序號匯入去重/清單狀態）
	rewardSerialRepo := rewardserial.NewRepository(pool)
	rewardSerialSvc := rewardserial.NewService(rewardSerialRepo)
	rewardSerialHandler := rewardserial.NewHandler(rewardSerialSvc)

	// 活動獎勵系統 P2：全域即時獎勵模板 CRUD（完成觸發機率 roll 本體 activityreward.RollAndGrant
	// 由 race.Service 在個人挑戰完成 CAS 點直接呼叫，不經過這裡的 HTTP handler）
	activityRewardRepo := activityreward.NewRepository(pool)
	activityRewardSvc := activityreward.NewService(activityRewardRepo)
	activityRewardHandler := activityreward.NewHandler(activityRewardSvc)

	// 環台大富翁（Phase 1：盤面遊戲，扣 GP 擲骰前進）
	monopolyRepo := monopoly.NewRepository(pool)
	monopolySvc := monopoly.NewService(monopolyRepo)
	monopolyHandler := monopoly.NewHandler(monopolySvc)

	// Profile（完賽紀錄 + 個人統計）
	profileHandler := profile.NewHandler(pool, wsManager)

	// Admin 帳號管理 + 各模組權限
	adminAcctHandler := adminacct.NewHandler(pool)

	// 事件任務（日常隨機事件 + Phase B 賽事多人連動）
	eventHandler := event.NewHandler(pool, wsManager)
	// Phase B3：/track/ping 心跳 → 推進排程主動觸發的活躍視窗（race 不 import event，改用注入 callback）
	raceHandler.SetOnActivity(eventHandler.NoteScheduleActivity)
	// 個人任務（跑者生命週期 10 計畫 × 100 天鏈式任務）
	personalHandler := personaltask.NewHandler(pool, wsManager)
	exploreHandler := explore.NewHandler(pool, wsManager)
	// 自主訓練（P1）：課表庫 + 配速等級表，VIP 限定
	trainingHandler := training.NewHandler(pool)
	// 虛擬選手：後台可建立/管理的機器人跑者帳號，用於賽事名額造勢/測試
	virtualRunnerHandler := virtualrunner.NewHandler(virtualrunner.NewRepository(pool))
	appSettingsHandler := appsettings.NewHandler(pool, wsManager)
	// 跑步路線建議（ORS foot-walking 代理；未設 ORS_API_KEY 時端點回 503，前端優雅隱藏）
	routingHandler := routing.NewHandler(os.Getenv("ORS_API_KEY"))

	// Image（圖片上傳，存 Postgres）
	imageHandler := image.NewHandler(image.NewRepository(pool))

	// Strava 運動數據整合
	stravaHandler := integration.NewStravaHandler(
		integration.NewRepository(pool),
		integration.StravaConfig{
			ClientID:           cfg.StravaClientID,
			ClientSecret:       cfg.StravaClientSecret,
			RedirectURI:        cfg.StravaRedirectURI,
			WebhookVerifyToken: cfg.StravaWebhookVerifyToken,
			FrontendURL:        cfg.FrontendURL,
			JWTSecret:          cfg.JWTSecret,
		},
		middleware.RequireAuth(authSvc),
		rdb,
	)

	// Terra 聚合器（Phase 0 骨架）：一條 webhook 收 Garmin/COROS/Strava 正規化活動。
	// 未設定 TERRA_SIGNING_SECRET → enabled()=false，webhook 只 ack 不處理。
	terraHandler := integration.NewTerraHandler(
		integration.NewRepository(pool),
		integration.TerraConfig{
			DevID:         os.Getenv("TERRA_DEV_ID"),
			APIKey:        os.Getenv("TERRA_API_KEY"),
			SigningSecret: os.Getenv("TERRA_SIGNING_SECRET"),
		},
		middleware.RequireAuth(authSvc),
	)

	// COROS 手錶直連：OAuth2 連接 + webhook 收活動。未設定 COROS_CLIENT_ID/SECRET → enabled()=false，
	// webhook 只 ack 不處理。token 加密沿用 STRAVA_TOKEN_KEY（跨 provider 共用金鑰，見 repository.go）。
	corosHandler := integration.NewCorosHandler(
		integration.NewRepository(pool),
		integration.CorosConfig{
			ClientID:     cfg.CorosClientID,
			ClientSecret: cfg.CorosClientSecret,
			RedirectURI:  cfg.CorosRedirectURI,
			FrontendURL:  cfg.FrontendURL,
			JWTSecret:    cfg.JWTSecret,
		},
		middleware.RequireAuth(authSvc),
		rdb,
	)

	// SMTP Email（推播擴充的 email 頻道用）：未設 SMTP_HOST/SMTP_FROM 時 enabled=false，發送 no-op。
	smtpPort, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
	mailerInst := mailer.NewMailer(mailer.Config{
		Host: os.Getenv("SMTP_HOST"),
		Port: smtpPort,
		User: os.Getenv("SMTP_USER"),
		Pass: os.Getenv("SMTP_PASS"),
		From: os.Getenv("SMTP_FROM"),
	})

	// Web Push（VAPID）：未設齊 VAPID_PUBLIC_KEY/VAPID_PRIVATE_KEY/VAPID_SUBJECT 時 enabled=false，發送 no-op。
	pushHandler := push.NewHandler(pool, push.Config{
		PublicKey:  os.Getenv("VAPID_PUBLIC_KEY"),
		PrivateKey: os.Getenv("VAPID_PRIVATE_KEY"),
		Subject:    os.Getenv("VAPID_SUBJECT"),
	}, mailerInst, mailHandler)

	// Email 廣播（後台向全部玩家發送，Resend）：未設 RESEND_API_KEY 時 notify.SendEmailBatch 回
	// ErrEmailNotConfigured，後台顯示「Email 服務未設定」（migration 141）。
	emailBroadcastHandler := emailbroadcast.NewHandler(pool, cfg.JWTSecret, cfg.FrontendURL)

	// Telegram 告警：未設 TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID 時 notify.Alert 靜默 no-op，
	// 正式環境會收不到關鍵錯誤告警（5xx激增/panic/金流結算失敗等），部署時務必記得設定。
	if os.Getenv("TELEGRAM_BOT_TOKEN") == "" {
		log.Warn().Msg("TG 告警未設定（TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID 未設），正式環境建議設定以接收關鍵錯誤告警")
	}

	// --- 路由 ---
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	// panic 告警：需掛在 Recoverer 之後（更內層），才能在 Recoverer 吞掉 panic 之前先攔截到並送出 Telegram。
	r.Use(middleware.PanicAlert)
	r.Use(chimiddleware.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-ID"},
		AllowCredentials: true,
	}))
	// PERF-M3：Go 層應用層 gzip/br 壓縮（不假設 Railway edge 有補），涵蓋 JSON/HTML/文字等預設類型。
	r.Use(chimiddleware.Compress(5))
	// SEC-M4：全域 request body 大小上限（1MB），避免超大 JSON payload 撐爆記憶體。
	// 圖片/音檔上傳端點各自已有自己的 5MB MaxBytesReader，排除在外（見 bodylimit.go 註解）。
	r.Use(middleware.MaxBodyBytes(1<<20,
		"/api/v1/admin/images", "/api/v1/profile/avatar",
		"/api/v1/admin/personal-tasks/import", // xlsx 轉 JSON 整包匯入，見 middleware.MaxBodyBytes 註解
	))
	// 5xx 聚合告警：短時間內大量 5xx 觸發一次 Telegram（避免每次 5xx 各自洗版）。
	r.Use(middleware.FiveXXAlert)
	// IP/流量每日聚合（migration 145）：in-memory 累計每請求 IP/國家/狀態碼，背景每 5 分鐘批次寫入
	// ops_ip_daily，供 opsHandler 每日報告的「流量安全」區塊與異常流量巡檢使用（見 internal/ops/
	// dailyreport.go）。掛在 FiveXXAlert 之後，順序本身不影響行為（兩者互相獨立、各自量測狀態碼）。
	r.Use(ipDailyAgg.Middleware)

	// Health check：不碰 DB（避免外部監控/部署探針每次喚醒 Neon compute）。DB 就緒檢查改走 /health/db。
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	r.Get("/health/db", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, `{"status":"db_down"}`, http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// 版號（公開）：v<base>.<commit>
		r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"version":%q,"base":%q,"serial":%q,"commit":%q}`, version.Full(), version.Base, version.Serial, version.Commit())
		})

		// --- 公開端點 ---
		// SEC-H1：登入/註冊/refresh/Google 皆為公開端點、無帳號可綁，以 IP 維度節流
		// （各自獨立 10/min，防暴破/憑證填充/refresh 濫用）——但 IP 維度依賴 chi RealIP，
		// 可被客戶端偽造的 X-Forwarded-For/X-Real-IP/True-Client-IP 繞過（SEC-M8，
		// Cloudflare 上線前無法根治，見 middleware.ClientIP 註解）。
		// 因此 login/register/google 再疊加「帳號維度」限流（10 次/5min，見
		// middleware.AccountField/GoogleIDTokenAccount）：以 body 內的 email/id_token
		// 帳號當 key，不可被 header 偽造繞過，即使攻擊者每次都換 IP/偽造標頭，
		// 對同一目標帳號（尤其 admin，/auth/login 與一般會員共用、無帳號級鎖定）的
		// 暴力破解仍會被擋下。refresh 的憑證是不可猜測的 token 本身，不需要帳號維度。
		r.Route("/auth", func(r chi.Router) {
			r.With(
				middleware.RateLimit(rdb, "auth_register", 10, time.Minute, middleware.ClientIP),
				middleware.RateLimit(rdb, "auth_register_acct", 10, 5*time.Minute, middleware.AccountField("email")),
			).Post("/register", authHandler.Register)
			r.With(
				middleware.RateLimit(rdb, "auth_login", 10, time.Minute, middleware.ClientIP),
				middleware.RateLimit(rdb, "auth_login_acct", 10, 5*time.Minute, middleware.AccountField("email")),
			).Post("/login", authHandler.Login)
			r.With(
				middleware.RateLimit(rdb, "auth_google", 10, time.Minute, middleware.ClientIP),
				middleware.RateLimit(rdb, "auth_google_acct", 10, 5*time.Minute, middleware.GoogleIDTokenAccount),
			).Post("/google", authHandler.Google)
			r.With(middleware.RateLimit(rdb, "auth_refresh", 10, time.Minute, middleware.ClientIP)).Post("/refresh", authHandler.Refresh)
			r.With(middleware.RequireAuth(authSvc)).Delete("/logout", authHandler.Logout)
			r.With(middleware.RequireAuth(authSvc)).Get("/me", authHandler.Me)
		})

		// 賽事列表和詳情（公開，登入後附帶報名狀態）
		r.With(middleware.OptionalAuth(authSvc)).Mount("/races", raceHandler.Router())

		// 跑者充電站（合作商家目錄，公開，登入後附帶收藏狀態）
		r.With(middleware.OptionalAuth(authSvc)).Mount("/partner-shops", partnerHandler.PublicRouter())

		// 百里英雄榜（累積里程 >= 100km 前 100 名，公開，登入後附帶追蹤/本人狀態）
		r.With(middleware.OptionalAuth(authSvc)).Get("/heroes/hundred", profileHandler.HundredHeroes)

		// 圖片取用（公開）
		r.Mount("/images", imageHandler.PublicRouter())

		// 全站外觀設定（公開讀取，前台會員面板底圖等）
		r.Get("/settings", profileHandler.GetSettings)

		// 通用系統設定的公開白名單（前台外觀，如 active_skin）
		r.Get("/app-settings/public", appSettingsHandler.Public)

		// 蓋板廣告（前台開啟時彈出）— 公開讀取，受總開關 interstitial_enabled 控制
		r.Get("/interstitial", appSettingsHandler.PublicInterstitial)

		// Strava 整合（callback/webhook 公開；connect/status/disconnect 由 router 內自帶登入）
		// SEC-H1/H4：webhook（per-owner）與 /sync（per-user）節流已在 StravaHandler 內部用注入的
		// rdb 做（見 internal/integration/strava.go allowRate），此處不再疊加粗粒度 mount 級節流，
		// 避免連坐節流到同掛載下呼叫頻率高的 /status、/connect。
		r.Mount("/integrations/strava", stravaHandler.Router())
		r.Mount("/integrations/terra", terraHandler.Router())
		r.Mount("/integrations/coros", corosHandler.Router())

		// 綠界付款結果通知（公開，server 對 server，自帶 CheckMacValue 驗章）
		r.Post("/payments/ecpay/notify", paymentHandler.Notify)

		// 站內付2.0 VIP 訂閱綁卡雙 callback（公開；ReturnURL 是 server-to-server AES-JSON，
		// OrderResultURL 是瀏覽器 3D 驗證完成後的一次性 Form POST 導回，見 BindHandler 註解）
		r.Post("/payments/ecpay/bind/notify", bindHandler.Notify)
		r.Post("/payments/ecpay/bind/result", bindHandler.Result)

		// Email 廣播退訂連結（公開，信件內點擊，HMAC token 驗證取代登入）
		r.Mount("/email", emailBroadcastHandler.PublicRouter())

		// --- 需要登入的端點 ---
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(authSvc))

			// 綠界結帳（產生付款表單參數）— SEC-H1：防灌單/重複觸發金流
			r.With(middleware.RateLimit(rdb, "payment_checkout", 10, time.Minute, middleware.UserOrIP)).
				Post("/payments/ecpay/checkout", paymentHandler.Checkout)

			// VIP 訂閱發起 + 綁卡完成（VIP 訂閱 Phase C2）— SEC-H1：同上，防灌單/重複觸發金流
			r.With(middleware.RateLimit(rdb, "vip_subscribe", 10, time.Minute, middleware.UserOrIP)).
				Post("/profile/vip/subscribe", bindHandler.Subscribe)
			r.With(middleware.RateLimit(rdb, "vip_bind_complete", 10, time.Minute, middleware.UserOrIP)).
				Post("/profile/vip/bind-card/complete", bindHandler.CompleteBindCard)

			// VIP 綁卡卡片管理（Phase E）：查詢/解除本人目前的綠界綁卡
			r.Get("/profile/vip/card", bindHandler.GetCard)
			r.Delete("/profile/vip/card", bindHandler.DeleteCard)

			// 活動上傳 — SEC-H1：整個掛載子路由共用一組節流（含讀取），數字放寬避免誤擋正常瀏覽
			r.With(middleware.RateLimit(rdb, "activities", 60, time.Minute, middleware.UserOrIP)).
				Mount("/activities", actHandler.Router())

			// 跑步中心跳（後台總覽的「目前在跑名單」用）
			r.Post("/track/ping", raceHandler.Ping)

			// 打卡點任務（geofence check-in）— SEC-H1
			r.With(middleware.RateLimit(rdb, "checkpoints", 30, time.Minute, middleware.UserOrIP)).
				Mount("/checkpoints", raceHandler.CheckpointRouter())

			// 事件任務（日常隨機事件）— 跑步引擎用 — SEC-H1：比照 /activities 節流，避免高頻輪詢灌爆
			r.With(middleware.RateLimit(rdb, "events", 60, time.Minute, middleware.UserOrIP)).
				Mount("/events", eventHandler.Router())
			// 賽事多人連動事件（Phase B）— 觸發/加入/完成 — SEC-H1：同上
			r.With(middleware.RateLimit(rdb, "events_race", 60, time.Minute, middleware.UserOrIP)).
				Mount("/events/race", eventHandler.RaceRouter())
			// 效果資產覆寫（前台跑步引擎讀正式圖片/音檔）
			r.Get("/effect-assets", eventHandler.PublicEffectAssets)

			// 個人任務（跑者生命週期計畫）— 讀計畫/任務 + 手動完成
			r.Mount("/personal-tasks", personalHandler.Router())
			r.Mount("/explore", exploreHandler.Router())
			// 自主訓練（P1）：課表庫 + 配速等級表（VIP 限定，handler 內判定）
			r.Mount("/training", trainingHandler.Router())
			r.Mount("/route", routingHandler.Router()) // 跑步路線建議（ORS foot-walking 代理）

			// 獎勵系統（轉盤 + 集點卡）
			r.Mount("/rewards", rewardHandler.Router())

			// 個人資料（完賽紀錄 + 統計）
			r.Mount("/profile", profileHandler.Router())

			// 頭像上傳（重用圖片上傳，登入即可）
			r.Post("/profile/avatar", imageHandler.Upload)

			// 取消報名申請/撤回（審核由後台 /admin/cancel-requests 進行）
			r.Post("/profile/registrations/{registrationID}/cancel-request", raceHandler.CreateCancelRequest)
			r.Delete("/profile/registrations/{registrationID}/cancel-request", raceHandler.WithdrawCancelRequest)

			// 跑者充電站收藏（比照 follow）
			r.Mount("/profile/partner-favorites", partnerHandler.FavoriteRouter())

			// 環台大富翁（Phase 1：盤面遊戲）— SEC-H1：扣 GP 擲骰是主要濫用面，整個子路由共用節流
			r.With(middleware.RateLimit(rdb, "monopoly", 30, time.Minute, middleware.UserOrIP)).
				Mount("/monopoly", monopolyHandler.Router())

			// Web Push 訂閱（VAPID 金鑰 + subscribe/unsubscribe）
			r.Mount("/push", pushHandler.Router())

			// 站內信（鈴鐺列表 + 未讀數 + 標記已讀）
			r.Mount("/mail", mailHandler.Router())
		})

		// --- 合作方端點（需 organizer 或 admin role）---
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(authSvc))
			r.Use(middleware.RequireOrganizer)
			r.Mount("/organizer", orgHandler.OrganizerRouter())
		})

		// --- Admin 端點（需 admin role；各模組再依 adminacct 權限把關）---
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(authSvc))
			r.Use(middleware.RequireAdmin)
			r.Use(adminAcctHandler.Audit) // 自動記錄異動操作（在身分確認之後）
			perm := adminAcctHandler.RequirePerm
			// 自己的身分與權限（任何 admin 皆可讀，前台用來決定選單）
			r.Get("/admin/me", adminAcctHandler.Me)
			// 數據總覽（任何 admin 皆可讀）
			r.Get("/admin/overview", raceHandler.AdminOverview)
			// 管理者管理 + 操作紀錄（僅超級管理員）
			r.With(adminAcctHandler.RequireSuper).Mount("/admin/admins", adminAcctHandler.Router())
			r.With(adminAcctHandler.RequireSuper).Get("/admin/audit", adminAcctHandler.AuditList)

			r.With(perm("races")).Mount("/admin/races", raceHandler.AdminRouter())
			r.With(perm("races")).Mount("/admin/group-presets", raceHandler.PresetRouter())
			r.With(perm("races")).Put("/admin/reward-completions/{regID}", raceHandler.AdminUpdateRewardCompletion) // 個人挑戰模式 P5：獎勵發放狀態
			r.With(perm("races")).Patch("/admin/reward-winners/{id}", raceHandler.AdminUpdateRewardWinner)          // 獎勵管理一般化 migration 135：中獎發放狀態
			r.With(perm("tasks")).Mount("/admin/task-modules", raceHandler.TaskModuleRouter())
			r.With(perm("event_tasks")).Mount("/admin/events", eventHandler.AdminRouter())
			r.With(perm("event_tasks")).Mount("/admin/event-races", eventHandler.RaceAdminRouter())
			r.With(perm("event_tasks")).Mount("/admin/effect-assets", eventHandler.EffectAssetsRouter())
			r.With(perm("event_tasks")).Mount("/admin/personal-tasks", personalHandler.AdminRouter())
			r.With(perm("event_tasks")).Mount("/admin/explore", exploreHandler.AdminRouter())
			r.With(perm("settings")).Mount("/admin/app-settings", appSettingsHandler.AdminRouter())
			r.With(perm("settings")).Mount("/admin/interstitial", appSettingsHandler.InterstitialAdminRouter())
			r.With(perm("settings")).Mount("/admin/test-whitelist", raceHandler.TestWhitelistRouter())
			r.Mount("/admin/images", imageHandler.AdminRouter()) // 共用工具，任何 admin 可上傳
			r.With(perm("signups")).Mount("/admin/signups", raceHandler.SignupRouter())
			r.With(perm("orders")).Mount("/admin/orders", raceHandler.OrderRouter())
			r.With(perm("orders")).Mount("/admin/payments", paymentHandler.AdminRouter())                  // 退款（沿用 orders 權限）
			r.With(perm("orders")).Mount("/admin/cancel-requests", raceHandler.CancelRequestAdminRouter()) // 取消報名審核（沿用 orders 權限）
			r.With(perm("promo")).Mount("/admin/promo-codes", promoHandler.Router())
			r.With(perm("members")).Mount("/admin/members", profileHandler.AdminMembersRouter())
			r.With(perm("members")).Get("/admin/vip-analytics", profileHandler.AdminVipAnalytics)
			r.With(perm("members")).Get("/admin/login-logs", authHandler.AdminLoginLogs) // 用戶登入紀錄（沿用 members 權限）
			r.With(perm("settings")).Mount("/admin/membership", profileHandler.MembershipAdminRouter())
			r.With(perm("settings")).Mount("/admin/vip-promos", profileHandler.VipPromoAdminRouter())
			r.With(perm("titles")).Mount("/admin/titles", profileHandler.TitleAdminRouter())
			r.With(perm("training")).Mount("/admin/training", trainingHandler.AdminRouter())
			r.With(perm("virtual")).Mount("/admin/virtual-runners", virtualRunnerHandler.AdminRouter())
			r.With(perm("settings")).Get("/admin/data-source-metrics", profileHandler.AdminDataSourceMetrics)
			r.With(perm("settings")).Get("/admin/signup-stats", profileHandler.AdminSignupStats) // 推廣連結頁「成效統計」：各通路週別趨勢＋彙總
			r.With(perm("organizer")).Mount("/admin/organizer", orgHandler.AdminOrganizerRouter())
			r.With(perm("partners")).Mount("/admin/partner-shops", partnerHandler.AdminRouter())
			r.With(perm("monopoly")).Mount("/admin/monopoly", monopolyHandler.AdminRouter())
			r.With(perm("rewards")).Mount("/admin/reward-merchants", rewardSerialHandler.MerchantRouter())
			r.With(perm("rewards")).Mount("/admin/reward-groups", rewardSerialHandler.GroupRouter())
			r.With(perm("rewards")).Mount("/admin/reward-templates", activityRewardHandler.TemplateRouter())
			r.With(perm("rewards")).Mount("/admin/event-coupons", activityRewardHandler.CouponDefRouter())
			r.With(perm("settings")).Put("/admin/settings", profileHandler.PutSettings)
			r.With(perm("gps_review")).Post("/admin/activities/add-mileage", actHandler.AdminAddMileage)
			r.With(perm("gps_review")).Mount("/admin/gps-runs", actHandler.AdminRouter())
			r.With(perm("gps_review")).Mount("/admin/checkin-review", raceHandler.CheckinReviewRouter())
			r.With(perm("settings")).Mount("/admin/push", pushHandler.AdminRouter())
			r.With(perm("settings")).Mount("/admin/push-groups", pushHandler.GroupAdminRouter())
			r.With(perm("settings")).Mount("/admin/email-broadcasts", emailBroadcastHandler.AdminRouter())
			r.With(perm("settings")).Post("/admin/ops/selfcheck", opsHandler.SelfCheckNow)
			r.With(perm("settings")).Post("/admin/ops/dailyreport", opsHandler.DailyReportNow)
			r.With(perm("analytics")).Get("/admin/analytics/report", analyticsHandler.GetReport)
			r.With(perm("analytics")).Post("/admin/analytics/recompute", analyticsHandler.Recompute)
		})
	})

	// WebSocket 端點（WS 無法送 Authorization header，改用 query param token）
	r.Get("/ws/race/{raceID}", func(w http.ResponseWriter, r *http.Request) {
		raceID := chi.URLParam(r, "raceID")
		userID := ""
		if token := r.URL.Query().Get("token"); token != "" {
			if claims, err := authSvc.ValidateAccessToken(r.Context(), token); err == nil {
				userID = claims.UserID
			}
		}
		wsManager.ServeWS(w, r, raceID, userID)
	})

	// 全站推播端點（data_updated 快取失效通知）：raceID 固定為 "global"，複用既有 Hub 機制。
	// 與 /ws/race 不同：這裡要擋匿名連線，無效/缺 token 一律 401，不 upgrade。
	r.Get("/ws/site", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := authSvc.ValidateAccessToken(r.Context(), token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		wsManager.ServeWS(w, r, "global", claims.UserID)
	})

	// 啟動伺服器
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // WebSocket 需設為 0（長連線）
		IdleTimeout:  120 * time.Second,
	}

	// 背景：定期清理逾時未完成的多人事件參與者（Phase B auto-expire）
	bgCtx, bgCancel := context.WithCancel(context.Background())
	go eventHandler.RunExpiryLoop(bgCtx)
	// 背景：Phase B3 排程主動觸發（到點且有人在跑才建立 collective 事件實例）
	go eventHandler.RunScheduleLoop(bgCtx)
	// 背景：VIP 訂閱 Phase D 每日續約排程（到期前 1 天起用綁定卡背景扣款，3 天寬限×最多 3 次重試）
	go bindHandler.RunRenewalLoop(bgCtx)
	// 背景：參賽虛擬獎勵排程（migration 140）——已開賽的賽事每 5 分鐘掃描一次，把設定的虛擬獎勵發給
	// 所有已報名(paid)者（不看任務條件，人人有獎）
	go raceSvc.RunEntryRewardLoop(bgCtx)
	// 背景：每日資料一致性自檢排程（台灣時間 08:00-08:59 執行一次；金流/報名表健檢異常送 Telegram）
	go opsHandler.RunSelfCheckLoop(bgCtx)
	// 背景：每日營運報告排程（同一 08:00-08:59 執行窗口，固定發送，不論當天有無異常；見
	// internal/ops/dailyreport.go）
	go opsHandler.RunDailyReportLoop(bgCtx)
	// 背景：會員活躍度分析每日排程（台灣時間 03:00-03:59 執行窗口，彙整六大區塊存進
	// member_analytics_reports；啟動時若最新報告超過 25h 未算會先補跑一次，見
	// internal/analytics/schedule.go）
	go analyticsHandler.RunLoop(bgCtx)
	// 背景：IP/流量每日聚合 flush（每 5 分鐘批次寫入 ops_ip_daily + 每天順手清理 30 天前舊資料）
	go ipDailyAgg.Run(bgCtx)
	// 背景：虛擬選手數據生成引擎 Phase 2（對齊台灣整點 H∈{5,6,7,20,21,22,23}，替 enabled 選手
	// 自動生成 window_hour=H-1 這個活躍時段的活動；天氣/機率/防重寫入見 internal/virtualrunner/generator.go）
	go virtualrunner.NewGenerator(pool).RunGenerateLoop(bgCtx)

	go func() {
		log.Info().Str("port", cfg.Port).Msg("DOR API server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	bgCancel() // 停止背景清理

	log.Info().Msg("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	log.Info().Msg("server stopped")
}
