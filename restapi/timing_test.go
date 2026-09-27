package restapi

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestTimingMiddleware_LogsDuration(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	handler := RequestTimingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/leaderboards/song?song_id=1&role_id=1", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rr.Code)
	}

	logged := buf.String()
	if !strings.Contains(logged, "REST timing:") {
		t.Fatalf("expected timing log line, got %q", logged)
	}
	if !strings.Contains(logged, "GET /leaderboards/song?song_id=1&role_id=1") {
		t.Fatalf("expected method and path with query in log, got %q", logged)
	}
	if !strings.Contains(logged, "status=201") {
		t.Fatalf("expected status=201 in log, got %q", logged)
	}
	if !strings.Contains(logged, "duration_ms=") {
		t.Fatalf("expected duration_ms in log, got %q", logged)
	}
}

func TestRequestTimingMiddleware_DefaultStatusOK(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	handler := RequestTimingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	logged := buf.String()
	if !strings.Contains(logged, "status=200") {
		t.Fatalf("expected default status=200 when WriteHeader not called, got %q", logged)
	}
}
