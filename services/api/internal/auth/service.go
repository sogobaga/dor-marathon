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
	"regexp"
	"strings"
	"sync"
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
	// ErrAuthUnavailable：2026-09-08 audit finding 3(e)——auth 撤銷/輪替機制依賴的後端
	// （Redis／DB session state 查詢）暫時打不通，且這次操作屬於「不能 fail-open」的類型
	// （refresh 的 single-use 輪替、admin token 的撤銷名單檢查）。handler 一律映射成 503，
	// 語意是「請稍後重試」，不是「未授權」。
	ErrAuthUnavailable = errors.New("auth service temporarily unavailable")
	// ErrGoogleLinkAdmin／ErrLinkRequiresPassword：2026-09-08 audit finding 7，見
	// isGoogleLinkTrusted 與 LoginWithGoogle 呼叫點的完整說明。
	ErrGoogleLinkAdmin      = errors.New("admin accounts must log in via the back office")
	ErrLinkRequiresPassword = errors.New("please log in with your existing method before linking google")
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

	// sessionCache 快取 getSessionState 的查詢結果（2026-09-08 audit finding 3）：
	// ValidateAccessToken 現在對每一個受保護請求都要額外查一次 role/session_epoch/
	// tokens_not_before 才能落實「密碼被改／單一登入立即讓舊 access token 失效」，若不快取會讓
	// 這條全站熱路徑每個請求多一次 DB 往返。TTL 見 sessionCacheTTL；BumpSessionEpoch／
	// RevokeAllSessions 成功後會立刻呼叫 invalidateSessionCache，縮短「操作剛好在本行程執行」
	// 情境下的生效延遲（跨行程／Railway 多副本仍受 TTL 限制）。field（非 pointer）的零值
	// sync.Map 已可直接使用，NewService 不需要另外初始化。
	sessionCache sync.Map
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
	s.invalidateSessionCache(user.ID) // 見 Service.sessionCache 欄位註解
	s.PublishKick(ctx, user.ID)       // 見 PublishKick：事件驅動踢舊 WS 連線，不靠它們背景重驗查 DB

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
	s.invalidateSessionCache(user.ID) // 見 Service.sessionCache 欄位註解
	s.PublishKick(ctx, user.ID)       // 見 PublishKick：事件驅動踢舊 WS 連線，不靠它們背景重驗查 DB

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
	// hd：Google Workspace「託管網域」claim，只有 Workspace 帳號會帶（見下方 isGoogleLinkTrusted
	// 用途說明，2026-09-08 audit finding 7）。
	hd, _ := payload.Claims["hd"].(string)
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
	//
	// 2026-09-08 audit finding 7（H2 修法的延伸強化）：email_verified==true 本身不足以涵蓋所有
	// 情境——見 isGoogleLinkTrusted 完整說明。
	if user == nil && email != "" && emailVerified {
		existing, err := s.repo.FindByEmail(ctx, email)
		if err != nil {
			return nil, nil, err
		}
		if existing != nil {
			if existing.Role == "admin" {
				// 管理者帳號一律不透過玩家端 Google 登入自動連結／換發 token：後台密碼登入與玩家端
				// Google 登入分屬不同信任邊界，即使真的是本人（自己的 Gmail 剛好等於後台帳號
				// email），也不該讓公開的 /auth/google endpoint 變成能拿到 admin-role token 的路徑
				// ——比原本「連結但不清密碼/不動 epoch」的豁免更嚴格：直接整段拒絕。
				return nil, nil, ErrGoogleLinkAdmin
			}
			hadPassword := existing.PasswordHash != ""
			if !isGoogleLinkTrusted(hadPassword, email, hd) {
				return nil, nil, ErrLinkRequiresPassword
			}
			if err := s.repo.LinkIdentity(ctx, existing.ID, sub, email, false); err != nil {
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
	s.invalidateSessionCache(user.ID) // 見 Service.sessionCache 欄位註解
	s.PublishKick(ctx, user.ID)       // 見 PublishKick：事件驅動踢舊 WS 連線，不靠它們背景重驗查 DB

	pair, err := s.issueTokens(ctx, user.ID, user.Role, epoch)
	if err != nil {
		return nil, nil, err
	}
	return user, pair, nil
}

// isGoogleLinkTrusted 判斷「Google 已驗證 email 屬於登入者本人」是否足以自動連結一個同 email 的
// 既有一般會員帳號（2026-09-08 audit finding 7，H2 修法的延伸強化）。呼叫端（LoginWithGoogle）
// 已經先排除 existing.Role=="admin" 的情況（一律回傳 ErrGoogleLinkAdmin、不會呼叫本函式），這裡
// 只需要處理一般會員帳號、決定是否要相信這次連結：
//   - hadPassword==false（既有帳號本來就沒設過密碼，是「純 Google 帳號」）：連結形同「換一台
//     裝置/瀏覽器再用同一個 Google 帳號登入一次」，沒有密碼可搶，永遠信任。
//   - hd claim 非空（Google Workspace 託管網域）：該網域已由 Workspace 管理員驗證持有，
//     email_verified 的保證力道足夠強。
//   - email 網域是 gmail.com/googlemail.com：這類地址由 Google 本身簽發/控管，不像自架郵件
//     伺服器可能被中間人偽造驗證流程，email_verified 保證力道足夠。
//
// 其餘情況（任意其他網域的自架密碼帳號、非 Workspace）一律不信任——攻擊者可能搶先用受害者的
// 外部 email 在本站註冊一個密碼帳號，受害者之後才第一次改用 Google 登入；這裡拒絕自動連結，
// 要求先用密碼登入本站既有帳號、確認本人持有後再自行改連 Google（該路徑不在本次修法範圍），
// 比「錯誤地把帳號連給非本人」安全。獨立成純函式方便單元測試（不需要 idtoken.Validate/DB）。
func isGoogleLinkTrusted(hadPassword bool, email, hd string) bool {
	if !hadPassword {
		return true
	}
	if hd != "" {
		return true
	}
	domain := ""
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		domain = strings.ToLower(email[i+1:])
	}
	return domain == "gmail.com" || domain == "googlemail.com"
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

// revokeKey 用整個 token 字串的 SHA-256 當撤銷名單的 key——只給「沒有 jti」的舊 token
// （本次修法上線前簽出，見 revocationKey）當退回方案；有 jti 的 token 一律改用 revokeKeyJTI。
// （舊版用 refreshToken[:16] 是 JWT 標頭前綴、所有 token 都相同 → 撤銷會誤傷，已修正）
func revokeKey(userID, rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return "revoked:refresh:" + userID + ":" + hex.EncodeToString(h[:])
}

// revokeKeyJTI 用 token 的 jti（RegisteredClaims.ID，簽發時的隨機值，見 genJTI／issueTokens）當
// 撤銷名單 key——2026-09-08 audit finding 1：整顆 token 字串的 sha256（revokeKey）有編碼變體
// 繞過問題（同一顆簽章、字串卻不同，見 canonicalJWTPattern／parseToken 的說明），jti 不受編碼
// 變體影響，是更穩固的撤銷鍵。
func revokeKeyJTI(userID, jti string) string {
	return "revoked:jti:" + userID + ":" + jti
}

// revocationKey 決定某顆 token（已解出的 claims + 原始字串）該用哪把撤銷鑰匙：有 jti（本次修法
// 後簽發的 access/refresh token 皆有，見 issueTokens）優先用 revokeKeyJTI；legacy token（本次
// 修法上線前簽出、沒有 jti，只可能是舊 access token——舊 refresh token 從 M1 修法起就有 jti）
// 退回用整串 token 的 sha256（revokeKey）——這在新版 parseToken 之下仍然安全，因為
// canonicalJWTPattern 已經只接受單一種 canonical base64url 寫法，同一顆 legacy token 不會有
// 第二種編碼變體通過驗簽卻算出不同 sha256。
func revocationKey(claims *Claims, rawToken string) string {
	if claims.ID != "" {
		return revokeKeyJTI(claims.UserID, claims.ID)
	}
	return revokeKey(claims.UserID, rawToken)
}

// canonicalJWTPattern：2026-09-08 audit finding 1——Go 的 base64.RawURLEncoding.DecodeString
// 對嵌入的 \r\n 是「忽略、照樣解出正確位元組」（已用獨立小程式驗證），意味著在補上這個檢查之前，
// 一顆 token 的簽章段落只要插入換行字元，字串就變了（sha256 也跟著變、撤銷名單白撞）但驗簽
// 照樣通過——形同撤銷/single-use 輪替可被繞過。這裡在真的丟給 jwt.ParseWithClaims 之前，先鎖死
// 三段都必須是「純」base64url 無 padding 字元集（A-Za-z0-9_-，中間用單一 '.' 分隔），直接排除
// 任何非 canonical 寫法（含換行、'='、標準 base64 的 '+'/'/'），讓每顆 token 只有一種字串表示，
// revocationKey／jti 撤銷的前提才成立。
var canonicalJWTPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

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

// isAcceptableAccessTyp 判斷 ValidateAccessToken 是否接受這個 typ claim（2026-09-07 audit 補上，
// 2026-09-08 audit finding 2 補強）。原本沒有寬限期的理由是「access token 效期只有 accessTTL
// （預設 60 分鐘），部署後前端 401 會自動用 refresh token 換發新 token，使用者感受不到差異」——
// 但這個假設對後台「不勾選保持登入」的 session 不成立：那類 session 完全不存 refresh token
// （見 apps/web/src/lib/adminAuth.ts setSession，keep=false 分支只寫 sessionStorage、還主動清掉
// REFRESH_KEY），refreshSession() 一開頭 `if (!rt) return null` 直接放棄，401 永遠無法自動恢復，
// 等於部署當下就把這批已登入的 admin 直接踢出、只能重新輸入帳密。這裡補上與
// isAcceptableRefreshTyp 相同的寬限期截止日（legacyRefreshCutoff），讓這批 session 撐到自然登出/
// 換上有 typ 的新 token 為止。
//
// finding 2：光靠「typ==""」判斷寬限期不夠——沒有 typ 的舊 refresh token（效期最長 30 天，見
// config.go JWT_REFRESH_TTL）在寬限期內也會被這個函式誤判成「可接受的 access token」，等於拿
// 一顆長效 refresh token 當短效 access token 用到寬限期結束為止，形同 H1 修法想避免的「access/
// refresh 互相冒用」在寬限期內破功。改成額外檢查效期跨距（exp-iat）：只有「跨距不超過
// accessTTL+5 分鐘」（5 分鐘是給時鐘飄移/簽發當下處理延遲的餘裕）的 typ-less token 才視為
// legacy access token；30 天跨距的 legacy refresh token 必定超過這個上限，一樣被拒絕
// （只能繼續走 /auth/refresh，那裡 isAcceptableRefreshTyp 才接受它）。
func isAcceptableAccessTyp(typ string, now, iat, exp time.Time, accessTTL time.Duration) bool {
	if typ == "access" {
		return true
	}
	if typ != "" {
		return false // "refresh" 或其他任何非空 typ 一律拒絕
	}
	if !now.Before(legacyRefreshCutoff) {
		return false
	}
	if iat.IsZero() || exp.IsZero() {
		return false // 缺少時間戳無法判斷效期跨距，保守拒絕
	}
	return exp.Sub(iat) <= accessTTL+5*time.Minute
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

	// 2026-09-08 audit finding 3：改查 GetSessionState（role/session_epoch/tokens_not_before
	// 三欄，見 Repository.GetSessionState）取代原本的 FindByID——這裡只需要這三個欄位。刻意
	// 繞過 Service.getSessionState 的行程內快取（那個只給 ValidateAccessToken 熱路徑用，見該
	// 函式與 getSessionState 的註解）：Refresh 是單一登入強制的主要防線，快取會讓「剛登入踢舊
	// 裝置」與「舊 refresh token 被拒絕」之間多一段最長 60 秒的視窗，削弱這個防線原本要保證的
	// 「立即生效」；Refresh 的呼叫頻率遠低於每個請求都要驗證一次的 access token，不快取的
	// 查詢成本可以接受。
	role, sessionEpoch, tokensNotBefore, err := s.repo.GetSessionState(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrTokenInvalid // 帳號不存在（已刪除）
		}
		// DB 暫時故障／migration 171 尚未套用（欄位不存在）：比照 ValidateAccessToken 對這段查詢的
		// fail-open 政策——以 claims 自帶的 role/epoch 續命並記 Warn，不能讓 /auth/refresh（每個 session
		// 約每小時必經之路）在 DB 抖動時把全站使用者一次登出（審查抓到）。撤銷名單（Redis）仍照常檢查。
		log.Printf("auth.Refresh: WARN session state lookup failed, fail-open user=%s err=%v", claims.UserID, err)
		role, sessionEpoch, tokensNotBefore = claims.Role, claims.SessionEpoch, nil
	}

	// tokens_not_before（finding 3，不分角色）：帳號被要求「全部裝置登出」（密碼變更／後台
	// 強制撤銷，見 RevokeAllSessions）之後，IssuedAt 早於這個時間戳的 refresh token 一律失效——
	// 與下面的 session_epoch 檢查不同，這裡連 admin 也不豁免。
	if tokensNotBefore != nil && claims.IssuedAt != nil && claims.IssuedAt.Time.Before(*tokensNotBefore) {
		return nil, ErrTokenInvalid
	}

	// 單一登入：refresh token 的 session epoch 若與帳號目前 epoch 不符，代表帳號已在
	// 別處重新登入（epoch 已被遞增）、這顆 refresh token 已被取代 → 拒絕（明確語意，
	// 讓 handler 回 401，前端走既有「refresh 失敗→登出」流程）。
	// admin 角色豁免：後台「保持登入」與玩家端共用同一顆帳號的 session_epoch，玩家端
	// 重新登入 bump epoch 會誤踢掉後台的長效 session；admin 不上傳 GPS 里程，epoch 單一
	// 登入防弊對 admin 無意義、只有誤傷 —— 故只有非 admin 才做此檢查（玩家端防弊不受影響）。
	// 用「本次從 DB 查到的 role」（非 claims 裡的舊 role）判斷，避免帳號被降級後仍被豁免。
	if role != "admin" && claims.SessionEpoch != sessionEpoch {
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
	if ok, err := s.rdb.SetNX(ctx, revocationKey(claims, refreshToken), 1, ttl).Result(); err != nil {
		// 2026-09-08 audit finding 3(e)：SetNX 失敗不再 fail-open——這裡是 refresh token 的
		// single-use 輪替，fail-open 等於「這次檢查完全不生效」，同一顆 refresh token 可以被
		// 重複兌換到底，single-use 保證形同虛設。改成回傳 ErrAuthUnavailable（handler 對外映射
		// 成 503），讓客戶端知道要稍後重試，而不是靜默放行、事後才在稽核時發現重放。
		log.Printf("auth.Refresh: WARN redis setnx failed, fail-closed (rotation) user=%s err=%v", claims.UserID, err)
		return nil, ErrAuthUnavailable
	} else if !ok {
		return nil, ErrTokenInvalid // key 已存在 = 這顆 refresh token 已經被用過一次
	}
	// refresh 不遞增 epoch（不踢自己）——沿用原 claims 的 epoch 續命。
	return s.issueTokens(ctx, claims.UserID, role, claims.SessionEpoch)
}

// revokeOne 撤銷單一 token（access 或 refresh 皆可，供 Logout 呼叫）：算出撤銷 key（優先 jti，
// 見 revocationKey）、TTL 抓 token 剩餘效期（過期就不用留在 Redis 那麼久），寫入撤銷名單。
// parseToken 失敗（理論上不該發生——呼叫端傳進來的都是這次請求自己帶的、格式應該正常的 token；
// 頂多是已經自然過期）時退回用 defaultTTL／revokeKey(userID, token) 的舊版 key，不因此整段放棄
// 撤銷（過期的 token 反正也打不進任何受保護端點，這裡只是盡力而為）。
func (s *Service) revokeOne(ctx context.Context, userID, token string, defaultTTL time.Duration) error {
	ttl := defaultTTL
	key := revokeKey(userID, token)
	if claims, err := s.parseToken(token); err == nil {
		key = revocationKey(claims, token)
		if claims.ExpiresAt != nil {
			if d := time.Until(claims.ExpiresAt.Time); d > 0 {
				ttl = d
			}
		}
	}
	return s.rdb.Set(ctx, key, 1, ttl).Err()
}

// Logout 撤銷這一組請求帶來的 access token 與 refresh token（H1 修法）：先前只撤銷 refresh
// token，access token 本身在到期前仍可繼續打 API——「登出」在使用者認知裡應該是立刻生效，不是
// 等 access token 自然過期（最長 accessTTL，預設 60 分鐘）才失效。兩者共用同一個撤銷名單 key
// 空間（revocationKey），ValidateAccessToken 每次請求都會查。accessToken/refreshToken 任一為
// 空字串就略過該側（呼叫端 handler 允許只帶其中一個）；個別 Redis 寫入失敗只記錄第一個錯誤
// 回傳（handler 會映射成 503，見 2026-09-08 audit finding 3(e)），不因其中一側失敗就放棄撤銷
// 另一側。
func (s *Service) Logout(ctx context.Context, userID, accessToken, refreshToken string) error {
	var firstErr error
	if accessToken != "" {
		if err := s.revokeOne(ctx, userID, accessToken, s.accessTTL); err != nil {
			firstErr = err
		}
	}
	if refreshToken != "" {
		if err := s.revokeOne(ctx, userID, refreshToken, s.refreshTTL); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// 2026-09-10 review finding：revalidateInterval 這版從 5 分鐘拉到 15 分鐘（backstop 用途，
	// 平常靠事件式踢除），若登出不順便發一次 kick，該分頁的 WS 連線最長要等 15 分鐘才會被
	// backstop（走 checkDenylist）關掉——比原本 5 分鐘的窗口寬了 3 倍。這裡不論撤銷成功與否都
	// 發，反正是 best-effort、不影響回傳的錯誤。
	s.PublishKick(ctx, userID)
	return firstErr
}

// RevokeAllSessions 讓某使用者「現有全部」access/refresh token 立即失效（tokens_not_before=now，
// 不分角色，含 admin——見 ValidateAccessToken／Refresh 對 tokens_not_before 的檢查），2026-09-08
// audit finding 3(d)。供兩處呼叫：
//   - internal/adminacct.Handler.Update 的密碼變更段落（改密碼後讓該管理者現有全部 session 失效；
//     若改的是操作者自己，代表這次請求本身的 session 也會跟著失效，之後動作需要重新登入）。
//   - Handler.AdminRevokeSessions（POST /admin/accounts/{id}/revoke-sessions，僅超級管理員）：
//     懷疑帳號外洩時手動強制登出全部裝置，不需要（也不會）改密碼。
func (s *Service) RevokeAllSessions(ctx context.Context, userID string) error {
	if err := s.repo.SetTokensNotBefore(ctx, userID, time.Now()); err != nil {
		return err
	}
	s.invalidateSessionCache(userID)
	s.PublishKick(ctx, userID) // 見 PublishKick：事件驅動踢舊 WS 連線，不靠它們背景重驗查 DB
	return nil
}

// KickChannel 是「單一登入強制踢除」的 Redis Pub/Sub 頻道名稱（2026-09-10 Neon 喚醒週期修法，
// 見 PublishKick／RevalidateAccessTokenNoDB 的完整說明，以及 internal/realtime.Manager 對本頻道
// 的訂閱）。訊息內容就是被踢帳號的 userID（純字串，不包 JSON）——realtime 套件已被 auth 套件
// import（見 Handler.SetRealtime／PublishSessionRevoked），無法反過來 import auth 取用這個常數
// 避免 import cycle，故 realtime 套件另外宣告一份同值的字串常數，純粹靠 Redis 頻道名稱本身耦合。
const KickChannel = "user_kick"

// PublishKick 廣播「這個使用者的所有 WebSocket 連線應立即中斷」事件（2026-09-10 Neon 喚醒週期
// 修法）：Redis Pub/Sub，純盡力而為——沒有訂閱者、Redis 短暫不通都只記一行警告，不影響呼叫端
// （登入／改密碼／後台強制登出）本身的流程，也不回傳錯誤（呼叫端不該因為「踢舊連線」這種
// 錦上添花的動作失敗就讓整個登入/改密碼請求失敗）。
//
// 呼叫點：Register／Login／LoginWithGoogle 的 BumpSessionEpoch 之後（新登入踢舊裝置）、
// RevokeAllSessions（密碼變更／後台 /admin/accounts/{id}/revoke-sessions 強制登出全部裝置）、
// adminacct.Handler 密碼變更段落（透過 main.go 注入的 SetSessionKicker hook，因為 adminacct
// 不持有 auth.Service 參照，直接對 users 表下 SQL，見該檔案的說明）。
//
// realtime.Manager 訂閱本頻道後直接關閉匹配 userID 的既有 WS 連線（見 hub.go 的
// subscribeKicks／KickUser）——auth 與 realtime 兩個套件不互相持有對方的參照，只靠這個 Redis
// 頻道名稱解耦串接，是本次修法把「WS 背景重驗改成不查 DB」（RevalidateAccessTokenNoDB）之後，
// 用來補回「立即生效」保證的另一半：兩者合起來仍等同修法前的行為（token 被撤銷/踢除後，連線
// 很快就會被關閉），差別只在於不再需要每條連線每 revalidateInterval 就查一次 DB。
func (s *Service) PublishKick(ctx context.Context, userID string) {
	if err := s.rdb.Publish(ctx, KickChannel, userID).Err(); err != nil {
		log.Printf("auth.PublishKick: WARN redis publish failed user=%s err=%v", userID, err)
	}
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
	return s.validateAccess(ctx, tokenStr, false)
}

// RevalidateAccessToken 給「已建立的長連線」（WebSocket）每 5 分鐘重驗用：與 ValidateAccessToken 完全相同的
// 檢查（正規形式、簽章、typ、撤銷名單、tokens_not_before、session_epoch），唯獨**不看 exp**——連線當初是用
// 有效 token 合法建立的，之後前端會自行換發新的 access token 但不會重送給 socket；重驗的目的只在抓
// 「登出／被踢／改密碼／撤銷」，若連過期也算失敗，每條 WS 在 accessTTL（60 分）後都會被 4401 踢掉，
// 而 /track 的賽事 socket 沒有重連機制，長跑中會失去團體任務推播（審查抓到）。
func (s *Service) RevalidateAccessToken(ctx context.Context, tokenStr string) (*Claims, error) {
	return s.validateAccess(ctx, tokenStr, true)
}

// RevalidateAccessTokenNoDB 給「已建立的長連線」（WebSocket）背景重驗用，取代 RevalidateAccessToken
// ——2026-09-10 生產環境調查（見 go-live-todos）：v806 加的 WS 背景重驗（每 revalidateInterval 重打一次
// RevalidateAccessToken）會經 getSessionState 查一次 DB（role/session_epoch/tokens_not_before），
// 60 秒行程內快取只能壓低頻率、壓不到零——任何一個開著的 App/後台分頁都會維持一條 socket，等於
// DB 永遠每 5 分鐘至少被查一次，Neon serverless compute 因此永遠無法閒置 5 分鐘、自動 suspend
// 失效（觀測：awake ratio 67%→81%）。
//
// 修法：把「立即生效」的保證從「背景重驗查 DB」搬到「事件驅動」——登入/改密碼/撤銷當下透過
// Redis Pub/Sub 主動關閉舊連線（見 PublishKick 與 internal/realtime.Manager 對 KickChannel 的
// 訂閱），背景重驗只需要保留「token 本身還沒被撤銷」這一層檢查（撤銷名單 EXISTS，Redis-only），
// 完全不碰 DB／不查 tokens_not_before／session_epoch、也不用行程內快取（sessionCache 本來就是
// 為了省下這條查詢，這裡直接不查，快取無用武之地）。revalidateInterval 從 5 分鐘拉長到 15 分鐘
// 只是備援（backstop）：萬一 Kick 事件因為 Redis 短暫抖動漏送，連線最慢 15 分鐘內仍會被這條
// 路徑清掉，不是主要防線。
//
// 與 validateAccess 共用 parseTokenOpts(...WithoutClaimsValidation())（不看 exp，理由同
// RevalidateAccessToken）、isAcceptableAccessTyp（typ 檢查）、checkDenylist（撤銷名單，fail-open
// 給一般會員／fail-closed 給 admin，見該函式）；刻意不呼叫 s.getSessionState／s.repo 任何方法。
func (s *Service) RevalidateAccessTokenNoDB(ctx context.Context, tokenStr string) (*Claims, error) {
	claims, err := s.parseTokenOpts(tokenStr, jwt.WithoutClaimsValidation())
	if err != nil {
		return nil, err
	}
	var iat, exp time.Time
	if claims.IssuedAt != nil {
		iat = claims.IssuedAt.Time
	}
	if claims.ExpiresAt != nil {
		exp = claims.ExpiresAt.Time
	}
	if !isAcceptableAccessTyp(claims.Typ, time.Now(), iat, exp, s.accessTTL) {
		return nil, ErrTokenInvalid
	}
	if err := s.checkDenylist(ctx, claims, tokenStr); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *Service) validateAccess(ctx context.Context, tokenStr string, ignoreExpiry bool) (*Claims, error) {
	var claims *Claims
	var err error
	if ignoreExpiry {
		claims, err = s.parseTokenOpts(tokenStr, jwt.WithoutClaimsValidation())
	} else {
		claims, err = s.parseToken(tokenStr)
	}
	if err != nil {
		return nil, err
	}
	var iat, exp time.Time
	if claims.IssuedAt != nil {
		iat = claims.IssuedAt.Time
	}
	if claims.ExpiresAt != nil {
		exp = claims.ExpiresAt.Time
	}
	// H1 修法（token 用途區分）：只接受 typ="access"，或本次修法上線前簽出、沒有 typ 欄位的舊
	// access token（見 isAcceptableAccessTyp 的寬限期說明）；typ="refresh" 一律拒絕，不讓 refresh
	// token 被當 access token 濫用。
	if !isAcceptableAccessTyp(claims.Typ, time.Now(), iat, exp, s.accessTTL) {
		return nil, ErrTokenInvalid
	}
	// 撤銷名單（denylist）：見 checkDenylist 的完整說明（2026-09-10 抽成獨立方法，供
	// RevalidateAccessTokenNoDB 共用同一份 fail-open/fail-closed 政策，不重複這段邏輯）。
	if err := s.checkDenylist(ctx, claims, tokenStr); err != nil {
		return nil, err
	}

	// 2026-09-08 audit finding 3：tokens_not_before（全角色）＋ session_epoch（非 admin）現在也在
	// access token 這一層落實，不再只靠 Refresh 擋——密碼被改／單一登入踢除後，舊 access token
	// 原本可以繼續用到自然過期（最長 accessTTL）才失效，這裡把這個視窗收斂到最多
	// sessionCacheTTL（見 getSessionState）。這是在既有 Redis 撤銷名單之外「新增」的一層防線，
	// 查詢本身故障不該變成新的可用性單點失效（DB 短暫抖動比 Redis 更常見）——不分角色一律
	// fail-open，只記 Warn；主要防線（token 簽章/效期、Redis 撤銷名單）不受影響。
	// s.repo 只有單元測試會留 nil（見 newTestService 註解，不需要真的 Postgres 就能測 typ／
	// 撤銷相關邏輯），nil 時略過本段——生產環境的 Service 一定經 NewService 帶一個真的 repo。
	if s.repo != nil {
		role, epoch, notBefore, err := s.getSessionState(ctx, claims.UserID)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				return nil, ErrTokenInvalid // 帳號已刪除：fail-closed（審查抓到）
			}
			log.Printf("auth.ValidateAccessToken: WARN session state lookup failed, fail-open user=%s err=%v", claims.UserID, err)
		} else {
			if notBefore != nil && claims.IssuedAt != nil && claims.IssuedAt.Time.Before(*notBefore) {
				return nil, ErrTokenInvalid
			}
			if role != "admin" && claims.SessionEpoch != epoch {
				return nil, ErrTokenInvalid
			}
		}
	}

	return claims, nil
}

// checkDenylist 查詢撤銷名單（denylist）是否命中——登出（Logout）時 access token 本身也會被
// 撤銷，與 refresh 撤銷共用同一個 key 空間（revocationKey）；查無 = 放行（Redis 重啟/清空不應
// 把所有人誤登出，比照既有 refresh 撤銷名單政策）。這是全站每個受保護請求（含 WS 背景重驗）都
// 會經過的路徑，刻意只做一次 EXISTS（成本低），不追加其他 Redis 往返。
//
// 這條查詢在「每一個」帶 token 的請求路徑上：Redis 失聯時不能讓整站每個請求都卡到 go-redis 預設
// 的讀取逾時（秒級）才 fail-open，故另給 300ms 的獨立上限——查不到／逾時一律放行並記 Warn（沿用
// 既有 refresh denylist「查無＝放行」政策），撤銷檢查退化為盡力而為，不影響可用性。
//
// 2026-09-08 audit finding 3(e)：一般會員維持既有 fail-open（不能讓 Redis 短暫抖動變成全站
// 中斷）；admin token fail-closed——admin 能操作的範圍（後台改資料、退費、發送廣播…）風險
// 遠高於一般會員，寧可讓後台在 Redis 故障時暫時 503（前端可重試），也不要讓一顆本該被撤銷
// 的 admin token 在撤銷名單失效的當下繼續暢行無阻。claims.Role 是 JWT 自帶的角色宣告，與
// RequireAdmin／RequirePerm 等既有授權判斷信任同一個來源，非新的信任假設。
//
// 2026-09-10：抽成獨立方法，供 validateAccess 與 RevalidateAccessTokenNoDB（WS 背景重驗，
// 刻意不查 DB，見該函式說明）共用同一份政策，避免兩處各維護一份容易日後改一邊漏改另一邊。
func (s *Service) checkDenylist(ctx context.Context, claims *Claims, tokenStr string) error {
	rctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if exists, err := s.rdb.Exists(rctx, revocationKey(claims, tokenStr)).Result(); err != nil {
		if claims.Role == "admin" {
			log.Printf("auth.checkDenylist: WARN redis exists check failed, fail-closed for admin user=%s err=%v", claims.UserID, err)
			return ErrAuthUnavailable
		}
		log.Printf("auth.checkDenylist: WARN redis exists check failed, fail-open user=%s err=%v", claims.UserID, err)
	} else if exists > 0 {
		return ErrTokenInvalid
	}
	return nil
}

// sessionCacheTTL 見 Service.sessionCache 欄位註解：ValidateAccessToken 每個受保護請求都可能
// 查一次 role/session_epoch/tokens_not_before，這裡快取縮短 DB 查詢量；60 秒是「立即生效」與
// 「查詢量」的折衷（比 Redis 撤銷名單的即時性弱，但這層本來就是防線二，見 ValidateAccessToken
// 對這段查詢的 fail-open 註解）。
const sessionCacheTTL = 60 * time.Second

type sessionCacheEntry struct {
	role            string
	sessionEpoch    int
	tokensNotBefore *time.Time
	expiresAt       time.Time
}

// getSessionState 是 Repository.GetSessionState 加上行程內快取（sync.Map，TTL 見
// sessionCacheTTL）的版本，只給 ValidateAccessToken 這個熱路徑用——Refresh 刻意繞過快取直接查
// repo（見 Refresh 內的說明：快取的過期視窗會削弱單一登入「立即生效」的保證，而 Refresh 的呼叫
// 頻率遠低於 access token 驗證，不快取的成本可以接受）。
func (s *Service) getSessionState(ctx context.Context, userID string) (role string, sessionEpoch int, tokensNotBefore *time.Time, err error) {
	if v, ok := s.sessionCache.Load(userID); ok {
		if e, ok := v.(*sessionCacheEntry); ok && time.Now().Before(e.expiresAt) {
			return e.role, e.sessionEpoch, e.tokensNotBefore, nil
		}
	}
	role, sessionEpoch, tokensNotBefore, err = s.repo.GetSessionState(ctx, userID)
	if err != nil {
		return "", 0, nil, err
	}
	s.sessionCache.Store(userID, &sessionCacheEntry{
		role:            role,
		sessionEpoch:    sessionEpoch,
		tokensNotBefore: tokensNotBefore,
		expiresAt:       time.Now().Add(sessionCacheTTL),
	})
	return role, sessionEpoch, tokensNotBefore, nil
}

// invalidateSessionCache 讓某使用者的行程內快取立即失效，供剛好在本行程內做了會改變
// role/session_epoch/tokens_not_before 的操作（BumpSessionEpoch 之後、RevokeAllSessions 內部）
// 呼叫，縮短「操作剛好在本行程執行」情境下的生效延遲；跨行程（Railway 多副本）仍受
// sessionCacheTTL 限制，見 Service.sessionCache 欄位註解。
func (s *Service) invalidateSessionCache(userID string) {
	s.sessionCache.Delete(userID)
}

// issueTokens 簽發 access+refresh token 對。sessionEpoch 會寫進兩顆 token 的 sev claim：
// - 登入入口（Register/Login/LoginWithGoogle）傳入「剛遞增過」的新 epoch → 舊 token 全數失效。
// - Refresh 傳入「原 claims 的 epoch」→ 續命、不影響自己也不踢自己。
func (s *Service) issueTokens(ctx context.Context, userID, role string, sessionEpoch int) (*TokenPair, error) {
	now := time.Now()

	// Access Token（短效，含 role）。2026-09-08 audit finding 1(b)：補上 jti（跟 refresh token
	// 同一顆 genJTI，crypto/rand 產生）——撤銷名單優先用 jti 當 key（見 revocationKey），不受
	// token 編碼變體影響；沒有這個之前，access token 的撤銷只能退回整串字串的 sha256
	// （revokeKey），在補上 canonicalJWTPattern 檢查之前存在編碼變體繞過的疑慮（見該檢查說明）。
	accessClaims := &Claims{
		UserID:       userID,
		Role:         role,
		SessionEpoch: sessionEpoch,
		Typ:          "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        genJTI(),
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
	return s.parseTokenOpts(tokenStr)
}

// parseTokenOpts 同 parseToken，可附加 jwt.ParserOption（目前只有 RevalidateAccessToken 用
// jwt.WithoutClaimsValidation() 略過 exp 檢查；正規形式 regex 與 WithStrictDecoding 永遠套用）。
func (s *Service) parseTokenOpts(tokenStr string, extra ...jwt.ParserOption) (*Claims, error) {
	// 2026-09-08 audit finding 1(a)：先擋非 canonical 編碼變體，見 canonicalJWTPattern 的完整
	// 說明——這一步必須在丟給 jwt.ParseWithClaims 之前做，讓「同一顆簽章只有一種字串表示」的
	// 前提在進到撤銷名單查詢（revocationKey）之前就成立。
	if !canonicalJWTPattern.MatchString(tokenStr) {
		return nil, ErrTokenInvalid
	}
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.jwtSecret, nil
	}, append([]jwt.ParserOption{jwt.WithStrictDecoding()}, extra...)...)
	if err != nil || !token.Valid {
		return nil, ErrTokenInvalid
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}
