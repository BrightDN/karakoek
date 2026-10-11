package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"karakoek/internal/config"
	"karakoek/internal/db"
	"karakoek/internal/handlers"
	"karakoek/internal/hub"
	"karakoek/internal/queue"
	"karakoek/internal/session"
)

func main() {
	genCodes := flag.Int("gen-codes", 0, "print N random table codes and exit")
	hashPw := flag.String("hash-password", "", "print a bcrypt hash for the given password and exit")
	flag.Parse()

	if *genCodes > 0 {
		codes, err := config.GenerateCodes(*genCodes)
		if err != nil {
			log.Fatal(err)
		}
		for i, c := range codes {
			fmt.Printf("%s:%d\n", c, i+1)
		}
		return
	}
	if *hashPw != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(*hashPw), bcrypt.DefaultCost)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(h))
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	q := queue.New(pool)

	// Crash recovery: the queue lives in PostgreSQL. A song that was playing is
	// restored as cued and paused; nothing ever starts by itself.
	if err := q.ResetPlaying(ctx); err != nil {
		log.Fatalf("recovery: %v", err)
	}
	if n, err := q.Unfinished(ctx); err == nil {
		log.Printf("restored %d unfinished request(s) from the database", n)
	}

	h := hub.New(q, cfg.BaseURL)
	app, err := handlers.New(cfg, pool, q, h, session.New(cfg.HashKey, cfg.BlockKey, cfg.CookieSecure))
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("KaraKoek listening on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
