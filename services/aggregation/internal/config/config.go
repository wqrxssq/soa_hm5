package config

import (
	"fmt"
	"net/url"
	"os"
	"time"
)

type S3 struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Secure    bool
	Host      string
}

type Config struct {
	HTTPAddr       string
	ClickHouseDSN  string
	DatabaseURL    string
	AggInterval    time.Duration
	ExportInterval time.Duration
	S3             S3
}

func Load() (Config, error) {
	s3cfg, err := LoadS3()
	if err != nil {
		return Config{}, err
	}
	addr, err := required("HTTP_ADDR")
	if err != nil {
		return Config{}, err
	}
	dsn, err := required("CLICKHOUSE_DSN")
	if err != nil {
		return Config{}, err
	}
	database, err := required("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	agg, err := duration("AGGREGATION_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	exportEvery, err := duration("EXPORT_INTERVAL")
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTPAddr:       addr,
		ClickHouseDSN:  dsn,
		DatabaseURL:    database,
		AggInterval:    agg,
		ExportInterval: exportEvery,
		S3:             s3cfg,
	}, nil
}

func LoadS3() (S3, error) {
	endpoint, err := required("S3_ENDPOINT")
	if err != nil {
		return S3{}, err
	}
	bucket, err := required("S3_BUCKET")
	if err != nil {
		return S3{}, err
	}
	access, err := required("S3_ACCESS_KEY")
	if err != nil {
		return S3{}, err
	}
	secret, err := required("S3_SECRET_KEY")
	if err != nil {
		return S3{}, err
	}
	host, secure, err := splitEndpoint(endpoint)
	if err != nil {
		return S3{}, err
	}
	return S3{
		Endpoint:  endpoint,
		Bucket:    bucket,
		AccessKey: access,
		SecretKey: secret,
		Secure:    secure,
		Host:      host,
	}, nil
}

func splitEndpoint(raw string) (string, bool, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("S3_ENDPOINT: %w", err)
	}
	if parsed.Host == "" {
		return "", false, fmt.Errorf("S3_ENDPOINT must include a host")
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

func required(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func duration(key string) (time.Duration, error) {
	raw, err := required(key)
	if err != nil {
		return 0, err
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return value, nil
}
