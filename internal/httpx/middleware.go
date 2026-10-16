package httpx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one listed is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recover turns a panic into a 500 without leaking details to the client.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					log.Error("panic", "request_id", RequestID(r.Context()), "path", r.URL.Path, "panic", v)
					WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// LogInfo collects the few request facts that may be logged. Handlers fill it
// via Info(ctx); it must never hold request bodies, images or drug names.
type LogInfo struct {
	mu               sync.Mutex
	images           int
	model            string
	promptTokens     int
	completionTokens int
}

func (l *LogInfo) SetImages(n int) {
	l.mu.Lock()
	l.images = n
	l.mu.Unlock()
}

func (l *LogInfo) AddUsage(model string, prompt, completion int) {
	l.mu.Lock()
	l.model = model
	l.promptTokens += prompt
	l.completionTokens += completion
	l.mu.Unlock()
}

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyLogInfo
)

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(keyRequestID).(string)
	return id
}

// Info returns the request's LogInfo, or a throwaway one outside Logger.
func Info(ctx context.Context) *LogInfo {
	if li, ok := ctx.Value(keyLogInfo).(*LogInfo); ok {
		return li
	}
	return &LogInfo{}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Logger logs one line per request: id, method, path, status, duration and
// whatever the handler put into LogInfo.
func Logger(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			id := newRequestID()
			li := &LogInfo{}
			ctx := context.WithValue(r.Context(), keyRequestID, id)
			ctx = context.WithValue(ctx, keyLogInfo, li)
			w.Header().Set("X-Request-Id", id)
			sw := &statusWriter{ResponseWriter: w}

			next.ServeHTTP(sw, r.WithContext(ctx))

			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			li.mu.Lock()
			attrs := []any{
				"request_id", id,
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			}
			if li.images > 0 {
				attrs = append(attrs, "images", li.images)
			}
			if li.model != "" {
				attrs = append(attrs, "model", li.model,
					"prompt_tokens", li.promptTokens,
					"completion_tokens", li.completionTokens)
			}
			li.mu.Unlock()
			log.Info("request", attrs...)
		})
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CORS allows the single configured origin ("*" allows any) and answers
// preflight requests before authentication runs.
func CORS(allowedOrigin string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := origin != "" && allowedOrigin != "" && (allowedOrigin == "*" || origin == allowedOrigin)
			if allowed {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					h := w.Header()
					h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "X-Api-Key, Content-Type")
					h.Set("Access-Control-Max-Age", "600")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// APIKey requires X-Api-Key on /api/* paths. An empty key disables the check
// (only allowed in mock mode, see config).
func APIKey(key string) Middleware {
	want := sha256.Sum256([]byte(key))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if key == "" || !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			// Comparing hashes keeps the comparison constant-time regardless of length.
			got := sha256.Sum256([]byte(r.Header.Get("X-Api-Key")))
			if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "missing or invalid X-Api-Key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP takes the first X-Forwarded-For entry (set by the App Service or
// Container Apps front end), falling back to RemoteAddr. App Service writes
// the entry as "ip:port", so the port is stripped.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := stripPort(strings.TrimSpace(first)); ip != "" {
			return ip
		}
	}
	return stripPort(r.RemoteAddr)
}

func stripPort(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// BodyLimit caps the request body; reads past the limit fail with
// *http.MaxBytesError, which handlers map to 413.
func BodyLimit(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				WriteError(w, http.StatusRequestEntityTooLarge, CodeTooLarge, "request body too large")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
