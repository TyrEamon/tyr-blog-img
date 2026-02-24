package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tyr-blog-img/internal/app"
	"tyr-blog-img/internal/config"
	"tyr-blog-img/internal/database"
	"tyr-blog-img/internal/gallery"
	"tyr-blog-img/internal/pixiv"
	"tyr-blog-img/internal/storage"
	"tyr-blog-img/internal/telegram"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func main() {
	cfg := config.Load()

	if !cfg.HasD1() {
		log.Fatal("D1 credentials missing")
	}
	if !cfg.HasR2() {
		log.Fatal("R2 credentials missing")
	}

	db := database.New(cfg.D1AccountID, cfg.D1APIToken, cfg.D1DatabaseID)
	bootstrapCtx, cancelBootstrap := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBootstrap()
	if err := db.EnsureSchema(bootstrapCtx); err != nil {
		log.Fatalf("ensure schema error: %v", err)
	}
	log.Println("D1 schema ready")

	r2, err := storage.NewR2Client(bootstrapCtx, storage.R2Config{
		Endpoint:  cfg.R2Endpoint,
		Region:    cfg.R2Region,
		Bucket:    cfg.R2Bucket,
		AccessKey: cfg.R2AccessKey,
		SecretKey: cfg.R2SecretKey,
	})
	if err != nil {
		log.Fatalf("init r2 client error: %v", err)
	}

	gallerySvc := gallery.NewService(db, r2, nil)
	pv := pixiv.New(cfg.PixivPHPSESSID, cfg.PixivUserID, cfg.PixivRest)

	var tg *telegram.Client
	if cfg.HasTelegram() {
		tg, err = telegram.New(cfg.BotToken)
		if err != nil {
			log.Fatalf("init telegram bot error: %v", err)
		}
	} else {
		log.Println("warning: BOT_TOKEN missing, telegram ingress disabled")
	}

	application := app.New(&cfg, db, tg, pv, gallerySvc)

	if tg != nil {
		tg.Bot.RegisterHandlerMatchFunc(func(update *models.Update) bool {
			return update.Message != nil && application.CanHandleTGMessage(update.Message)
		}, func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
			result, err := application.HandleTGMessage(ctx, update.Message)
			if err != nil {
				log.Printf("tg handle error: %v", err)
				if update.Message != nil {
					_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
						ChatID: update.Message.Chat.ID,
						Text:   fmt.Sprintf("处理失败：%v", err),
					})
				}
				return
			}
			if result != nil && strings.TrimSpace(result.Summary) != "" && update.Message != nil {
				_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
					ChatID: update.Message.Chat.ID,
					Text:   result.Summary,
				})
			}
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application.StartPixivCrawler(ctx)
	application.StartTwitterAuthorCrawler(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	httpSrv := &http.Server{Addr: cfg.ListenAddr, Handler: mux}
	go func() {
		log.Printf("HTTP server listening on %s", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	if tg != nil {
		go tg.Start(ctx)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	if tg != nil {
		tg.Stop()
	}
	log.Println("shutdown complete")
}
