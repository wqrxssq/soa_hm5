package kafka

import (
	"context"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"

	"movieproducer/internal/codec"
	"movieproducer/internal/event"
	"movieproducer/internal/retry"
)

const Topic = "movie-events"

type Publisher struct {
	client *kgo.Client
	codec  *codec.Codec
}

func New(client *kgo.Client, cdc *codec.Codec) *Publisher {
	return &Publisher{client: client, codec: cdc}
}

func NewRecord(userID string, value []byte) *kgo.Record {
	return &kgo.Record{
		Topic: Topic,
		Key:   []byte(userID),
		Value: value,
	}
}

func (p *Publisher) Publish(ctx context.Context, ev event.Event) error {
	payload, err := p.codec.Encode(ev)
	if err != nil {
		return err
	}
	err = retry.Do(ctx, func() error {
		record := NewRecord(ev.UserID, payload)
		return p.client.ProduceSync(ctx, record).FirstErr()
	})
	if err != nil {
		return err
	}
	slog.Info("event published",
		"event_id", ev.ID,
		"event_type", ev.Type,
		"timestamp", ev.Timestamp.UTC().Format(timeRFC3339),
	)
	return nil
}

const timeRFC3339 = "2006-01-02T15:04:05.000Z"
