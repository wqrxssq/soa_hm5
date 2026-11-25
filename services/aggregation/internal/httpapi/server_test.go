package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeService struct {
	fail     bool
	days     []time.Time
	canceled bool
}

func (f *fakeService) Run(ctx context.Context, day time.Time) error {
	return f.record(ctx, day)
}

func (f *fakeService) ExportDate(ctx context.Context, day time.Time) error {
	return f.record(ctx, day)
}

func (f *fakeService) record(ctx context.Context, day time.Time) error {
	f.days = append(f.days, day)
	f.canceled = ctx.Err() != nil
	if f.fail {
		return errors.New("down")
	}
	return nil
}

func call(t *testing.T, svc Service, method, target string, ctx context.Context) (int, map[string]any) {
	t.Helper()
	handler, err := New(svc)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return rec.Code, body
}

func TestRecalculate(t *testing.T) {
	svc := &fakeService{}
	code, body := call(t, svc, http.MethodPost, "/recalculate?date=2026-04-02", context.Background())
	if code != http.StatusOK || body["date"] != "2026-04-02" {
		t.Fatalf("%d %v", code, body)
	}
	if len(svc.days) != 1 || !svc.days[0].Equal(time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v", svc.days)
	}
}

func TestBadDates(t *testing.T) {
	for _, target := range []string{"/recalculate", "/recalculate?date=2026-13-01", "/export?date=2026-02-31", "/export?date=x"} {
		svc := &fakeService{}
		code, body := call(t, svc, http.MethodPost, target, context.Background())
		if code != http.StatusBadRequest || body["error_code"] != "VALIDATION_ERROR" {
			t.Fatalf("%s: %d %v", target, code, body)
		}
		fields, _ := body["details"].(map[string]any)["fields"].([]any)
		if len(fields) == 0 || fields[0].(map[string]any)["field"] != "date" {
			t.Fatalf("%s: error does not name the date field: %v", target, body)
		}
		if len(svc.days) != 0 {
			t.Fatalf("%s reached the service", target)
		}
	}
}

func TestFailureIsUnavailable(t *testing.T) {
	for _, target := range []string{"/recalculate?date=2026-04-02", "/export?date=2026-04-02"} {
		code, body := call(t, &fakeService{fail: true}, http.MethodPost, target, context.Background())
		if code != http.StatusServiceUnavailable || body["error_code"] != "UNAVAILABLE" {
			t.Fatalf("%s: %d %v", target, code, body)
		}
	}
}

func TestClientCancelDoesNotStopJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := &fakeService{}
	call(t, svc, http.MethodPost, "/export?date=2026-04-02", ctx)
	if svc.canceled {
		t.Fatal("job saw the client's cancellation")
	}
}

func TestUnknownRouteAndMethod(t *testing.T) {
	if code, body := call(t, &fakeService{}, http.MethodGet, "/nope", context.Background()); code != http.StatusNotFound || body["error_code"] != "NOT_FOUND" {
		t.Fatalf("%d %v", code, body)
	}
	if code, body := call(t, &fakeService{}, http.MethodGet, "/recalculate?date=2026-04-02", context.Background()); code != http.StatusMethodNotAllowed {
		t.Fatalf("%d %v", code, body)
	}
}
