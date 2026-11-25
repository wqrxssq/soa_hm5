package event

import (
	"net/http"
	"time"

	"movieproducer/internal/apperr"
)

const (
	ViewStarted  = "VIEW_STARTED"
	ViewFinished = "VIEW_FINISHED"
	ViewPaused   = "VIEW_PAUSED"
	ViewResumed  = "VIEW_RESUMED"
	Liked        = "LIKED"
	Searched     = "SEARCHED"

	Mobile  = "MOBILE"
	Desktop = "DESKTOP"
	TV      = "TV"
	Tablet  = "TABLET"
)

type Event struct {
	ID        string
	UserID    string
	MovieID   string
	Type      string
	Timestamp time.Time
	Device    string
	SessionID string
	Progress  *int32
}

func (e Event) Validate() error {
	var pairs []string
	if e.UserID == "" {
		pairs = append(pairs, "user_id", "must not be empty")
	}
	if e.MovieID == "" {
		pairs = append(pairs, "movie_id", "must not be empty")
	}
	if e.SessionID == "" {
		pairs = append(pairs, "session_id", "must not be empty")
	}
	if e.Timestamp.IsZero() {
		pairs = append(pairs, "timestamp", "is required")
	}
	if !validDevice(e.Device) {
		pairs = append(pairs, "device_type", "unknown value")
	}
	switch e.Type {
	case ViewStarted, ViewFinished, ViewPaused, ViewResumed:
		if e.Progress == nil {
			pairs = append(pairs, "progress_seconds", "required for view events")
		} else if *e.Progress < 0 {
			pairs = append(pairs, "progress_seconds", "must not be negative")
		}
	case Liked, Searched:
		if e.Progress != nil {
			pairs = append(pairs, "progress_seconds", "not allowed for this event type")
		}
	default:
		pairs = append(pairs, "event_type", "unknown value")
	}
	if len(pairs) == 0 {
		return nil
	}
	return apperr.WithDetails(http.StatusBadRequest, "VALIDATION_ERROR", "validation failed", apperr.Fields(pairs...))
}

func validDevice(device string) bool {
	switch device {
	case Mobile, Desktop, TV, Tablet:
		return true
	default:
		return false
	}
}
