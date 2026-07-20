package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ListenAddress string
	DatabasePath  string
	AdminUsername string
	AdminPassword string
	SessionTTL    time.Duration
	CookieSecure  bool
	TrustedProxy  bool
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress: env("MYPROBE_LISTEN", ":25775"),
		DatabasePath:  env("MYPROBE_DATABASE", filepath.Join("data", "myprobe.db")),
		AdminUsername: env("MYPROBE_ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("MYPROBE_ADMIN_PASSWORD"),
		SessionTTL:    24 * time.Hour,
		CookieSecure:  envBool("MYPROBE_COOKIE_SECURE", false),
		TrustedProxy:  envBool("MYPROBE_TRUST_PROXY", false),
	}
	if raw := os.Getenv("MYPROBE_SESSION_HOURS"); raw != "" {
		hours, err := strconv.Atoi(raw)
		if err != nil || hours < 1 || hours > 24*30 {
			return Config{}, errors.New("MYPROBE_SESSION_HOURS must be between 1 and 720")
		}
		cfg.SessionTTL = time.Duration(hours) * time.Hour
	}
	return cfg, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
