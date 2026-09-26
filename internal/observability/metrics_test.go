package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestMiddlewareRecordsRouteStatusClassAndLatency(t *testing.T) {
	metrics := NewMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	server := httptest.NewServer(metrics.RequestMiddleware(mux))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/items/secret-id")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	labels := map[string]string{"method": "GET", "route": "GET /api/v1/items/{id}", "status": "200"}
	if got := metrics.Counter(MetricRequestsTotal, labels); got != 1 {
		t.Fatalf("got %d requests", got)
	}
	count, _ := metrics.Latency("GET", "GET /api/v1/items/{id}", http.StatusCreated)
	if count != 1 {
		t.Fatalf("got %d latency samples", count)
	}
	if strings.Contains(metrics.Export(), "secret-id") {
		t.Fatal("export included a request identifier")
	}
}

func TestMetricLabelsAreBounded(t *testing.T) {
	metrics := NewMetrics()
	metrics.RecordThrottle("client-123")
	metrics.RecordPublication("note-123", "actor@example.test")
	metrics.RecordExport("exam-123", "temporary failure")
	metrics.RecordError("database-123", "sql: select secret")
	if got := metrics.Counter(MetricThrottlesTotal, map[string]string{"scope": "client-456"}); got != 1 {
		t.Fatalf("got %d throttles", got)
	}
	if got := metrics.Counter(MetricPublicationsTotal, map[string]string{"kind": "note-456", "outcome": "actor@example.test"}); got != 1 {
		t.Fatalf("got %d publications", got)
	}
	if got := metrics.Counter(MetricExportsTotal, map[string]string{"kind": "exam-456", "outcome": "temporary failure"}); got != 1 {
		t.Fatalf("got %d exports", got)
	}
	if got := metrics.Counter(MetricErrorsTotal, map[string]string{"component": "database-456", "kind": "sql: select secret"}); got != 1 {
		t.Fatalf("got %d errors", got)
	}
	export := metrics.Export()
	for _, sensitive := range []string{"client-123", "actor@example.test", "temporary failure", "select secret"} {
		if strings.Contains(export, sensitive) {
			t.Fatalf("export included %q", sensitive)
		}
	}
}

func TestMetricsExport(t *testing.T) {
	metrics := NewMetrics()
	metrics.RecordRequest(http.MethodGet, "GET /api/v1", http.StatusOK, 10*time.Millisecond)
	metrics.RecordPublication("note", "validation_failed")
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("unexpected content type %q", response.Header().Get("Content-Type"))
	}
	for _, expected := range []string{MetricRequestsTotal, MetricRequestLatency + "_count", MetricPublicationsTotal} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("missing %q from %q", expected, response.Body.String())
		}
	}
}
