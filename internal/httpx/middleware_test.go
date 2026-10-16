package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
})

func TestAPIKey(t *testing.T) {
	h := APIKey("secret")(okHandler)
	tests := []struct {
		name, path, key string
		want            int
	}{
		{"valid key", "/api/analyze", "secret", 200},
		{"wrong key", "/api/analyze", "nope", 401},
		{"missing key", "/api/analyze", "", 401},
		{"prefix of key", "/api/analyze", "secre", 401},
		{"health needs no key", "/health", "", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tt.path, nil)
			if tt.key != "" {
				r.Header.Set("X-Api-Key", tt.key)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestCORSPreflight(t *testing.T) {
	// Preflight carries no X-Api-Key, so CORS must answer before auth.
	h := Chain(okHandler, CORS("https://app.example"), APIKey("secret"))

	r := httptest.NewRequest("OPTIONS", "/api/analyze", nil)
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "x-api-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "X-Api-Key, Content-Type" {
		t.Errorf("Allow-Headers = %q", got)
	}
}

func TestCORSOtherOrigin(t *testing.T) {
	h := CORS("https://app.example")(okHandler)
	r := httptest.NewRequest("OPTIONS", "/api/analyze", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want empty", got)
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewRateLimiter(30, time.Minute)
	l.now = func() time.Time { return now }
	h := l.Middleware()(okHandler)

	do := func(ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/analyze", nil)
		r.Header.Set("X-Forwarded-For", ip+", 10.0.0.1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	for i := range 30 {
		if w := do("1.2.3.4"); w.Code != 200 {
			t.Fatalf("request %d: status %d", i, w.Code)
		}
	}
	w := do("1.2.3.4")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("31st request: status %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") != "2" {
		t.Errorf("Retry-After = %q, want 2", w.Header().Get("Retry-After"))
	}
	if w := do("5.6.7.8"); w.Code != 200 {
		t.Fatalf("other IP: status %d", w.Code)
	}

	now = now.Add(2 * time.Second)
	if w := do("1.2.3.4"); w.Code != 200 {
		t.Fatalf("after refill: status %d", w.Code)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "9.9.9.9:1234"
	if got := ClientIP(r); got != "9.9.9.9" {
		t.Errorf("RemoteAddr: got %q", got)
	}
	r.Header.Set("X-Forwarded-For", " 1.1.1.1 , 2.2.2.2")
	if got := ClientIP(r); got != "1.1.1.1" {
		t.Errorf("XFF: got %q", got)
	}
}

func TestBodyLimit(t *testing.T) {
	var readErr error
	h := BodyLimit(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("123456"))
	r.ContentLength = -1
	h.ServeHTTP(httptest.NewRecorder(), r)
	if _, ok := readErr.(*http.MaxBytesError); !ok {
		t.Fatalf("err = %v, want MaxBytesError", readErr)
	}
}

func TestRecover(t *testing.T) {
	h := Recover(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 500 {
		t.Fatalf("status = %d", w.Code)
	}
}
