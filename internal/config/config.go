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
	Retention      Retention
	PublicHTTPAck  bool
}

type Retention struct {
	Raw        time.Duration
	OneMinute  time.Duration
	FiveMinute time.Duration
	Interval   time.Duration
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
		PublicHTTPAck: envBool("MYPROBE_PUBLIC_HTTP_ACKNOWLEDGED", false),
		Retention: Retention{
			Raw:        7 * 24 * time.Hour,
			OneMinute:  30 * 24 * time.Hour,
			FiveMinute: 365 * 24 * time.Hour,
			Interval:   time.Hour,
		},
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
	var err error
	if cfg.Retention.Raw, err = envDays("MYPROBE_RAW_RETENTION_DAYS", cfg.Retention.Raw); err != nil {
		return Config{}, err
	}
	if cfg.Retention.OneMinute, err = envDays("MYPROBE_MINUTE_RETENTION_DAYS", cfg.Retention.OneMinute); err != nil {
		return Config{}, err
	}
	if cfg.Retention.FiveMinute, err = envDays("MYPROBE_FIVE_MINUTE_RETENTION_DAYS", cfg.Retention.FiveMinute); err != nil {
		return Config{}, err
	}
	if cfg.Retention.Interval, err = envHours("MYPROBE_RETENTION_INTERVAL_HOURS", cfg.Retention.Interval); err != nil {
		return Config{}, err
	}
	if cfg.Retention.OneMinute < cfg.Retention.Raw || cfg.Retention.FiveMinute < cfg.Retention.OneMinute {
		return Config{}, errors.New("retention must satisfy raw <= one-minute <= five-minute")
	}
	return cfg, nil
}

func envDays(name string, fallback time.Duration) (time.Duration, error) {
	if os.Getenv(name) == "" {
		return fallback, nil
	}
	days, err := strconv.Atoi(os.Getenv(name))
	if err != nil || days < 1 || days > 3650 {
		return 0, errors.New(name + " must be between 1 and 3650")
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

func envHours(name string, fallback time.Duration) (time.Duration, error) {
	if os.Getenv(name) == "" {
		return fallback, nil
	}
	hours, err := strconv.Atoi(os.Getenv(name))
	if err != nil || hours < 1 || hours > 168 {
		return 0, errors.New(name + " must be between 1 and 168")
	}
	return time.Duration(hours) * time.Hour, nil
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
