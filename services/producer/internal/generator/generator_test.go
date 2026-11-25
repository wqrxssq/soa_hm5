package generator

import (
	"testing"
	"time"

	"movieproducer/internal/event"
)

func TestHistoryShape(t *testing.T) {
	today := time.Date(2026, 11, 25, 18, 0, 0, 0, time.UTC)
	events := History(today)
	if len(events) != 303 {
		t.Fatalf("len %d", len(events))
	}
	byUserDay := map[string][]event.Event{}
	starts := map[string]int{}
	usersOn := map[string]map[string]bool{}
	for _, ev := range events {
		if err := ev.Validate(); err != nil {
			t.Fatal(err)
		}
		day := ev.Timestamp.Format(time.DateOnly)
		key := ev.UserID + "/" + day
		byUserDay[key] = append(byUserDay[key], ev)
		if usersOn[day] == nil {
			usersOn[day] = map[string]bool{}
		}
		usersOn[day][ev.UserID] = true
		if ev.Type == event.ViewStarted {
			starts[ev.MovieID]++
		}
	}

	oldest := "2026-11-18"
	if _, ok := byUserDay["user-15/"+oldest]; ok {
		t.Fatal("user-15 starts later and must be absent on the oldest day")
	}
	session := byUserDay["user-01/"+oldest]
	if len(session) != 3 {
		t.Fatalf("session %d", len(session))
	}
	wantTypes := []string{event.ViewStarted, event.ViewPaused, event.Searched}
	var previous *int32
	for i, ev := range session {
		if ev.Type != wantTypes[i] {
			t.Fatalf("type %d = %s", i, ev.Type)
		}
		if ev.Device != event.Mobile {
			t.Fatalf("device %s", ev.Device)
		}
		if i < 2 {
			if ev.Progress == nil {
				t.Fatal("missing progress")
			}
			if previous != nil && *ev.Progress <= *previous {
				t.Fatalf("progress does not grow: %d then %d", *previous, *ev.Progress)
			}
			previous = ev.Progress
			if ev.MovieID != "movie-01" {
				t.Fatalf("movie %s", ev.MovieID)
			}
		}
		if i > 0 && !ev.Timestamp.After(session[0].Timestamp) {
			t.Fatal("timestamps are not increasing")
		}
	}
	if session[2].Progress != nil {
		t.Fatal("search must omit progress")
	}
	if session[2].MovieID != "movie-02" {
		t.Fatalf("searched %s", session[2].MovieID)
	}
	if session[0].Timestamp != time.Date(2026, 11, 18, 12, 1, 0, 0, time.UTC) {
		t.Fatalf("start %s", session[0].Timestamp)
	}

	if usersOn[oldest]["user-06"] == false || len(usersOn[oldest]) != 8 {
		t.Fatalf("oldest day users %d", len(usersOn[oldest]))
	}
	if len(usersOn["2026-11-20"]) != 12 {
		t.Fatalf("day 2 users %d", len(usersOn["2026-11-20"]))
	}
	if len(usersOn["2026-11-25"]) != 6 {
		t.Fatalf("latest day users %d", len(usersOn["2026-11-25"]))
	}
	if starts["movie-01"] <= starts["movie-10"] {
		t.Fatalf("top is flat: movie-01=%d movie-10=%d", starts["movie-01"], starts["movie-10"])
	}

	full := byUserDay["user-03/"+oldest]
	if full[len(full)-2].Type != event.Liked || full[len(full)-2].Progress != nil {
		t.Fatalf("like %#v", full[len(full)-2])
	}
	desktop := byUserDay["user-06/"+oldest][0]
	if desktop.Device != event.Desktop {
		t.Fatalf("device %s", desktop.Device)
	}
}
