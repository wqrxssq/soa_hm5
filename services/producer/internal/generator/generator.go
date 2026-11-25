package generator

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"movieproducer/internal/event"
)

const (
	historyDays    = 7
	cohortMin      = 300
	cohortSpread   = 101
	movieCount     = 10
	tick           = 5 * time.Second
	publishWorkers = 32
	// longestSession bounds session() in minutes: browse 3, play 25, pause 10 and play 40, then like 1.
	longestSession = 80
)

// retention is the per-mille chance that a user is active on day N of their life.
// Users stop coming back after the last entry.
var retention = []uint64{1000, 550, 420, 350, 300, 260, 230, 200}

// Event ids are derived from the session and the step, so a restart republishes the same ids
// and ClickHouse collapses the copies.
var idSpace = uuid.MustParse("4f6b3c2e-8a51-4d0f-9c7e-2b1d6e5a9f30")

var devices = []struct {
	weight uint64
	name   string
}{
	{8, event.Mobile},
	{5, event.Desktop},
	{5, event.TV},
	{2, event.Tablet},
}

type Publisher interface {
	Publish(ctx context.Context, ev event.Event) error
}

// Generator is a pure function of calendar dates: the same day always yields the same events.
// Cohorts older than firstCohort are left out so the window starts with fresh users.
type Generator struct {
	firstCohort time.Time
}

func New(firstCohort time.Time) Generator {
	return Generator{firstCohort: dateOnly(firstCohort)}
}

func Run(ctx context.Context, pub Publisher) {
	gen := New(dateOnly(time.Now()).AddDate(0, 0, -historyDays))
	cursor := gen.firstCohort
	history := true
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		now := time.Now().UTC()
		events := gen.Between(cursor, now)
		if err := publishAll(ctx, pub, events); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("generator publish failed, retrying on next tick", "events", len(events), "err", err)
		} else {
			if history {
				slog.Info("generator published history", "events", len(events), "from", cursor.Format(time.DateOnly), "to", now.Format(time.RFC3339))
				history = false
			}
			cursor = now
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Between returns events with from <= timestamp < to in timestamp order.
func (g Generator) Between(from, to time.Time) []event.Event {
	var out []event.Event
	for day := dateOnly(from); day.Before(to); day = day.AddDate(0, 0, 1) {
		for _, ev := range g.Day(day) {
			if !ev.Timestamp.Before(from) && ev.Timestamp.Before(to) {
				out = append(out, ev)
			}
		}
	}
	return out
}

func (g Generator) Day(day time.Time) []event.Event {
	day = dateOnly(day)
	var out []event.Event
	for cohort := day.AddDate(0, 0, -(len(retention) - 1)); !cohort.After(day); cohort = cohort.AddDate(0, 0, 1) {
		if cohort.Before(g.firstCohort) {
			continue
		}
		life := int(day.Sub(cohort).Hours() / 24)
		for n := 1; n <= cohortSize(cohort); n++ {
			userID := fmt.Sprintf("user-%s-%03d", cohort.Format("20060102"), n)
			if !active(userID, day, life) {
				continue
			}
			out = append(out, userDay(userID, day)...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].Timestamp.Before(out[j].Timestamp)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func cohortSize(cohort time.Time) int {
	return cohortMin + int(hash("cohort", cohort.Format(time.DateOnly))%cohortSpread)
}

func active(userID string, day time.Time, life int) bool {
	if life < 0 || life >= len(retention) {
		return false
	}
	return hash("active", userID, day.Format(time.DateOnly))%1000 < retention[life]
}

// userDay spreads one to three sessions over the day, one per slot. Earlier sessions end inside
// their slot, so two sessions of one user never overlap. The last one may start until midnight
// and is cut there, as if the user closed the player, so the late evening is not quiet.
func userDay(userID string, day time.Time) []event.Event {
	date := day.Format(time.DateOnly)
	sessions := 1 + int(hash("sessions", userID, date)%3)
	slot := 24 * 60 / sessions
	midnight := day.AddDate(0, 0, 1)
	var out []event.Event
	for s := 0; s < sessions; s++ {
		span := slot - longestSession
		if s == sessions-1 {
			span = slot
		}
		offset := slot*s + int(hash("start", userID, date, strconv.Itoa(s))%uint64(span))
		start := day.Add(time.Duration(offset)*time.Minute + time.Duration(hash("second", userID, date, strconv.Itoa(s))%60)*time.Second)
		var kept []event.Event
		for _, ev := range session(userID, fmt.Sprintf("sess-%s-%s-%d", userID, date, s), start) {
			if ev.Timestamp.Before(midnight) {
				kept = append(kept, ev)
			}
		}
		// A session that did not reach VIEW_STARTED before midnight is dropped as a whole.
		if len(kept) > 0 && kept[len(kept)-1].Type != event.Searched {
			out = append(out, kept...)
		}
	}
	return out
}

// session plays one movie. Playback moves progress_seconds at wall-clock speed. VIEW_RESUMED is
// the first player report ten seconds after the pause ends, so progress grows at every view event.
func session(userID, sessionID string, start time.Time) []event.Event {
	movieID := pickMovie(sessionID)
	device := pickDevice(userID)
	kind := hash("kind", sessionID) % 10
	at := start
	var progress int32
	var out []event.Event
	add := func(typ string, withProgress bool) {
		var p *int32
		if withProgress {
			value := progress
			p = &value
		}
		out = append(out, event.Event{
			ID:        uuid.NewSHA1(idSpace, []byte(sessionID+"/"+strconv.Itoa(len(out)))).String(),
			UserID:    userID,
			MovieID:   movieID,
			Type:      typ,
			Timestamp: at,
			Device:    device,
			SessionID: sessionID,
			Progress:  p,
		})
	}
	play := func(minMinutes, spread uint64, salt string) {
		seconds := int32((minMinutes + hash(salt, sessionID)%spread) * 60)
		at = at.Add(time.Duration(seconds) * time.Second)
		progress += seconds
	}

	if kind < 2 || hash("search", sessionID)%2 == 0 {
		add(event.Searched, false)
		at = at.Add(time.Duration(1+hash("browse", sessionID)%3) * time.Minute)
	}
	add(event.ViewStarted, true)
	play(5, 21, "play1")
	add(event.ViewPaused, true)
	if kind < 4 {
		return out
	}
	at = at.Add(time.Duration(1+hash("pause", sessionID)%10)*time.Minute + 10*time.Second)
	progress += 10
	add(event.ViewResumed, true)
	play(5, 36, "play2")
	add(event.ViewFinished, true)
	if hash("like", sessionID)%3 == 0 {
		at = at.Add(time.Minute)
		add(event.Liked, false)
	}
	return out
}

// pickMovie gives movie-01 weight 10, movie-02 weight 9 and so on, so the top chart has a slope.
func pickMovie(sessionID string) string {
	roll := hash("movie", sessionID) % uint64(movieCount*(movieCount+1)/2)
	for n := 1; n <= movieCount; n++ {
		weight := uint64(movieCount - n + 1)
		if roll < weight {
			return fmt.Sprintf("movie-%02d", n)
		}
		roll -= weight
	}
	return fmt.Sprintf("movie-%02d", movieCount)
}

func pickDevice(userID string) string {
	var total uint64
	for _, d := range devices {
		total += d.weight
	}
	roll := hash("device", userID) % total
	for _, d := range devices {
		if roll < d.weight {
			return d.name
		}
		roll -= d.weight
	}
	return devices[len(devices)-1].name
}

// publishAll sends all events of one user from one worker, so they keep their order inside the
// user's Kafka partition. The first error stops every worker.
func publishAll(ctx context.Context, pub Publisher, events []event.Event) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	queues := make([][]event.Event, publishWorkers)
	for _, ev := range events {
		n := hash("worker", ev.UserID) % publishWorkers
		queues[n] = append(queues[n], ev)
	}
	var wg sync.WaitGroup
	for _, queue := range queues {
		wg.Go(func() {
			for _, ev := range queue {
				if ctx.Err() != nil {
					return
				}
				err := ev.Validate()
				if err == nil {
					err = pub.Publish(ctx, ev)
				}
				if err != nil {
					cancel(err)
					return
				}
			}
		})
	}
	wg.Wait()
	return context.Cause(ctx)
}

func hash(parts ...string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(parts, "|")))
	return h.Sum64()
}

func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
