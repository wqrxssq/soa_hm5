package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"movieaggregation/internal/api"
	"movieaggregation/internal/apperr"
	"movieaggregation/internal/cycle"
)

type Server struct {
	svc *cycle.Service
}

func New(svc *cycle.Service) (http.Handler, error) {
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
			writeError(w, apperr.WithDetails(http.StatusBadRequest, "VALIDATION_ERROR", "validation failed", apperr.Fields("request", err.Error())))
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
	if err := s.svc.Run(r.Context(), day); err != nil {
		slog.Error("recalculate failed", "date", day.Format("2006-01-02"), "err", err)
		writeError(w, apperr.New(http.StatusServiceUnavailable, "UNAVAILABLE", "could not recalculate metrics"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "date": day.Format("2006-01-02")})
}

func (s *Server) ExportDay(w http.ResponseWriter, r *http.Request, _ api.ExportDayParams) {
	day, err := parseDate(r.URL.Query().Get("date"))
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.svc.ExportDate(r.Context(), day); err != nil {
		writeError(w, apperr.New(http.StatusServiceUnavailable, "UNAVAILABLE", "could not export metrics"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "date": day.Format("2006-01-02")})
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
