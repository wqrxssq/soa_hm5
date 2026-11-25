package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"movieaggregation/internal/metrics"
	"movieaggregation/internal/retry"
)

const upsertSQL = `
INSERT INTO metric_points (metric_date, metric_name, movie_id, cohort_day, value, computed_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (metric_date, metric_name, movie_id, cohort_day)
DO UPDATE SET value = EXCLUDED.value, computed_at = EXCLUDED.computed_at`

type Postgres struct {
	pool *pgxpool.Pool
}

func OpenPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := wait(ctx, "postgres", pool.Ping); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) Save(ctx context.Context, day time.Time, points []metrics.Point) error {
	attempt := 0
	return retry.Do(ctx, func() error {
		attempt++
		err := p.saveOnce(ctx, day, points)
		if err != nil {
			slog.Warn("postgres write failed", "attempt", attempt, "date", day.UTC().Format(time.DateOnly), "err", err)
		}
		return err
	})
}

func (p *Postgres) saveOnce(ctx context.Context, day time.Time, points []metrics.Point) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		DELETE FROM metric_points
		WHERE metric_date = $1 AND metric_name = $2
	`, day.UTC(), metrics.TopMovieViews); err != nil {
		return err
	}
	for _, point := range points {
		if _, err := tx.Exec(ctx, upsertSQL, point.Date.UTC(), point.Name, point.MovieID, point.CohortDay, point.Value, point.ComputedAt.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) List(ctx context.Context, day time.Time) ([]metrics.Point, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT metric_date, metric_name, movie_id, cohort_day, value, computed_at
		FROM metric_points
		WHERE metric_date = $1
		ORDER BY metric_name, movie_id, cohort_day
	`, day.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []metrics.Point
	for rows.Next() {
		var point metrics.Point
		if err := rows.Scan(&point.Date, &point.Name, &point.MovieID, &point.CohortDay, &point.Value, &point.ComputedAt); err != nil {
			return nil, err
		}
		out = append(out, point)
	}
	return out, rows.Err()
}
