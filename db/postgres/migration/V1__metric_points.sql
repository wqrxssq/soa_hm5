CREATE TABLE metric_points (
    metric_date DATE NOT NULL,
    metric_name TEXT NOT NULL,
    movie_id TEXT NOT NULL,
    cohort_day SMALLINT NOT NULL,
    value DOUBLE PRECISION,
    computed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (metric_date, metric_name, movie_id, cohort_day)
);
