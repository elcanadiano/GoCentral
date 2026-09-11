package restapi

import (
	"log"
	"net/http"
	"time"
)

// statusCapturingResponseWriter records the HTTP status code written by handlers.
type statusCapturingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *statusCapturingResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// RequestTimingMiddleware logs method, path, query, status, and duration for each REST request.
// Intended for live latency benchmarking; enable via ENABLERESTBENCHMARK on the Chi router only.
func RequestTimingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusCapturingResponseWriter{ResponseWriter: w, status: 0}

		next.ServeHTTP(sw, r)

		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}

		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path = path + "?" + r.URL.RawQuery
		}

		log.Printf("REST timing: %s %s status=%d duration_ms=%.2f",
			r.Method, path, status, float64(time.Since(start).Microseconds())/1000.0)
	})
}
