package config

import (
	"log/slog"
	"testing"
)

func TestDefaultsAndOverrides(t *testing.T) {
	for _, key := range []string{"HTTP_ADDR", "IPV4_DB_PATH", "IPV6_DB_PATH", "LOG_LEVEL"} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil || cfg.HTTPAddr != ":8080" || cfg.IPv4Path != "data/ip2region_v4.xdb" || cfg.IPv6Path != "data/ip2region_v6.xdb" || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:18080")
	t.Setenv("IPV4_DB_PATH", "/tmp/v4.xdb")
	t.Setenv("IPV6_DB_PATH", "/tmp/v6.xdb")
	t.Setenv("LOG_LEVEL", "debug")
	cfg, err = Load()
	if err != nil || cfg.HTTPAddr != "127.0.0.1:18080" || cfg.IPv4Path != "/tmp/v4.xdb" || cfg.IPv6Path != "/tmp/v6.xdb" || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("overrides: %+v, %v", cfg, err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{"HTTP_ADDR", "8080"}, {"HTTP_ADDR", ":0"}, {"HTTP_ADDR", ":65536"}, {"HTTP_ADDR", ":http"}, {"LOG_LEVEL", "invalid"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv("HTTP_ADDR", ":8080")
			t.Setenv("LOG_LEVEL", "info")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatalf("accepted invalid %s=%s", test.key, test.value)
			}
		})
	}
}
