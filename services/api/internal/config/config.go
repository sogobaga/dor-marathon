package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env          string
	Port         string
	DatabaseURL  string
	RedisURL     string
	JWTSecret    string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	CORSOrigins  []string
	GoogleClientID string // 選用：空字串 = Google 登入未啟用
}

func Load() *Config {
	return &Config{
		Env:         getEnv("ENV", "development"),
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: mustEnv("DATABASE_URL"),
		RedisURL:    mustEnv("REDIS_URL"),
		JWTSecret:   mustEnv("JWT_SECRET"),
		AccessTTL:   parseDuration(getEnv("JWT_ACCESS_TTL", "15m")),
		RefreshTTL:  parseDuration(getEnv("JWT_REFRESH_TTL", "168h")),
		CORSOrigins: strings.Split(getEnv("CORS_ORIGINS", "http://localhost:3000"), ","),
		GoogleClientID: getEnv("GOOGLE_CLIENT_ID", ""),
	}
}

func (c *Config) IsDev() bool { return c.Env == "development" }

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
