package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// HttpWriterWithStatus wraps an http.ResponseWriter to capture the status
// code written. Embedding (rather than holding a named field) means it gets
// Header() and Write() for free via Go's method promotion, and only needs to
// override WriteHeader — the one method net/http and httputil.ReverseProxy
// actually call to set the status.
type HttpWriterWithStatus struct {
	http.ResponseWriter
	status int
}

func (w *HttpWriterWithStatus) WriteHeader(status int) {
	w.status = status

	w.ResponseWriter.WriteHeader(status)
}

// LogMiddleware returns a Middleware that logs one line per request. It's a
// factory (takes the logger, returns a Middleware) rather than a bare
// Middleware because a Middleware has no way to receive a *slog.Logger, and
// the factory is called once at startup — the per-request work has to live
// in the innermost closure, which is handed a fresh w/r on every request.
func LogMiddleware(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			wrappedWriter := &HttpWriterWithStatus{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(wrappedWriter, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrappedWriter.status,
				"duration", time.Since(start),
				"request_id", GetRequestId(r.Context()),
			)
		})
	}
}
