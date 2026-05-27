package main

import (
	"context"
	"fmt"
	"io"
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

	if !cfg.IsTelegramPollingMode() && !cfg.IsTelegramWebhookMode() {
		log.Fatalf("unsupported BOT_MODE %q (use polling or webhook)", cfg.BotMode)
	}
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
	if applied, err := db.EnsureGallerySeqBaselineIfEmpty(bootstrapCtx, cfg.GalleryBaselineH, cfg.GalleryBaselineV); err != nil {
		log.Fatalf("init gallery seq baseline error: %v", err)
	} else if applied {
		log.Printf("gallery seq baseline initialized: h=%d, v=%d", cfg.GalleryBaselineH, cfg.GalleryBaselineV)
	} else if cfg.GalleryBaselineH > 0 || cfg.GalleryBaselineV > 0 {
		log.Printf("gallery seq baseline skipped (already initialized or gallery_images not empty): h=%d, v=%d", cfg.GalleryBaselineH, cfg.GalleryBaselineV)
	}

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
		var botOpts []tgbot.Option
		if cfg.IsTelegramWebhookMode() {
			if cfg.TGWebhookSecret == "" {
				log.Fatal("TELEGRAM_WEBHOOK_SECRET is required when BOT_MODE=webhook")
			}
			botOpts = append(botOpts, tgbot.WithWebhookSecretToken(cfg.TGWebhookSecret))
		}
		tg, err = telegram.New(cfg.BotToken, botOpts...)
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("tyr-blog-img is running\nhealth: /healthz\n"))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	if tg != nil && cfg.IsTelegramWebhookMode() {
		webhookHandler := tg.Bot.WebhookHandler()
		mux.HandleFunc("/telegram/webhook", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			webhookHandler(w, r)
		})
	}

	httpSrv := &http.Server{Addr: cfg.ListenAddr, Handler: mux}
	go func() {
		log.Printf("HTTP server listening on %s", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	if tg != nil {
		if cfg.IsTelegramWebhookMode() {
			go tg.StartWebhook(ctx)
			if cfg.TGWebhookURL != "" {
				webhookCtx, cancelWebhook := context.WithTimeout(context.Background(), 15*time.Second)
				if _, err := tg.Bot.SetWebhook(webhookCtx, &tgbot.SetWebhookParams{
					URL:            cfg.TGWebhookURL,
					SecretToken:    cfg.TGWebhookSecret,
					AllowedUpdates: []string{models.AllowedUpdateMessage},
				}); err != nil {
					cancelWebhook()
					log.Fatalf("set telegram webhook error: %v", err)
				}
				cancelWebhook()
				log.Printf("telegram webhook configured: %s", cfg.TGWebhookURL)
			} else {
				log.Println("telegram webhook mode enabled; TELEGRAM_WEBHOOK_URL not set, configure setWebhook manually")
			}
		} else {
			if cfg.DeleteWebhookOnPolling {
				webhookCtx, cancelWebhook := context.WithTimeout(context.Background(), 15*time.Second)
				err := deleteTelegramWebhookBeforePolling(webhookCtx, tg, cfg.BotToken)
				cancelWebhook()
				if err != nil {
					log.Printf("warning: delete telegram webhook before polling failed: %v; polling may still hit getUpdates conflict", err)
				} else {
					log.Println("telegram webhook deleted before polling")
				}
			}
			go tg.Start(ctx)
		}
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

func deleteTelegramWebhookBeforePolling(ctx context.Context, tg *telegram.Client, token string) error {
	if _, err := tg.Bot.DeleteWebhook(ctx, &tgbot.DeleteWebhookParams{
		DropPendingUpdates: false,
	}); err == nil {
		return nil
	} else {
		log.Printf("telegram deleteWebhook via bot client failed, retrying direct HTTP: %v", err)
		if fallbackErr := deleteTelegramWebhookDirect(ctx, token); fallbackErr != nil {
			return fmt.Errorf("bot client: %v; direct HTTP: %w", err, fallbackErr)
		}
	}
	return nil
}

func deleteTelegramWebhookDirect(ctx context.Context, token string) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/deleteWebhook?drop_pending_updates=false", strings.TrimSpace(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("telegram status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
