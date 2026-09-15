package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr string
	IPv4Path string
	IPv6Path string
	LogLevel slog.Level
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr: value("HTTP_ADDR", ":8080"),
		IPv4Path: value("IPV4_DB_PATH", "data/ip2region_v4.xdb"),
		IPv6Path: value("IPV6_DB_PATH", "data/ip2region_v6.xdb"),
	}
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("invalid HTTP_ADDR: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("invalid HTTP_ADDR port: %q", port)
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(value("LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("invalid LOG_LEVEL: %w", err)
	}
	return cfg, nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
