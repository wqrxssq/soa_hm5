package config

import (
	"testing"
	"time"
)

func setAll(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"HTTP_ADDR":            ":8080",
		"CLICKHOUSE_DSN":       "clickhouse://default:x@clickhouse:9000/analytics",
		"DATABASE_URL":         "postgres://a:b@postgres:5432/analytics",
		"AGGREGATION_INTERVAL": "1m",
		"EXPORT_INTERVAL":      "24h",
		"S3_ENDPOINT":          "https://minio:9000",
		"S3_BUCKET":            "movie-analytics",
		"S3_ACCESS_KEY":        "a",
		"S3_SECRET_KEY":        "b",
	} {
		t.Setenv(key, value)
	}
}

func TestLoad(t *testing.T) {
	setAll(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AggInterval != time.Minute || cfg.ExportInterval != 24*time.Hour {
		t.Fatalf("%+v", cfg)
	}
	if cfg.S3.Host != "minio:9000" || !cfg.S3.Secure {
		t.Fatalf("%+v", cfg.S3)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"DATABASE_URL":         "",
		"AGGREGATION_INTERVAL": "soon",
		"EXPORT_INTERVAL":      "-1h",
		"S3_ENDPOINT":          "minio",
	}
	for key, value := range cases {
		t.Run(key, func(t *testing.T) {
			setAll(t)
			t.Setenv(key, value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q accepted", key, value)
			}
		})
	}
}
