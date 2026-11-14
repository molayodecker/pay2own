package httpapi

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder remembers the status code the handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests writes one log line per request, without IMEIs or tokens.
func logRequests(log *slog.Logger, mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		_, pattern := mux.Handler(r)
		if pattern == "" {
			pattern = "unmatched"
		}
		log.Info("request",
			"method", r.Method,
			"route", pattern,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
