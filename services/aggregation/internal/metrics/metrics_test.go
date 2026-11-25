package metrics

import (
	"testing"
	"time"
)

var day = time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)

func find(points []Point, date time.Time, name string, cohortDay int16) *Point {
	for i := range points {
		p := points[i]
		if p.Name == name && p.CohortDay == cohortDay && p.Date.Equal(date) {
			return &points[i]
		}
	}
	return nil
}

func TestConversionNULLWithoutStarts(t *testing.T) {
	if Conversion(0, 4) != nil {
		t.Fatal("expected nil")
	}
	got := Conversion(4, 1)
	if got == nil || *got != 0.25 {
		t.Fatalf("%v", got)
	}
	points := Build(ScopeFor(day), Daily{Started: 0, Finished: 3}, nil, nil, day)
	conversion := find(points, day, ConversionName, -1)
	if conversion == nil || conversion.Value != nil {
		t.Fatalf("%+v", conversion)
	}
	if find(points, day, AvgWatch, -1).Value != nil {
		t.Fatal("average without finished views must be NULL")
	}
	if dau := find(points, day, DAU, -1); dau == nil || *dau.Value != 0 {
		t.Fatalf("DAU without events must be 0: %+v", dau)
	}
}

func TestTopTieBreaksToSmallerMovieID(t *testing.T) {
	rows := []MovieViews{
		{MovieID: "movie-10", Views: 9},
		{MovieID: "movie-02", Views: 5},
		{MovieID: "movie-01", Views: 5},
		{MovieID: "movie-11", Views: 1},
		{MovieID: "movie-04", Views: 1},
		{MovieID: "movie-05", Views: 1},
		{MovieID: "movie-06", Views: 1},
		{MovieID: "movie-07", Views: 1},
		{MovieID: "movie-08", Views: 1},
		{MovieID: "movie-09", Views: 1},
		{MovieID: "movie-12", Views: 1},
		{MovieID: "movie-03", Views: 1},
	}
	got := Top(rows, 10)
	if len(got) != 10 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].MovieID != "movie-10" || got[1].MovieID != "movie-01" || got[2].MovieID != "movie-02" {
		t.Fatalf("%+v", got[:3])
	}
	if got[len(got)-1].MovieID != "movie-09" {
		t.Fatalf("last %+v", got[len(got)-1])
	}
}

func TestRetentionOfFutureDaysIsNULL(t *testing.T) {
	cohorts := []Cohort{
		{Date: day, Size: 4, D1Users: 0, D7Users: 0},
		{Date: day.AddDate(0, 0, -1), Size: 4, D1Users: 2, D7Users: 0},
		{Date: day.AddDate(0, 0, -7), Size: 5, D1Users: 4, D7Users: 1},
	}
	now := day.Add(15 * time.Hour)
	points := Build(ScopeFor(day), Daily{}, nil, cohorts, now)

	for _, n := range []int16{1, 7} {
		if p := find(points, day, Retention, n); p == nil || p.Value != nil {
			t.Fatalf("today's cohort D%d must be NULL: %+v", n, p)
		}
	}
	yesterday := day.AddDate(0, 0, -1)
	if p := find(points, yesterday, Retention, 1); p == nil || p.Value == nil || *p.Value != 0.5 {
		t.Fatalf("yesterday D1: %+v", p)
	}
	if p := find(points, yesterday, Retention, 7); p == nil || p.Value != nil {
		t.Fatalf("yesterday D7 must be NULL: %+v", p)
	}
	week := day.AddDate(0, 0, -7)
	if p := find(points, week, Retention, 7); p == nil || p.Value == nil || *p.Value != 0.2 {
		t.Fatalf("week-old D7: %+v", p)
	}
}

func TestEmptyCohortHasNoRows(t *testing.T) {
	cohorts := []Cohort{{Date: day.AddDate(0, 0, -1), Size: 0}}
	points := Build(ScopeFor(day), Daily{}, nil, cohorts, day.AddDate(0, 0, 10))
	for _, p := range points {
		if p.Name == Retention {
			t.Fatalf("unexpected %+v", p)
		}
	}
}

func TestStaleKeysInsideScope(t *testing.T) {
	scope := ScopeFor(day)
	views := []MovieViews{{MovieID: "movie-01", Views: 3}}
	points := Build(scope, Daily{DAU: 1}, views, nil, day)
	existing := []Key{
		{Date: "2026-04-02", Name: TopMovieViews, MovieID: "movie-01", CohortDay: -1},
		{Date: "2026-04-02", Name: TopMovieViews, MovieID: "movie-09", CohortDay: -1},
		{Date: "2026-04-01", Name: Retention, CohortDay: 1},
		{Date: "2026-04-01", Name: TopMovieViews, MovieID: "movie-09", CohortDay: -1},
		{Date: "2026-03-30", Name: Retention, CohortDay: 1},
	}
	stale := Stale(scope, existing, points)
	want := map[Key]bool{existing[1]: true, existing[2]: true}
	if len(stale) != len(want) {
		t.Fatalf("%+v", stale)
	}
	for _, key := range stale {
		if !want[key] {
			t.Fatalf("unexpected stale %+v", key)
		}
	}
}

func TestRepeatCalculationKeepsKeys(t *testing.T) {
	scope := ScopeFor(day)
	first := Build(scope, Daily{DAU: 1, Started: 2, Finished: 1}, []MovieViews{{MovieID: "movie-01", Views: 2}}, nil, day)
	second := Build(scope, Daily{DAU: 5, Started: 2, Finished: 1}, []MovieViews{{MovieID: "movie-01", Views: 4}}, nil, day.Add(time.Minute))
	keys := map[Key]bool{}
	for _, p := range first {
		keys[p.Key()] = true
	}
	for _, p := range second {
		if !keys[p.Key()] {
			t.Fatalf("new key %+v", p.Key())
		}
	}
	if len(first) != len(second) {
		t.Fatalf("%d != %d", len(first), len(second))
	}
}
