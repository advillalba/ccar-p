package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type pingDependency struct {
	err error
	ctx context.Context
}

func (d *pingDependency) Ping(ctx context.Context) error {
	d.ctx = ctx
	return d.err
}

func TestLiveHandlerIsIndependentOfDependency(t *testing.T) {
	health := NewHealth(&pingDependency{err: errors.New("unavailable")}, time.Millisecond)
	response := httptest.NewRecorder()
	health.LiveHandler(response, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("unexpected liveness response: %d %q", response.Code, response.Body.String())
	}
}

func TestReadyHandler(t *testing.T) {
	for name, dependency := range map[string]*pingDependency{
		"ready":     {},
		"unhealthy": {err: errors.New("database unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			health := NewHealth(dependency, time.Second)
			response := httptest.NewRecorder()
			health.ReadyHandler(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			want := http.StatusOK
			if dependency.err != nil {
				want = http.StatusServiceUnavailable
			}
			if response.Code != want {
				t.Fatalf("got status %d, want %d", response.Code, want)
			}
			if _, ok := dependency.ctx.Deadline(); !ok {
				t.Fatal("dependency did not receive a deadline")
			}
		})
	}
}

func TestReadyHandlerWithoutDependencyIsUnhealthy(t *testing.T) {
	health := NewHealth(nil, time.Second)
	response := httptest.NewRecorder()
	health.ReadyHandler(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d", response.Code)
	}
}
