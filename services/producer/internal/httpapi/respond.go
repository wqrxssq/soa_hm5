package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"movieproducer/internal/apperr"
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

func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return apperr.WithDetails(http.StatusBadRequest, "VALIDATION_ERROR", "validation failed", apperr.Fields("body", err.Error()))
	}
	return nil
}
