package metrics

import (
	"sort"
	"time"
)

const (
	DAU            = "dau"
	AvgWatch       = "avg_watch_seconds"
	ConversionName = "conversion"
	TopMovieViews  = "top_movie_views"
	Retention      = "retention"
)

type Point struct {
	Date       time.Time
	Name       string
	MovieID    string
	CohortDay  int16
	Value      *float64
	ComputedAt time.Time
}

type Key struct {
	Date      string
	Name      string
	MovieID   string
	CohortDay int16
}

func (p Point) Key() Key {
	return Key{
		Date:      p.Date.UTC().Format(time.DateOnly),
		Name:      p.Name,
		MovieID:   p.MovieID,
		CohortDay: p.CohortDay,
	}
}

type MovieViews struct {
	MovieID string
	Views   uint64
}

type Daily struct {
	RawRows  uint64
	DAU      uint64
	Started  uint64
	Finished uint64
	AvgWatch *float64
}

type Cohort struct {
	Date    time.Time
	Size    uint64
	D1Users uint64
	D7Users uint64
}

func Conversion(started, finished uint64) *float64 {
	if started == 0 {
		return nil
	}
	value := float64(finished) / float64(started)
	return &value
}

func Top(rows []MovieViews, limit int) []MovieViews {
	copied := append([]MovieViews(nil), rows...)
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].Views != copied[j].Views {
			return copied[i].Views > copied[j].Views
		}
		return copied[i].MovieID < copied[j].MovieID
	})
	if limit >= 0 && len(copied) > limit {
		return copied[:limit]
	}
	return copied
}

func Apply(dst map[Key]Point, points []Point) {
	for _, point := range points {
		dst[point.Key()] = point
	}
}

func Build(day time.Time, daily Daily, movies []MovieViews, cohorts []Cohort, now time.Time) []Point {
	day = dateOnly(day)
	now = now.UTC()
	dau := float64(daily.DAU)
	points := []Point{
		{Date: day, Name: DAU, CohortDay: -1, Value: &dau, ComputedAt: now},
		{Date: day, Name: AvgWatch, CohortDay: -1, Value: daily.AvgWatch, ComputedAt: now},
		{Date: day, Name: ConversionName, CohortDay: -1, Value: Conversion(daily.Started, daily.Finished), ComputedAt: now},
	}
	for _, movie := range Top(movies, 10) {
		views := float64(movie.Views)
		points = append(points, Point{
			Date:       day,
			Name:       TopMovieViews,
			MovieID:    movie.MovieID,
			CohortDay:  -1,
			Value:      &views,
			ComputedAt: now,
		})
	}
	found := make(map[string]Cohort, len(cohorts))
	for _, cohort := range cohorts {
		found[dateOnly(cohort.Date).Format(time.DateOnly)] = cohort
	}
	for _, cohortDay := range []time.Time{day, day.AddDate(0, 0, -1), day.AddDate(0, 0, -7)} {
		var d1, d7 *float64
		if cohort, ok := found[cohortDay.Format(time.DateOnly)]; ok && cohort.Size > 0 {
			first := float64(cohort.D1Users) / float64(cohort.Size)
			seventh := float64(cohort.D7Users) / float64(cohort.Size)
			d1 = &first
			d7 = &seventh
		}
		points = append(points,
			Point{Date: cohortDay, Name: Retention, CohortDay: 1, Value: d1, ComputedAt: now},
			Point{Date: cohortDay, Name: Retention, CohortDay: 7, Value: d7, ComputedAt: now},
		)
	}
	return points
}

func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
