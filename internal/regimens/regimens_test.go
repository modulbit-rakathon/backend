package regimens

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"pillgaze/internal/interactions"
)

func load(t *testing.T) *Table {
	t.Helper()
	tab, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return tab
}

func TestLookup(t *testing.T) {
	tab := load(t)
	tests := map[string][]string{
		"FOLFOX":   {"Fluorouracil", "Leucovorin", "Oxaliplatin"},
		"mFOLFOX6": {"Fluorouracil", "Leucovorin", "Oxaliplatin"},
		"folfox 4": {"Fluorouracil", "Leucovorin", "Oxaliplatin"},
		"R-CHOP":   {"Rituximab", "Cyclophosphamide", "Doxorubicin", "Vincristine", "Prednisone"},
		"r chop":   {"Rituximab", "Cyclophosphamide", "Doxorubicin", "Vincristine", "Prednisone"},
		"XELOX":    {"Capecitabine", "Oxaliplatin"},
		"CyBorD":   {"Bortezomib", "Cyclophosphamide", "Dexamethasone"},
	}
	for in, want := range tests {
		got := tab.Lookup(in)
		if len(got) != 1 || !slices.Equal(got[0].Drugs, want) {
			t.Errorf("%q: got %+v, want one regimen with %v", in, got, want)
		}
	}
	for _, in := range []string{"EC", "TC", "AC-T", "7+3", "THP"} {
		if n := len(tab.Lookup(in)); n != 2 {
			t.Errorf("%q: got %d variants, want 2", in, n)
		}
	}
	// Similar spellings are never guessed.
	for _, in := range []string{"FOLFOXX", "FOLFO", "RCHOPP", "warfarin", "ABC"} {
		if got := tab.Lookup(in); got != nil {
			t.Errorf("%q: got %+v, want nothing", in, got)
		}
	}
}

// Every drug must be found in DDInter, so a resolved regimen can go straight
// to /api/interactions.
func TestDrugsInDDInter(t *testing.T) {
	tab := load(t)
	db, err := interactions.Load()
	if err != nil {
		t.Fatalf("ddinter: %v", err)
	}
	for _, regs := range tab.byKey {
		for _, r := range regs {
			for _, d := range r.Drugs {
				if n, ok := db.Resolve([]string{d}); !ok || !strings.EqualFold(n, d) {
					t.Errorf("%s: %q resolves to %q (found %v)", r.Name, d, n, ok)
				}
			}
		}
	}
}

func TestHandler(t *testing.T) {
	h := &Handler{Table: load(t)}
	tests := []struct {
		body string
		code int
	}{
		{`{"text":"FOLFOX"}`, 200},
		{`{"text":"EC"}`, 200},
		{`{"text":"NEZNAMY"}`, 404},
		{`{"text":""}`, 400},
		{`{"text":"--"}`, 400},
		{`{"text":"` + strings.Repeat("A", 101) + `"}`, 400},
		{`{"text":"FOLFOX","extra":1}`, 400},
	}
	for _, tt := range tests {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/regimens/resolve", strings.NewReader(tt.body)))
		if w.Code != tt.code {
			t.Errorf("%s: code %d, want %d: %s", tt.body, w.Code, tt.code, w.Body)
			continue
		}
		if tt.code == 200 {
			var res Result
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || len(res.Regimens) == 0 || res.Source.Name == "" || res.Note == "" {
				t.Errorf("%s: bad body %s", tt.body, w.Body)
			}
		}
	}
}
