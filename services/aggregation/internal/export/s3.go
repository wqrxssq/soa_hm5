package export

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"movieaggregation/internal/config"
	"movieaggregation/internal/metrics"
)

type Client struct {
	bucket string
	api    *minio.Client
}

type row struct {
	MetricDate string   `json:"metric_date"`
	MetricName string   `json:"metric_name"`
	MovieID    string   `json:"movie_id"`
	CohortDay  int16    `json:"cohort_day"`
	Value      *float64 `json:"value"`
	ComputedAt string   `json:"computed_at"`
}

func New(cfg config.S3) (*Client, error) {
	api, err := minio.New(cfg.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.Secure,
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, err
	}
	return &Client{bucket: cfg.Bucket, api: api}, nil
}

func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.api.BucketExists(ctx, c.bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return c.api.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{Region: "us-east-1"})
}

func (c *Client) PutDay(ctx context.Context, day time.Time, points []metrics.Point) error {
	body, err := marshal(points)
	if err != nil {
		return err
	}
	_, err = c.api.PutObject(ctx, c.bucket, objectKey(day), bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType: "application/json",
	})
	return err
}

func objectKey(day time.Time) string {
	return fmt.Sprintf("daily/%s/aggregates.json", day.UTC().Format(time.DateOnly))
}

func marshal(points []metrics.Point) ([]byte, error) {
	rows := make([]row, 0, len(points))
	for _, point := range points {
		rows = append(rows, row{
			MetricDate: point.Date.UTC().Format(time.DateOnly),
			MetricName: point.Name,
			MovieID:    point.MovieID,
			CohortDay:  point.CohortDay,
			Value:      point.Value,
			ComputedAt: point.ComputedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return json.MarshalIndent(rows, "", "  ")
}
