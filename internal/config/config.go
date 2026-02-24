package config

import (
	"os"
	"strings"
)

type Config struct {
	ListenAddr   string
	D1AccountID  string
	D1APIToken   string
	D1DatabaseID string
	ImageDomain  string
	R2Endpoint   string
	R2Region     string
	R2Bucket     string
	R2AccessKey  string
	R2SecretKey  string
}

func Load() Config {
	return Config{
		ListenAddr:   envOrDefault("LISTEN_ADDR", ":8080"),
		D1AccountID:  strings.TrimSpace(os.Getenv("D1_ACCOUNT_ID")),
		D1APIToken:   strings.TrimSpace(os.Getenv("D1_API_TOKEN")),
		D1DatabaseID: strings.TrimSpace(os.Getenv("D1_DATABASE_ID")),
		ImageDomain:  strings.TrimSpace(os.Getenv("IMAGE_DOMAIN")),
		R2Endpoint:   strings.TrimSpace(os.Getenv("R2_ENDPOINT")),
		R2Region:     envOrDefault("R2_REGION", "auto"),
		R2Bucket:     strings.TrimSpace(os.Getenv("R2_BUCKET")),
		R2AccessKey:  strings.TrimSpace(os.Getenv("R2_ACCESS_KEY_ID")),
		R2SecretKey:  strings.TrimSpace(os.Getenv("R2_SECRET_ACCESS_KEY")),
	}
}

func (c Config) HasD1() bool {
	return c.D1AccountID != "" && c.D1APIToken != "" && c.D1DatabaseID != ""
}

func (c Config) HasR2() bool {
	return c.R2Endpoint != "" && c.R2Bucket != "" && c.R2AccessKey != "" && c.R2SecretKey != ""
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
