package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const maxBodyBytes = 1 << 20

type API struct {
	Handler http.Handler
	logger  *slog.Logger
	mux     *http.ServeMux
	origins map[string]struct{}
	timeout time.Duration
}

type Error struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id"`
}

type errorEnvelope struct {
	Error Error `json:"error"`
}
type successEnvelope struct {
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}
type contextKey string

const requestIDKey contextKey = "request-id"

func New(logger *slog.Logger, allowedOrigins []string, timeout time.Duration) *API {
	api := &API{logger: logger, origins: make(map[string]struct{}), timeout: timeout}
	for _, origin := range allowedOrigins {
		api.origins[origin] = struct{}{}
	}
	api.mux = http.NewServeMux()
	api.mux.HandleFunc("GET /api/v1", api.index)
	api.mux.HandleFunc("POST /api/v1/echo", api.echo)
	api.mux.HandleFunc("GET /api/v1/test/panic", func(http.ResponseWriter, *http.Request) { panic("test panic") })
	api.mux.HandleFunc("GET /api/v1/test/slow", api.slow)
	api.Handler = api.requestID(api.recover(api.security(api.cors(api.limitBody(api.withTimeout(api.mux))))))
	return api
}

func (a *API) Mux() *http.ServeMux {
	return a.mux
}

func (a *API) index(w http.ResponseWriter, r *http.Request) {
	WriteData(w, r, http.StatusOK, map[string]string{"version": "v1"})
}
func (a *API) slow(w http.ResponseWriter, r *http.Request) {
	<-r.Context().Done()
	WriteError(w, r, http.StatusGatewayTimeout, "request_timeout", "request timed out", nil)
}
func (a *API) echo(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) {
			WriteError(w, r, http.StatusBadRequest, "invalid_json", "request body is required", nil)
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds 1 MiB", nil)
			return
		}
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one valid JSON object", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one valid JSON object", nil)
		return
	}
	WriteData(w, r, http.StatusOK, payload)
}

func WriteData(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, status, successEnvelope{Data: data, RequestID: RequestID(r.Context())})
}
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string, fields map[string]string) {
	writeJSON(w, status, errorEnvelope{Error: Error{Code: code, Message: message, Fields: fields, RequestID: RequestID(r.Context())}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func (a *API) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if len(id) < 8 || len(id) > 128 {
			var raw [16]byte
			_, _ = rand.Read(raw[:])
			id = hex.EncodeToString(raw[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}
func (a *API) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "request_id", RequestID(r.Context()))
				WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (a *API) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
func (a *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := a.origins[origin]; !ok {
				WriteError(w, r, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed", nil)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, X-Request-ID")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
