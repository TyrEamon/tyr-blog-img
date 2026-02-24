package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

const pixivBootstrapStateKey = "pixiv_bootstrap_done"

func (a *App) StartPixivCrawler(ctx context.Context) {
	if a.Pixiv == nil || a.Cfg == nil || !a.Cfg.HasPixivCrawler() {
		log.Println("Pixiv crawler disabled (missing PIXIV_PHPSESSID or PIXIV_USER_ID)")
		return
	}
	go func() {
		a.crawlPixivOnce(ctx)
		ticker := time.NewTicker(time.Duration(maxInt(a.Cfg.PixivIntervalMinutes, 120)) * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.crawlPixivOnce(ctx)
			}
		}
	}()
}

func (a *App) crawlPixivOnce(ctx context.Context) {
	order := strings.ToLower(strings.TrimSpace(a.Cfg.PixivCrawlOrder))
	if order == "" {
		order = "desc"
	}
	bootstrapDone := false
	if val, ok, err := a.DB.GetCrawlerState(ctx, pixivBootstrapStateKey); err == nil && ok && val == "1" {
		bootstrapDone = true
	}
	maxPages := a.resolvePixivMaxPages(bootstrapDone)
	log.Printf("Pixiv crawl started (mode=%s, order=%s, tag=%q, rest=%q, limit=%d, max_pages=%d)",
		map[bool]string{true: "incremental", false: "bootstrap"}[bootstrapDone],
		order, a.Cfg.PixivTag, a.Cfg.PixivRest, maxInt(a.Cfg.PixivLimit, 40), maxPages)

	var err error
	if order == "asc" {
		err = a.crawlPixivAsc(ctx, maxPages)
	} else {
		err = a.crawlPixivDesc(ctx, maxPages)
	}
	if err != nil {
		log.Printf("Pixiv crawl failed: %v", err)
		log.Println("Pixiv crawl finished")
		return
	}
	if !bootstrapDone {
		if err := a.DB.SetCrawlerState(ctx, pixivBootstrapStateKey, "1"); err != nil {
			log.Printf("Pixiv bootstrap state write failed: %v", err)
		}
	}
	log.Println("Pixiv crawl finished")
}

func (a *App) resolvePixivMaxPages(bootstrapDone bool) int {
	if bootstrapDone {
		if a.Cfg.PixivIncrementalMaxPages >= 0 {
			return a.Cfg.PixivIncrementalMaxPages
		}
		return 2
	}
	if a.Cfg.PixivBootstrapMaxPages >= 0 {
		return a.Cfg.PixivBootstrapMaxPages
	}
	return a.Cfg.PixivMaxPages
}

func (a *App) crawlPixivDesc(ctx context.Context, maxPages int) error {
	offset := 0
	page := 0
	limit := maxInt(a.Cfg.PixivLimit, 40)
	for {
		ids, total, err := a.Pixiv.FetchBookmarkIDs(offset, limit, a.Cfg.PixivTag)
		if err != nil {
			return fmt.Errorf("pixiv bookmarks error: %w", err)
		}
		log.Printf("Pixiv page fetched (offset=%d, count=%d, total=%d)", offset, len(ids), total)
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return nil
			}
			a.processPixivID(ctx, id)
		}
		page++
		offset += limit
		if shouldStopPageLoop(page, offset, total, maxPages) {
			return nil
		}
		time.Sleep(4 * time.Second)
	}
}

func (a *App) crawlPixivAsc(ctx context.Context, maxPages int) error {
	offset := 0
	page := 0
	limit := maxInt(a.Cfg.PixivLimit, 40)
	var allIDs []string
	for {
		ids, total, err := a.Pixiv.FetchBookmarkIDs(offset, limit, a.Cfg.PixivTag)
		if err != nil {
			return fmt.Errorf("pixiv bookmarks error: %w", err)
		}
		if len(ids) == 0 {
			break
		}
		allIDs = append(allIDs, ids...)
		page++
		offset += limit
		if shouldStopPageLoop(page, offset, total, maxPages) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	for i := len(allIDs) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			return nil
		}
		a.processPixivID(ctx, allIDs[i])
	}
	return nil
}

func shouldStopPageLoop(page, offset, total, maxPages int) bool {
	if maxPages > 0 && page >= maxPages {
		return true
	}
	return total > 0 && offset >= total
}
