package config

import "testing"

func TestHistoryMmapEnablement(t *testing.T) {
	for _, test := range []struct {
		input string
		want  bool
		bad   bool
	}{{"", true, false}, {"true", true, false}, {" 1 ", true, false}, {"false", false, false}, {"0", false, false}, {"enabled", false, true}} {
		t.Run(test.input, func(t *testing.T) {
			t.Setenv("MYPROBE_HISTORY_MMAP_ENABLED", test.input)
			cfg, err := Load()
			if (err != nil) != test.bad || (!test.bad && cfg.HistoryMmapEnabled != test.want) {
				t.Fatalf("history mmap=%v error=%v", cfg.HistoryMmapEnabled, err)
			}
		})
	}
}
