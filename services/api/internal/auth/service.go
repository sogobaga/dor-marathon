package auth

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/api/idtoken"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email already registered")
	ErrHandleTaken        = errors.New("handle already taken")
	ErrTokenInvalid       = errors.New("token invalid or expired")
	ErrGoogleNotConfigured = errors.New("google login not configured")
	ErrGoogleTokenInvalid  = errors.New("invalid google id token")
)

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // 秒
}

type Claims struct {
	UserID string `json:"uid"`
	Role   string `json:"role"` // user | organizer | admin
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

// Register 建立新使用者（role 由呼叫方指定：user 或 organizer）
func (s *Service) Register(ctx context.Context, email, handle, name, password, role string) (*User, *TokenPair, error) {
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

	user, err := s.repo.Create(ctx, email, handle, name, string(hash), role)
	if err != nil {
		return nil, nil, err
	}

	pair, err := s.issueTokens(ctx, user.ID, user.Role)
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

	pair, err := s.issueTokens(ctx, user.ID, user.Role)
	if err != nil {
		return nil, nil, err
	}

	return user, pair, nil
}

// LoginWithGoogle 用 Google ID token 登入/註冊（GIS ID-token 流程）。
// 驗證 token → 依 sub 找帳號；無則用 email 連結既有帳號；再無則建立新會員。
func (s *Service) LoginWithGoogle(ctx context.Context, idToken string) (*User, *TokenPair, error) {
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

	// 2) 同 email 既有帳號 → 連結
	if user == nil && email != "" {
		existing, err := s.repo.FindByEmail(ctx, email)
		if err != nil {
			return nil, nil, err
		}
		if existing != nil {
			if err := s.repo.LinkIdentity(ctx, existing.ID, sub, email); err != nil {
				return nil, nil, err
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
		user, err = s.repo.CreateGoogleUser(ctx, email, handle, name, picture, sub)
		if err != nil {
			return nil, nil, err
		}
	}

	pair, err := s.issueTokens(ctx, user.ID, user.Role)
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

func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.parseToken(refreshToken)
	if err != nil {
		return nil, ErrTokenInvalid
	}

	// 確認 refresh token 未被撤銷（存在 Redis）
	key := "user:" + claims.UserID + ":refresh:" + refreshToken[:16]
	if s.rdb.Exists(ctx, key).Val() == 0 {
		return nil, ErrTokenInvalid
	}

	// 查詢最新 role（role 可能被 admin 升級過）
	user, err := s.repo.FindByID(ctx, claims.UserID)
	if err != nil || user == nil {
		return nil, ErrTokenInvalid
	}

	// 舊 token 撤銷，發新的
	s.rdb.Del(ctx, key)
	return s.issueTokens(ctx, user.ID, user.Role)
}

func (s *Service) Logout(ctx context.Context, userID, refreshToken string) error {
	key := "user:" + userID + ":refresh:" + refreshToken[:16]
	return s.rdb.Del(ctx, key).Err()
}

// GetUserByID 查詢使用者資料（供 handler 呼叫）
func (s *Service) GetUserByID(ctx context.Context, id string) (*User, error) {
	return s.repo.FindByID(ctx, id)
}

func (s *Service) ValidateAccessToken(ctx context.Context, tokenStr string) (*Claims, error) {
	return s.parseToken(tokenStr)
}

func (s *Service) issueTokens(ctx context.Context, userID, role string) (*TokenPair, error) {
	now := time.Now()

	// Access Token（短效，含 role）
	accessClaims := &Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// Refresh Token（長效，也帶 role 方便 refresh 時判斷）
	refreshClaims := &Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	refreshToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign refresh token: %w", err)
	}

	// 將 Refresh Token 存入 Redis（用於撤銷驗證）
	key := "user:" + userID + ":refresh:" + refreshToken[:16]
	s.rdb.Set(ctx, key, 1, s.refreshTTL)

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(s.accessTTL.Seconds()),
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
