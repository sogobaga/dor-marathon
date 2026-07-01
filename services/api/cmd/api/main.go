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
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/cache"
	"github.com/dor/api/internal/config"
	"github.com/dor/api/internal/db"
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
	raceHandler := race.NewHandler(raceSvc)

	// Payment（綠界 ECPay）
	payCfg := &payment.Config{
		MerchantID:    cfg.ECPayMerchantID,
		HashKey:       cfg.ECPayHashKey,
		HashIV:        cfg.ECPayHashIV,
		Env:           cfg.ECPayEnv,
		ReturnURL:     cfg.ECPayReturnURL,
		ClientBackURL: cfg.ECPayClientBackURL,
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
	profileHandler := profile.NewHandler(pool)

	// Admin 帳號管理 + 各模組權限
	adminAcctHandler := adminacct.NewHandler(pool)

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

		// Strava 整合（callback/webhook 公開；connect/status/disconnect 由 router 內自帶登入）
		r.Mount("/integrations/strava", stravaHandler.Router())

		// 綠界付款結果通知（公開，server 對 server，自帶 CheckMacValue 驗章）
		r.Post("/payments/ecpay/notify", paymentHandler.Notify)

		// --- 需要登入的端點 ---
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(authSvc))

			// 綠界結帳（產生付款表單參數）
			r.Post("/payments/ecpay/checkout", paymentHandler.Checkout)

			// 活動上傳
			r.Mount("/activities", actHandler.Router())

			// 打卡點任務（geofence check-in）
			r.Mount("/checkpoints", raceHandler.CheckpointRouter())

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
			perm := adminAcctHandler.RequirePerm
			// 自己的身分與權限（任何 admin 皆可讀，前台用來決定選單）
			r.Get("/admin/me", adminAcctHandler.Me)
			// 管理者管理（僅超級管理員）
			r.With(adminAcctHandler.RequireSuper).Mount("/admin/admins", adminAcctHandler.Router())

			r.With(perm("races")).Mount("/admin/races", raceHandler.AdminRouter())
			r.With(perm("races")).Mount("/admin/group-presets", raceHandler.PresetRouter())
			r.With(perm("tasks")).Mount("/admin/task-modules", raceHandler.TaskModuleRouter())
			r.With(perm("settings")).Mount("/admin/test-whitelist", raceHandler.TestWhitelistRouter())
			r.Mount("/admin/images", imageHandler.AdminRouter()) // 共用工具，任何 admin 可上傳
			r.With(perm("signups")).Mount("/admin/signups", raceHandler.SignupRouter())
			r.With(perm("orders")).Mount("/admin/orders", raceHandler.OrderRouter())
			r.With(perm("promo")).Mount("/admin/promo-codes", promoHandler.Router())
			r.With(perm("members")).Mount("/admin/members", profileHandler.AdminMembersRouter())
			r.With(perm("settings")).Mount("/admin/membership", profileHandler.MembershipAdminRouter())
			r.With(perm("organizer")).Mount("/admin/organizer", orgHandler.AdminOrganizerRouter())
			r.With(perm("settings")).Put("/admin/settings", profileHandler.PutSettings)
			r.With(perm("gps_review")).Post("/admin/activities/add-mileage", actHandler.AdminAddMileage)
			r.With(perm("gps_review")).Mount("/admin/gps-runs", actHandler.AdminRouter())
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

	// 啟動伺服器
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // WebSocket 需設為 0（長連線）
		IdleTimeout:  120 * time.Second,
	}

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

	log.Info().Msg("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	log.Info().Msg("server stopped")
}
