package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"movieaggregation/internal/apperr"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	message := "internal error"
	var details map[string]any
	if target, ok := apperr.As(err); ok {
		status = target.Status
		code = target.Code
		message = target.Message
		details = target.Details
	} else {
		slog.Error("unhandled error", "err", err)
	}
	body := map[string]any{
		"error_code": code,
		"message":    message,
	}
	if details != nil {
		body["details"] = details
	}
	writeJSON(w, status, body)
}

func routeNotFound() error {
	return apperr.New(http.StatusNotFound, "NOT_FOUND", "route not found")
}

func methodNotAllowed() error {
	return apperr.New(http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
}

func parseDate(raw string) (time.Time, error) {
	// time.Parse normalizes 2026-02-31 to March 3. Reject that instead of querying the wrong day.
	day, err := time.Parse(time.DateOnly, raw)
	if err != nil || day.Format(time.DateOnly) != raw {
		return time.Time{}, apperr.WithDetails(http.StatusBadRequest, "VALIDATION_ERROR", "validation failed", apperr.Fields("date", "expected YYYY-MM-DD"))
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), nil
}
