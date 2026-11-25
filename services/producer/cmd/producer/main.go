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

	"github.com/twmb/franz-go/pkg/kgo"

	"movieproducer/api/avro"
	"movieproducer/internal/codec"
	"movieproducer/internal/config"
	"movieproducer/internal/generator"
	"movieproducer/internal/httpapi"
	"movieproducer/internal/kafka"
	"movieproducer/internal/registry"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	schemaID, err := registry.Lookup(lookupCtx, cfg.SchemaRegistryURL)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	cdc, err := codec.New(avroschema.Text, schemaID)
	if err != nil {
		log.Fatal(err)
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordRetries(0),
		kgo.ProduceRequestTimeout(10*time.Second),
		kgo.UnknownTopicRetries(0),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	handler, err := httpapi.New(kafka.New(client, cdc))
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
	if cfg.GeneratorEnabled {
		go generator.Run(ctx, kafka.New(client, cdc))
	}

	slog.Info("producer listening", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
