package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/activity"
	"github.com/dor/api/internal/adminacct"
	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/cache"
	"github.com/dor/api/internal/config"
	"github.com/dor/api/internal/db"
	"github.com/dor/api/internal/event"
	"github.com/dor/api/internal/explore"
	"github.com/dor/api/internal/personaltask"
	"github.com/dor/api/internal/image"
	"github.com/dor/api/internal/integration"
	"github.com/dor/api/internal/middleware"
	"github.com/dor/api/internal/organizer"
	"github.com/dor/api/internal/payment"
	"github.com/dor/api/internal/profile"
	"github.com/dor/api/internal/promo"
	"github.com/dor/api/internal/race"
	"github.com/dor/api/internal/realtime"
	"github.com/dor/api/internal/reward"
	"github.com/dor/api/internal/version"
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

	// Promo（優惠序號）
	promoRepo := promo.NewRepository(pool)
	promoSvc := promo.NewService(promoRepo)
	promoHandler := promo.NewHandler(promoSvc)

	// Race
	raceRepo := race.NewRepository(pool)
	raceSvc := race.NewService(raceRepo, rdb, promoSvc)
	raceHandler := race.NewHandler(raceSvc, wsManager)

	// Payment（綠界 ECPay）
	payCfg := &payment.Config{
		MerchantID:    cfg.ECPayMerchantID,
		HashKey:       cfg.ECPayHashKey,
		HashIV:        cfg.ECPayHashIV,
		Env:           cfg.ECPayEnv,
		ReturnURL:     cfg.ECPayReturnURL,
		ClientBackURL: cfg.ECPayClientBackURL,
		AllowedBacks:  cfg.CORSOrigins, // 付款返回網址白名單＝允許的前台來源（含 www.dor.tw / dor.hero-mi.com）
	}
	paymentHandler := payment.NewHandler(payCfg, payment.NewRepository(pool), raceSvc)

	// Activity
	actRepo := activity.NewRepository(pool)
	actSvc := activity.NewService(actRepo, raceSvc, rdb, wsManager)
	actHandler := activity.NewHandler(actSvc)

	// Organizer
	orgRepo := organizer.NewRepository(pool)
	orgSvc := organizer.NewService(orgRepo, raceSvc)
	orgHandler := organizer.NewHandler(orgSvc)

	// Reward（轉盤 + 集點卡）
	rewardRepo := reward.NewRepository(pool)
	rewardSvc := reward.NewService(rewardRepo)
	rewardHandler := reward.NewHandler(rewardSvc)

	// Profile（完賽紀錄 + 個人統計）
	profileHandler := profile.NewHandler(pool, wsManager)

	// Admin 帳號管理 + 各模組權限
	adminAcctHandler := adminacct.NewHandler(pool)

	// 事件任務（日常隨機事件）
	eventHandler := event.NewHandler(pool, wsManager)
	// 個人任務（跑者生命週期 10 計畫 × 100 天鏈式任務）
	personalHandler := personaltask.NewHandler(pool, wsManager)
	exploreHandler := explore.NewHandler(pool, wsManager)
	appSettingsHandler := appsettings.NewHandler(pool, wsManager)

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

	// --- 路由 ---
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-ID"},
		AllowCredentials: true,
	}))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
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
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", authHandler.Register)
			r.Post("/login", authHandler.Login)
			r.Post("/google", authHandler.Google)
			r.Post("/refresh", authHandler.Refresh)
			r.With(middleware.RequireAuth(authSvc)).Delete("/logout", authHandler.Logout)
			r.With(middleware.RequireAuth(authSvc)).Get("/me", authHandler.Me)
		})

		// 賽事列表和詳情（公開，登入後附帶報名狀態）
		r.With(middleware.OptionalAuth(authSvc)).Mount("/races", raceHandler.Router())

		// 圖片取用（公開）
		r.Mount("/images", imageHandler.PublicRouter())

		// 全站外觀設定（公開讀取，前台會員面板底圖等）
		r.Get("/settings", profileHandler.GetSettings)

		// 通用系統設定的公開白名單（前台外觀，如 active_skin）
		r.Get("/app-settings/public", appSettingsHandler.Public)

		// 蓋板廣告（前台開啟時彈出）— 公開讀取，受總開關 interstitial_enabled 控制
		r.Get("/interstitial", appSettingsHandler.PublicInterstitial)

		// Strava 整合（callback/webhook 公開；connect/status/disconnect 由 router 內自帶登入）
		r.Mount("/integrations/strava", stravaHandler.Router())
		r.Mount("/integrations/terra", terraHandler.Router())

		// 綠界付款結果通知（公開，server 對 server，自帶 CheckMacValue 驗章）
		r.Post("/payments/ecpay/notify", paymentHandler.Notify)

		// --- 需要登入的端點 ---
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(authSvc))

			// 綠界結帳（產生付款表單參數）
			r.Post("/payments/ecpay/checkout", paymentHandler.Checkout)

			// 活動上傳
			r.Mount("/activities", actHandler.Router())

			// 跑步中心跳（後台總覽的「目前在跑名單」用）
			r.Post("/track/ping", raceHandler.Ping)

			// 打卡點任務（geofence check-in）
			r.Mount("/checkpoints", raceHandler.CheckpointRouter())

			// 事件任務（日常隨機事件）— 跑步引擎用
			r.Mount("/events", eventHandler.Router())
			// 賽事多人連動事件（Phase B）— 觸發/加入/完成
			r.Mount("/events/race", eventHandler.RaceRouter())
			// 效果資產覆寫（前台跑步引擎讀正式圖片/音檔）
			r.Get("/effect-assets", eventHandler.PublicEffectAssets)

			// 個人任務（跑者生命週期計畫）— 讀計畫/任務 + 手動完成
			r.Mount("/personal-tasks", personalHandler.Router())
			r.Mount("/explore", exploreHandler.Router())

			// 獎勵系統（轉盤 + 集點卡）
			r.Mount("/rewards", rewardHandler.Router())

			// 個人資料（完賽紀錄 + 統計）
			r.Mount("/profile", profileHandler.Router())

			// 頭像上傳（重用圖片上傳，登入即可）
			r.Post("/profile/avatar", imageHandler.Upload)
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
			r.With(perm("promo")).Mount("/admin/promo-codes", promoHandler.Router())
			r.With(perm("members")).Mount("/admin/members", profileHandler.AdminMembersRouter())
			r.With(perm("members")).Get("/admin/vip-analytics", profileHandler.AdminVipAnalytics)
			r.With(perm("settings")).Mount("/admin/membership", profileHandler.MembershipAdminRouter())
			r.With(perm("settings")).Mount("/admin/vip-promos", profileHandler.VipPromoAdminRouter())
			r.With(perm("settings")).Get("/admin/data-source-metrics", profileHandler.AdminDataSourceMetrics)
			r.With(perm("organizer")).Mount("/admin/organizer", orgHandler.AdminOrganizerRouter())
			r.With(perm("settings")).Put("/admin/settings", profileHandler.PutSettings)
			r.With(perm("gps_review")).Post("/admin/activities/add-mileage", actHandler.AdminAddMileage)
			r.With(perm("gps_review")).Mount("/admin/gps-runs", actHandler.AdminRouter())
			r.With(perm("gps_review")).Mount("/admin/checkin-review", raceHandler.CheckinReviewRouter())
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
