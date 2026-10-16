package config

import "testing"

func TestReleaseCheckAutoConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value            string
		enabled, invalid bool
	}{{"", true, false}, {"false", false, false}, {"true", true, false}, {"0", false, false}, {"enabled", false, true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("MYPROBE_RELEASE_CHECKS_AUTO", tc.value)
			cfg, err := Load()
			if (err != nil) != tc.invalid || (!tc.invalid && cfg.ReleaseChecksAuto != tc.enabled) {
				t.Fatalf("enabled=%v err=%v", cfg.ReleaseChecksAuto, err)
			}
		})
	}
}
