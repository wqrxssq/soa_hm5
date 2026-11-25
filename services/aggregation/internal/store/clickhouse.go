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

// RawCounts returns the number of stored raw rows per date starting at from. The scheduler
// compares it between ticks to find days that received events.
func (c *ClickHouse) RawCounts(ctx context.Context, from time.Time) (map[string]uint64, error) {
	rows, err := c.conn.Query(ctx, `
		SELECT toString(event_date), toUInt64(count())
		FROM analytics.movie_events
		WHERE event_date >= toDate(?)
		GROUP BY event_date
	`, from.UTC().Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var day string
		var n uint64
		if err := rows.Scan(&day, &n); err != nil {
			return nil, err
		}
		out[day] = n
	}
	return out, rows.Err()
}

// MissingUserDays lists raw dates that have no user_day partition yet. Cohorts are found by
// the first active day, so every older day has to be present.
func (c *ClickHouse) MissingUserDays(ctx context.Context) ([]time.Time, error) {
	rows, err := c.conn.Query(ctx, `
		SELECT DISTINCT toString(event_date) AS day
		FROM analytics.movie_events
		WHERE event_date NOT IN (SELECT DISTINCT event_date FROM analytics.user_day)
		ORDER BY day
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		day, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, day)
	}
	return out, rows.Err()
}

// RebuildUserDay fills a staging partition and swaps it in with REPLACE PARTITION, so
// readers see either the old day or the new one, never an empty partition.
func (c *ClickHouse) RebuildUserDay(ctx context.Context, day time.Time) error {
	partition := day.UTC().Format(time.DateOnly)
	dropStaging := fmt.Sprintf("ALTER TABLE analytics.user_day_staging DROP PARTITION '%s'", partition)
	if err := c.conn.Exec(ctx, dropStaging); err != nil {
		return err
	}
	err := c.conn.Exec(ctx, `
		INSERT INTO analytics.user_day_staging
		SELECT
			event_date,
			user_id,
			toUInt64(count()),
			toUInt64(countIf(event_type = 'VIEW_STARTED')),
			toUInt64(countIf(event_type = 'VIEW_FINISHED')),
			toInt64(sumIf(ifNull(progress_seconds, 0), event_type = 'VIEW_FINISHED')),
			toUInt64(countIf(event_type = 'VIEW_FINISHED' AND progress_seconds IS NOT NULL))
		FROM analytics.movie_events FINAL
		WHERE event_date = toDate(?)
		GROUP BY event_date, user_id
	`, partition)
	if err != nil {
		return err
	}
	replace := fmt.Sprintf("ALTER TABLE analytics.user_day REPLACE PARTITION '%s' FROM analytics.user_day_staging", partition)
	if err := c.conn.Exec(ctx, replace); err != nil {
		return err
	}
	return c.conn.Exec(ctx, dropStaging)
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
		FROM analytics.movie_events FINAL
		WHERE event_date = toDate(?)
	`, day.UTC().Format(time.DateOnly)).Scan(&daily.RawRows, &daily.DAU, &daily.Started, &daily.Finished, &daily.AvgWatch)
	return daily, err
}

func (c *ClickHouse) MovieViews(ctx context.Context, day time.Time) ([]metrics.MovieViews, error) {
	rows, err := c.conn.Query(ctx, `
		SELECT movie_id, toUInt64(count())
		FROM analytics.movie_events FINAL
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

// Retention reads user_day: the cohort is the first day with VIEW_STARTED, a return is any
// user_day row on day 1 or day 7 of life.
func (c *ClickHouse) Retention(ctx context.Context, cohorts []time.Time) ([]metrics.Cohort, error) {
	if len(cohorts) == 0 {
		return nil, nil
	}
	latest := cohorts[0]
	for _, day := range cohorts {
		if day.After(latest) {
			latest = day
		}
	}
	rows, err := c.conn.Query(ctx, fmt.Sprintf(`
		WITH cohort AS (
			SELECT user_id, min(event_date) AS cohort_date
			FROM analytics.user_day
			WHERE view_started > 0 AND event_date <= %s
			GROUP BY user_id
			HAVING cohort_date IN (%s)
		)
		SELECT
			toString(c.cohort_date),
			toUInt64(uniqExact(c.user_id)),
			toUInt64(uniqExactIf(c.user_id, u.event_date = addDays(c.cohort_date, 1))),
			toUInt64(uniqExactIf(c.user_id, u.event_date = addDays(c.cohort_date, 7)))
		FROM cohort AS c
		LEFT JOIN analytics.user_day AS u
			ON u.user_id = c.user_id
			AND u.event_date >= addDays(c.cohort_date, 1)
			AND u.event_date <= addDays(c.cohort_date, 7)
		GROUP BY c.cohort_date
	`, dateLiteral(latest), dateList(cohorts)))
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

// WriteMetrics inserts the new points and a tombstone for every stored key of the scope
// that the calculation no longer produces. One batch keeps the swap atomic for readers.
func (c *ClickHouse) WriteMetrics(ctx context.Context, scope metrics.Scope, points []metrics.Point) error {
	existing, err := c.metricKeys(ctx, scope)
	if err != nil {
		return err
	}
	stale := metrics.Stale(scope, existing, points)
	batch, err := c.conn.PrepareBatch(ctx, `
		INSERT INTO analytics.metric_points
		(metric_date, metric_name, movie_id, cohort_day, value, computed_at, is_deleted)
	`)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, point := range points {
		if err := batch.Append(point.Date.UTC(), point.Name, point.MovieID, point.CohortDay, point.Value, point.ComputedAt.UTC(), uint8(0)); err != nil {
			return err
		}
	}
	for _, key := range stale {
		date, err := time.Parse(time.DateOnly, key.Date)
		if err != nil {
			return err
		}
		if err := batch.Append(date, key.Name, key.MovieID, key.CohortDay, (*float64)(nil), now, uint8(1)); err != nil {
			return err
		}
	}
	return batch.Send()
}

func (c *ClickHouse) metricKeys(ctx context.Context, scope metrics.Scope) ([]metrics.Key, error) {
	names := make([]string, 0, len(metrics.DayMetrics))
	for _, name := range metrics.DayMetrics {
		names = append(names, "'"+name+"'")
	}
	rows, err := c.conn.Query(ctx, fmt.Sprintf(`
		SELECT toString(metric_date), metric_name, movie_id, cohort_day
		FROM analytics.metric_points FINAL
		WHERE (metric_date = %s AND metric_name IN (%s))
		   OR (metric_name = '%s' AND metric_date IN (%s))
	`, dateLiteral(scope.Day), strings.Join(names, ", "), metrics.Retention, dateList(scope.Cohorts)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []metrics.Key
	for rows.Next() {
		var key metrics.Key
		if err := rows.Scan(&key.Date, &key.Name, &key.MovieID, &key.CohortDay); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func dateLiteral(day time.Time) string {
	return "toDate('" + day.UTC().Format(time.DateOnly) + "')"
}

func dateList(days []time.Time) string {
	items := make([]string, 0, len(days))
	for _, day := range days {
		items = append(items, dateLiteral(day))
	}
	return strings.Join(items, ", ")
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
