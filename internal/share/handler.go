package share

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"pillgaze/internal/httpx"
	"pillgaze/internal/model"
)

const (
	MaxItems     = 50
	MaxStringLen = 200
	MaxBodyBytes = 512 << 10
)

type Handler struct {
	Store   *Store
	Limiter *FailureLimiter
	// Mock makes MockPIN always return Fixture, without consuming it.
	Mock    bool
	Fixture func() []model.SharedMedication
}

type shareRequest struct {
	Medications []model.SharedMedication `json:"medications"`
}

type shareResponse struct {
	PIN       string    `json:"pin"`
	ExpiresAt time.Time `json:"expires_at"`
	ExpiresIn int       `json:"expires_in"`
}

type claimRequest struct {
	PIN string `json:"pin"`
}

type claimResponse struct {
	Medications []model.SharedMedication `json:"medications"`
}

// Share handles POST /api/share.
func (h *Handler) Share(w http.ResponseWriter, r *http.Request) {
	var req shareRequest
	if status, err := httpx.DecodeJSON(r, &req); err != nil {
		code := httpx.CodeInvalidInput
		if status == http.StatusRequestEntityTooLarge {
			code = httpx.CodeTooLarge
		}
		httpx.WriteError(w, status, code, err.Error())
		return
	}
	if len(req.Medications) > MaxItems {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, fmt.Sprintf("at most %d medications", MaxItems))
		return
	}
	if len(req.Medications) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, "medications must not be empty")
		return
	}
	for i, m := range req.Medications {
		if err := validate(m); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, fmt.Sprintf("medications[%d]: %v", i, err))
			return
		}
	}

	pin, expires, err := h.Store.Create(req.Medications)
	if errors.Is(err, ErrFull) {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "too many active shares, try again shortly")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not create PIN")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, shareResponse{
		PIN:       pin,
		ExpiresAt: expires.UTC().Truncate(time.Second),
		ExpiresIn: int(h.Store.TTL().Seconds()),
	})
}

// Claim handles POST /api/share/claim.
func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	ip := httpx.ClientIP(r)
	if blocked, wait := h.Limiter.Blocked(ip); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many wrong PINs, try again later")
		return
	}

	var req claimRequest
	if status, err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, status, httpx.CodeInvalidInput, err.Error())
		return
	}
	if !validPIN(req.PIN) {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, "pin must be 4 digits")
		return
	}

	if h.Mock && req.PIN == MockPIN {
		httpx.WriteJSON(w, http.StatusOK, claimResponse{Medications: h.Fixture()})
		return
	}
	meds, ok := h.Store.Claim(req.PIN)
	if !ok {
		h.Limiter.Fail(ip)
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "PIN not found or expired")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, claimResponse{Medications: meds})
}

func validPIN(p string) bool {
	if len(p) != 4 {
		return false
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var (
	kinds      = map[string]bool{model.KindDrug: true, model.KindRegimen: true}
	sources    = map[string]bool{model.SourceNote: true, model.SourceBox: true, model.SourceManual: true}
	noteStyles = map[string]bool{model.NoteStylePrinted: true, model.NoteStyleHandwritten: true}
)

func validate(m model.SharedMedication) error {
	if m.ID == "" || m.Name == "" {
		return errors.New("id and name are required")
	}
	if !kinds[m.Kind] {
		return errors.New("unknown kind")
	}
	if !sources[m.Source] {
		return errors.New("unknown source")
	}
	for _, s := range m.Sources {
		if !sources[s] {
			return errors.New("unknown value in sources")
		}
	}
	if len(m.Sources) > len(sources) {
		return errors.New("too many sources")
	}
	if m.NoteStyle != nil && !noteStyles[*m.NoteStyle] {
		return errors.New("unknown note_style")
	}
	for _, s := range []*string{&m.ID, &m.Name, m.ActiveSubstance, m.Strength, m.Form, m.Frequency, m.Amount} {
		if s != nil && utf8.RuneCountInString(*s) > MaxStringLen {
			return fmt.Errorf("strings are limited to %d characters", MaxStringLen)
		}
	}
	return nil
}
