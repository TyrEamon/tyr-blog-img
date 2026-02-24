package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tyr-blog-img/internal/gallery"
)

func (a *App) ingestPixivFromLink(ctx context.Context, item supportedLink) (*TGIngestResult, error) {
	stats, err := a.ingestPixivArtwork(ctx, item.ID, item.URL)
	if err != nil {
		return nil, err
	}
	return &TGIngestResult{
		ID:        stats.FirstID,
		Title:     stats.Title,
		SourceURL: item.URL,
		Summary:   fmt.Sprintf("Pixiv %s done: +%d, skipped %d, failed %d", item.ID, stats.Downloaded, stats.Skipped, stats.Failed),
	}, nil
}

func (a *App) ingestPixivArtwork(ctx context.Context, artworkID, sourceURL string) (*ingestStats, error) {
	if a.Pixiv == nil {
		return nil, fmt.Errorf("pixiv client not configured")
	}
	detail, err := a.Pixiv.FetchDetail(artworkID)
	if err != nil {
		return nil, err
	}
	pages, err := a.Pixiv.FetchPages(artworkID)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("pixiv pages empty")
	}
	if strings.TrimSpace(sourceURL) == "" {
		sourceURL = fmt.Sprintf("https://www.pixiv.net/artworks/%s", artworkID)
	}

	stats := &ingestStats{Title: strings.TrimSpace(detail.Body.Title)}
	if stats.Title == "" {
		stats.Title = "Pixiv/" + artworkID
	}
	for i, p := range pages {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		sourceKey := fmt.Sprintf("pixiv_%s_p%d", artworkID, i)
		if blocked, err := a.DB.IsBlocked(ctx, sourceKey); err == nil && blocked {
			stats.Skipped++
			continue
		}
		if exists, _ := a.DB.ExistsGallerySourceKey(ctx, sourceKey); exists {
			stats.Skipped++
			continue
		}
		data, err := a.Pixiv.Download(p.URL)
		if err != nil {
			stats.Failed++
			continue
		}
		storeRes, err := a.Gallery.StoreToGallery(ctx, gallery.StoreInput{
			Source:       "pixiv",
			SourceKey:    sourceKey,
			SourceURL:    sourceURL,
			SourcePostID: artworkID,
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
