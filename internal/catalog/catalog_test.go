package catalog

import (
	"sync"
	"testing"

	"pillgaze/internal/model"
)

var (
	once sync.Once
	cat  *Catalog
)

func load(t *testing.T) *Catalog {
	t.Helper()
	once.Do(func() {
		c, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		cat = c
	})
	return cat
}

func sp(s string) *string { return &s }

func TestLoad(t *testing.T) {
	c := load(t)
	// 309 rows, 6 exact duplicates (LOKREN, LAMICTAL, MOXOSTAD, PROSTAMOL,
	// ZEXITOR, XARELTO 20 mg).
	if len(c.Products) != 303 {
		t.Errorf("products = %d, want 303", len(c.Products))
	}
	for fam, n := range map[string]int{"agen": 2, "lozap": 3, "furorese": 2, "betaloc zok": 3, "helicid zentiva": 1} {
		if got := len(c.families[fam]); got != n {
			t.Errorf("family %q: %d variants, want %d", fam, got, n)
		}
	}
	for _, p := range c.Products {
		if p.Name == "PYRIDOXIN GENERICA" && !p.Supplement {
			t.Error("supplement flag not set")
		}
		if p.Name == "MAGNESII LACTICI 0,5 TBL. MEDICAMENTA" && len(p.numbers) != 1 {
			t.Errorf("bracketed strength repeat counted: %v", p.numbers)
		}
	}
}

func TestMatch(t *testing.T) {
	c := load(t)
	tests := []struct {
		name     string
		strength *string
		unsure   bool
		status   string
		want     string // product (matched) or first candidate, "name strength"
	}{
		{"Prestarium Neo 5 mg", nil, false, StatusMatched, "PRESTARIUM NEO 5 mg"},
		{"AGEN 5", nil, false, StatusMatched, "AGEN 5 5 mg"},
		{"Agen", sp("10 mg"), false, StatusMatched, "AGEN 10 10 mg"},
		{"Agen", nil, false, StatusNeedsConfirmation, ""}, // which strength?
		{"Helicid", sp("20 mg"), false, StatusMatched, "HELICID 20 ZENTIVA 20 mg"},
		{"Hydrochlorothiazid", sp("25 mg"), false, StatusMatched, "HYDROCHLOROTHIAZID LÉČIVA 25 mg"},
		{"Magnesii lactici Medicamenta", sp("0,5 g"), false, StatusMatched, "MAGNESII LACTICI 0,5 TBL. MEDICAMENTA 0,5 g (500 mg)"},
		{"Siofor® 850 mg", sp("850 mg"), false, StatusMatched, "SIOFOR 850 mg"},
		{"BISOPROLOL PMCS (Concor)", sp("5 mg"), false, StatusMatched, "BISOPROLOL PMCS 5 mg"},
		{"Tramal retard", sp("100 mg"), false, StatusMatched, "TRAMAL RETARD 100 mg"},
		// A different product behind a prefix is never sure.
		{"Concor", sp("5 mg"), false, StatusNeedsConfirmation, "CONCOR COMBI 5 mg / 10 mg"},
		{"Tramal", sp("100 mg"), false, StatusNeedsConfirmation, "TRAMAL RETARD 100 mg"},
		// A read name the model was unsure of is never sure.
		{"Pantomyl", sp("40 mg"), true, StatusNeedsConfirmation, "PANTOMYL 40 mg"},
		{"Pantomil", sp("40mg"), true, StatusNeedsConfirmation, "PANTOMYL 40 mg"},
		// No strength read: candidates only.
		{"Zaldiar", nil, false, StatusNeedsConfirmation, "ZALDIAR 37,5 mg / 325 mg"},
		// Generic names and misspellings point to products, never sure.
		{"Gabapentin", sp("300 mg"), false, StatusNeedsConfirmation, "NEURONTIN 300 mg"},
		{"Ibuprofen 400", sp("400 mg"), false, StatusNeedsConfirmation, "BRUFEN 400 mg"},
		{"SPIRINOLAKTON", sp("25 mg"), true, StatusNeedsConfirmation, "VEROSPIRON 25 mg"},
		{"Tritace Piramil", sp("5 mg"), false, StatusNeedsConfirmation, "TRITACE 5 mg"},
		{"Atevhon", sp("8mg"), true, StatusNotFound, ""},
		{"Myrbetriq", sp("25 mg"), true, StatusNotFound, ""},
	}
	for _, tt := range tests {
		m := c.Match(tt.name, tt.strength, tt.unsure)
		if m.Status != tt.status {
			t.Errorf("%q: status %s, want %s (%+v)", tt.name, m.Status, tt.status, m)
			continue
		}
		var got string
		switch {
		case m.Product != nil:
			got = m.Product.Name + " " + m.Product.Strength
		case len(m.Candidates) > 0:
			got = m.Candidates[0].Name + " " + m.Candidates[0].Strength
		}
		if tt.want != "" && got != tt.want {
			t.Errorf("%q: got %q, want %q (%+v)", tt.name, got, tt.want, m.Candidates)
		}
		if m.Status == StatusMatched && len(m.Candidates) > 0 {
			t.Errorf("%q: matched with candidates", tt.name)
		}
		if m.Status == StatusNeedsConfirmation && len(m.Candidates) > maxCandidates {
			t.Errorf("%q: %d candidates", tt.name, len(m.Candidates))
		}
	}
}

func TestSearch(t *testing.T) {
	c := load(t)
	for q, want := range map[string]string{"agen": "AGEN 10", "pantom": "PANTOMYL", "amlodipin": "", "warfarin": "WARFARIN ORION", "xarelto": "XARELTO"} {
		res := c.Search(q, 20)
		if len(res) == 0 {
			t.Errorf("%q: no results", q)
			continue
		}
		if want != "" && res[0].Name != want {
			t.Errorf("%q: first %q, want %q", q, res[0].Name, want)
		}
	}
	if res := c.Search("amlodipin", 20); len(res) < 5 {
		t.Errorf("substance search found %d products", len(res))
	}
	var _ []model.CatalogProduct = c.Search("x", 1)
}
