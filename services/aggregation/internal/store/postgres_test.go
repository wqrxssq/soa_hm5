package store

import (
	"strings"
	"testing"
)

func TestUpsertReplacesSameKey(t *testing.T) {
	if !strings.Contains(upsertSQL, "ON CONFLICT (metric_date, metric_name, movie_id, cohort_day)") {
		t.Fatal(upsertSQL)
	}
	if !strings.Contains(upsertSQL, "DO UPDATE SET value = EXCLUDED.value, computed_at = EXCLUDED.computed_at") {
		t.Fatal(upsertSQL)
	}
}
