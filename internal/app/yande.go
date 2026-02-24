package app

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"tyr-blog-img/internal/gallery"
)

const (
	yandeAPITimeout      = 60 * time.Second
	yandeDownloadTimeout = 90 * time.Second
	yandeAPIRetries      = 2
	yandeDownloadRetries = 2
	yandeRetryBackoff    = 1500 * time.Millisecond
)

type yandePost struct {
	ID          int    `json:"id"`
	ParentID    *int   `json:"parent_id"`
	HasChildren bool   `json:"has_children"`
	FileURL     string `json:"file_url"`
	JPEGURL     string `json:"jpeg_url"`
	PNGURL      string `json:"png_url"`
	SampleURL   string `json:"sample_url"`
	Tags        string `json:"tags"`
}

func (p yandePost) imageURLCandidates() []string {
	rawCandidates := []string{p.FileURL, p.JPEGURL, p.PNGURL, p.SampleURL}
	out := make([]string, 0, len(rawCandidates))
	seen := make(map[string]struct{}, len(rawCandidates))
	for _, raw := range rawCandidates {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "//") {
			raw = "https:" + raw
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
	}
	return out
}

func (a *App) ingestYandeFromLink(ctx context.Context, item supportedLink) (*TGIngestResult, error) {
	posts, err := fetchYandeFamilyPosts(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, fmt.Errorf("yande post not found")
	}
	stats, err := a.ingestYandePosts(ctx, posts)
	if err != nil {
		return nil, err
	}
	return &TGIngestResult{
		ID:        stats.FirstID,
		Title:     stats.Title,
		SourceURL: item.URL,
		Summary:   fmt.Sprintf("Yande %s done: +%d, skipped %d, failed %d", item.ID, stats.Downloaded, stats.Skipped, stats.Failed),
	}, nil
}

func (a *App) ingestYandePosts(ctx context.Context, posts []yandePost) (*ingestStats, error) {
	stats := &ingestStats{Title: "Yande"}
	for _, post := range posts {
		sourceKey := fmt.Sprintf("yande_%d", post.ID)
		if blocked, err := a.DB.IsBlocked(ctx, sourceKey); err == nil && blocked {
			stats.Skipped++
			continue
		}
		if exists, _ := a.DB.ExistsGallerySourceKey(ctx, sourceKey); exists {
			stats.Skipped++
			continue
		}
		imgURLs := post.imageURLCandidates()
		if len(imgURLs) == 0 {
			stats.Failed++
			continue
		}
		var (
			data []byte
			err  error
		)
		for _, u := range imgURLs {
			data, err = downloadWithHeadersRetry(ctx, u, "https://yande.re/", yandeDownloadTimeout, yandeDownloadRetries, yandeRetryBackoff)
			if err == nil {
				break
			}
		}
		if err != nil {
			stats.Failed++
			continue
		}
		storeRes, err := a.Gallery.StoreToGallery(ctx, gallery.StoreInput{
			Source:       "yande",
			SourceKey:    sourceKey,
			SourceURL:    fmt.Sprintf("https://yande.re/post/show/%d", post.ID),
			SourcePostID: strconv.Itoa(post.ID),
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

func fetchYandePosts(ctx context.Context, tags string) ([]yandePost, error) {
	tags = strings.TrimSpace(tags)
	if tags == "" {
		return nil, fmt.Errorf("yande tags is empty")
	}
	endpoint := fmt.Sprintf("https://yande.re/post.json?tags=%s", neturl.QueryEscape(tags))
	body, err := downloadWithHeadersRetry(ctx, endpoint, "https://yande.re/", yandeAPITimeout, yandeAPIRetries, yandeRetryBackoff)
	if err != nil {
		return nil, err
	}
	var arr []yandePost
	if err := json.Unmarshal(body, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

func fetchYandePost(ctx context.Context, id string) (*yandePost, error) {
	arr, err := fetchYandePosts(ctx, fmt.Sprintf("id:%s", strings.TrimSpace(id)))
	if err != nil {
		return nil, err
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("yande post not found")
	}
	return &arr[0], nil
}

func fetchYandeFamilyPosts(ctx context.Context, id string) ([]yandePost, error) {
	seed, err := fetchYandePost(ctx, id)
	if err != nil {
		return nil, err
	}
	rootID := seed.ID
	if seed.ParentID != nil && *seed.ParentID > 0 {
		rootID = *seed.ParentID
	}
	family, err := fetchYandePosts(ctx, fmt.Sprintf("parent:%d", rootID))
	if err != nil || len(family) == 0 {
		return []yandePost{*seed}, nil
	}
	merged := make(map[int]yandePost, len(family)+1)
	for _, p := range family {
		merged[p.ID] = p
	}
	merged[seed.ID] = *seed
	out := make([]yandePost, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
