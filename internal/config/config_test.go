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
