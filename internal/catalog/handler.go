package catalog

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"pillgaze/internal/httpx"
	"pillgaze/internal/model"
)

const (
	MinQueryLen = 2
	MaxQueryLen = 100
	MaxResults  = 20
)

// SearchHandler handles GET /api/catalog?q=...
type SearchHandler struct {
	Catalog *Catalog
}

func (h *SearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := utf8.RuneCountInString(q); n < MinQueryLen || n > MaxQueryLen {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidInput, "q must have 2 to 100 characters")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Products []model.CatalogProduct `json:"products"`
	}{h.Catalog.Search(q, MaxResults)})
}
