package observability

import (
	"context"
	"net/http"
	"time"
)

type ReadinessDependency interface {
	Ping(context.Context) error
}

type Health struct {
	dependency ReadinessDependency
	timeout    time.Duration
}

func NewHealth(dependency ReadinessDependency, timeout time.Duration) *Health {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Health{dependency: dependency, timeout: timeout}
}

func (h *Health) LiveHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (h *Health) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	if h.dependency == nil {
		h.unavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	if err := h.dependency.Ping(ctx); err != nil {
		h.unavailable(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (h *Health) unavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("unavailable\n"))
}
