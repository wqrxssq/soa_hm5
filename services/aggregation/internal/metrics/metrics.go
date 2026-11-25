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

// DayMetrics are keyed by the calculated day. Retention is keyed by the cohort date instead.
var DayMetrics = []string{DAU, AvgWatch, ConversionName, TopMovieViews}

var RetentionDays = []int16{1, 7}

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

// Scope is the set of keys one recalculation owns. Keys inside it that the new
// calculation did not produce are stale and get removed.
type Scope struct {
	Day     time.Time
	Cohorts []time.Time
}

// ScopeFor covers the day itself and the cohorts whose D1 or D7 falls on that day.
func ScopeFor(day time.Time) Scope {
	day = DateOnly(day)
	cohorts := []time.Time{day}
	for _, n := range RetentionDays {
		cohorts = append(cohorts, day.AddDate(0, 0, -int(n)))
	}
	return Scope{Day: day, Cohorts: cohorts}
}

func (s Scope) Covers(k Key) bool {
	if k.Name == Retention {
		for _, cohort := range s.Cohorts {
			if cohort.Format(time.DateOnly) == k.Date {
				return true
			}
		}
		return false
	}
	return k.Date == s.Day.Format(time.DateOnly)
}

// Stale returns keys that exist in storage inside the scope but are absent from points.
func Stale(scope Scope, existing []Key, points []Point) []Key {
	fresh := make(map[Key]bool, len(points))
	for _, point := range points {
		fresh[point.Key()] = true
	}
	var out []Key
	for _, key := range existing {
		if scope.Covers(key) && !fresh[key] {
			out = append(out, key)
		}
	}
	return out
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

// Build turns query results into points. A cohort without users gets no retention rows.
// A day of life that has not started yet relative to now stays NULL instead of 0.
func Build(scope Scope, daily Daily, movies []MovieViews, cohorts []Cohort, now time.Time) []Point {
	day := scope.Day
	now = now.UTC()
	today := DateOnly(now)
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
		found[DateOnly(cohort.Date).Format(time.DateOnly)] = cohort
	}
	for _, cohortDate := range scope.Cohorts {
		cohort, ok := found[cohortDate.Format(time.DateOnly)]
		if !ok || cohort.Size == 0 {
			continue
		}
		returned := map[int16]uint64{1: cohort.D1Users, 7: cohort.D7Users}
		for _, n := range RetentionDays {
			var value *float64
			if !cohortDate.AddDate(0, 0, int(n)).After(today) {
				share := float64(returned[n]) / float64(cohort.Size)
				value = &share
			}
			points = append(points, Point{Date: cohortDate, Name: Retention, CohortDay: n, Value: value, ComputedAt: now})
		}
	}
	return points
}

func DateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
