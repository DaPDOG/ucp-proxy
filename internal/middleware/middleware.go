// Package middleware provides HTTP middleware for the UCP proxy.
package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Logging returns middleware that logs request details.
// Logs method, path, status, duration, and remote address.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			logger.Info("request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", wrapped.status),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}

// Recovery returns middleware that recovers from panics.
// Logs the panic and stack trace, returns 500 Internal Server Error.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						slog.Any("error", err),
						slog.String("path", r.URL.Path),
						slog.String("method", r.Method),
						slog.String("stack", string(debug.Stack())),
					)

					// Avoid writing if headers already sent
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(wrapped(w), r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// wrapped returns a responseWriter, handling the case where w is already wrapped.
func wrapped(w http.ResponseWriter) http.ResponseWriter {
	if _, ok := w.(*responseWriter); ok {
		return w
	}
	return &responseWriter{ResponseWriter: w, status: http.StatusOK}
}

// Chain combines multiple middleware into a single middleware.
// Middleware is applied in order: first middleware wraps the last.
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
