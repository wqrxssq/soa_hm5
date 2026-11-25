package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"movieaggregation/internal/config"
	"movieaggregation/internal/cycle"
	"movieaggregation/internal/export"
	"movieaggregation/internal/httpapi"
	"movieaggregation/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ch, err := store.OpenClickHouse(ctx, cfg.ClickHouseDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer ch.Close()
	pg, err := store.OpenPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pg.Close()
	s3, err := export.New(cfg.S3)
	if err != nil {
		log.Fatal(err)
	}

	svc := cycle.New(ch, pg, s3, cfg.AggInterval, cfg.ExportInterval)
	handler, err := httpapi.New(svc)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go svc.Start(ctx)

	slog.Info("aggregation listening", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
