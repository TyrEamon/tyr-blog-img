package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr string

	D1AccountID  string
	D1APIToken   string
	D1DatabaseID string

	ImageDomain string

	R2Endpoint  string
	R2Region    string
	R2Bucket    string
	R2AccessKey string
	R2SecretKey string

	BotToken         string
	TGAllowedUserIDs map[int64]struct{}

	PixivPHPSESSID           string
	PixivUserID              string
	PixivTag                 string
	PixivRest                string
	PixivCrawlOrder          string
	PixivLimit               int
	PixivMaxPages            int
	PixivBootstrapMaxPages   int
	PixivIncrementalMaxPages int
	PixivIntervalMinutes     int

	TwitterAPIDomain         string
	TwitterAuthorEnabled     bool
	TwitterAuthorUsers       []string
	TwitterRSSSources        []string
	TwitterAuthorIntervalMin int
	TwitterAuthorFetchLimit  int
}

func Load() Config {
	d1AccountID := firstNonEmpty(
		strings.TrimSpace(os.Getenv("D1_ACCOUNT_ID")),
		strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID")),
	)
	d1APIToken := firstNonEmpty(
		strings.TrimSpace(os.Getenv("D1_API_TOKEN")),
		strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")),
	)
	d1DatabaseID := strings.TrimSpace(os.Getenv("D1_DATABASE_ID"))

	return Config{
		ListenAddr:   envOrDefault("LISTEN_ADDR", ":8080"),
		D1AccountID:  d1AccountID,
		D1APIToken:   d1APIToken,
		D1DatabaseID: d1DatabaseID,
		ImageDomain:  strings.TrimSpace(os.Getenv("IMAGE_DOMAIN")),
		R2Endpoint:   strings.TrimSpace(os.Getenv("R2_ENDPOINT")),
		R2Region:     envOrDefault("R2_REGION", "auto"),
		R2Bucket:     strings.TrimSpace(os.Getenv("R2_BUCKET")),
		R2AccessKey:  strings.TrimSpace(os.Getenv("R2_ACCESS_KEY_ID")),
		R2SecretKey:  strings.TrimSpace(os.Getenv("R2_SECRET_ACCESS_KEY")),

		BotToken:         strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		TGAllowedUserIDs: parseIDSet(os.Getenv("TG_ALLOWED_USER_IDS")),

		PixivPHPSESSID:           strings.TrimSpace(os.Getenv("PIXIV_PHPSESSID")),
		PixivUserID:              strings.TrimSpace(os.Getenv("PIXIV_USER_ID")),
		PixivTag:                 strings.TrimSpace(os.Getenv("PIXIV_TAG")),
		PixivRest:                envOrDefault("PIXIV_REST", "show"),
		PixivCrawlOrder:          envOrDefault("PIXIV_CRAWL_ORDER", "desc"),
		PixivLimit:               envInt("PIXIV_LIMIT", 40),
		PixivMaxPages:            envInt("PIXIV_MAX_PAGES", 0),
		PixivBootstrapMaxPages:   envInt("PIXIV_BOOTSTRAP_MAX_PAGES", -1),
		PixivIncrementalMaxPages: envInt("PIXIV_INCREMENTAL_MAX_PAGES", 2),
		PixivIntervalMinutes:     envInt("PIXIV_INTERVAL_MINUTES", 120),

		TwitterAPIDomain:         envOrDefault("TWITTER_API_DOMAIN", "fxtwitter.com"),
		TwitterAuthorEnabled:     envBool("TWITTER_AUTHOR_ENABLED", false),
		TwitterAuthorUsers:       parseStringList(os.Getenv("TWITTER_AUTHOR_USERS"), ","),
		TwitterRSSSources:        parseStringList(os.Getenv("TWITTER_RSS_SOURCES"), ";"),
		TwitterAuthorIntervalMin: envInt("TWITTER_AUTHOR_INTERVAL_MINUTES", 60),
		TwitterAuthorFetchLimit:  envInt("TWITTER_AUTHOR_FETCH_LIMIT", 20),
	}
}

func (c Config) HasD1() bool {
	return c.D1AccountID != "" && c.D1APIToken != "" && c.D1DatabaseID != ""
}

func (c Config) HasR2() bool {
	return c.R2Endpoint != "" && c.R2Bucket != "" && c.R2AccessKey != "" && c.R2SecretKey != ""
}

func (c Config) HasTelegram() bool {
	return c.BotToken != ""
}

func (c Config) HasPixivCrawler() bool {
	return c.PixivPHPSESSID != "" && c.PixivUserID != ""
}

func (c Config) HasTwitterAuthorCrawler() bool {
	return c.TwitterAuthorEnabled && len(c.TwitterAuthorUsers) > 0 && len(c.TwitterRSSSources) > 0
}

func (c Config) IsTGUserAllowed(userID int64) bool {
	if len(c.TGAllowedUserIDs) == 0 {
		return true
	}
	_, ok := c.TGAllowedUserIDs[userID]
	return ok
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseIDSet(raw string) map[int64]struct{} {
	out := make(map[int64]struct{})
	for _, part := range strings.Split(raw, ",") {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			log.Printf("invalid TG_ALLOWED_USER_IDS item %q: %v", v, err)
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func parseStringList(raw, sep string) []string {
	parts := strings.Split(raw, sep)
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
