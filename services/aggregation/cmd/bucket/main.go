package main

import (
	"context"
	"log"
	"time"

	"movieaggregation/internal/config"
	"movieaggregation/internal/export"
)

func main() {
	cfg, err := config.LoadS3()
	if err != nil {
		log.Fatal(err)
	}
	client, err := export.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var last error
	for {
		last = client.EnsureBucket(ctx)
		if last == nil {
			log.Printf("bucket %s ready", cfg.Bucket)
			return
		}
		select {
		case <-ctx.Done():
			log.Fatal(last)
		case <-time.After(time.Second):
		}
	}
}
