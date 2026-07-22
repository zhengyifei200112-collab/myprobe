package config

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddress  string
	DatabasePath   string
	AdminUsername  string
	AdminPassword  string
	EncryptionKey  string
	SessionTTL     time.Duration
	CookieSecure   bool
	TrustedProxies []string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress: env("MYPROBE_LISTEN", ":25775"),
		DatabasePath:  env("MYPROBE_DATABASE", filepath.Join("data", "myprobe.db")),
		AdminUsername: env("MYPROBE_ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("MYPROBE_ADMIN_PASSWORD"),
		EncryptionKey: os.Getenv("MYPROBE_ENCRYPTION_KEY"),
		SessionTTL:    24 * time.Hour,
		CookieSecure:  envBool("MYPROBE_COOKIE_SECURE", false),
	}
	if raw := strings.TrimSpace(os.Getenv("MYPROBE_TRUSTED_PROXIES")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if net.ParseIP(item) == nil {
				if _, _, err := net.ParseCIDR(item); err != nil {
					return Config{}, errors.New("MYPROBE_TRUSTED_PROXIES must contain IP addresses or CIDRs")
				}
			}
			cfg.TrustedProxies = append(cfg.TrustedProxies, item)
		}
	} else if envBool("MYPROBE_TRUST_PROXY", false) {
		cfg.TrustedProxies = []string{"127.0.0.1", "::1"}
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
