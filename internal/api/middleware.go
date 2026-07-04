package api

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

// statusRecorder captures the status code written by a handler so the
// logging middleware can report it. It delegates Flush/Hijack to the
// underlying ResponseWriter when present so SSE streaming and WebSocket
// upgrades work transparently through the middleware.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush delegates to the underlying writer if it supports flushing,
// so SSE streaming works through the logging middleware.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack delegates to the underlying writer if it supports hijacking.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("response writer does not support hijacking")
}

// Push delegates to the underlying writer if it supports HTTP/2 server push.
func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	if p, ok := r.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// withLogging wraps h with a structured per-request slog log line:
// method, path, status, duration, bytes. It uses the server's logger.
func (s *Server) withLogging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)

		// Skip noise: don't log health probes at debug level.
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).String(),
			"bytes", rec.bytes,
		}
		if strings.HasPrefix(r.URL.Path, "/health") {
			s.logger.Debug("request", attrs...)
			return
		}
		if rec.status >= 500 {
			s.logger.Error("request", attrs...)
			return
		}
		if rec.status >= 400 {
			s.logger.Warn("request", attrs...)
			return
		}
		s.logger.Info("request", attrs...)
	})
}

// withRecover catches panics in handlers so a bug in one route cannot
// crash the whole gateway. The panic is logged and a 500 returned.
func (s *Server) withRecover(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("handler panic", "panic", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal server error", "server_error")
			}
		}()
		h.ServeHTTP(w, r)
	})
}
