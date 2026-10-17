package share

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"pillgaze/internal/httpx"
)

// Handoff: the doctor (/doc) or a helper sends a list back to the patient's
// phone. The frontend packs it into the same string it used to put in links
// (familyShare.js) and the patient types the PIN into the installed app.
// The backend only holds the string, it never unpacks it.
const (
	MaxHandoffLen       = 12000
	HandoffMaxBodyBytes = 16 << 10
)

// Same shape as encodeShare in the frontend: "<version><z|j>.<base64url>".
var handoffData = regexp.MustCompile(`^\d+[zj]\.[A-Za-z0-9_-]+$`)

type HandoffHandler struct {
	Store   *Store[string]
	Limiter *FailureLimiter
}

type handoffRequest struct {
	Data string `json:"data"`
}

type handoffResponse struct {
	Data string `json:"data"`
}

// Create handles POST /api/handoff.
func (h *HandoffHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req handoffRequest
	if status, err := httpx.DecodeJSON(r, &req); err != nil {
		code := httpx.CodeInvalidInput
		if status == http.StatusRequestEntityTooLarge {
			code = httpx.CodeTooLarge
		}
		httpx.WriteError(w, status, code, err.Error())
		return
	}
	if len(req.Data) > MaxHandoffLen {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "data is too long")
		return
	}
	if !handoffData.MatchString(req.Data) {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, "data has an unknown format")
		return
	}

	pin, expires, err := h.Store.Create(req.Data)
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

// Claim handles POST /api/handoff/claim.
func (h *HandoffHandler) Claim(w http.ResponseWriter, r *http.Request) {
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

	data, ok := h.Store.Claim(req.PIN)
	if !ok {
		h.Limiter.Fail(ip)
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "PIN not found or expired")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, handoffResponse{Data: data})
}
