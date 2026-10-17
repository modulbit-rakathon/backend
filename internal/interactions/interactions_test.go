package interactions

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var (
	dbOnce sync.Once
	testDB *DB
)

func loadDB(t *testing.T) *DB {
	t.Helper()
	dbOnce.Do(func() {
		db, err := Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		testDB = db
	})
	return testDB
}

func TestResolve(t *testing.T) {
	db := loadDB(t)
	tests := map[string][]string{
		"Amlodipin (amlodipinum)": {"Amlodipine"},
		"amlodipinum":             {"Amlodipine"},
		"perindoprilum erbuminum + amlodipinum + indapamidum": {"Perindopril", "Amlodipine", "Indapamide"},
		"Bisoprolol-fumarát / amlodipin":                      {"Bisoprolol", "Amlodipine"},
		"metformini hydrochloridum":                           {"Metformin"},
		"Sodná sůl warfarinu":                                 {"Warfarin"},
		"warfarinum natricum":                                 {"Warfarin"},
		"Kyselina acetylsalicylová":                           {"Acetylsalicylic acid"},
		"acidum acetylsalicylicum":                            {"Acetylsalicylic acid"},
		"Kvetiapin (quetiapinum ve formě fumarátu)":           {"Quetiapine"},
		"Alopurinol":   {"Allopurinol"},
		"tacrolimusum": {"Tacrolimus"},
		"paracetamol":  {"Acetaminophen"},
		"Draselná sůl losartanu (losartanum kalicum)":             {"Losartan"},
		"Chlorid draselný (kalii chloridum)":                      {"Potassium chloride"},
		"Dihydrát kodein-fosfátu (codeini phosphas hemihydricus)": {"Codeine"},
		"Trospium-chlorid (trospii chloridum)":                    {"Trospium"},
	}
	for in, want := range tests {
		parts := SplitSubstances(in)
		if len(parts) != len(want) {
			t.Errorf("%q: split into %v, want %d parts", in, parts, len(want))
			continue
		}
		for i, alts := range parts {
			got, ok := db.Resolve(alts)
			if !ok || got != want[i] {
				t.Errorf("%q part %d %v: got %q, %v; want %q", in, i, alts, got, ok, want[i])
			}
		}
	}
}

// A wrong match is worse than none: these must stay unresolved.
func TestResolveNoFalseMatch(t *testing.T) {
	db := loadDB(t)
	for _, in := range []string{
		"Dihydrát magnesium-laktátu (magnesii lactas dihydricus)", // not "Calcium lactate"
		"Kandesartan-cilexetil (candesartanum cilexetilum)",       // not in DDInter
		"Metamizol",
		"Prestarium Neo", // a brand, not a substance
		"kalium",
		"",
	} {
		for _, alts := range SplitSubstances(in) {
			if got, ok := db.Resolve(alts); ok {
				t.Errorf("%q resolved to %q, want no match", in, got)
			}
		}
	}
}

func s(v string) *string { return &v }

func TestCheck(t *testing.T) {
	db := loadDB(t)
	res := db.Check([]Med{
		{ID: "m1", Name: "Warfarin Orion 5 mg", ActiveSubstance: s("warfarinum natricum")},
		{ID: "m2", Name: "Ibuprofen 400", ActiveSubstance: s("Ibuprofen")},
		{ID: "m3", Name: "Prestarium Neo Combi", ActiveSubstance: s("perindoprilum argininum + indapamidum")},
		{ID: "m4", Name: "Atacand", ActiveSubstance: s("candesartanum cilexetilum")},
		{ID: "m5", Name: "Neznámý lék"},
		{ID: "m6", Name: "Combo", ActiveSubstance: s("amlodipinum + candesartanum")},
	})

	if res.CheckedAgainst.Name != "DDInter" || res.Note == "" {
		t.Errorf("source/note missing: %+v", res.CheckedAgainst)
	}
	var warfIbu *Pair
	for i, p := range res.Pairs {
		if p.A == p.B {
			t.Errorf("pair within one medication: %+v", p)
		}
		if (p.SubstanceA == "Ibuprofen" && p.SubstanceB == "Warfarin") || (p.SubstanceA == "Warfarin" && p.SubstanceB == "Ibuprofen") {
			warfIbu = &res.Pairs[i]
		}
	}
	if warfIbu == nil || warfIbu.Level != LevelMajor {
		t.Fatalf("warfarin + ibuprofen: got %+v, want major; pairs %+v", warfIbu, res.Pairs)
	}
	if res.Pairs[0].Level != LevelMajor {
		t.Errorf("pairs not sorted by severity: first is %s", res.Pairs[0].Level)
	}

	reasons := map[string]Unchecked{}
	for _, u := range res.Unchecked {
		reasons[u.ID] = u
	}
	if reasons["m4"].Reason != ReasonNotInSource {
		t.Errorf("m4: %+v", reasons["m4"])
	}
	if reasons["m5"].Reason != ReasonNoSubstance {
		t.Errorf("m5: %+v", reasons["m5"])
	}
	if u := reasons["m6"]; u.Reason != ReasonPartlyInSrc || len(u.Substances) != 1 || u.Substances[0] != "candesartanum" {
		t.Errorf("m6: %+v", u)
	}
	if _, ok := reasons["m1"]; ok {
		t.Errorf("m1 should be fully checked")
	}
}

func TestCheckEmptyIsNotAllClear(t *testing.T) {
	db := loadDB(t)
	res := db.Check([]Med{{ID: "a", Name: "Prestarium", ActiveSubstance: s("Metamizol")}})
	b, _ := json.Marshal(res)
	// Empty pairs still carry the source, the unchecked list and the note.
	for _, want := range []string{`"pairs":[]`, `"checked_against":{"name":"DDInter"`, `"unchecked":[{"id":"a"`, `"note":"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

func TestHandler(t *testing.T) {
	h := &Handler{DB: loadDB(t)}
	do := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/interactions", strings.NewReader(body)))
		return w
	}

	// Shapes from both analyze (uncertain_fields) and claim (sources) decode.
	w := do(`{"medications":[
		{"id":"m1","kind":"drug","name":"Warfarin","active_substance":"warfarinum","source":"box","uncertain_fields":[]},
		{"id":"m2","kind":"drug","name":"Ibalgin","active_substance":"Ibuprofen","source":"note","sources":["note","box"]}]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"level":"major"`) {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}

	many := `{"medications":[` + strings.Repeat(`{"id":"x","name":"a"},`, 50) + `{"id":"y","name":"b"}]}`
	for body, want := range map[string]int{
		`{"medications":[]}`: 400,
		`{"medications":[{"id":"a","name":"x"},{"id":"a","name":"y"}]}`: 400,
		`{"medications":[{"name":"x"}]}`:                                400,
		`{"medications":[{"id":"a","name":"x","patient":"y"}]}`:         400,
		`{"meds":[]}`: 400,
		many:          413,
	} {
		if w := do(body); w.Code != want {
			t.Errorf("%.60s: status %d, want %d", body, w.Code, want)
		}
	}
}

// Major and moderate pairs of catalogue substances carry DDInter's
// description; other pairs have none.
func TestDescription(t *testing.T) {
	db := loadDB(t)
	warfarin, ibuprofen := "Warfarin", "Ibuprofen"
	res := db.Check([]Med{
		{ID: "w", Name: "Warfarin", ActiveSubstance: &warfarin},
		{ID: "i", Name: "Ibalgin", ActiveSubstance: &ibuprofen},
	})
	if len(res.Pairs) != 1 {
		t.Fatalf("pairs: %+v", res.Pairs)
	}
	p := res.Pairs[0]
	if !strings.Contains(p.Description, "bleeding") || p.Management == "" {
		t.Errorf("description %q, management %q", p.Description, p.Management)
	}
	if !strings.HasPrefix(p.URL, "https://ddinter.scbdd.com/ddinter/interact/") {
		t.Errorf("url %q", p.URL)
	}
	for k := range db.texts {
		if l := db.pairs[k]; l != LevelMajor && l != LevelModerate {
			t.Errorf("%v has a text but level %q", k, l)
		}
	}
}
