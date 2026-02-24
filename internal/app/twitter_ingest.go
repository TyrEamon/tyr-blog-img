package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"path"
	"strings"
	"time"

	"tyr-blog-img/internal/gallery"
)

const defaultTwitterAPIDomain = "fxtwitter.com"

type twitterStatusResp struct {
	Tweet   *twitterTweet `json:"tweet"`
	Message string        `json:"message"`
	Code    int           `json:"code"`
}

type twitterTweet struct {
	ID     string        `json:"id"`
	Text   string        `json:"text"`
	Author twitterAuthor `json:"author"`
	Media  *twitterMedia `json:"media"`
}

type twitterAuthor struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"screen_name"`
}

type twitterMedia struct {
	Photos []twitterMediaItem `json:"photos"`
	All    []twitterMediaItem `json:"all"`
}

type twitterMediaItem struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

func (a *App) ingestTwitterFromLink(ctx context.Context, item supportedLink) (*TGIngestResult, error) {
	stats, err := a.ingestTwitterTweet(ctx, item.ID, item.URL)
	if err != nil {
		return nil, err
	}
	return &TGIngestResult{
		ID:        stats.FirstID,
		Title:     stats.Title,
		SourceURL: item.URL,
		Summary:   fmt.Sprintf("Twitter %s done: +%d, skipped %d, failed %d", item.ID, stats.Downloaded, stats.Skipped, stats.Failed),
	}, nil
}

func (a *App) ingestTwitterTweet(ctx context.Context, tweetID, sourceURL string) (*ingestStats, error) {
	tweet, err := fetchTwitterTweet(ctx, a.Cfg.TwitterAPIDomain, tweetID)
	if err != nil {
		return nil, fmt.Errorf("twitter status: %w", err)
	}
	if strings.TrimSpace(sourceURL) == "" {
		sourceURL = canonicalTwitterURL(tweet.Author.Username, tweetID)
	}
	stats := &ingestStats{Title: buildTwitterTitle(tweet.Text, tweetID, tweet.Author.Username)}
	photos := tweet.photoURLs()
	if len(photos) == 0 {
		return nil, fmt.Errorf("tweet has no photo media")
	}
	for i, rawURL := range photos {
		sourceKey := fmt.Sprintf("twitter_%s_p%d", tweetID, i)
		if blocked, err := a.DB.IsBlocked(ctx, sourceKey); err == nil && blocked {
			stats.Skipped++
			continue
		}
		if exists, _ := a.DB.ExistsGallerySourceKey(ctx, sourceKey); exists {
			stats.Skipped++
			continue
		}
		data, err := downloadWithHeaders(ctx, buildTwitterImageURL(rawURL), "https://x.com/")
		if err != nil {
			stats.Failed++
			continue
		}
		storeRes, err := a.Gallery.StoreToGallery(ctx, gallery.StoreInput{
			Source:       "twitter",
			SourceKey:    sourceKey,
			SourceURL:    sourceURL,
			SourcePostID: tweetID,
			RawData:      data,
			CollectedAt:  time.Now().Unix(),
		})
		if err != nil {
			stats.Failed++
			continue
		}
		if storeRes.Added {
			stats.Downloaded++
			if stats.FirstID == "" {
				stats.FirstID = sourceKey
			}
		} else {
			stats.Skipped++
		}
		time.Sleep(1200 * time.Millisecond)
	}
	return stats, nil
}

func (t *twitterTweet) photoURLs() []string {
	if t == nil || t.Media == nil {
		return nil
	}
	items := make([]twitterMediaItem, 0, len(t.Media.Photos)+len(t.Media.All))
	items = append(items, t.Media.Photos...)
	items = append(items, t.Media.All...)
	return collectTwitterMediaURLs(items)
}

func collectTwitterMediaURLs(items []twitterMediaItem) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if mediaType := strings.ToLower(strings.TrimSpace(item.Type)); mediaType != "" && mediaType != "photo" {
			continue
		}
		u := strings.TrimSpace(item.URL)
		if u == "" {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

func fetchTwitterTweet(ctx context.Context, domain, tweetID string) (*twitterTweet, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = defaultTwitterAPIDomain
	}
	endpoint := fmt.Sprintf("https://api.%s/_/status/%s", domain, tweetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twitter status %d", resp.StatusCode)
	}
	var payload twitterStatusResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 && payload.Code != 200 {
		msg := strings.TrimSpace(payload.Message)
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("twitter api code %d: %s", payload.Code, msg)
	}
	if payload.Tweet == nil {
		return nil, fmt.Errorf("tweet not found")
	}
	return payload.Tweet, nil
}

func buildTwitterTitle(text, tweetID, username string) string {
	text = strings.TrimSpace(text)
	if text != "" {
		first := strings.TrimSpace(strings.Split(text, "\n")[0])
		if first != "" {
			return truncateRunes(first, 120)
		}
	}
	username = normalizeTwitterUsername(username)
	if username != "" {
		return fmt.Sprintf("%s/%s", username, tweetID)
	}
	return "Twitter/" + tweetID
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= limit {
		return string(r)
	}
	return string(r[:limit])
}

func buildTwitterImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return raw
	}
	if !strings.Contains(strings.ToLower(u.Hostname()), "twimg.com") {
		return raw
	}
	q := u.Query()
	q.Set("name", "orig")
	if q.Get("format") == "" {
		if ext := strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), "."); ext != "" {
			q.Set("format", ext)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
