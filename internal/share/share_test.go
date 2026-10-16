package share

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"pillgaze/internal/fixtures"
	"pillgaze/internal/httpx"
	"pillgaze/internal/model"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestHandler(mock bool) (*Handler, *clock) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	st := NewStore(3 * time.Minute)
	st.now = c.now
	lim := NewFailureLimiter()
	lim.now = c.now
	return &Handler{Store: st, Limiter: lim, Mock: mock, Fixture: fixtures.Shared}, c
}

func med(id string) model.SharedMedication {
	s := "5 mg"
	return model.SharedMedication{ID: id, Kind: "drug", Name: "Prestarium Neo", Strength: &s, Source: "box"}
}

func do(h http.HandlerFunc, ip, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/share", strings.NewReader(body))
	r.Header.Set("X-Forwarded-For", ip)
	w := httptest.NewRecorder()
	httpx.BodyLimit(MaxBodyBytes)(h).ServeHTTP(w, r)
	return w
}

func shareBody(meds ...model.SharedMedication) string {
	b, _ := json.Marshal(map[string]any{"medications": meds})
	return string(b)
}

func claimBody(pin string) string { return fmt.Sprintf(`{"pin":%q}`, pin) }

func mustShare(t *testing.T, h *Handler, meds ...model.SharedMedication) string {
	t.Helper()
	w := do(h.Share, "1.1.1.1", shareBody(meds...))
	if w.Code != http.StatusCreated {
		t.Fatalf("share: status %d: %s", w.Code, w.Body)
	}
	var resp shareResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !validPIN(resp.PIN) || resp.ExpiresIn != 180 {
		t.Fatalf("share response %+v", resp)
	}
	return resp.PIN
}

func TestShareAndClaimOnce(t *testing.T) {
	h, _ := newTestHandler(false)
	pin := mustShare(t, h, med("med_1"), med("med_2"))

	w := do(h.Claim, "2.2.2.2", claimBody(pin))
	if w.Code != 200 {
		t.Fatalf("claim: status %d", w.Code)
	}
	var resp claimResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Medications) != 2 || resp.Medications[0].ID != "med_1" {
		t.Fatalf("claimed %+v", resp.Medications)
	}
	if w := do(h.Claim, "2.2.2.2", claimBody(pin)); w.Code != 404 {
		t.Fatalf("second claim: status %d, want 404", w.Code)
	}
	if h.Store.Len() != 0 {
		t.Fatalf("store still holds %d entries", h.Store.Len())
	}
}

func TestExpiry(t *testing.T) {
	h, c := newTestHandler(false)
	pin := mustShare(t, h, med("med_1"))
	c.add(3*time.Minute - time.Second)
	if _, ok := h.Store.Claim(pin); !ok {
		t.Fatal("claim just before expiry failed")
	}

	pin = mustShare(t, h, med("med_1"))
	c.add(3 * time.Minute)
	if w := do(h.Claim, "2.2.2.2", claimBody(pin)); w.Code != 404 {
		t.Fatalf("expired claim: status %d, want 404", w.Code)
	}
}

func TestJanitorDropsExpired(t *testing.T) {
	h, c := newTestHandler(false)
	mustShare(t, h, med("med_1"))
	c.add(4 * time.Minute)
	h.Store.mu.Lock()
	h.Store.sweepLocked(c.now())
	h.Store.mu.Unlock()
	if h.Store.Len() != 0 {
		t.Fatal("expired entry not swept")
	}
}

func TestCollisionAndFull(t *testing.T) {
	h, _ := newTestHandler(false)
	pins := []string{"0000", "1234", "1234", "5678"}
	h.Store.newPIN = func() (string, error) {
		p := pins[0]
		pins = pins[1:]
		return p, nil
	}
	if p := mustShare(t, h, med("a")); p != "1234" {
		t.Fatalf("first pin %s, want 1234 (0000 is reserved)", p)
	}
	if p := mustShare(t, h, med("b")); p != "5678" {
		t.Fatalf("second pin %s, want 5678 (1234 is taken)", p)
	}

	h.Store.newPIN = func() (string, error) { return "1234", nil }
	if w := do(h.Share, "1.1.1.1", shareBody(med("c"))); w.Code != 503 {
		t.Fatalf("endless collisions: status %d, want 503", w.Code)
	}

	h2, _ := newTestHandler(false)
	for i := range MaxActive {
		h2.Store.entries[fmt.Sprintf("x%d", i)] = entry{expires: time.Unix(1_900_000_000, 0)}
	}
	if w := do(h2.Share, "1.1.1.1", shareBody(med("d"))); w.Code != 503 {
		t.Fatalf("full store: status %d, want 503", w.Code)
	}
}

func TestClaimFailureLimitPerIP(t *testing.T) {
	h, c := newTestHandler(false)
	pin := mustShare(t, h, med("med_1"))
	wrong := "9999"
	if pin == wrong {
		wrong = "9998"
	}
	for i := range IPFailures {
		if w := do(h.Claim, "6.6.6.6", claimBody(wrong)); w.Code != 404 {
			t.Fatalf("attempt %d: status %d", i, w.Code)
		}
	}
	// Blocked even for the right PIN, so guessing cannot continue.
	w := do(h.Claim, "6.6.6.6", claimBody(pin))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("after %d failures: status %d, Retry-After %q", IPFailures, w.Code, w.Header().Get("Retry-After"))
	}
	if w := do(h.Claim, "7.7.7.7", claimBody(pin)); w.Code != 200 {
		t.Fatalf("other IP: status %d", w.Code)
	}
	c.add(IPWindow)
	if w := do(h.Claim, "6.6.6.6", claimBody(wrong)); w.Code != 404 {
		t.Fatalf("after window: status %d", w.Code)
	}
}

func TestClaimFailureLimitGlobal(t *testing.T) {
	h, c := newTestHandler(false)
	for i := range GlobalFailures {
		ip := fmt.Sprintf("10.0.%d.%d", i/250, i%250)
		if w := do(h.Claim, ip, claimBody("4321")); w.Code != 404 {
			t.Fatalf("attempt %d: status %d", i, w.Code)
		}
	}
	if w := do(h.Claim, "10.9.9.9", claimBody("4321")); w.Code != 429 {
		t.Fatalf("fresh IP after global limit: status %d, want 429", w.Code)
	}
	c.add(GlobalWindow)
	if w := do(h.Claim, "10.9.9.9", claimBody("4321")); w.Code != 404 {
		t.Fatalf("after global window: status %d", w.Code)
	}
}

func TestShareValidation(t *testing.T) {
	h, _ := newTestHandler(false)
	long := strings.Repeat("x", MaxStringLen+1)
	many := make([]model.SharedMedication, MaxItems+1)
	for i := range many {
		many[i] = med(fmt.Sprint(i))
	}
	bad := func(f func(m *model.SharedMedication)) string {
		m := med("med_1")
		f(&m)
		return shareBody(m)
	}
	tests := []struct {
		name, body string
		want       int
	}{
		{"ok", shareBody(med("med_1")), 201},
		{"unknown field", `{"medications":[{"id":"a","kind":"drug","name":"x","source":"box","uncertain_fields":["name"]}]}`, 400},
		{"unknown top-level field", `{"medications":[],"patient":"x"}`, 400},
		{"conflicts are not shared", `{"medications":[{"id":"a","kind":"drug","name":"x","source":"box","conflicts":[]}]}`, 400},
		{"bad kind", bad(func(m *model.SharedMedication) { m.Kind = "pill" }), 400},
		{"bad source", bad(func(m *model.SharedMedication) { m.Source = "regimen" }), 400},
		{"bad sources", bad(func(m *model.SharedMedication) { m.Sources = []string{"box", "web"} }), 400},
		{"bad note_style", bad(func(m *model.SharedMedication) { s := "typed"; m.NoteStyle = &s }), 400},
		{"long string", bad(func(m *model.SharedMedication) { m.Strength = &long }), 400},
		{"long name", bad(func(m *model.SharedMedication) { m.Name = long }), 400},
		{"missing name", bad(func(m *model.SharedMedication) { m.Name = "" }), 400},
		{"empty list", `{"medications":[]}`, 400},
		{"not json", `hello`, 400},
		{"too many", shareBody(many...), 413},
		{"body too large", `{"medications":[` + strings.Repeat(" ", MaxBodyBytes) + `]}`, 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := do(h.Share, "1.1.1.1", tt.body); w.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body)
			}
		})
	}
}

func TestClaimValidation(t *testing.T) {
	h, _ := newTestHandler(false)
	for _, body := range []string{`{"pin":"123"}`, `{"pin":"12345"}`, `{"pin":"12a4"}`, `{"pin":1234}`, `{"pin":"1234","x":1}`} {
		if w := do(h.Claim, "1.1.1.1", body); w.Code != 400 {
			t.Errorf("%s: status %d, want 400", body, w.Code)
		}
	}
	// Malformed requests do not eat into the failure budget.
	if blocked, _ := h.Limiter.Blocked("1.1.1.1"); blocked {
		t.Fatal("malformed requests counted as failures")
	}
}

func TestMockPIN(t *testing.T) {
	h, _ := newTestHandler(true)
	for range 3 {
		w := do(h.Claim, "1.1.1.1", claimBody(MockPIN))
		var resp claimResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		if w.Code != 200 || len(resp.Medications) == 0 {
			t.Fatalf("mock claim: status %d, %d items", w.Code, len(resp.Medications))
		}
	}
	for _, m := range fixtures.Shared() {
		if err := validate(m); err != nil {
			t.Errorf("fixture %s is not shareable: %v", m.ID, err)
		}
	}

	h, _ = newTestHandler(false)
	if w := do(h.Claim, "1.1.1.1", claimBody(MockPIN)); w.Code != 404 {
		t.Fatalf("0000 outside mock mode: status %d, want 404", w.Code)
	}
}

func TestConcurrentShareAndClaim(t *testing.T) {
	h, _ := newTestHandler(false)
	// Each race has a loser whose claim fails; keep the limits out of the way.
	h.Limiter.ipMax, h.Limiter.globalMax = 1<<30, 1<<30
	var wg sync.WaitGroup
	pins := make(chan string, 200)
	for i := range 200 {
		wg.Go(func() {
			w := do(h.Share, "1.1.1.1", shareBody(med(fmt.Sprint(i))))
			var resp shareResponse
			json.Unmarshal(w.Body.Bytes(), &resp)
			pins <- resp.PIN
		})
	}
	wg.Wait()
	close(pins)

	seen := map[string]bool{}
	var claimed sync.Map
	for pin := range pins {
		if seen[pin] {
			t.Fatalf("PIN %s issued twice", pin)
		}
		seen[pin] = true
		// Two claimers race for every PIN; exactly one may win.
		for k := range 2 {
			wg.Go(func() {
				ip := fmt.Sprintf("10.%d.%s.1", k, pin[:2])
				if w := do(h.Claim, ip, claimBody(pin)); w.Code == 200 {
					if _, dup := claimed.LoadOrStore(pin, true); dup {
						t.Errorf("PIN %s claimed twice", pin)
					}
				}
			})
		}
	}
	wg.Wait()
	if len(seen) != 200 || h.Store.Len() != 0 {
		t.Fatalf("%d unique pins, %d left in store", len(seen), h.Store.Len())
	}
}
