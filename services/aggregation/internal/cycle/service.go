package cycle

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"movieaggregation/internal/metrics"
)

// watchDays is how far back the scheduler looks for new raw events. A late event for one of
// these days changes D1 or D7 of a cohort that is still on the dashboard.
const watchDays = 7

type Warehouse interface {
	RawCounts(ctx context.Context, from time.Time) (map[string]uint64, error)
	MissingUserDays(ctx context.Context) ([]time.Time, error)
	RebuildUserDay(ctx context.Context, day time.Time) error
	Daily(ctx context.Context, day time.Time) (metrics.Daily, error)
	MovieViews(ctx context.Context, day time.Time) ([]metrics.MovieViews, error)
	Retention(ctx context.Context, cohorts []time.Time) ([]metrics.Cohort, error)
	WriteMetrics(ctx context.Context, scope metrics.Scope, points []metrics.Point) error
}

type MetricStore interface {
	Save(ctx context.Context, scope metrics.Scope, points []metrics.Point) error
	List(ctx context.Context, day time.Time) ([]metrics.Point, error)
}

type ObjectStore interface {
	PutDay(ctx context.Context, day time.Time, points []metrics.Point) error
}

type Service struct {
	wh             Warehouse
	pg             MetricStore
	s3             ObjectStore
	aggInterval    time.Duration
	exportInterval time.Duration
	now            func() time.Time

	runMu sync.Mutex

	stateMu       sync.Mutex
	seen          map[string]uint64
	pendingRecalc map[string]time.Time
	pendingExport map[string]time.Time
}

func New(wh Warehouse, pg MetricStore, s3 ObjectStore, aggInterval, exportInterval time.Duration) *Service {
	return &Service{
		wh:             wh,
		pg:             pg,
		s3:             s3,
		aggInterval:    aggInterval,
		exportInterval: exportInterval,
		now:            time.Now,
		seen:           map[string]uint64{},
		pendingRecalc:  map[string]time.Time{},
		pendingExport:  map[string]time.Time{},
	}
}

func (s *Service) Start(ctx context.Context) {
	s.backfill(ctx)
	s.AggregateTick(ctx)
	s.ExportTick(ctx)
	go every(ctx, s.aggInterval, s.AggregateTick)
	go every(ctx, s.exportInterval, s.ExportTick)
}

// Run recalculates one day. A failed day is retried by the next aggregation tick.
func (s *Service) Run(ctx context.Context, day time.Time) error {
	day = metrics.DateOnly(day)
	s.runMu.Lock()
	err := s.run(ctx, day)
	s.runMu.Unlock()
	if err != nil {
		slog.Error("aggregation cycle failed", "date", day.Format(time.DateOnly), "err", err)
		s.remember(s.pendingRecalc, day)
		return err
	}
	s.forget(s.pendingRecalc, day)
	return nil
}

// ExportDate writes one day to object storage, even when the day has no metrics.
// A failed day is retried by the next export tick.
func (s *Service) ExportDate(ctx context.Context, day time.Time) error {
	return s.export(ctx, day, false)
}

// export with skipEmpty is the scheduled path: a date without metrics gets no object.
func (s *Service) export(ctx context.Context, day time.Time, skipEmpty bool) error {
	day = metrics.DateOnly(day)
	s.runMu.Lock()
	rows, err := s.writeExport(ctx, day, skipEmpty)
	s.runMu.Unlock()
	if err != nil {
		slog.Error("export failed", "date", day.Format(time.DateOnly), "err", err)
		s.remember(s.pendingExport, day)
		return err
	}
	s.forget(s.pendingExport, day)
	if rows == 0 && skipEmpty {
		return nil
	}
	slog.Info("export finished", "date", day.Format(time.DateOnly), "rows", rows)
	return nil
}

// AggregateTick recalculates today, every watched day whose raw row count changed since the
// last tick and every day that failed before. A recalculated past day is exported again.
func (s *Service) AggregateTick(ctx context.Context) {
	today := metrics.DateOnly(s.now())
	counts, err := s.wh.RawCounts(ctx, today.AddDate(0, 0, -watchDays))
	if err != nil {
		slog.Error("read raw counts failed", "err", err)
		return
	}
	reexport := map[string]time.Time{}
	for _, day := range s.dueDays(today, counts) {
		if ctx.Err() != nil {
			return
		}
		key := day.Format(time.DateOnly)
		if err := s.Run(ctx, day); err != nil {
			continue
		}
		s.stateMu.Lock()
		s.seen[key] = counts[key]
		s.stateMu.Unlock()
		if day.Before(today) {
			for _, affected := range metrics.ScopeFor(day).Cohorts {
				if affected.Before(today) {
					reexport[affected.Format(time.DateOnly)] = affected
				}
			}
		}
	}
	for _, day := range sorted(reexport) {
		_ = s.export(ctx, day, true)
	}
}

// ExportTick exports yesterday, the day whose D1 became final and the day whose D7 became
// final, plus every date that failed before.
func (s *Service) ExportTick(ctx context.Context) {
	yesterday := metrics.DateOnly(s.now()).AddDate(0, 0, -1)
	due := map[string]time.Time{}
	s.stateMu.Lock()
	for key, day := range s.pendingExport {
		due[key] = day
	}
	s.stateMu.Unlock()
	for _, n := range []int{0, 1, 7} {
		day := yesterday.AddDate(0, 0, -n)
		due[day.Format(time.DateOnly)] = day
	}
	for _, day := range sorted(due) {
		if ctx.Err() != nil {
			return
		}
		_ = s.export(ctx, day, true)
	}
}

func (s *Service) dueDays(today time.Time, counts map[string]uint64) []time.Time {
	due := map[string]time.Time{today.Format(time.DateOnly): today}
	oldest := today.AddDate(0, 0, -watchDays)
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	for key, n := range counts {
		if seen, ok := s.seen[key]; !ok || seen != n {
			day, err := time.Parse(time.DateOnly, key)
			if err == nil {
				due[key] = day
			}
		}
	}
	for key, seen := range s.seen {
		day, err := time.Parse(time.DateOnly, key)
		if err != nil || day.Before(oldest) {
			delete(s.seen, key)
			continue
		}
		if _, ok := counts[key]; !ok && seen > 0 {
			due[key] = day
		}
	}
	for key, day := range s.pendingRecalc {
		due[key] = day
	}
	return sorted(due)
}

// backfill builds user_day for raw dates older than the watched window, so cohorts there are
// found by the real first day of a user.
func (s *Service) backfill(ctx context.Context) {
	days, err := s.wh.MissingUserDays(ctx)
	if err != nil {
		slog.Error("find missing user_day partitions failed", "err", err)
		return
	}
	oldest := metrics.DateOnly(s.now()).AddDate(0, 0, -watchDays)
	for _, day := range days {
		if !day.Before(oldest) {
			continue
		}
		s.runMu.Lock()
		err := s.wh.RebuildUserDay(ctx, day)
		s.runMu.Unlock()
		if err != nil {
			slog.Error("user_day backfill failed", "date", day.Format(time.DateOnly), "err", err)
		}
	}
}

func (s *Service) run(ctx context.Context, day time.Time) error {
	started := time.Now()
	slog.Info("aggregation cycle started", "date", day.Format(time.DateOnly))
	scope := metrics.ScopeFor(day)
	if err := s.wh.RebuildUserDay(ctx, day); err != nil {
		return err
	}
	daily, err := s.wh.Daily(ctx, day)
	if err != nil {
		return err
	}
	movies, err := s.wh.MovieViews(ctx, day)
	if err != nil {
		return err
	}
	cohorts, err := s.wh.Retention(ctx, scope.Cohorts)
	if err != nil {
		return err
	}
	points := metrics.Build(scope, daily, movies, cohorts, s.now())
	if err := s.wh.WriteMetrics(ctx, scope, points); err != nil {
		return err
	}
	if err := s.pg.Save(ctx, scope, points); err != nil {
		return err
	}
	slog.Info(fmt.Sprintf("cycle finished, processed %d records in %.3f sec", daily.RawRows, time.Since(started).Seconds()))
	return nil
}

func (s *Service) writeExport(ctx context.Context, day time.Time, skipEmpty bool) (int, error) {
	points, err := s.pg.List(ctx, day)
	if err != nil {
		return 0, err
	}
	if len(points) == 0 && skipEmpty {
		return 0, nil
	}
	return len(points), s.s3.PutDay(ctx, day, points)
}

func (s *Service) remember(set map[string]time.Time, day time.Time) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	set[day.Format(time.DateOnly)] = day
}

func (s *Service) forget(set map[string]time.Time, day time.Time) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	delete(set, day.Format(time.DateOnly))
}

func every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

func sorted(days map[string]time.Time) []time.Time {
	out := make([]time.Time, 0, len(days))
	for _, day := range days {
		out = append(out, day)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
