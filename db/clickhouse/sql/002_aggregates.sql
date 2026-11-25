CREATE TABLE IF NOT EXISTS analytics.user_day (
    event_date Date,
    user_id String,
    event_count UInt64,
    view_started UInt64,
    view_finished UInt64,
    finished_progress_sum Int64,
    finished_progress_count UInt64
)
ENGINE = MergeTree
PARTITION BY event_date
ORDER BY (event_date, user_id);

-- REPLACE PARTITION needs the same structure, engine and partition key as user_day.
CREATE TABLE IF NOT EXISTS analytics.user_day_staging AS analytics.user_day;

CREATE TABLE IF NOT EXISTS analytics.metric_points (
    metric_date Date,
    metric_name LowCardinality(String),
    movie_id String,
    cohort_day Int16,
    value Nullable(Float64),
    computed_at DateTime64(3, 'UTC'),
    is_deleted UInt8 DEFAULT 0
)
ENGINE = ReplacingMergeTree(computed_at, is_deleted)
PARTITION BY toYYYYMM(metric_date)
ORDER BY (metric_date, metric_name, movie_id, cohort_day);
