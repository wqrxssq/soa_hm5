package codec

import (
	"testing"
	"time"

	"movieproducer/api/avro"
	"movieproducer/internal/event"
)

func TestRoundTripLogicalTypes(t *testing.T) {
	cdc, err := New(avroschema.Text, 7)
	if err != nil {
		t.Fatal(err)
	}
	progress := int32(40)
	stamp := time.Date(2026, 4, 1, 15, 4, 5, 123000000, time.UTC)
	ev := event.Event{
		ID:        "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		UserID:    "user-01",
		MovieID:   "movie-02",
		Type:      event.ViewPaused,
		Timestamp: stamp,
		Device:    event.Desktop,
		SessionID: "sess-1",
		Progress:  &progress,
	}
	frame, err := cdc.Encode(ev)
	if err != nil {
		t.Fatal(err)
	}
	if frame[0] != 0 || int(frame[1])<<24|int(frame[2])<<16|int(frame[3])<<8|int(frame[4]) != 7 {
		t.Fatalf("header %v", frame[:5])
	}
	got, err := cdc.decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if got.EventID != ev.ID || got.UserID != ev.UserID || got.EventType != ev.Type {
		t.Fatalf("%+v", got)
	}
	if !got.Timestamp.Equal(stamp.Truncate(time.Millisecond)) {
		t.Fatalf("timestamp %s", got.Timestamp)
	}
	if got.ProgressSeconds == nil || *got.ProgressSeconds != 40 {
		t.Fatalf("progress %+v", got.ProgressSeconds)
	}

	ev.Type = event.Liked
	ev.Progress = nil
	frame, err = cdc.Encode(ev)
	if err != nil {
		t.Fatal(err)
	}
	got, err = cdc.decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProgressSeconds != nil {
		t.Fatalf("liked progress %+v", got.ProgressSeconds)
	}
}
