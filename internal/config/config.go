package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port          string
	DataDir       string
	DBPath        string
	BaseURL       string
	MaxUploadMB   int64
	RateLimitRPM  int
	RateLimitBurst int
}

func Load() (*Config, error) {
	c := &Config{
		Port:           getEnv("PORT", "8080"),
		DataDir:        getEnv("DATA_DIR", "/data"),
		BaseURL:        getEnv("BASE_URL", ""),
		MaxUploadMB:    getEnvInt64("MAX_UPLOAD_MB", 25),
		RateLimitRPM:   getEnvInt("RATE_LIMIT_RPM", 30),
		RateLimitBurst: getEnvInt("RATE_LIMIT_BURST", 10),
	}
	if c.BaseURL == "" {
		return nil, fmt.Errorf("BASE_URL is required (e.g. https://img.example.com)")
	}
	c.DBPath = getEnv("DB_PATH", c.DataDir+"/clipshot.db")
	return c, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
