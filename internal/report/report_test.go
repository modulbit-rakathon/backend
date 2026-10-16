package report

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"pillgaze/internal/fixtures"
	"pillgaze/internal/interactions"
	"pillgaze/internal/model"
	"pillgaze/internal/regimens"
)

func s(v string) *string { return &v }

func newHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := interactions.Load()
	if err != nil {
		t.Fatal(err)
	}
	table, err := regimens.Load()
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{DB: db, Table: table, Now: func() time.Time { return time.Date(2026, 10, 16, 19, 30, 0, 0, time.UTC) }}
}

func testMeds() []model.SharedMedication {
	return append(fixtures.Shared(),
		model.SharedMedication{ID: "med_x1", Kind: model.KindDrug, Name: "Ibalgin 400", ActiveSubstance: s("ibuprofenum"), Strength: s("400 mg"), Source: model.SourceBox, EditedFields: []string{"strength", "dosage"}},
		model.SharedMedication{ID: "med_x2", Kind: model.KindRegimen, Name: "FOLFOXX", Source: model.SourceNote},
		model.SharedMedication{ID: "med_x3", Kind: model.KindDrug, Name: "Neznámý lék 😀 s velmi dlouhým názvem, který se musí zalomit do více řádků", Source: model.SourceManual},
	)
}

func post(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/report/pdf", bytes.NewReader(b)))
	return w
}

func TestRun(t *testing.T) {
	h := newHandler(t)
	c := Run(testMeds(), h.DB, h.Table)

	if r := c.Regimens["med_reg001"]; r == nil || len(r.Drugs) == 0 {
		t.Fatalf("FOLFOX not resolved: %+v", r)
	}
	if r, ok := c.Regimens["med_x2"]; !ok || r != nil {
		t.Fatalf("FOLFOXX must be an unknown regimen, got %+v", r)
	}
	var warfarinIbuprofen bool
	for _, f := range c.Findings {
		if f.A == f.B || strings.Contains(f.A, partSep) || strings.Contains(f.B, partSep) {
			t.Fatalf("finding not mapped to rows: %+v", f)
		}
		pair := rowKey(f.A, f.B)
		if pair == rowKey("med_box003", "med_note003") || pair == rowKey("med_box003", "med_x1") {
			warfarinIbuprofen = true
		}
	}
	if !warfarinIbuprofen {
		t.Fatalf("warfarin × ibuprofen missing: %+v", c.Findings)
	}
	reasons := map[string]string{}
	for _, u := range c.Unchecked {
		reasons[u.ID] = u.Reason
	}
	if reasons["med_x2"] != reasonUnknownRegimen || reasons["med_x3"] != interactions.ReasonNoSubstance {
		t.Fatalf("unchecked: %+v", c.Unchecked)
	}
}

func TestHandlerPDF(t *testing.T) {
	h := newHandler(t)
	for _, lang := range []string{"", "cs", "en", "uk"} {
		w := post(t, h, map[string]any{"medications": testMeds(), "lang": lang})
		if w.Code != http.StatusOK {
			t.Fatalf("lang %q: %d %s", lang, w.Code, w.Body)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
			t.Fatalf("content type %q", ct)
		}
		if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="pillgaze-leky-2026-10-16-2130.pdf"` {
			t.Fatalf("content disposition %q", cd)
		}
		if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
			t.Fatal("not a PDF")
		}
		if dir := os.Getenv("REPORT_PDF_DIR"); dir != "" && lang != "" {
			os.WriteFile(dir+"/report-"+lang+".pdf", w.Body.Bytes(), 0o644)
		}
	}
}

func TestHandlerRejects(t *testing.T) {
	h := newHandler(t)
	dup := fixtures.Shared()
	dup[1].ID = dup[0].ID
	cases := map[string]any{
		"empty":        map[string]any{"medications": []any{}},
		"bad lang":     map[string]any{"medications": fixtures.Shared(), "lang": "de"},
		"unknown kind": map[string]any{"medications": []map[string]any{{"id": "a", "kind": "pill", "name": "x", "source": "box"}}},
		"extra field":  map[string]any{"medications": fixtures.Shared(), "patient": "Jan"},
		"duplicate id": map[string]any{"medications": dup},
	}
	for name, body := range cases {
		if w := post(t, h, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d", name, w.Code)
		}
	}
}
