package config

import "testing"

func TestTrustedProxiesAreExplicitAndValidated(t *testing.T) {
	t.Setenv("MYPROBE_TRUST_PROXY", "")
	t.Setenv("MYPROBE_TRUSTED_PROXIES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("default trusted proxies = %#v", cfg.TrustedProxies)
	}
	t.Setenv("MYPROBE_TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8")
	cfg, err = Load()
	if err != nil || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %#v, %v", cfg.TrustedProxies, err)
	}
	t.Setenv("MYPROBE_TRUSTED_PROXIES", "not-a-network")
	if _, err := Load(); err == nil {
		t.Fatal("invalid trusted proxy was accepted")
	}
}

func TestRetentionConfigurationIsOrderedAndConfigurable(t *testing.T) {
	t.Setenv("MYPROBE_RAW_RETENTION_DAYS", "5")
	t.Setenv("MYPROBE_MINUTE_RETENTION_DAYS", "20")
	t.Setenv("MYPROBE_FIVE_MINUTE_RETENTION_DAYS", "400")
	t.Setenv("MYPROBE_RETENTION_INTERVAL_HOURS", "6")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retention.Raw.Hours() != 120 || cfg.Retention.OneMinute.Hours() != 480 || cfg.Retention.FiveMinute.Hours() != 9600 || cfg.Retention.Interval.Hours() != 6 {
		t.Fatalf("retention = %#v", cfg.Retention)
	}
	t.Setenv("MYPROBE_MINUTE_RETENTION_DAYS", "4")
	if _, err := Load(); err == nil {
		t.Fatal("unordered retention configuration was accepted")
	}
}
