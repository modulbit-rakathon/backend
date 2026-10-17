package share

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pillgaze/internal/httpx"
)

func newTestHandoff() (*HandoffHandler, *clock) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	st := NewStore[string](15 * time.Minute)
	st.now = c.now
	lim := NewFailureLimiter()
	lim.now = c.now
	return &HandoffHandler{Store: st, Limiter: lim}, c
}

func doHandoff(h http.HandlerFunc, ip, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/handoff", strings.NewReader(body))
	r.Header.Set("X-Forwarded-For", ip)
	w := httptest.NewRecorder()
	httpx.BodyLimit(HandoffMaxBodyBytes)(h).ServeHTTP(w, r)
	return w
}

func handoffBody(data string) string { return fmt.Sprintf(`{"data":%q}`, data) }

func mustHandoff(t *testing.T, h *HandoffHandler, data string) string {
	t.Helper()
	w := doHandoff(h.Create, "1.1.1.1", handoffBody(data))
	if w.Code != http.StatusCreated {
		t.Fatalf("handoff: status %d: %s", w.Code, w.Body)
	}
	var resp shareResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !validPIN(resp.PIN) || resp.ExpiresIn != 900 {
		t.Fatalf("handoff response %+v", resp)
	}
	return resp.PIN
}

func TestHandoffClaimOnce(t *testing.T) {
	h, _ := newTestHandoff()
	pin := mustHandoff(t, h, "1z.abc-_09")

	w := doHandoff(h.Claim, "2.2.2.2", claimBody(pin))
	var resp handoffResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != 200 || resp.Data != "1z.abc-_09" {
		t.Fatalf("claim: status %d, data %q", w.Code, resp.Data)
	}
	if w := doHandoff(h.Claim, "2.2.2.2", claimBody(pin)); w.Code != 404 {
		t.Fatalf("second claim: status %d, want 404", w.Code)
	}
}

func TestHandoffExpiry(t *testing.T) {
	h, c := newTestHandoff()
	pin := mustHandoff(t, h, "1j.e30")
	c.add(15 * time.Minute)
	if w := doHandoff(h.Claim, "2.2.2.2", claimBody(pin)); w.Code != 404 {
		t.Fatalf("expired claim: status %d, want 404", w.Code)
	}
}

func TestHandoffSeparateFromShare(t *testing.T) {
	h, _ := newTestHandoff()
	sh, _ := newTestHandler(false)
	pin := mustHandoff(t, h, "1j.e30")
	if w := do(sh.Claim, "3.3.3.3", claimBody(pin)); w.Code != 404 {
		t.Fatalf("handoff PIN claimed via /api/share/claim: status %d", w.Code)
	}
}

func TestHandoffFailureLimit(t *testing.T) {
	h, _ := newTestHandoff()
	pin := mustHandoff(t, h, "1j.e30")
	wrong := "9999"
	if pin == wrong {
		wrong = "9998"
	}
	for range IPFailures {
		doHandoff(h.Claim, "6.6.6.6", claimBody(wrong))
	}
	if w := doHandoff(h.Claim, "6.6.6.6", claimBody(pin)); w.Code != 429 {
		t.Fatalf("after %d failures: status %d, want 429", IPFailures, w.Code)
	}
}

func TestHandoffValidation(t *testing.T) {
	h, _ := newTestHandoff()
	tests := []struct {
		name, body string
		want       int
	}{
		{"ok", handoffBody("1z.AbC_-9"), 201},
		{"empty", handoffBody(""), 400},
		{"no prefix", handoffBody("AbC"), 400},
		{"bad mode", handoffBody("1x.AbC"), 400},
		{"bad chars", handoffBody("1z.Ab+C/="), 400},
		{"unknown field", `{"data":"1z.a","pin":"1234"}`, 400},
		{"too long", handoffBody("1z." + strings.Repeat("a", MaxHandoffLen)), 413},
		{"body too large", `{"data":"` + strings.Repeat("a", HandoffMaxBodyBytes) + `"}`, 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := doHandoff(h.Create, "1.1.1.1", tt.body); w.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body)
			}
		})
	}
}
