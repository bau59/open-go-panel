package config

import (
	"errors"
	"os"
)

const (
	defaultListenAddr = "127.0.0.1:8443"
	defaultAdminUser  = "admin"
)

type Config struct {
	ListenAddr    string
	AdminUser     string
	AdminPassword string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:    envOrDefault("OGP_LISTEN_ADDR", defaultListenAddr),
		AdminUser:     envOrDefault("OGP_ADMIN_USER", defaultAdminUser),
		AdminPassword: os.Getenv("OGP_ADMIN_PASSWORD"),
	}

	if cfg.AdminPassword == "" {
		return Config{}, errors.New("OGP_ADMIN_PASSWORD is required")
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
