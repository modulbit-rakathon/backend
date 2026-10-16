package regimens

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"pillgaze/internal/httpx"
)

const (
	MaxTextLen   = 100
	MaxBodyBytes = 1 << 10
)

type Handler struct {
	Table *Table
}

type request struct {
	Text string `json:"text"`
}

type Result struct {
	Query    string    `json:"query"`
	Regimens []Regimen `json:"regimens"`
	Source   Source    `json:"source"`
	Note     string    `json:"note"`
}

const note = "Složení režimu je podle zkratky z tabulky, ne z dokumentace pacienta. " +
	"Jsou uvedena jen protinádorová léčiva, bez podpůrné léčby a dávkování. " +
	"Víc variant znamená, že zkratka není jednoznačná."

// ServeHTTP handles POST /api/regimens/resolve.
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
	text := strings.TrimSpace(req.Text)
	if text == "" || utf8.RuneCountInString(text) > MaxTextLen || Key(text) == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, "text must have 1 to 100 characters and contain a letter or digit")
		return
	}
	regs := h.Table.Lookup(text)
	if len(regs) == 0 {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "regimen not in table")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, Result{Query: text, Regimens: regs, Source: TableSource, Note: note})
}
