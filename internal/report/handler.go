package report

import (
	"fmt"
	"net/http"
	"time"

	"pillgaze/internal/httpx"
	"pillgaze/internal/interactions"
	"pillgaze/internal/model"
	"pillgaze/internal/regimens"
	"pillgaze/internal/share"
)

const MaxBodyBytes = share.MaxBodyBytes

type Handler struct {
	DB    *interactions.DB
	Table *regimens.Table
	// Now is the report time; nil means time.Now.
	Now func() time.Time
}

// The list as claim hands it to the doctor, plus the screen's language.
type request struct {
	Medications []model.SharedMedication `json:"medications"`
	Lang        string                   `json:"lang"`
}

// ServeHTTP handles POST /api/report/pdf.
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
	if req.Lang == "" {
		req.Lang = "cs"
	}
	if _, ok := langs[req.Lang]; !ok {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, "lang must be cs, en or uk")
		return
	}
	if len(req.Medications) > share.MaxItems {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, fmt.Sprintf("at most %d medications", share.MaxItems))
		return
	}
	if len(req.Medications) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, "medications must not be empty")
		return
	}
	ids := map[string]bool{}
	for i, m := range req.Medications {
		if err := share.Validate(m); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, fmt.Sprintf("medications[%d]: %v", i, err))
			return
		}
		if ids[m.ID] {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, fmt.Sprintf("medications[%d]: id must be unique", i))
			return
		}
		ids[m.ID] = true
	}

	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	at := now()
	pdf, err := Render(req.Medications, Run(req.Medications, h.DB, h.Table), req.Lang, at)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not render PDF")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pillgaze-leky-%s.pdf"`, at.In(prague).Format("2006-01-02-1504")))
	// Patient data: never keep it in a browser or proxy cache.
	w.Header().Set("Cache-Control", "no-store")
	w.Write(pdf)
}
