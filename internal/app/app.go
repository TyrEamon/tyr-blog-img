package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"tyr-blog-img/internal/config"
	"tyr-blog-img/internal/database"
	"tyr-blog-img/internal/gallery"
	"tyr-blog-img/internal/pixiv"
	"tyr-blog-img/internal/telegram"

	"github.com/go-telegram/bot/models"
)

type App struct {
	Cfg     *config.Config
	DB      *database.Client
	TG      *telegram.Client
	Pixiv   *pixiv.Client
	Gallery *gallery.Service
}

type TGIngestResult struct {
	ID        string
	Title     string
	SourceURL string
	Summary   string
}

func New(cfg *config.Config, db *database.Client, tg *telegram.Client, pv *pixiv.Client, g *gallery.Service) *App {
	return &App{Cfg: cfg, DB: db, TG: tg, Pixiv: pv, Gallery: g}
}

func (a *App) CanHandleTGMessage(msg *models.Message) bool {
	if msg == nil {
		return false
	}
	if len(msg.Photo) > 0 || msg.Document != nil || msg.Video != nil || msg.Animation != nil {
		return true
	}
	return len(extractSupportedLinks(msg.Text, msg.Caption)) > 0
}

func (a *App) HandleTGMessage(ctx context.Context, msg *models.Message) (*TGIngestResult, error) {
	if msg == nil {
		return nil, nil
	}
	if a.TG == nil || a.Gallery == nil {
		return &TGIngestResult{Summary: "服务未完成初始化"}, nil
	}
	if msg.From == nil || !a.Cfg.IsTGUserAllowed(msg.From.ID) {
		return &TGIngestResult{Summary: "未授权使用该入库功能"}, nil
	}

	links := extractSupportedLinks(msg.Text, msg.Caption)
	media, hasMedia := extractIncomingMedia(msg)
	if !hasMedia && len(links) == 0 {
		return nil, nil
	}

	if hasMedia && media.isImage() {
		data, filePath, err := a.TG.DownloadFile(ctx, media.FileID)
		if err != nil {
			return nil, err
		}
		sourceKey := fmt.Sprintf("tg_%d_%d", msg.Chat.ID, msg.ID)
		if media.FileUniqueID != "" {
			sourceKey = fmt.Sprintf("tgfile_%s", media.FileUniqueID)
		}
		sourceURL := fmt.Sprintf("tg://chat/%d/message/%d", msg.Chat.ID, msg.ID)
		storeRes, err := a.Gallery.StoreToGallery(ctx, gallery.StoreInput{
			Source:       "tg",
			SourceKey:    sourceKey,
			SourceURL:    sourceURL,
			SourcePostID: fmt.Sprintf("%d_%d", msg.Chat.ID, msg.ID),
			RawData:      data,
			CollectedAt:  time.Now().Unix(),
		})
		if err != nil {
			return nil, err
		}
		return &TGIngestResult{
			ID:        sourceKey,
			Title:     fallbackTitle(msg.Caption, msg.Text, "TG"),
			SourceURL: sourceURL,
			Summary:   buildStoreSummary("TG图片", storeRes, filePath),
		}, nil
	}

	if hasMedia && !media.isImage() {
		return &TGIngestResult{Summary: "暂不处理视频/GIF，仅处理图片与链接"}, nil
	}

	return a.handleTGLinks(ctx, links)
}

func fallbackTitle(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return "Untitled"
}

func buildStoreSummary(prefix string, res gallery.StoreResult, extra string) string {
	if !res.Added {
		reason := strings.TrimSpace(res.SkipReason)
		if reason == "" {
			reason = "skipped"
		}
		if extra != "" {
			return fmt.Sprintf("%s：跳过（%s）\n文件：%s", prefix, reason, extra)
		}
		return fmt.Sprintf("%s：跳过（%s）", prefix, reason)
	}
	if extra != "" {
		return fmt.Sprintf("%s：已入库 %s/%d\ncounts: h=%d v=%d\n文件：%s",
			prefix, res.Image.Orientation, res.Image.Seq, res.Counts.H, res.Counts.V, extra)
	}
	return fmt.Sprintf("%s：已入库 %s/%d\ncounts: h=%d v=%d",
		prefix, res.Image.Orientation, res.Image.Seq, res.Counts.H, res.Counts.V)
}

func (a *App) processPixivID(ctx context.Context, id string) {
	stats, err := a.ingestPixivArtwork(ctx, id, "")
	if err != nil {
		log.Printf("pixiv ingest failed id=%s err=%v", id, err)
		return
	}
	log.Printf("pixiv ingest done id=%s added=%d skipped=%d failed=%d", id, stats.Downloaded, stats.Skipped, stats.Failed)
}
