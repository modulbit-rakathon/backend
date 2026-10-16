package catalog

import (
	"cmp"
	"regexp"
	"slices"
	"strings"

	"pillgaze/internal/interactions"
	"pillgaze/internal/model"
)

const (
	StatusMatched           = "matched"
	StatusNeedsConfirmation = "needs_confirmation"
	StatusNotFound          = "not_found"

	// How a candidate was found.
	ByName      = "name"      // same brand (strength aside)
	ByPrefix    = "prefix"    // brand without the manufacturer suffix
	BySubstance = "substance" // the generic substance name was written
	ByWord      = "word"      // one word of a longer name ("Tritace Piramil")
	ByFuzzy     = "fuzzy"     // similar spelling

	// MinFuzzyScore is the lowest similarity offered as a candidate.
	MinFuzzyScore = 0.6
	maxCandidates = 3
)

type candidate struct {
	p     *Product
	score float64
	by    string
	key   string // the read name's family key
}

// productWords mark a different product, not a manufacturer: "CONCOR COMBI"
// is not "Concor", "TRAMAL RETARD" is not "Tramal". A prefix match across
// one of them is never sure.
var productWords = map[string]bool{
	"combi": true, "retard": true, "neo": true, "plus": true, "forte": true, "h": true, "hct": true,
	"duo": true, "mr": true, "sr": true, "zok": true, "cr": true, "n": true, "chrono": true, "rapid": true,
	"oro": true, "tab": true, "uno": true, "prolong": true, "s": true, "melt": true, "continus": true,
	"ac": true, "mite": true, "durules": true, "en": true, "dragees": true, "f": true, "due": true,
}

func changesProduct(family, key string) bool {
	for _, w := range strings.Fields(strings.TrimPrefix(family, key)) {
		if productWords[w] {
			return true
		}
	}
	return false
}

// alternativesRe splits "BISOPROLOL PMCS (Concor)" and "Videmel /Caltrate"
// into the names written on the line.
var alternativesRe = regexp.MustCompile(`\s*[()/]\s*`)

// Match finds the catalogue product for a name read from a photo.
//
// Only a same-brand match with one fitting strength, read with certainty,
// is "matched". Everything else needs the patient to confirm, so a misread
// never turns silently into a real drug.
func (c *Catalog) Match(name string, strength *string, nameUncertain bool) *model.CatalogMatch {
	var cands []candidate
	for _, part := range alternativesRe.Split(name, -1) {
		if familyKey(part) != "" {
			cands = append(cands, c.candidates(part)...)
		}
	}

	nums := Numbers(name)
	if strength != nil {
		nums = Numbers(*strength)
	}
	// Prefer candidates whose strength fits, then by score.
	cands = dedupe(cands)
	// Same strength first, then a strength that fits ("5" in "5 mg / 5 mg"),
	// then by score.
	rank := func(cd candidate) int {
		switch {
		case len(nums) > 0 && slices.Equal(nums, cd.p.numbers):
			return 0
		case fits(nums, cd.p):
			return 1
		}
		return 2
	}
	slices.SortStableFunc(cands, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(b.score, a.score))
	})

	m := &model.CatalogMatch{Status: StatusNotFound}
	if len(cands) == 0 {
		return m
	}

	// matched: same brand, and exactly one variant of it has the read strength.
	var sure []candidate
	for _, cd := range cands {
		if cd.by == ByName || cd.by == ByPrefix {
			if cd.by == ByPrefix && changesProduct(cd.p.family, cd.key) {
				continue
			}
			// Without a read strength nothing is sure: a handwritten
			// "Zaldiar" may stand for another dose than the catalogue's.
			if len(nums) > 0 && slices.Equal(nums, cd.p.numbers) {
				sure = append(sure, cd)
			}
		}
	}
	if len(sure) == 1 && !nameUncertain && (sure[0].by == ByName || onlyFamily(cands, sure[0].p.family, nums)) {
		m.Status = StatusMatched
		m.Product = ref(sure[0].p)
		return m
	}

	m.Status = StatusNeedsConfirmation
	for _, cd := range cands[:min(len(cands), maxCandidates)] {
		m.Candidates = append(m.Candidates, model.CatalogCandidate{CatalogProduct: *ref(cd.p), Score: round(cd.score), By: cd.by})
	}
	return m
}

// candidates collects every product the name could refer to.
func (c *Catalog) candidates(name string) []candidate {
	key := familyKey(name)
	flat := strings.ReplaceAll(key, " ", "")
	var out []candidate

	for _, p := range c.families[key] {
		out = append(out, candidate{p, 1, ByName, key})
	}
	// "Helicid" for "HELICID 20 ZENTIVA", "Hydrochlorothiazid" for
	// "HYDROCHLOROTHIAZID LÉČIVA": the read name is the family's first words.
	for fam, ps := range c.families {
		if fam != key && strings.HasPrefix(fam+" ", key+" ") {
			for _, p := range ps {
				out = append(out, candidate{p, 0.9, ByPrefix, key})
			}
		}
	}
	for _, p := range c.bySubstance[interactions.SubstanceKey(name)] {
		out = append(out, candidate{p, 0.8, BySubstance, key})
	}
	if len(out) > 0 {
		return out
	}
	// Two names run together without brackets ("Febuxostat Adenuric",
	// "Amlodipin Sandoz"): try each word on its own, as candidates only.
	if words := strings.Fields(key); len(words) > 1 {
		for _, w := range words {
			if len(w) < 4 {
				continue
			}
			for fam, ps := range c.families {
				if fam == w || strings.HasPrefix(fam+" ", w+" ") {
					for _, p := range ps {
						out = append(out, candidate{p, 0.7, ByWord, key})
					}
				}
			}
			for _, p := range c.bySubstance[interactions.SubstanceKey(w)] {
				out = append(out, candidate{p, 0.7, ByWord, key})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	for fam, ps := range c.families {
		score := similarity(flat, strings.ReplaceAll(fam, " ", ""))
		// Also against the brand alone: "Pantomil" vs "pantomyl".
		if first, _, ok := strings.Cut(fam, " "); ok {
			score = max(score, similarity(flat, first)*0.95)
		}
		if score >= MinFuzzyScore {
			for _, p := range ps {
				out = append(out, candidate{p, score, ByFuzzy, key})
			}
		}
	}
	// A misspelt generic name: "SPIRINOLAKTON" for spironolakton.
	sk := interactions.SubstanceKey(name)
	for sub, ps := range c.bySubstance {
		if score := similarity(sk, sub); score >= MinFuzzyScore+0.15 {
			for _, p := range ps {
				out = append(out, candidate{p, score * 0.9, ByFuzzy, key})
			}
		}
	}
	return out
}

func dedupe(cs []candidate) []candidate {
	best := map[*Product]candidate{}
	var order []*Product
	for _, c := range cs {
		if prev, ok := best[c.p]; !ok || c.score > prev.score {
			if !ok {
				order = append(order, c.p)
			}
			best[c.p] = c
		}
	}
	out := make([]candidate, len(order))
	for i, p := range order {
		out[i] = best[p]
	}
	return out
}

// onlyFamily reports whether no other brand fitting the strength competes.
func onlyFamily(cs []candidate, family string, nums []string) bool {
	for _, c := range cs {
		if c.by != ByFuzzy && c.p.family != family && (len(nums) == 0 || fits(nums, c.p)) {
			return false
		}
	}
	return true
}

// fits reports whether the read strength numbers fit the product: equal, or
// the read numbers appear in the product's in order ("0,5 g" fits "0,5 g
// (500 mg)").
func fits(nums []string, p *Product) bool {
	if len(nums) == 0 {
		return false
	}
	i := 0
	for _, n := range p.numbers {
		if i < len(nums) && n == nums[i] {
			i++
		}
	}
	return i == len(nums)
}

func ref(p *Product) *model.CatalogProduct {
	return &model.CatalogProduct{ID: p.ID, Name: p.Name, Strength: p.Strength, Form: p.Form, ActiveSubstance: p.ActiveSubstance, Supplement: p.Supplement}
}

func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	d := levenshtein(a, b)
	return 1 - float64(d)/float64(max(len([]rune(a)), len([]rune(b))))
}

func round(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
