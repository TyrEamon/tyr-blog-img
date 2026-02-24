package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	accountID string
	apiToken  string
	dbID      string
	http      *http.Client
}

type d1Request struct {
	SQL    string        `json:"sql"`
	Params []interface{} `json:"params"`
}

type d1Response struct {
	Success bool `json:"success"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Result []struct {
		Results []map[string]interface{} `json:"results"`
		Success bool                     `json:"success"`
	} `json:"result"`
}

type GalleryImage struct {
	ID           string
	Source       string
	SourceKey    string
	SourceURL    string
	SourcePostID string
	SHA256       string
	Orientation  string // h / v
	Seq          int64
	R2Key        string
	Width        int
	Height       int
	Bytes        int64
	MimeType     string
	PublishedAt  int64
	CollectedAt  int64
	Status       string
}

type GalleryCounts struct {
	H int64 `json:"h"`
	V int64 `json:"v"`
}

func New(accountID, apiToken, dbID string) *Client {
	return &Client{
		accountID: accountID,
		apiToken:  apiToken,
		dbID:      dbID,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) exec(ctx context.Context, sql string, params ...interface{}) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/d1/database/%s/query", c.accountID, c.dbID)
	body, err := json.Marshal(d1Request{SQL: sql, Params: params})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data d1Response
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if !data.Success {
		if len(data.Errors) > 0 {
			return nil, fmt.Errorf("d1 error: %s", data.Errors[0].Message)
		}
		return nil, fmt.Errorf("d1 error")
	}
	if len(data.Result) == 0 {
		return nil, nil
	}
	return data.Result[0].Results, nil
}

func (c *Client) EnsureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS gallery_images (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			source_key TEXT NOT NULL UNIQUE,
			source_url TEXT,
			source_post_id TEXT,
			sha256 TEXT NOT NULL UNIQUE,
			orientation TEXT NOT NULL,
			seq INTEGER NOT NULL,
			r2_key TEXT NOT NULL UNIQUE,
			width INTEGER NOT NULL,
			height INTEGER NOT NULL,
			bytes INTEGER NOT NULL DEFAULT 0,
			mime_type TEXT NOT NULL DEFAULT 'image/webp',
			published_at INTEGER NOT NULL DEFAULT 0,
			collected_at INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'active'
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gallery_images_orientation_seq
			ON gallery_images(orientation, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_gallery_images_status_orientation_seq
			ON gallery_images(status, orientation, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_gallery_images_collected_at
			ON gallery_images(collected_at)`,
		`CREATE INDEX IF NOT EXISTS idx_gallery_images_source
			ON gallery_images(source, source_post_id)`,
		`CREATE TABLE IF NOT EXISTS ingest_blocklist (
			block_key TEXT PRIMARY KEY,
			reason TEXT,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS crawler_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	}

	for _, stmt := range stmts {
		if _, err := c.exec(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) IsBlocked(ctx context.Context, key string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return false, nil
	}
	rows, err := c.exec(ctx, "SELECT 1 FROM ingest_blocklist WHERE block_key = ? LIMIT 1", key)
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (c *Client) ExistsGallerySourceKey(ctx context.Context, sourceKey string) (bool, error) {
	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey == "" {
		return false, nil
	}
	rows, err := c.exec(ctx, "SELECT 1 FROM gallery_images WHERE source_key = ? LIMIT 1", sourceKey)
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (c *Client) ExistsGallerySHA256(ctx context.Context, sha256 string) (bool, error) {
	sha256 = strings.ToLower(strings.TrimSpace(sha256))
	if sha256 == "" {
		return false, nil
	}
	rows, err := c.exec(ctx, "SELECT 1 FROM gallery_images WHERE sha256 = ? LIMIT 1", sha256)
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (c *Client) GetCrawlerState(ctx context.Context, key string) (string, bool, error) {
	rows, err := c.exec(ctx, "SELECT value FROM crawler_state WHERE key = ? LIMIT 1", key)
	if err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	return rowString(rows[0], "value"), true, nil
}

func (c *Client) SetCrawlerState(ctx context.Context, key, value string) error {
	_, err := c.exec(ctx,
		"INSERT OR REPLACE INTO crawler_state (key, value, updated_at) VALUES (?, ?, ?)",
		strings.TrimSpace(key), strings.TrimSpace(value), time.Now().Unix(),
	)
	return err
}

func (c *Client) NextGallerySeq(ctx context.Context, orientation string) (int64, error) {
	orientation = normalizeOrientation(orientation)
	if orientation == "" {
		return 0, fmt.Errorf("invalid orientation")
	}
	rows, err := c.exec(ctx,
		"SELECT COALESCE(MAX(seq), 0) + 1 AS next_seq FROM gallery_images WHERE orientation = ?",
		orientation,
	)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 1, nil
	}
	next := rowInt64(rows[0], "next_seq")
	if next < 1 {
		return 1, nil
	}
	return next, nil
}

func (c *Client) InsertGalleryImage(ctx context.Context, img GalleryImage) error {
	img.Source = strings.TrimSpace(img.Source)
	img.SourceKey = strings.TrimSpace(img.SourceKey)
	img.SourceURL = strings.TrimSpace(img.SourceURL)
	img.SourcePostID = strings.TrimSpace(img.SourcePostID)
	img.SHA256 = strings.ToLower(strings.TrimSpace(img.SHA256))
	img.Orientation = normalizeOrientation(img.Orientation)
	img.R2Key = strings.TrimSpace(img.R2Key)
	img.MimeType = strings.TrimSpace(img.MimeType)
	img.Status = strings.TrimSpace(img.Status)

	if img.ID == "" {
		img.ID = strings.TrimSpace(img.SourceKey)
	}
	if img.Orientation == "" {
		return fmt.Errorf("invalid orientation")
	}
	if img.SourceKey == "" {
		return fmt.Errorf("source_key is required")
	}
	if img.SHA256 == "" {
		return fmt.Errorf("sha256 is required")
	}
	if img.Seq < 1 {
		return fmt.Errorf("seq must be >= 1")
	}
	if img.R2Key == "" {
		return fmt.Errorf("r2_key is required")
	}
	if img.MimeType == "" {
		img.MimeType = "image/webp"
	}
	if img.CollectedAt <= 0 {
		img.CollectedAt = time.Now().Unix()
	}
	if img.Status == "" {
		img.Status = "active"
	}

	sql := `INSERT INTO gallery_images (
		id, source, source_key, source_url, source_post_id,
		sha256, orientation, seq, r2_key,
		width, height, bytes, mime_type,
		published_at, collected_at, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := c.exec(ctx, sql,
		img.ID,
		img.Source,
		img.SourceKey,
		img.SourceURL,
		img.SourcePostID,
		img.SHA256,
		img.Orientation,
		img.Seq,
		img.R2Key,
		img.Width,
		img.Height,
		img.Bytes,
		img.MimeType,
		img.PublishedAt,
		img.CollectedAt,
		img.Status,
	)
	return err
}

func (c *Client) CountGalleryActive(ctx context.Context) (GalleryCounts, error) {
	rows, err := c.exec(ctx, `
		SELECT orientation, COUNT(*) AS c
		FROM gallery_images
		WHERE status = 'active'
		GROUP BY orientation
	`)
	if err != nil {
		return GalleryCounts{}, err
	}

	var counts GalleryCounts
	for _, row := range rows {
		switch normalizeOrientation(rowString(row, "orientation")) {
		case "h":
			counts.H = rowInt64(row, "c")
		case "v":
			counts.V = rowInt64(row, "c")
		}
	}
	return counts, nil
}

func normalizeOrientation(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "h" || v == "v" {
		return v
	}
	return ""
}

func rowString(row map[string]interface{}, key string) string {
	if row == nil {
		return ""
	}
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", x))
	}
}

func rowInt64(row map[string]interface{}, key string) int64 {
	if row == nil {
		return 0
	}
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	case json.Number:
		n, _ := x.Int64()
		return n
	default:
		n, _ := strconv.ParseInt(strings.TrimSpace(fmt.Sprintf("%v", x)), 10, 64)
		return n
	}
}
