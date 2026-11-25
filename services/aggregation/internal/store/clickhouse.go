package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"movieaggregation/internal/metrics"
)

type ClickHouse struct {
	conn driver.Conn
}

func OpenClickHouse(ctx context.Context, dsn string) (*ClickHouse, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	if err := wait(ctx, "clickhouse", conn.Ping); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &ClickHouse{conn: conn}, nil
}

func (c *ClickHouse) Close() error {
	return c.conn.Close()
}

func (c *ClickHouse) CountRecent(ctx context.Context) (uint64, error) {
	var n uint64
	err := c.conn.QueryRow(ctx, `
		SELECT toUInt64(count())
		FROM analytics.movie_events
		WHERE event_date >= today() - 7
	`).Scan(&n)
	return n, err
}

func (c *ClickHouse) RebuildUserDay(ctx context.Context, day time.Time) error {
	partition := day.UTC().Format(time.DateOnly)
	err := c.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE analytics.user_day DROP PARTITION '%s'", partition))
	if err != nil && !missingPartition(err) {
		return err
	}
	return c.conn.Exec(ctx, `
		INSERT INTO analytics.user_day
		SELECT
			event_date,
			user_id,
			toUInt64(count()),
			toUInt64(countIf(event_type = 'VIEW_STARTED')),
			toUInt64(countIf(event_type = 'VIEW_FINISHED')),
			toInt64(sumIf(ifNull(progress_seconds, 0), event_type = 'VIEW_FINISHED')),
			toUInt64(countIf(event_type = 'VIEW_FINISHED' AND progress_seconds IS NOT NULL))
		FROM analytics.movie_events
		WHERE event_date = toDate(?)
		GROUP BY event_date, user_id
	`, partition)
}

func (c *ClickHouse) Daily(ctx context.Context, day time.Time) (metrics.Daily, error) {
	var daily metrics.Daily
	err := c.conn.QueryRow(ctx, `
		SELECT
			toUInt64(count()),
			toUInt64(uniqExact(user_id)),
			toUInt64(countIf(event_type = 'VIEW_STARTED')),
			toUInt64(countIf(event_type = 'VIEW_FINISHED')),
			if(
				countIf(event_type = 'VIEW_FINISHED') = 0,
				CAST(NULL AS Nullable(Float64)),
				avgIf(toFloat64(progress_seconds), event_type = 'VIEW_FINISHED')
			)
		FROM analytics.movie_events
		WHERE event_date = toDate(?)
	`, day.UTC().Format(time.DateOnly)).Scan(&daily.RawRows, &daily.DAU, &daily.Started, &daily.Finished, &daily.AvgWatch)
	return daily, err
}

func (c *ClickHouse) MovieViews(ctx context.Context, day time.Time) ([]metrics.MovieViews, error) {
	rows, err := c.conn.Query(ctx, `
		SELECT movie_id, toUInt64(count())
		FROM analytics.movie_events
		WHERE event_date = toDate(?) AND event_type = 'VIEW_STARTED'
		GROUP BY movie_id
	`, day.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []metrics.MovieViews
	for rows.Next() {
		var item metrics.MovieViews
		if err := rows.Scan(&item.MovieID, &item.Views); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c *ClickHouse) Retention(ctx context.Context, days []time.Time) ([]metrics.Cohort, error) {
	if len(days) != 3 {
		return nil, fmt.Errorf("retention expects 3 cohort dates")
	}
	args := []any{
		days[0].UTC().Format(time.DateOnly),
		days[1].UTC().Format(time.DateOnly),
		days[2].UTC().Format(time.DateOnly),
	}
	rows, err := c.conn.Query(ctx, `
		WITH per_user AS (
			SELECT
				user_id,
				minIf(event_date, event_type = 'VIEW_STARTED') AS cohort_date,
				groupUniqArray(32)(event_date) AS active_days
			FROM analytics.movie_events
			GROUP BY user_id
			HAVING countIf(event_type = 'VIEW_STARTED') > 0
			   AND cohort_date IN (toDate(?), toDate(?), toDate(?))
		)
		SELECT
			toString(cohort_date),
			toUInt64(uniqExact(user_id)),
			toUInt64(uniqExactIf(user_id, has(active_days, addDays(cohort_date, 1)))),
			toUInt64(uniqExactIf(user_id, has(active_days, addDays(cohort_date, 7))))
		FROM per_user
		GROUP BY cohort_date
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []metrics.Cohort
	for rows.Next() {
		var raw string
		var item metrics.Cohort
		if err := rows.Scan(&raw, &item.Size, &item.D1Users, &item.D7Users); err != nil {
			return nil, err
		}
		item.Date, err = time.Parse(time.DateOnly, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c *ClickHouse) InsertMetrics(ctx context.Context, points []metrics.Point) error {
	batch, err := c.conn.PrepareBatch(ctx, `
		INSERT INTO analytics.metric_points
		(metric_date, metric_name, movie_id, cohort_day, value, computed_at)
	`)
	if err != nil {
		return err
	}
	for _, point := range points {
		if err := batch.Append(point.Date.UTC(), point.Name, point.MovieID, point.CohortDay, point.Value, point.ComputedAt.UTC()); err != nil {
			return err
		}
	}
	return batch.Send()
}

func missingPartition(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "doesn't exist") || strings.Contains(text, "DOESNT_EXIST")
}

func wait(ctx context.Context, name string, op func(context.Context) error) error {
	deadline := time.Now().Add(60 * time.Second)
	var err error
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = op(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		slog.Info("waiting for connection", "target", name, "err", err)
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
