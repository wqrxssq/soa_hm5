package metrics

import (
	"testing"
	"time"
)

func TestConversionNULLWithoutStarts(t *testing.T) {
	if Conversion(0, 4) != nil {
		t.Fatal("expected nil")
	}
	got := Conversion(4, 1)
	if got == nil || *got != 0.25 {
		t.Fatalf("%v", got)
	}
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	points := Build(day, Daily{Started: 0, Finished: 3, DAU: 0}, nil, nil, day)
	var conversion *Point
	for i := range points {
		if points[i].Name == ConversionName {
			conversion = &points[i]
		}
	}
	if conversion == nil || conversion.Value != nil {
		t.Fatalf("%+v", conversion)
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
	for _, movie := range got {
		if movie.MovieID == "movie-11" || movie.MovieID == "movie-12" {
			t.Fatalf("kept %s", movie.MovieID)
		}
	}
}

func TestRepeatCalculationReplacesSameKey(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	first := Build(day, Daily{DAU: 1, Started: 2, Finished: 1}, []MovieViews{{MovieID: "movie-01", Views: 2}}, nil, day)
	secondAt := day.Add(time.Minute)
	second := Build(day, Daily{DAU: 5, Started: 2, Finished: 1}, []MovieViews{{MovieID: "movie-01", Views: 4}}, nil, secondAt)
	store := map[Key]Point{}
	Apply(store, first)
	Apply(store, second)
	var dau []Point
	for _, point := range store {
		if point.Name == DAU && point.Date.Equal(day) {
			dau = append(dau, point)
		}
	}
	if len(dau) != 1 || dau[0].Value == nil || *dau[0].Value != 5 {
		t.Fatalf("%+v", dau)
	}
	if len(store) != len(second) {
		t.Fatalf("rows %d, want %d", len(store), len(second))
	}
}
