package auth

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/api/idtoken"

	"github.com/dor/api/internal/notify"
)

var (
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrEmailTaken          = errors.New("email already registered")
	ErrHandleTaken         = errors.New("handle already taken")
	ErrTokenInvalid        = errors.New("token invalid or expired")
	ErrGoogleNotConfigured = errors.New("google login not configured")
	ErrGoogleTokenInvalid  = errors.New("invalid google id token")
	// ErrSessionSuperseded：refresh token 的 session epoch 與帳號目前 epoch 不符——
	// 代表帳號已在別處重新登入（單一登入強制踢舊裝置），這顆 refresh token 已失效。
	ErrSessionSuperseded = errors.New("session superseded by a newer login")
)

// Acq 前端 lib/acquisition.ts 於 App 首次進站（first-touch）擷取的來源歸因原始輸入，隨
// Register/Google 請求選填夾帶；只在「新建用戶」路徑才會被用到（見 Repository.Create /
// CreateGoogleUser），交給 internal/attribution.Classify 判斷本次註冊來源。皆可能為空字串
// （前端尚未部署、使用者關閉 localStorage、或走既有帳號登入分支等情形）——零值即代表無歸因資訊。
type Acq struct {
	LandingURL  string
	ReferrerURL string
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`    // 秒
	SessionEpoch int    `json:"session_epoch"` // 單一登入：本組 token 所屬的 session_epoch
}

type Claims struct {
	UserID string `json:"uid"`
	Role   string `json:"role"` // user | organizer | admin
	// SessionEpoch 單一登入強制用：舊 token（簽發時沒有此欄位）JSON 解析後為零值 0，
	// 與帳號 session_epoch 預設值 0 相容（legacy token 在帳號從未被新登入踢過的情況下仍可用）。
	SessionEpoch int `json:"sev"`
	// Typ 標記這顆 token 的用途："access" 或 "refresh"（H1 修法：token 用途區分，避免短效 access
	// token 被拿去當長效 refresh token 用、或反過來）。本次修法上線前簽出的 token 沒有這個欄位，
	// JSON 解析後為零值 ""——ValidateAccessToken 與 Refresh 都給 legacyRefreshCutoff 為止的寬限期
	// 相容零值（見 isAcceptableAccessTyp／isAcceptableRefreshTyp），讓部署當下已登入、尚未換上
	// 新 typ token 的 session（含後台不勾選「保持登入」、完全沒有 refresh token 可換的那類 session）
	// 不會被立即強制登出。
	Typ string `json:"typ,omitempty"`
	jwt.RegisteredClaims
}

type Service struct {
	repo           *Repository
	rdb            *redis.Client
	jwtSecret      []byte
	accessTTL      time.Duration
	refreshTTL     time.Duration
	googleClientID string
}

func NewService(repo *Repository, rdb *redis.Client, jwtSecret string, accessTTL, refreshTTL time.Duration, googleClientID string) *Service {
	return &Service{
		repo:           repo,
		rdb:            rdb,
		jwtSecret:      []byte(jwtSecret),
		accessTTL:      accessTTL,
		refreshTTL:     refreshTTL,
		googleClientID: googleClientID,
	}
}

// Register 建立新使用者（role 由呼叫方指定：user 或 organizer）。refCode 為可選的推薦碼
// （?ref=<code> 連結帶入），交給 repo.Create 綁定推薦關係（見 internal/referral.BindReferrer）；
// acq 為可選的來源歸因原始輸入，一併交給 repo.Create 記錄（見 internal/attribution.Record），
// 失敗只 log 絕不影響本函式的回傳結果——不需要在此處理錯誤分支。
func (s *Service) Register(ctx context.Context, email, handle, name, password, role, refCode string, acq Acq) (*User, *TokenPair, error) {
	if role == "" {
		role = "user"
	}
	// 安全限制：只允許透過此 API 建立 user 和 organizer，admin 需由 DB 直接設定
	if role != "user" && role != "organizer" {
		role = "user"
	}

	if ok, _ := s.repo.EmailExists(ctx, email); ok {
		return nil, nil, ErrEmailTaken
	}
	if ok, _ := s.repo.HandleExists(ctx, handle); ok {
		return nil, nil, ErrHandleTaken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, nil, fmt.Errorf("hash password: %w", err)
	}

	user, err := s.repo.Create(ctx, email, handle, name, string(hash), role, refCode, acq)
	if err != nil {
		return nil, nil, err
	}

	// 單一登入：每次「登入」(註冊視為首次登入) 遞增 session_epoch，讓舊 token 全數失效。
	epoch, err := s.repo.BumpSessionEpoch(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}

	pair, err := s.issueTokens(ctx, user.ID, user.Role, epoch)
	if err != nil {
		return nil, nil, err
	}

	return user, pair, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (*User, *TokenPair, error) {
	user, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	if user == nil {
		return nil, nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, nil, ErrInvalidCredentials
	}

	// 單一登入：每次登入遞增 session_epoch（踢掉所有舊 token；refresh 不會呼叫這裡，不影響自己續命）。
	epoch, err := s.repo.BumpSessionEpoch(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}

	pair, err := s.issueTokens(ctx, user.ID, user.Role, epoch)
	if err != nil {
		return nil, nil, err
	}

	return user, pair, nil
}

// LoginWithGoogle 用 Google ID token 登入/註冊（GIS ID-token 流程）。
// 驗證 token → 依 sub 找帳號；無則用 email 連結既有帳號；再無則建立新會員。
// refCode 為可選的推薦碼（?ref=<code> 連結帶入）、acq 為可選的來源歸因原始輸入，皆只在
// 「全新會員」分支才會被用到——既有帳號（已用 Google 登入過 / email 連結）完全不碰推薦綁定
// 或來源歸因（一個帳號只會有一筆首次註冊來源，不因之後改用 Google 登入而改寫）。
func (s *Service) LoginWithGoogle(ctx context.Context, idToken, refCode string, acq Acq) (*User, *TokenPair, error) {
	if s.googleClientID == "" {
		return nil, nil, ErrGoogleNotConfigured
	}

	payload, err := idtoken.Validate(ctx, idToken, s.googleClientID)
	if err != nil {
		return nil, nil, ErrGoogleTokenInvalid
	}

	sub := payload.Subject
	email, _ := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	picture, _ := payload.Claims["picture"].(string)
	// email_verified：Google 對某些 email（如未驗證的第三方 IdP 轉聯）也可能回傳 email 欄位卻標記
	// 未驗證。JSON 布林值通常解成 Go bool，保守起見也接受字串 "true"（不同來源/版本的 claim 型別
	// 曾有出入），其餘一律視為未驗證（H2 修法：見下方第 2 步為何要卡這個旗標）。
	emailVerified, _ := payload.Claims["email_verified"].(bool)
	if !emailVerified {
		if ev, ok := payload.Claims["email_verified"].(string); ok {
			emailVerified = ev == "true"
		}
	}
	if sub == "" {
		return nil, nil, ErrGoogleTokenInvalid
	}
	if name == "" {
		if email != "" {
			name = strings.SplitN(email, "@", 2)[0]
		} else {
			name = "跑者"
		}
	}

	// 1) 已用 Google 登入過
	user, err := s.repo.FindByGoogleSub(ctx, sub)
	if err != nil {
		return nil, nil, err
	}

	// 2) 同 email 既有帳號 → 連結。H2 修法（pre-registration hijack）：只有在 Google 已驗證這個
	// email 真的屬於登入者本人（email_verified==true）時，才可以拿它去比對/連結既有帳號——否則
	// 攻擊者能用一個「填了受害者 email 但 Google 自己都不保證是本人」的身分，直接接管受害者在本站
	// 用密碼註冊的帳號。email_verified==false 時，user 維持 nil、往下走「全新會員」分支（此時
	// email 唯一鍵會在 DB 層擋下建立重複帳號，不會有機可乘）。
	if user == nil && email != "" && emailVerified {
		existing, err := s.repo.FindByEmail(ctx, email)
		if err != nil {
			return nil, nil, err
		}
		if existing != nil {
			isAdmin := existing.Role == "admin"
			// admin 帳號豁免：後台密碼登入與玩家端 Google 連結分屬不同信任邊界，不因此被清空密碼。
			hadPassword := !isAdmin && existing.PasswordHash != ""
			if err := s.repo.LinkIdentity(ctx, existing.ID, sub, email, isAdmin); err != nil {
				return nil, nil, err
			}
			if hadPassword {
				// 代表這裡真的奪回了一個「已被設過密碼」的既有帳號（可能是攻擊者搶先用受害者 email
				// 註冊的釣魚帳號，也可能只是使用者自己改用 Google 登入）——密碼已在 LinkIdentity 的
				// 同一交易內清空、session_epoch 已遞增踢掉舊 session，這裡只發告警供人工複查，
				// 不影響本次登入流程。detail 只帶 user id，不帶 email（避免告警內容外洩個資）。
				notify.Alert("google_link_took_over_password_account", "Google 登入連結既有帳號並清空密碼", fmt.Sprintf("user_id=%s", existing.ID))
			}
			user = existing
		}
	}

	// 3) 全新會員
	if user == nil {
		handle, err := s.genHandle(ctx, email, name)
		if err != nil {
			return nil, nil, err
		}
		user, err = s.repo.CreateGoogleUser(ctx, email, handle, name, picture, sub, refCode, acq)
		if err != nil {
			return nil, nil, err
		}
	}

	// 單一登入：每次登入遞增 session_epoch（不論走既有帳號或全新會員分支）。
	epoch, err := s.repo.BumpSessionEpoch(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}

	pair, err := s.issueTokens(ctx, user.ID, user.Role, epoch)
	if err != nil {
		return nil, nil, err
	}
	return user, pair, nil
}

// genHandle 由 email/姓名 推導唯一 handle（英數，必要時補隨機字尾）
func (s *Service) genHandle(ctx context.Context, email, name string) (string, error) {
	base := email
	if i := strings.IndexByte(base, '@'); i > 0 {
		base = base[:i]
	}
	if base == "" {
		base = name
	}
	// 僅留英數小寫
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	clean := b.String()
	if len(clean) < 3 {
		clean = "runner" + clean
	}
	if len(clean) > 24 {
		clean = clean[:24]
	}

	// 先試原值，重複則加隨機字尾
	candidate := clean
	for i := 0; i < 8; i++ {
		taken, err := s.repo.HandleExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		candidate = clean + randSuffix(4)
	}
	return clean + randSuffix(8), nil
}

const handleChars = "abcdefghijklmnopqrstuvwxyz0123456789"

func randSuffix(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = handleChars[rand.Intn(len(handleChars))]
	}
	return string(b)
}

// genJTI 產生 refresh token 的隨機 jti（M1 修法，見 issueTokens 呼叫點的說明）。用
// crypto/rand（而非上面 handle 字尾用的 math/rand，那個只是為了人類可讀、不需要密碼學等級亂數）。
func genJTI() string {
	b := make([]byte, 16)
	if _, err := cryptorand.Read(b); err != nil {
		// 極端情況：作業系統熵源讀取失敗。退回用奈秒級時間戳，仍能讓同一使用者在同一秒內兩次
		// issueTokens 呼叫大機率產生不同 jti；這裡只是降低 token 內容重複機率的輔助措施，
		// 不是唯一安全防線（撤銷仍以整顆 token 的 sha256 為準，見 revokeKey）。
		return hex.EncodeToString([]byte(fmt.Sprintf("fallback-%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(b)
}

// revokeKey 用整個 refresh token 的 SHA-256 當撤銷名單的 key。
// （舊版用 refreshToken[:16] 是 JWT 標頭前綴、所有 token 都相同 → 撤銷會誤傷，已修正）
func revokeKey(userID, refreshToken string) string {
	h := sha256.Sum256([]byte(refreshToken))
	return "revoked:refresh:" + userID + ":" + hex.EncodeToString(h[:])
}

// legacyRefreshCutoff H1 修法的相容寬限期截止日：本次修法上線前簽出的 refresh token 沒有 typ
// 欄位（JSON 解析後零值 ""），refresh token 最長效期就是 30 天（見 config.go JWT_REFRESH_TTL），
// 寬限期滿後所有沒有 typ 的舊 token 必已自然過期，屆時 isAcceptableRefreshTyp 這段相容判斷可以整段拿掉。
var legacyRefreshCutoff = time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

// isAcceptableRefreshTyp 判斷 refresh 端點是否接受這個 typ claim。抽成獨立函式方便單元測試
// （不需要 DB/Redis 就能驗證 access/refresh/legacy 三種情況的判定邏輯）。
func isAcceptableRefreshTyp(typ string, now time.Time) bool {
	if typ == "refresh" {
		return true
	}
	return typ == "" && now.Before(legacyRefreshCutoff)
}

// isAcceptableAccessTyp 判斷 ValidateAccessToken 是否接受這個 typ claim（2026-09-07 audit 補上）。
// 原本沒有寬限期的理由是「access token 效期只有 accessTTL（預設 60 分鐘），部署後前端 401 會
// 自動用 refresh token 換發新 token，使用者感受不到差異」——但這個假設對後台「不勾選保持登入」
// 的 session 不成立：那類 session 完全不存 refresh token（見 apps/web/src/lib/adminAuth.ts
// setSession，keep=false 分支只寫 sessionStorage、還主動清掉 REFRESH_KEY），refreshSession()
// 一開頭 `if (!rt) return null` 直接放棄，401 永遠無法自動恢復，等於部署當下就把這批已登入的
// admin 直接踢出、只能重新輸入帳密。這裡補上與 isAcceptableRefreshTyp 相同的寬限期截止日
// （legacyRefreshCutoff）：寬限期內沒有 typ 的舊 access token 仍被接受，讓這批 session 撐到
// 自然登出/換上有 typ 的新 token 為止；因為舊 access token 本身效期只有 60 分鐘、又不可能在
// 這次修法上線「之後」簽出，寬限期本身不放寬任何有效攻擊面，純粹是相容期限的對齊。
func isAcceptableAccessTyp(typ string, now time.Time) bool {
	if typ == "access" {
		return true
	}
	return typ == "" && now.Before(legacyRefreshCutoff)
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.parseToken(refreshToken)
	if err != nil {
		return nil, ErrTokenInvalid // 簽章/效期無效（含過期 30 天）
	}

	// H1 修法（token 用途區分）：refresh 端點只接受 typ="refresh" 的 token；access token
	// （typ="access"）拿來打這支一律拒絕，避免短效 access token 被誤用/濫用成能無限續命的
	// 長效 refresh token。見 isAcceptableRefreshTyp 的寬限期說明。
	if !isAcceptableRefreshTyp(claims.Typ, time.Now()) {
		return nil, ErrTokenInvalid
	}

	// 查詢最新 role（role 可能被 admin 升級過）
	user, err := s.repo.FindByID(ctx, claims.UserID)
	if err != nil || user == nil {
		return nil, ErrTokenInvalid
	}

	// 單一登入：refresh token 的 session epoch 若與帳號目前 epoch 不符，代表帳號已在
	// 別處重新登入（epoch 已被遞增）、這顆 refresh token 已被取代 → 拒絕（明確語意，
	// 讓 handler 回 401，前端走既有「refresh 失敗→登出」流程）。
	// admin 角色豁免：後台「保持登入」與玩家端共用同一顆帳號的 session_epoch，玩家端
	// 重新登入 bump epoch 會誤踢掉後台的長效 session；admin 不上傳 GPS 里程，epoch 單一
	// 登入防弊對 admin 無意義、只有誤傷 —— 故只有非 admin 才做此檢查（玩家端防弊不受影響）。
	// 用「本次從 DB 查到的 user.Role」（非 claims 裡的舊 role）判斷，避免帳號被降級後仍被豁免。
	if user.Role != "admin" && claims.SessionEpoch != user.SessionEpoch {
		return nil, ErrSessionSuperseded
	}

	// 一次性輪替（M1 修法，原子化）：SET NX + TTL 一次完成「檢查是否已用過」與「標記為已用」，
	// 取代原本 Exists-then-Set 的兩次往返——兩次往返之間若同一顆 refresh token 被併發打兩次，
	// 會在兩邊都通過 Exists 檢查（都還沒被標記）後才各自 Set，結果同一顆 token 兌換出兩組新
	// token，違反 single-use 前提。SetNX 把「查」與「標記」壓進 Redis 保證的單一原子操作，
	// 第二個併發請求必定拿到 ok=false 被拒絕。
	ttl := s.refreshTTL
	if claims.ExpiresAt != nil {
		if d := time.Until(claims.ExpiresAt.Time); d > 0 {
			ttl = d
		}
	}
	if ok, err := s.rdb.SetNX(ctx, revokeKey(claims.UserID, refreshToken), 1, ttl).Result(); err != nil {
		// Redis 不可用 → fail-open（比照既有撤銷名單政策「查無=放行」：refresh 撤銷/限流機制本身
		// 不該變成單點故障，擋掉所有人的登入續期）；只記 log，不中斷這次 refresh。
		log.Printf("auth.Refresh: WARN redis setnx failed, fail-open user=%s err=%v", claims.UserID, err)
	} else if !ok {
		return nil, ErrTokenInvalid // key 已存在 = 這顆 refresh token 已經被用過一次
	}
	// refresh 不遞增 epoch（不踢自己）——沿用原 claims 的 epoch 續命。
	return s.issueTokens(ctx, user.ID, user.Role, claims.SessionEpoch)
}

// Logout 撤銷這一組請求帶來的 access token 與 refresh token（H1 修法）：先前只撤銷 refresh
// token，access token 本身在到期前仍可繼續打 API——「登出」在使用者認知裡應該是立刻生效，不是
// 等 access token 自然過期（最長 accessTTL，預設 60 分鐘）才失效。兩者共用同一個撤銷名單 key
// 空間（revokeKey），ValidateAccessToken 每次請求都會查。accessToken/refreshToken 任一為空字串
// 就略過該側（呼叫端 handler 允許只帶其中一個）；個別 Redis 寫入失敗只記錄第一個錯誤回傳，
// 不因其中一側失敗就放棄撤銷另一側。
func (s *Service) Logout(ctx context.Context, userID, accessToken, refreshToken string) error {
	var firstErr error
	if accessToken != "" {
		ttl := s.accessTTL
		if claims, err := s.parseToken(accessToken); err == nil && claims.ExpiresAt != nil {
			if d := time.Until(claims.ExpiresAt.Time); d > 0 {
				ttl = d
			}
		}
		if err := s.rdb.Set(ctx, revokeKey(userID, accessToken), 1, ttl).Err(); err != nil {
			firstErr = err
		}
	}
	if refreshToken != "" {
		ttl := s.refreshTTL
		if claims, err := s.parseToken(refreshToken); err == nil && claims.ExpiresAt != nil {
			if d := time.Until(claims.ExpiresAt.Time); d > 0 {
				ttl = d
			}
		}
		if err := s.rdb.Set(ctx, revokeKey(userID, refreshToken), 1, ttl).Err(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// GetUserByID 查詢使用者資料（供 handler 呼叫）
func (s *Service) GetUserByID(ctx context.Context, id string) (*User, error) {
	return s.repo.FindByID(ctx, id)
}

// RecordLogin 寫入登入紀錄（user_login_logs）+ 更新 users.last_login_at，供後台檢查
// 「有沒有人天天登入」。呼叫端（handler）用獨立 context + goroutine fire-and-forget 呼叫，
// 失敗不影響登入回應本身；method 只會是 password/google/register，刻意不含 refresh。
func (s *Service) RecordLogin(ctx context.Context, userID, method, ip string) error {
	return s.repo.InsertLoginLog(ctx, userID, method, ip)
}

// ListLoginLogs 後台查詢登入紀錄流水（供 AdminLoginLogs 呼叫）。
func (s *Service) ListLoginLogs(ctx context.Context, q string, limit, offset int) ([]LoginLog, int, error) {
	return s.repo.ListLoginLogs(ctx, q, limit, offset)
}

func (s *Service) ValidateAccessToken(ctx context.Context, tokenStr string) (*Claims, error) {
	claims, err := s.parseToken(tokenStr)
	if err != nil {
		return nil, err
	}
	// H1 修法（token 用途區分）：只接受 typ="access"，或本次修法上線前簽出、沒有 typ 欄位的舊
	// access token（見 isAcceptableAccessTyp 的寬限期說明）；typ="refresh" 一律拒絕，不讓 refresh
	// token 被當 access token 濫用。
	if !isAcceptableAccessTyp(claims.Typ, time.Now()) {
		return nil, ErrTokenInvalid
	}
	// 撤銷名單（denylist）：登出（Logout）時 access token 本身也會被撤銷，與 refresh 撤銷共用
	// 同一個 key 空間（revokeKey）；查無 = 放行（Redis 重啟/清空不應把所有人誤登出，比照既有
	// refresh 撤銷名單政策）。這裡是全站每個受保護請求都會經過的路徑，刻意只做一次 EXISTS
	// （成本低），不追加其他 Redis 往返。
	// 這條查詢在「每一個」帶 token 的請求路徑上：Redis 失聯時不能讓整站每個請求都卡到 go-redis 預設的
	// 讀取逾時（秒級）才 fail-open，故另給 300ms 的獨立上限——查不到／逾時一律放行並記 Warn（沿用既有
	// refresh denylist「查無＝放行」政策），撤銷檢查退化為盡力而為，不影響可用性。
	rctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if exists, err := s.rdb.Exists(rctx, revokeKey(claims.UserID, tokenStr)).Result(); err != nil {
		log.Printf("auth.ValidateAccessToken: WARN redis exists check failed, fail-open user=%s err=%v", claims.UserID, err)
	} else if exists > 0 {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}

// issueTokens 簽發 access+refresh token 對。sessionEpoch 會寫進兩顆 token 的 sev claim：
// - 登入入口（Register/Login/LoginWithGoogle）傳入「剛遞增過」的新 epoch → 舊 token 全數失效。
// - Refresh 傳入「原 claims 的 epoch」→ 續命、不影響自己也不踢自己。
func (s *Service) issueTokens(ctx context.Context, userID, role string, sessionEpoch int) (*TokenPair, error) {
	now := time.Now()

	// Access Token（短效，含 role）
	accessClaims := &Claims{
		UserID:       userID,
		Role:         role,
		SessionEpoch: sessionEpoch,
		Typ:          "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// Refresh Token（長效，也帶 role 方便 refresh 時判斷）。M1 修法：帶一個 crypto/rand 產生的隨機
	// jti（RegisteredClaims.ID），避免同一使用者在同一秒內（IssuedAt/ExpiresAt 精度為秒）簽出的
	// 兩顆 refresh token 除了 jti 外其餘 claim 完全相同時，簽出位元組完全相同的 JWT
	// （HS256 對相同 header+payload 一定簽出相同簽章）——不只是理論疑慮，token 內容若可預測/重複，
	// 會讓撤銷名單的 key（整顆 token 的 sha256）也跟著重複，模糊了「這是哪一次登入簽出的哪一顆」。
	refreshClaims := &Claims{
		UserID:       userID,
		Role:         role,
		SessionEpoch: sessionEpoch,
		Typ:          "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        genJTI(),
		},
	}
	refreshToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}

	// 改用撤銷名單（denylist）：簽發時不需在 Redis 註冊，refresh 只擋被撤銷的 token。
	// 這樣 Redis 重啟/清空也不會把人誤登出。

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(s.accessTTL.Seconds()),
		SessionEpoch: sessionEpoch,
	}, nil
}

func (s *Service) parseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrTokenInvalid
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}
