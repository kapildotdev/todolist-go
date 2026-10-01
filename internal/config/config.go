package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port         string
	DatabaseURL  string
	JWTSecret    string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	AllowOffline bool
}

func Load() Config {
	_ = godotenv.Load() // optional .env for local dev

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "dev-only-change-me-32bytes-minimum!!"
	}
	accessTTL := 15 * time.Minute
	if v := os.Getenv("ACCESS_TTL_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			accessTTL = time.Duration(n) * time.Minute
		}
	}
	refreshTTL := 7 * 24 * time.Hour
	if v := os.Getenv("REFRESH_TTL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			refreshTTL = time.Duration(n) * time.Hour
		}
	}
	return Config{
		Port:        port,
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   secret,
		AccessTTL:   accessTTL,
		RefreshTTL:  refreshTTL,
	}
}
