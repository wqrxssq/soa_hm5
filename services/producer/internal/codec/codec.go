package codec

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/hamba/avro/v2"

	"movieproducer/internal/event"
)

type Codec struct {
	schema avro.Schema
	id     uint32
}

type wire struct {
	EventID         string    `avro:"event_id"`
	UserID          string    `avro:"user_id"`
	MovieID         string    `avro:"movie_id"`
	EventType       string    `avro:"event_type"`
	Timestamp       time.Time `avro:"timestamp"`
	DeviceType      string    `avro:"device_type"`
	SessionID       string    `avro:"session_id"`
	ProgressSeconds *int32    `avro:"progress_seconds"`
}

func New(schemaText string, id int) (*Codec, error) {
	schema, err := avro.Parse(schemaText)
	if err != nil {
		return nil, fmt.Errorf("avro schema: %w", err)
	}
	if id <= 0 {
		return nil, fmt.Errorf("schema id must be positive")
	}
	return &Codec{schema: schema, id: uint32(id)}, nil
}

func (c *Codec) Encode(ev event.Event) ([]byte, error) {
	body, err := avro.Marshal(c.schema, wire{
		EventID:         ev.ID,
		UserID:          ev.UserID,
		MovieID:         ev.MovieID,
		EventType:       ev.Type,
		Timestamp:       ev.Timestamp.UTC(),
		DeviceType:      ev.Device,
		SessionID:       ev.SessionID,
		ProgressSeconds: ev.Progress,
	})
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 5+len(body))
	frame[0] = 0
	binary.BigEndian.PutUint32(frame[1:5], c.id)
	copy(frame[5:], body)
	return frame, nil
}

func (c *Codec) decode(frame []byte) (wire, error) {
	var out wire
	if len(frame) < 5 || frame[0] != 0 {
		return out, fmt.Errorf("confluent frame")
	}
	err := avro.Unmarshal(c.schema, frame[5:], &out)
	return out, err
}
