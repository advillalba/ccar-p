package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MetricRequestsTotal     = "ccarp_http_requests_total"
	MetricRequestLatency    = "ccarp_http_request_duration_seconds"
	MetricErrorsTotal       = "ccarp_errors_total"
	MetricThrottlesTotal    = "ccarp_throttles_total"
	MetricPublicationsTotal = "ccarp_publications_total"
	MetricExportsTotal      = "ccarp_exports_total"
)

type Metrics struct {
	mu       sync.Mutex
	counters map[counterKey]uint64
	latency  map[latencyKey]latencyValue
}

type RequestRecorder struct {
	metrics *Metrics
	next    http.Handler
}

type counterKey struct {
	name   string
	labels string
}

type latencyKey struct {
	method string
	route  string
	status string
}

type latencyValue struct {
	count uint64
	sum   float64
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func NewMetrics() *Metrics {
	return &Metrics{counters: make(map[counterKey]uint64), latency: make(map[latencyKey]latencyValue)}
}

func (m *Metrics) RequestMiddleware(next http.Handler) http.Handler {
	return &RequestRecorder{metrics: m, next: next}
}

func (r *RequestRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	started := time.Now()
	recorder := &responseWriter{ResponseWriter: w, status: http.StatusOK}
	r.next.ServeHTTP(recorder, req)
	r.metrics.RecordRequest(req.Method, Route(req), recorder.status, time.Since(started))
}

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func Route(r *http.Request) string {
	if pattern := r.Pattern; pattern != "" {
		return pattern
	}
	return "unmatched"
}

func (m *Metrics) RecordRequest(method, route string, status int, duration time.Duration) {
	method = requestMethod(method)
	route = requestRoute(route)
	statusLabel := strconv.Itoa(statusClass(status))
	labels := "method=" + method + ",route=" + route + ",status=" + statusLabel
	m.add(MetricRequestsTotal, labels)
	m.mu.Lock()
	key := latencyKey{method: method, route: route, status: statusLabel}
	value := m.latency[key]
	value.count++
	value.sum += duration.Seconds()
	m.latency[key] = value
	m.mu.Unlock()
	if status >= http.StatusInternalServerError {
		m.RecordError("http", "server_error")
	}
}

func (m *Metrics) RecordError(component, kind string) {
	m.add(MetricErrorsTotal, "component="+errorComponent(component)+",kind="+errorKind(kind))
}

func (m *Metrics) RecordThrottle(scope string) {
	m.add(MetricThrottlesTotal, "scope="+oneOf(scope, "auth", "mcp"))
}

func (m *Metrics) RecordPublication(kind, outcome string) {
	m.add(MetricPublicationsTotal, "kind="+oneOf(kind, "note", "exam")+",outcome="+outcomeLabel(outcome))
}

func (m *Metrics) RecordExport(kind, outcome string) {
	m.add(MetricExportsTotal, "kind="+oneOf(kind, "note", "exam")+",outcome="+outcomeLabel(outcome))
}

func (m *Metrics) Counter(name string, labels map[string]string) uint64 {
	return m.counter(name, labelsFor(name, labels))
}

func (m *Metrics) Latency(method, route string, status int) (uint64, time.Duration) {
	m.mu.Lock()
	value := m.latency[latencyKey{method: requestMethod(method), route: requestRoute(route), status: strconv.Itoa(statusClass(status))}]
	m.mu.Unlock()
	return value.count, time.Duration(value.sum * float64(time.Second))
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(m.Export()))
	})
}

func (m *Metrics) Export() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]counterKey, 0, len(m.counters))
	for key := range m.counters {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].name+keys[i].labels < keys[j].name+keys[j].labels })
	var output strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&output, "%s{%s} %d\n", key.name, prometheusLabels(key.labels), m.counters[key])
	}
	latencyKeys := make([]latencyKey, 0, len(m.latency))
	for key := range m.latency {
		latencyKeys = append(latencyKeys, key)
	}
	sort.Slice(latencyKeys, func(i, j int) bool {
		return latencyKeys[i].method+latencyKeys[i].route+latencyKeys[i].status < latencyKeys[j].method+latencyKeys[j].route+latencyKeys[j].status
	})
	for _, key := range latencyKeys {
		value := m.latency[key]
		labels := prometheusLabels("method=" + key.method + ",route=" + key.route + ",status=" + key.status)
		fmt.Fprintf(&output, "%s_count{%s} %d\n%s_sum{%s} %g\n", MetricRequestLatency, labels, value.count, MetricRequestLatency, labels, value.sum)
	}
	return output.String()
}

func (m *Metrics) add(name, labels string) {
	m.mu.Lock()
	m.counters[counterKey{name: name, labels: labels}]++
	m.mu.Unlock()
}

func (m *Metrics) counter(name, labels string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[counterKey{name: name, labels: labels}]
}

func labelsFor(name string, labels map[string]string) string {
	switch name {
	case MetricRequestsTotal:
		return "method=" + requestMethod(labels["method"]) + ",route=" + requestRoute(labels["route"]) + ",status=" + strconv.Itoa(statusClassValue(labels["status"]))
	case MetricErrorsTotal:
		return "component=" + errorComponent(labels["component"]) + ",kind=" + errorKind(labels["kind"])
	case MetricThrottlesTotal:
		return "scope=" + oneOf(labels["scope"], "auth", "mcp")
	case MetricPublicationsTotal, MetricExportsTotal:
		return "kind=" + oneOf(labels["kind"], "note", "exam") + ",outcome=" + outcomeLabel(labels["outcome"])
	default:
		return ""
	}
}

func requestMethod(value string) string {
	return oneOf(value, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions)
}

func requestRoute(value string) string {
	if value == "" {
		return "unmatched"
	}
	return value
}

func statusClass(status int) int {
	return status / 100 * 100
}

func statusClassValue(value string) int {
	status, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return statusClass(status)
}

func errorComponent(value string) string {
	return oneOf(value, "http", "auth", "mcp", "publication", "export")
}

func errorKind(value string) string {
	return oneOf(value, "server_error", "validation", "dependency", "timeout", "rejected")
}

func outcomeLabel(value string) string {
	return oneOf(value, "success", "failure", "validation_failed", "warning", "retry")
}

func oneOf(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "other"
}

func prometheusLabels(labels string) string {
	parts := strings.Split(labels, ",")
	for index, part := range parts {
		key, value, _ := strings.Cut(part, "=")
		parts[index] = key + "=\"" + value + "\""
	}
	return strings.Join(parts, ",")
}
