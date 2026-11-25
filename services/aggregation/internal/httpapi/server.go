package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"movieaggregation/internal/api"
	"movieaggregation/internal/apperr"
)

// A dropped client must not cancel a recalculation halfway through.
const jobTimeout = 2 * time.Minute

type Service interface {
	Run(ctx context.Context, day time.Time) error
	ExportDate(ctx context.Context, day time.Time) error
}

type Server struct {
	svc Service
}

func New(svc Service) (http.Handler, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	validator, err := newValidator(spec)
	if err != nil {
		return nil, err
	}
	server := &Server{svc: svc}
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
			field := "request"
			var format *api.InvalidParamFormatError
			if errors.As(err, &format) {
				field = format.ParamName
			}
			writeError(w, apperr.WithDetails(http.StatusBadRequest, "VALIDATION_ERROR", "validation failed", apperr.Fields(field, err.Error())))
		},
	}), nil
}

func (s *Server) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) Recalculate(w http.ResponseWriter, r *http.Request, _ api.RecalculateParams) {
	day, err := parseDate(r.URL.Query().Get("date"))
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, cancel := jobContext(r)
	defer cancel()
	if err := s.svc.Run(ctx, day); err != nil {
		writeError(w, apperr.New(http.StatusServiceUnavailable, "UNAVAILABLE", "could not recalculate metrics"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "date": day.Format(time.DateOnly)})
}

func (s *Server) ExportDay(w http.ResponseWriter, r *http.Request, _ api.ExportDayParams) {
	day, err := parseDate(r.URL.Query().Get("date"))
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, cancel := jobContext(r)
	defer cancel()
	if err := s.svc.ExportDate(ctx, day); err != nil {
		writeError(w, apperr.New(http.StatusServiceUnavailable, "UNAVAILABLE", "could not export metrics"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "date": day.Format(time.DateOnly)})
}

func jobContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), jobTimeout)
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
