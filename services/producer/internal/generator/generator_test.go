package generator

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"movieproducer/internal/event"
)

var today = time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)

func window() Generator {
	return New(today.AddDate(0, 0, -historyDays))
}

func TestDayIsDeterministic(t *testing.T) {
	first := window().Day(today)
	second := window().Day(today)
	if len(first) == 0 {
		t.Fatal("no events")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two runs produced different events")
	}
	ids := map[string]bool{}
	for _, ev := range first {
		if ids[ev.ID] {
			t.Fatalf("duplicate id %s", ev.ID)
		}
		ids[ev.ID] = true
	}
}

func TestRestartNextDayKeepsOverlap(t *testing.T) {
	before := window().Day(today.AddDate(0, 0, -1))
	after := New(today.AddDate(0, 0, -historyDays+1)).Day(today.AddDate(0, 0, -1))
	ids := map[string]bool{}
	for _, ev := range before {
		ids[ev.ID] = true
	}
	for _, ev := range after {
		if !ids[ev.ID] {
			t.Fatalf("restart produced a new event %s for an already generated day", ev.ID)
		}
	}
}

func TestSessionsAreRealistic(t *testing.T) {
	gen := window()
	for day := gen.firstCohort; !day.After(today); day = day.AddDate(0, 0, 1) {
		sessions := map[string][]event.Event{}
		for _, ev := range gen.Day(day) {
			if err := ev.Validate(); err != nil {
				t.Fatal(err)
			}
			if !dateOnly(ev.Timestamp).Equal(day) {
				t.Fatalf("event %s outside %s", ev.Timestamp, day)
			}
			sessions[ev.SessionID] = append(sessions[ev.SessionID], ev)
		}
		for id, items := range sessions {
			checkSession(t, id, items)
		}
	}
}

func checkSession(t *testing.T, id string, items []event.Event) {
	t.Helper()
	started := false
	var progress int32 = -1
	for i, ev := range items {
		if i > 0 && !ev.Timestamp.After(items[i-1].Timestamp) {
			t.Fatalf("%s: timestamps do not grow", id)
		}
		if ev.MovieID != items[0].MovieID || ev.UserID != items[0].UserID || ev.Device != items[0].Device {
			t.Fatalf("%s: session changes movie, user or device", id)
		}
		switch ev.Type {
		case event.ViewStarted:
			if started || *ev.Progress != 0 {
				t.Fatalf("%s: bad start", id)
			}
			started = true
		case event.ViewPaused, event.ViewResumed, event.ViewFinished:
			if !started {
				t.Fatalf("%s: %s before VIEW_STARTED", id, ev.Type)
			}
		case event.Searched:
			if started {
				t.Fatalf("%s: search after start", id)
			}
		case event.Liked:
			if items[i-1].Type != event.ViewFinished {
				t.Fatalf("%s: like before finish", id)
			}
		}
		if ev.Progress != nil {
			if *ev.Progress <= progress {
				t.Fatalf("%s: progress does not grow: %d then %d", id, progress, *ev.Progress)
			}
			progress = *ev.Progress
		}
	}
	if !started {
		t.Fatalf("%s: no VIEW_STARTED", id)
	}
}

func TestUserSessionsDoNotOverlap(t *testing.T) {
	type span struct{ from, to time.Time }
	sessions := map[string]map[string]*span{}
	for _, ev := range window().Between(today.AddDate(0, 0, -historyDays), today.AddDate(0, 0, 1)) {
		if sessions[ev.UserID] == nil {
			sessions[ev.UserID] = map[string]*span{}
		}
		if s := sessions[ev.UserID][ev.SessionID]; s != nil {
			s.to = ev.Timestamp
		} else {
			sessions[ev.UserID][ev.SessionID] = &span{ev.Timestamp, ev.Timestamp}
		}
	}
	for user, byID := range sessions {
		spans := make([]*span, 0, len(byID))
		for _, s := range byID {
			spans = append(spans, s)
		}
		sort.Slice(spans, func(i, j int) bool { return spans[i].from.Before(spans[j].from) })
		for i := 1; i < len(spans); i++ {
			if !spans[i].from.After(spans[i-1].to) {
				t.Fatalf("%s: sessions overlap at %s", user, spans[i].from)
			}
		}
	}
}

func TestLateEveningIsNotQuiet(t *testing.T) {
	hours := make([]int, 24)
	for _, ev := range window().Day(today) {
		hours[ev.Timestamp.Hour()]++
	}
	total := 0
	for _, n := range hours {
		total += n
	}
	if hours[23]*24*2 < total {
		t.Fatalf("23:00 has %d of %d events", hours[23], total)
	}
}

func TestCohortsShrink(t *testing.T) {
	gen := window()
	cohort := gen.firstCohort.Format("20060102")
	var active []int
	for day := gen.firstCohort; !day.After(today); day = day.AddDate(0, 0, 1) {
		users := map[string]bool{}
		for _, ev := range gen.Day(day) {
			if strings.HasPrefix(ev.UserID, "user-"+cohort+"-") {
				users[ev.UserID] = true
			}
		}
		active = append(active, len(users))
	}
	if active[0] != cohortSize(gen.firstCohort) {
		t.Fatalf("day 0 has %d of %d users", active[0], cohortSize(gen.firstCohort))
	}
	if active[1] >= active[0] || active[len(active)-1] >= active[1] {
		t.Fatalf("retention does not decay: %v", active)
	}
	if active[len(active)-1] == 0 {
		t.Fatalf("nobody is left on day 7: %v", active)
	}
}

func TestOlderCohortsAreLeftOut(t *testing.T) {
	gen := window()
	old := gen.firstCohort.AddDate(0, 0, -1).Format("20060102")
	for _, ev := range gen.Day(gen.firstCohort.AddDate(0, 0, 2)) {
		if strings.Contains(ev.UserID, old) {
			t.Fatalf("user %s is older than the window", ev.UserID)
		}
	}
}

func TestTopIsNotFlat(t *testing.T) {
	starts := map[string]int{}
	finished, started := 0, 0
	for _, ev := range window().Between(today.AddDate(0, 0, -historyDays), today.AddDate(0, 0, 1)) {
		switch ev.Type {
		case event.ViewStarted:
			starts[ev.MovieID]++
			started++
		case event.ViewFinished:
			finished++
		}
	}
	if starts["movie-01"] <= starts["movie-10"] {
		t.Fatalf("top is flat: movie-01=%d movie-10=%d", starts["movie-01"], starts["movie-10"])
	}
	if finished == 0 || finished >= started {
		t.Fatalf("conversion %d/%d", finished, started)
	}
}

func TestBetweenIsHalfOpen(t *testing.T) {
	gen := window()
	all := gen.Day(today)
	cut := all[len(all)/2].Timestamp
	left := gen.Between(today, cut)
	right := gen.Between(cut, today.AddDate(0, 0, 1))
	if len(left)+len(right) != len(all) {
		t.Fatalf("%d + %d != %d", len(left), len(right), len(all))
	}
	for _, ev := range left {
		if !ev.Timestamp.Before(cut) {
			t.Fatalf("left has %s", ev.Timestamp)
		}
	}
}

type flaky struct {
	mu     sync.Mutex
	failOn string
	got    map[string][]string
}

func (f *flaky) Publish(_ context.Context, ev event.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ev.ID == f.failOn {
		f.failOn = ""
		return errors.New("broker down")
	}
	f.got[ev.UserID] = append(f.got[ev.UserID], ev.ID)
	return nil
}

func TestPublishAllKeepsUserOrderAndRetries(t *testing.T) {
	events := window().Day(today)
	pub := &flaky{failOn: events[len(events)/2].ID, got: map[string][]string{}}
	if err := publishAll(context.Background(), pub, events); err == nil {
		t.Fatal("error swallowed")
	}
	// The retry resends the whole batch. Ids that already went out repeat and ClickHouse collapses them.
	pub.got = map[string][]string{}
	if err := publishAll(context.Background(), pub, events); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{}
	for _, ev := range events {
		want[ev.UserID] = append(want[ev.UserID], ev.ID)
	}
	if !reflect.DeepEqual(pub.got, want) {
		t.Fatal("events of a user were reordered or lost")
	}
}
