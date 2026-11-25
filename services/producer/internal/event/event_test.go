package event

import (
	"testing"
	"time"
)

func valid() Event {
	progress := int32(0)
	return Event{
		UserID:    "user-01",
		MovieID:   "movie-01",
		Type:      ViewStarted,
		Timestamp: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
		Device:    Mobile,
		SessionID: "sess-1",
		Progress:  &progress,
	}
}

func TestProgressRequiredOnlyForViews(t *testing.T) {
	views := []string{ViewStarted, ViewFinished, ViewPaused, ViewResumed}
	for _, typ := range views {
		ev := valid()
		ev.Type = typ
		ev.Progress = nil
		if err := ev.Validate(); err == nil {
			t.Fatalf("%s without progress accepted", typ)
		}
		progress := int32(10)
		ev.Progress = &progress
		if err := ev.Validate(); err != nil {
			t.Fatalf("%s with progress: %v", typ, err)
		}
	}
	for _, typ := range []string{Liked, Searched} {
		ev := valid()
		ev.Type = typ
		ev.Progress = nil
		if err := ev.Validate(); err != nil {
			t.Fatalf("%s without progress: %v", typ, err)
		}
		progress := int32(1)
		ev.Progress = &progress
		if err := ev.Validate(); err == nil {
			t.Fatalf("%s with progress accepted", typ)
		}
	}
}

func TestEmptyMovieID(t *testing.T) {
	ev := valid()
	ev.Type = Searched
	ev.Progress = nil
	ev.MovieID = ""
	if err := ev.Validate(); err == nil {
		t.Fatal("empty movie_id accepted")
	}
}

func TestNegativeProgress(t *testing.T) {
	ev := valid()
	progress := int32(-1)
	ev.Progress = &progress
	if err := ev.Validate(); err == nil {
		t.Fatal("negative progress accepted")
	}
}
