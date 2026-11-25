package export

import (
	"encoding/json"
	"testing"
	"time"

	"movieaggregation/internal/metrics"
)

func TestMarshalKeepsDateAndComputedAt(t *testing.T) {
	day := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	computed := time.Date(2026, 4, 3, 1, 2, 3, 0, time.UTC)
	dau := 12.0
	body, err := marshal([]metrics.Point{
		{Date: day, Name: metrics.DAU, CohortDay: -1, Value: &dau, ComputedAt: computed},
		{Date: day, Name: metrics.Retention, CohortDay: 7, ComputedAt: computed},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("%s", body)
	}
	if rows[0]["metric_date"] != "2026-04-02" || rows[0]["computed_at"] != "2026-04-03T01:02:03Z" || rows[0]["value"] != 12.0 {
		t.Fatalf("%v", rows[0])
	}
	if value, ok := rows[1]["value"]; !ok || value != nil {
		t.Fatalf("unknown retention must be null: %v", rows[1])
	}
}

func TestObjectKey(t *testing.T) {
	if got := objectKey(time.Date(2026, 4, 2, 23, 0, 0, 0, time.UTC)); got != "daily/2026-04-02/aggregates.json" {
		t.Fatal(got)
	}
}
