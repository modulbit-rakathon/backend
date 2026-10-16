// Package catalog holds the organisers' list of medicinal products and
// matches names read from photos against it.
package catalog

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"pillgaze/internal/interactions"
)

//go:embed katalog.csv
var katalogCSV string

// Product is one catalogue row: a brand at one strength.
type Product struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Strength        string `json:"strength"`
	Form            string `json:"form"`
	ActiveSubstance string `json:"active_substance"`
	Supplement      bool   `json:"supplement,omitempty"`

	family     string   // name without strength: "AGEN 5" and "AGEN 10" -> "agen"
	substances []string // substance keys, sorted
	numbers    []string // numbers in the strength: "2,5 mg / 25 mg" -> [2.5 25]
}

type Catalog struct {
	Products []*Product
	families map[string][]*Product
	// bySubstance indexes single-substance products by substance key.
	bySubstance map[string][]*Product
}

var supplementRe = regexp.MustCompile(`(?i)\s*\(doplněk stravy\)`)

// Load parses the embedded catalogue, dropping exact duplicate rows.
func Load() (*Catalog, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(katalogCSV, "\uFEFF")))
	r.FieldsPerRecord = 4
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	c := &Catalog{families: map[string][]*Product{}, bySubstance: map[string][]*Product{}}
	seen := map[string]bool{}
	for i, row := range rows {
		for j := range row {
			row[j] = strings.TrimSpace(row[j])
		}
		dup := strings.Join(row, "\x00")
		if seen[dup] {
			continue
		}
		seen[dup] = true

		name := row[0]
		p := &Product{
			ID:              fmt.Sprintf("cat_%03d", i+1),
			Name:            strings.TrimSpace(supplementRe.ReplaceAllString(name, "")),
			Supplement:      supplementRe.MatchString(name),
			Strength:        row[1],
			ActiveSubstance: row[2],
			Form:            strings.TrimSpace(strings.SplitN(row[3], ",", 2)[0]),
		}
		p.family = familyKey(p.Name)
		// "0,5 g (500 mg)" states one strength twice.
		p.numbers = Numbers(parenRe.ReplaceAllString(p.Strength, " "))
		for _, alts := range interactions.SplitSubstances(p.ActiveSubstance) {
			p.substances = append(p.substances, interactions.SubstanceKey(alts[len(alts)-1]))
		}
		slices.Sort(p.substances)
		c.Products = append(c.Products, p)
		c.families[p.family] = append(c.families[p.family], p)
		if len(p.substances) == 1 {
			for _, alts := range interactions.SplitSubstances(p.ActiveSubstance) {
				for _, a := range alts {
					k := interactions.SubstanceKey(a)
					if !slices.Contains(c.bySubstance[k], p) {
						c.bySubstance[k] = append(c.bySubstance[k], p)
					}
				}
			}
		}
	}
	return c, nil
}

var (
	parenRe  = regexp.MustCompile(`\([^()]*\)`)
	wordRe   = regexp.MustCompile(`[a-z]+`)
	numberRe = regexp.MustCompile(`\d+(?:[ ]\d{3})*(?:[,.]\d+)?`)
	unitWord = map[string]bool{"mg": true, "g": true, "ug": true, "mcg": true, "ml": true, "iu": true, "tbl": true, "mikrogramu": true, "h": true, "u": true}
)

// familyKey drops brackets, numbers and units: "HELICID 20 ZENTIVA" ->
// "helicid zentiva", "Prestarium Neo 5 mg" -> "prestarium neo".
func familyKey(name string) string {
	s := interactions.Fold(parenRe.ReplaceAllString(name, " "))
	s = strings.NewReplacer("®", " ", "™", " ", "µ", "u", "μ", "u").Replace(s)
	var words []string
	for _, w := range wordRe.FindAllString(s, -1) {
		if !unitWord[w] {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

// Numbers returns the numbers in a strength as canonical strings:
// "2,5 mg / 25 mg" -> ["2.5", "25"], "25 000 U" -> ["25000"].
func Numbers(s string) []string {
	var out []string
	for _, m := range numberRe.FindAllString(s, -1) {
		m = strings.ReplaceAll(strings.ReplaceAll(m, " ", ""), ",", ".")
		if f, err := strconv.ParseFloat(m, 64); err == nil {
			out = append(out, strconv.FormatFloat(f, 'f', -1, 64))
		}
	}
	return out
}

// FamilyKey is the brand key of a product name, strength and units dropped.
func FamilyKey(name string) string { return familyKey(name) }

// StrengthNumbers is Numbers without bracketed repeats: "0,5 g (500 mg)" -> ["0.5"].
func StrengthNumbers(s string) []string { return Numbers(parenRe.ReplaceAllString(s, " ")) }
