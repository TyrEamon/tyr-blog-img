package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"tyr-blog-img/internal/config"
	"tyr-blog-img/internal/database"
)

func main() {
	cfg := config.Load()

	if cfg.HasD1() {
		db := database.New(cfg.D1AccountID, cfg.D1APIToken, cfg.D1DatabaseID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.EnsureSchema(ctx); err != nil {
			log.Fatalf("ensure schema error: %v", err)
		}
		log.Println("D1 schema ready")
	} else {
		log.Println("D1 credentials missing; starting without DB bootstrap")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("HTTP server listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatal(err)
	}
}
