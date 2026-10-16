package interactions

import (
	"fmt"
	"net/http"
	"unicode/utf8"

	"pillgaze/internal/httpx"
	"pillgaze/internal/model"
)

const (
	MaxItems     = 50
	MaxStringLen = 300
	MaxBodyBytes = 256 << 10
)

type Handler struct {
	DB *DB
}

// The request takes medications as analyze returns them or as claim hands
// them to the doctor, so both shapes decode.
type request struct {
	Medications []model.Medication `json:"medications"`
}

// ServeHTTP handles POST /api/interactions.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req request
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
	ids := map[string]bool{}
	meds := make([]Med, len(req.Medications))
	for i, m := range req.Medications {
		if m.ID == "" || ids[m.ID] {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, fmt.Sprintf("medications[%d]: id must be set and unique", i))
			return
		}
		ids[m.ID] = true
		for _, s := range []*string{&m.Name, m.ActiveSubstance} {
			if s != nil && utf8.RuneCountInString(*s) > MaxStringLen {
				httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, fmt.Sprintf("medications[%d]: strings are limited to %d characters", i, MaxStringLen))
				return
			}
		}
		meds[i] = Med{ID: m.ID, Name: m.Name, ActiveSubstance: m.ActiveSubstance}
	}
	httpx.WriteJSON(w, http.StatusOK, h.DB.Check(meds))
}
