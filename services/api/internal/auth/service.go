package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email already registered")
	ErrHandleTaken        = errors.New("handle already taken")
	ErrTokenInvalid       = errors.New("token invalid or expired")
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
	repo       *Repository
	rdb        *redis.Client
	jwtSecret  []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewService(repo *Repository, rdb *redis.Client, jwtSecret string, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{
		repo:       repo,
		rdb:        rdb,
		jwtSecret:  []byte(jwtSecret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
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
