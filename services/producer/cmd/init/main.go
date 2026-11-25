package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"movieproducer/internal/config"
	"movieproducer/internal/registry"
)

func main() {
	brokers, schemaURL, err := config.LoadRegistry()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := createTopic(ctx, brokers); err != nil {
		log.Fatal(err)
	}
	saved, err := registry.Register(ctx, schemaURL)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("topic %s ready, schema %s version=%d id=%d", registry.Topic, registry.Subject, saved.Version, saved.ID)
}

func createTopic(ctx context.Context, brokers []string) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer client.Close()
	admin := kadm.NewClient(client)
	minISR := "2"
	deadline := time.Now().Add(90 * time.Second)
	var last error
	for {
		_, err := admin.CreateTopic(ctx, 3, 3, map[string]*string{
			"min.insync.replicas": &minISR,
		}, registry.Topic)
		if err == nil || errors.Is(err, kerr.TopicAlreadyExists) {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return fmt.Errorf("create topic: %w", last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("create topic: %w", last)
		case <-time.After(time.Second):
		}
	}
}
