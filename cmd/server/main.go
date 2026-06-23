package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Lapius7/clipshot-server/internal/auth"
	"github.com/Lapius7/clipshot-server/internal/cli"
	"github.com/Lapius7/clipshot-server/internal/config"
	"github.com/Lapius7/clipshot-server/internal/db"
	"github.com/Lapius7/clipshot-server/internal/handler"
	"github.com/Lapius7/clipshot-server/internal/storage"
	ratelimit "github.com/Lapius7/go-rataliy_lib"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	openDB := func() (*sql.DB, error) { return db.Open(cfg.DBPath) }

	if cli.Run(os.Args[1:], openDB) {
		return
	}

	dbConn, err := openDB()
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer dbConn.Close()

	if err := ensureBootstrapToken(dbConn); err != nil {
		log.Fatalf("bootstrap token error: %v", err)
	}

	store, err := storage.NewLocal(cfg.DataDir)
	if err != nil {
		log.Fatalf("storage error: %v", err)
	}

	srv := &handler.Server{
		DB:          dbConn,
		Storage:     store,
		BaseURL:     cfg.BaseURL,
		MaxUploadMB: cfg.MaxUploadMB,
		Limiter: ratelimit.New(ratelimit.TokenBucket, ratelimit.Config{
			Rate:  cfg.RateLimitRPM,
			Per:   time.Minute,
			Burst: cfg.RateLimitBurst,
		}),
	}

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("clipshot-server listening on %s (base url: %s)", addr, cfg.BaseURL)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// ensureBootstrapToken issues a first API token automatically if none exist
// yet, so self-hosters have a working credential immediately after `docker run`.
func ensureBootstrapToken(dbConn *sql.DB) error {
	n, err := auth.CountActiveTokens(dbConn)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	token, err := auth.CreateToken(dbConn, "bootstrap")
	if err != nil {
		return err
	}
	log.Println("===========================================================")
	log.Println("No active tokens found. Created a bootstrap token:")
	log.Println(token)
	log.Println("Save this now -- it will not be shown again. Use it as the")
	log.Println("API key in the clipshot-app client (Authorization: Bearer ...)")
	log.Println("===========================================================")
	return nil
}
