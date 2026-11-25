package cycle

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"movieaggregation/internal/metrics"
)

var now = time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)

type fakeWarehouse struct {
	counts  map[string]uint64
	missing []time.Time
	rebuilt []string
	fail    map[string]bool
}

func (f *fakeWarehouse) RawCounts(context.Context, time.Time) (map[string]uint64, error) {
	out := map[string]uint64{}
	for k, v := range f.counts {
		out[k] = v
	}
	return out, nil
}

func (f *fakeWarehouse) MissingUserDays(context.Context) ([]time.Time, error) {
	return f.missing, nil
}

func (f *fakeWarehouse) RebuildUserDay(_ context.Context, day time.Time) error {
	key := day.Format(time.DateOnly)
	if f.fail[key] {
		return errors.New("clickhouse down")
	}
	f.rebuilt = append(f.rebuilt, key)
	return nil
}

func (f *fakeWarehouse) Daily(context.Context, time.Time) (metrics.Daily, error) {
	return metrics.Daily{DAU: 1}, nil
}

func (f *fakeWarehouse) MovieViews(context.Context, time.Time) ([]metrics.MovieViews, error) {
	return nil, nil
}

func (f *fakeWarehouse) Retention(context.Context, []time.Time) ([]metrics.Cohort, error) {
	return nil, nil
}

func (f *fakeWarehouse) WriteMetrics(context.Context, metrics.Scope, []metrics.Point) error {
	return nil
}

type fakeStore struct {
	saved []string
	empty map[string]bool
}

func (f *fakeStore) Save(_ context.Context, scope metrics.Scope, _ []metrics.Point) error {
	f.saved = append(f.saved, scope.Day.Format(time.DateOnly))
	return nil
}

func (f *fakeStore) List(_ context.Context, day time.Time) ([]metrics.Point, error) {
	if f.empty[day.Format(time.DateOnly)] {
		return nil, nil
	}
	return []metrics.Point{{Date: day, Name: metrics.DAU}}, nil
}

type fakeObjects struct {
	put  []string
	down bool
}

func (f *fakeObjects) PutDay(_ context.Context, day time.Time, _ []metrics.Point) error {
	if f.down {
		return errors.New("minio down")
	}
	f.put = append(f.put, day.Format(time.DateOnly))
	return nil
}

func newService(wh *fakeWarehouse) (*Service, *fakeStore, *fakeObjects) {
	pg := &fakeStore{}
	s3 := &fakeObjects{}
	svc := New(wh, pg, s3, time.Minute, 24*time.Hour)
	svc.now = func() time.Time { return now }
	return svc, pg, s3
}

func uniq(items []string) []string {
	set := map[string]bool{}
	for _, item := range items {
		set[item] = true
	}
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func TestTickRecalculatesOnlyChangedDays(t *testing.T) {
	wh := &fakeWarehouse{counts: map[string]uint64{"2026-11-23": 10, "2026-11-24": 20, "2026-11-25": 5}}
	svc, pg, _ := newService(wh)

	svc.AggregateTick(context.Background())
	if want := []string{"2026-11-23", "2026-11-24", "2026-11-25"}; !reflect.DeepEqual(pg.saved, want) {
		t.Fatalf("first tick %v", pg.saved)
	}

	pg.saved = nil
	wh.counts["2026-11-23"] = 11
	svc.AggregateTick(context.Background())
	if want := []string{"2026-11-23", "2026-11-25"}; !reflect.DeepEqual(pg.saved, want) {
		t.Fatalf("second tick %v", pg.saved)
	}
}

func TestPastDayRecalculationIsExportedAgain(t *testing.T) {
	wh := &fakeWarehouse{counts: map[string]uint64{"2026-11-25": 5}}
	svc, _, s3 := newService(wh)
	svc.AggregateTick(context.Background())
	if len(s3.put) != 0 {
		t.Fatalf("today alone must not export: %v", s3.put)
	}

	wh.counts["2026-11-24"] = 3
	svc.AggregateTick(context.Background())
	if want := []string{"2026-11-17", "2026-11-23", "2026-11-24"}; !reflect.DeepEqual(uniq(s3.put), want) {
		t.Fatalf("exports %v", s3.put)
	}
}

func TestFailedDayIsRetried(t *testing.T) {
	wh := &fakeWarehouse{counts: map[string]uint64{"2026-11-25": 5}, fail: map[string]bool{"2026-11-01": true}}
	svc, pg, _ := newService(wh)
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := svc.Run(context.Background(), day); err == nil {
		t.Fatal("expected error")
	}
	wh.fail = nil
	svc.AggregateTick(context.Background())
	if want := []string{"2026-11-01", "2026-11-25"}; !reflect.DeepEqual(pg.saved, want) {
		t.Fatalf("saved %v", pg.saved)
	}
	pg.saved = nil
	svc.AggregateTick(context.Background())
	if want := []string{"2026-11-25"}; !reflect.DeepEqual(pg.saved, want) {
		t.Fatalf("retry repeated: %v", pg.saved)
	}
}

func TestExportTickDatesAndRetry(t *testing.T) {
	svc, _, s3 := newService(&fakeWarehouse{})
	s3.down = true
	if err := svc.ExportDate(context.Background(), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected error")
	}
	s3.down = false
	svc.ExportTick(context.Background())
	if want := []string{"2026-11-02", "2026-11-17", "2026-11-23", "2026-11-24"}; !reflect.DeepEqual(s3.put, want) {
		t.Fatalf("exports %v", s3.put)
	}
	s3.put = nil
	svc.ExportTick(context.Background())
	if want := []string{"2026-11-17", "2026-11-23", "2026-11-24"}; !reflect.DeepEqual(s3.put, want) {
		t.Fatalf("pending export repeated: %v", s3.put)
	}
}

func TestScheduledExportSkipsEmptyDays(t *testing.T) {
	svc, pg, s3 := newService(&fakeWarehouse{})
	pg.empty = map[string]bool{"2026-11-17": true}
	svc.ExportTick(context.Background())
	if want := []string{"2026-11-23", "2026-11-24"}; !reflect.DeepEqual(s3.put, want) {
		t.Fatalf("exports %v", s3.put)
	}
	if err := svc.ExportDate(context.Background(), time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if s3.put[len(s3.put)-1] != "2026-11-17" {
		t.Fatalf("manual export of an empty day was skipped: %v", s3.put)
	}
}

func TestBackfillOnlyOlderThanWindow(t *testing.T) {
	wh := &fakeWarehouse{missing: []time.Time{
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC),
	}}
	svc, _, _ := newService(wh)
	svc.backfill(context.Background())
	if want := []string{"2026-11-01"}; !reflect.DeepEqual(wh.rebuilt, want) {
		t.Fatalf("rebuilt %v", wh.rebuilt)
	}
}
