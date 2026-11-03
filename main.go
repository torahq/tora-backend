// Tora data service: polls venues, normalizes them and serves the app's server functions. Internal only.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("data service stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	pool, err := pgxpool.New(ctx, env("DATABASE_URL", "postgres://tora:tora@localhost:5433/tora?sslmode=disable"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	opt, err := redis.ParseURL(env("REDIS_URL", "redis://localhost:6380/0"))
	if err != nil {
		return err
	}
	rdb := redis.NewClient(opt)
	defer rdb.Close()

	store := db{pool, rdb}
	hl := HL{URL: env("HL_INFO_URL", "https://api.hyperliquid.xyz/info"), Client: &http.Client{Timeout: 10 * time.Second}}
	go every(ctx, 15*time.Second, "pulse", pollPulse(hl, store))
	go every(ctx, 30*time.Second, "outcomes", pollOutcomes(hl, store))

	srv := &http.Server{Addr: env("ADDR", "127.0.0.1:8790"), Handler: routes(store, time.Now), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	slog.Info("data service listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
