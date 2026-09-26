package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testAPI() *API {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), []string{"https://allowed.example"}, 5*time.Millisecond)
}

func TestSuccessEnvelopeAndHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1", nil)
	w := httptest.NewRecorder()
	testAPI().Handler.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("X-Request-ID") == "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected response: %d %#v", w.Code, w.Header())
	}
	var body successEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.RequestID == "" {
		t.Fatalf("invalid envelope: %s", w.Body.String())
	}
}

func TestMalformedAndOversizedJSON(t *testing.T) {
	cases := map[string]struct {
		body string
		want int
	}{
		"malformed": {body: "{", want: http.StatusBadRequest},
		"oversized": {body: `{"value":"` + strings.Repeat("x", maxBodyBytes) + `"}`, want: http.StatusRequestEntityTooLarge},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/echo", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			testAPI().Handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			var envelope errorEnvelope
			if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Error.Code == "" || envelope.Error.RequestID == "" {
				t.Fatalf("invalid error envelope: %s", w.Body.String())
			}
		})
	}
}

func TestDisallowedOrigin(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	testAPI().Handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || strings.Contains(w.Header().Get("Access-Control-Allow-Origin"), "evil") {
		t.Fatalf("unexpected CORS response: %d %#v", w.Code, w.Header())
	}
}
func TestAllowedOrigin(t *testing.T) {
	r := httptest.NewRequest(http.MethodOptions, "/api/v1", nil)
	r.Header.Set("Origin", "https://allowed.example")
	w := httptest.NewRecorder()
	testAPI().Handler.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://allowed.example" {
		t.Fatalf("unexpected CORS response")
	}
}
func TestTimeoutEnvelope(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/test/slow", nil)
	w := httptest.NewRecorder()
	testAPI().Handler.ServeHTTP(w, r)
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "request_timeout") {
		t.Fatalf("unexpected timeout: %d %s", w.Code, w.Body.String())
	}
}
func TestRecoveryEnvelope(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/test/panic", nil)
	w := httptest.NewRecorder()
	testAPI().Handler.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "internal_error") {
		t.Fatalf("unexpected recovery: %d %s", w.Code, w.Body.String())
	}
}
