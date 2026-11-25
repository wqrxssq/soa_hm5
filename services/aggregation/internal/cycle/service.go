package cycle

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"movieaggregation/internal/export"
	"movieaggregation/internal/metrics"
	"movieaggregation/internal/store"
)

type Service struct {
	ch             *store.ClickHouse
	pg             *store.Postgres
	s3             *export.Client
	aggInterval    time.Duration
	exportInterval time.Duration

	mu      sync.Mutex
	pending []time.Time
}

func New(ch *store.ClickHouse, pg *store.Postgres, s3 *export.Client, aggInterval, exportInterval time.Duration) *Service {
	return &Service{
		ch:             ch,
		pg:             pg,
		s3:             s3,
		aggInterval:    aggInterval,
		exportInterval: exportInterval,
	}
}

func (s *Service) Start(ctx context.Context) {
	s.waitHistory(ctx)
	today := dateOnly(time.Now())
	for i := 7; i >= 0; i-- {
		if ctx.Err() != nil {
			return
		}
		day := today.AddDate(0, 0, -i)
		if err := s.Run(ctx, day); err != nil {
			slog.Error("startup recalculation failed", "date", day.Format(time.DateOnly), "err", err)
		}
	}
	s.exportTick(ctx)
	go s.aggLoop(ctx)
	go s.exportLoop(ctx)
}

func (s *Service) Run(ctx context.Context, day time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run(ctx, day)
}

func (s *Service) ExportDate(ctx context.Context, day time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	day = dateOnly(day)
	if err := s.writeExport(ctx, day); err != nil {
		slog.Error("export failed", "date", day.Format(time.DateOnly), "err", err)
		s.rememberLocked(day)
		return err
	}
	return nil
}

func (s *Service) run(ctx context.Context, day time.Time) error {
	day = dateOnly(day)
	started := time.Now()
	slog.Info("aggregation cycle started", "date", day.Format(time.DateOnly))
	if err := s.ch.RebuildUserDay(ctx, day); err != nil {
		return err
	}
	daily, err := s.ch.Daily(ctx, day)
	if err != nil {
		return err
	}
	movies, err := s.ch.MovieViews(ctx, day)
	if err != nil {
		return err
	}
	cohorts, err := s.ch.Retention(ctx, []time.Time{day, day.AddDate(0, 0, -1), day.AddDate(0, 0, -7)})
	if err != nil {
		return err
	}
	points := metrics.Build(day, daily, movies, cohorts, time.Now())
	if err := s.ch.InsertMetrics(ctx, points); err != nil {
		return err
	}
	if err := s.pg.Save(ctx, day, points); err != nil {
		return err
	}
	slog.Info(fmt.Sprintf("cycle finished, processed %d records in %.3f sec", daily.RawRows, time.Since(started).Seconds()))
	return nil
}

func (s *Service) writeExport(ctx context.Context, day time.Time) error {
	points, err := s.pg.List(ctx, day)
	if err != nil {
		return err
	}
	return s.s3.PutDay(ctx, day, points)
}

func (s *Service) waitHistory(ctx context.Context) {
	deadline := time.Now().Add(3 * time.Minute)
	var last uint64
	stable := 0
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := s.ch.CountRecent(ctx)
		// History() emits 303 events. Matching that count avoids recalculating a partial ingest.
		if err == nil && n >= 303 {
			slog.Info("raw events ready", "rows", n)
			return
		}
		if err == nil && n > 0 && n == last {
			stable++
			if stable >= 5 {
				slog.Info("raw events stabilized", "rows", n)
				return
			}
		} else if err == nil {
			stable = 0
			last = n
		}
		if time.Now().After(deadline) {
			slog.Warn("timed out waiting for raw events", "rows", last, "err", err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Service) aggLoop(ctx context.Context) {
	ticker := time.NewTicker(s.aggInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			day := dateOnly(time.Now())
			if err := s.Run(ctx, day); err != nil {
				slog.Error("aggregation cycle failed", "date", day.Format(time.DateOnly), "err", err)
			}
		}
	}
}

func (s *Service) exportLoop(ctx context.Context) {
	ticker := time.NewTicker(s.exportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.exportTick(ctx)
		}
	}
}

func (s *Service) exportTick(ctx context.Context) {
	s.mu.Lock()
	pending := append([]time.Time{}, s.pending...)
	s.pending = nil
	s.mu.Unlock()

	seen := map[string]bool{}
	for _, day := range pending {
		seen[day.Format(time.DateOnly)] = true
		_ = s.ExportDate(ctx, day)
	}
	previous := dateOnly(time.Now().UTC()).AddDate(0, 0, -1)
	if !seen[previous.Format(time.DateOnly)] {
		_ = s.ExportDate(ctx, previous)
	}
}

func (s *Service) rememberLocked(day time.Time) {
	key := day.Format(time.DateOnly)
	for _, item := range s.pending {
		if item.Format(time.DateOnly) == key {
			return
		}
	}
	s.pending = append(s.pending, day)
}

func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
