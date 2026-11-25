package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"movieproducer/internal/api"
	"movieproducer/internal/apperr"
	"movieproducer/internal/event"
	"movieproducer/internal/kafka"
)

type Server struct {
	pub *kafka.Publisher
}

type eventBody struct {
	UserID          string    `json:"user_id"`
	MovieID         string    `json:"movie_id"`
	EventType       string    `json:"event_type"`
	Timestamp       time.Time `json:"timestamp"`
	DeviceType      string    `json:"device_type"`
	SessionID       string    `json:"session_id"`
	ProgressSeconds *int32    `json:"progress_seconds"`
}

func New(pub *kafka.Publisher) (http.Handler, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	validator, err := newValidator(spec)
	if err != nil {
		return nil, err
	}
	server := &Server{pub: pub}
	router := chi.NewRouter()
	router.Use(recoverer)
	router.Use(validator)
	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, routeNotFound())
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, methodNotAllowed())
	})
	return api.HandlerWithOptions(server, api.ChiServerOptions{
		BaseRouter: router,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, apperr.WithDetails(http.StatusBadRequest, "VALIDATION_ERROR", "validation failed", apperr.Fields("request", err.Error())))
		},
	}), nil
}

func (s *Server) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) PublishEvent(w http.ResponseWriter, r *http.Request) {
	var body eventBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	ev := event.Event{
		UserID:    body.UserID,
		MovieID:   body.MovieID,
		Type:      body.EventType,
		Timestamp: body.Timestamp.UTC(),
		Device:    body.DeviceType,
		SessionID: body.SessionID,
		Progress:  body.ProgressSeconds,
	}
	if err := ev.Validate(); err != nil {
		writeError(w, err)
		return
	}
	ev.ID = uuid.NewString()
	if err := s.pub.Publish(r.Context(), ev); err != nil {
		slog.Error("kafka publish failed", "err", err, "event_id", ev.ID)
		writeError(w, apperr.New(http.StatusServiceUnavailable, "UNAVAILABLE", "Kafka did not accept the event"))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"event_id": ev.ID})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("panic", "err", recovered)
				writeError(w, apperr.New(http.StatusInternalServerError, "INTERNAL_ERROR", "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
