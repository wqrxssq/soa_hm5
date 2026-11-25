package generator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"movieproducer/internal/event"
)

const (
	userCount = 20
	dayCount  = 8
)

type Publisher interface {
	Publish(ctx context.Context, ev event.Event) error
}

func Run(ctx context.Context, pub Publisher) {
	now := time.Now().UTC()
	today := dateOnly(now)
	origin := today.AddDate(0, 0, -7)
	events := History(today)
	if err := publishAll(ctx, pub, events); err != nil {
		slog.Error("generator stopped", "err", err)
		return
	}
	slog.Info("generator published history", "events", len(events), "from", origin.Format(time.DateOnly), "to", today.Format(time.DateOnly))

	last := today
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := dateOnly(time.Now().UTC())
			if !current.After(last) {
				continue
			}
			dayIndex := int(current.Sub(origin).Hours() / 24)
			if err := publishAll(ctx, pub, Day(current, dayIndex)); err != nil {
				slog.Error("generator stopped", "err", err)
				return
			}
			last = current
		}
	}
}

func History(today time.Time) []event.Event {
	today = dateOnly(today.UTC())
	var out []event.Event
	for dayIndex := 0; dayIndex < dayCount; dayIndex++ {
		day := today.AddDate(0, 0, dayIndex-7)
		out = append(out, Day(day, dayIndex)...)
	}
	return out
}

func Day(day time.Time, dayIndex int) []event.Event {
	day = dateOnly(day.UTC())
	var out []event.Event
	for user := 1; user <= userCount; user++ {
		if !active(user, dayIndex) {
			continue
		}
		out = append(out, session(day, dayIndex, user)...)
	}
	return out
}

// active repeats every 8 days so a process left running does not go silent.
// Users 1–8 start on day 0 and drop off one by one. Users 9–14 join on day 2,
// users 15–20 on day 5, and each of those groups shrinks the same way.
func active(user, dayIndex int) bool {
	day := dayIndex % dayCount
	switch {
	case user <= 8:
		return user > day
	case user <= 14:
		if day < 2 {
			return false
		}
		return user-8 > day-2
	default:
		if day < 5 {
			return false
		}
		return user-14 > day-5
	}
}

func session(day time.Time, dayIndex, user int) []event.Event {
	userID := fmt.Sprintf("user-%02d", user)
	movieID := fmt.Sprintf("movie-%02d", movieOf(user, dayIndex))
	searchID := fmt.Sprintf("movie-%02d", movieOf(user, dayIndex)%10+1)
	sessionID := fmt.Sprintf("sess-%s-%s", userID, day.Format(time.DateOnly))
	base := time.Date(day.Year(), day.Month(), day.Day(), 12, user, 0, 0, time.UTC)
	type step struct {
		typ      string
		progress *int32
		movie    string
	}
	var steps []step
	switch user % 4 {
	case 0:
		steps = []step{{event.ViewStarted, ptr(0), movieID}, {event.Searched, nil, searchID}}
	case 1:
		steps = []step{{event.ViewStarted, ptr(0), movieID}, {event.ViewPaused, ptr(25), movieID}, {event.Searched, nil, searchID}}
	case 2:
		steps = []step{
			{event.ViewStarted, ptr(0), movieID},
			{event.ViewPaused, ptr(30), movieID},
			{event.ViewResumed, ptr(60), movieID},
			{event.ViewFinished, ptr(90), movieID},
			{event.Liked, nil, movieID},
			{event.Searched, nil, searchID},
		}
	default:
		finish := int32(120 + user*5)
		steps = []step{
			{event.ViewStarted, ptr(0), movieID},
			{event.ViewPaused, ptr(40), movieID},
			{event.ViewResumed, ptr(80), movieID},
			{event.ViewFinished, ptr(finish), movieID},
			{event.Liked, nil, movieID},
			{event.Searched, nil, searchID},
		}
	}
	out := make([]event.Event, 0, len(steps))
	for i, item := range steps {
		out = append(out, event.Event{
			ID:        uuid.NewString(),
			UserID:    userID,
			MovieID:   item.movie,
			Type:      item.typ,
			Timestamp: base.Add(time.Duration(i) * 5 * time.Minute),
			Device:    deviceOf(user),
			SessionID: sessionID,
			Progress:  item.progress,
		})
	}
	return out
}

// movieOf keeps movie-01 and movie-02 in front. The remaining titles share the tail,
// otherwise every movie has the same start count and the bar chart is a flat line.
func movieOf(user, dayIndex int) int {
	switch user % 5 {
	case 0:
		return 1
	case 1:
		if dayIndex%2 == 0 {
			return 1
		}
		return 2
	case 2:
		return 2
	case 3:
		return 3 + (user+dayIndex)%3
	default:
		return 6 + (user+dayIndex)%5
	}
}

func deviceOf(user int) string {
	switch {
	case user <= 5:
		return event.Mobile
	case user <= 10:
		return event.Desktop
	case user <= 15:
		return event.TV
	default:
		return event.Tablet
	}
}

func publishAll(ctx context.Context, pub Publisher, events []event.Event) error {
	for _, ev := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ev.Validate(); err != nil {
			return err
		}
		if err := pub.Publish(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func ptr(v int32) *int32 {
	return &v
}
