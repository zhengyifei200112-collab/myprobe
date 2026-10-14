package config

import "testing"

func TestHTTPProbeEnablement(t *testing.T) {
	for _, tc := range []struct {
		value            string
		enabled, invalid bool
	}{{"", false, false}, {"false", false, false}, {"true", true, false}, {"1", true, false}, {"enabled", false, true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("MYPROBE_HTTP_PROBES_ENABLED", tc.value)
			cfg, err := Load()
			if (err != nil) != tc.invalid || (!tc.invalid && cfg.HTTPProbesEnabled != tc.enabled) {
				t.Fatalf("enabled=%v error=%v", cfg.HTTPProbesEnabled, err)
			}
		})
	}
}
