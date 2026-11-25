package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/sr"

	"movieproducer/api/avro"
)

const (
	Subject = "movie-events-value"
	Topic   = "movie-events"
)

func Client(url string) (*sr.Client, error) {
	return sr.NewClient(sr.URLs(url))
}

func Register(ctx context.Context, url string) (sr.SubjectSchema, error) {
	client, err := Client(url)
	if err != nil {
		return sr.SubjectSchema{}, err
	}
	schema := sr.Schema{Schema: avroschema.Text, Type: sr.TypeAvro}
	var saved sr.SubjectSchema
	err = retry(ctx, func() error {
		saved, err = client.CreateSchema(ctx, Subject, schema)
		if err != nil {
			return err
		}
		results := client.SetCompatibility(ctx, sr.SetCompatibility{Level: sr.CompatBackward}, Subject)
		for _, result := range results {
			if result.Err != nil {
				return result.Err
			}
		}
		return nil
	})
	if err != nil {
		return sr.SubjectSchema{}, err
	}
	return saved, nil
}

func Lookup(ctx context.Context, url string) (int, error) {
	client, err := Client(url)
	if err != nil {
		return 0, err
	}
	schema := sr.Schema{Schema: avroschema.Text, Type: sr.TypeAvro}
	var found sr.SubjectSchema
	err = retry(ctx, func() error {
		found, err = client.LookupSchema(ctx, Subject, schema)
		return err
	})
	if err != nil {
		return 0, err
	}
	if found.ID <= 0 {
		return 0, fmt.Errorf("schema %s has no id", Subject)
	}
	return found.ID, nil
}

func retry(ctx context.Context, op func() error) error {
	deadline := time.Now().Add(90 * time.Second)
	var err error
	for {
		err = op()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w", err)
		case <-time.After(time.Second):
		}
	}
}
