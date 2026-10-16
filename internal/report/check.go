// Package report renders the doctor's medication report as a PDF: the
// shared list, the interaction check and the sources it was checked against.
// Nothing is stored; the PDF is built in memory and streamed back.
package report

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"pillgaze/internal/interactions"
	"pillgaze/internal/model"
	"pillgaze/internal/regimens"
)

// Severity ranks, worst last. "same" is a duplicate active substance,
// which DDInter does not report.
var levelRank = map[string]int{"same": 1, "unknown": 2, "minor": 3, "moderate": 4, "major": 5}

const reasonUnknownRegimen = "unknown_regimen"

// partSep joins a regimen's id and a drug index into a helper id, so
// interaction pairs can be mapped back to the table row.
const partSep = "#"

// Finding is every interaction between two rows of the table, at the level
// of the worst substance pair.
type Finding struct {
	A, B  string // medication ids
	Level string
	Pairs []SubstancePair
}

type SubstancePair struct {
	A, B  string
	Level string
}

type Duplicate struct {
	A, B      string
	Substance string
}

// Regimen is what an abbreviation resolved to. Drugs is the union over all
// variants, since an ambiguous abbreviation is checked for every one.
type Regimen struct {
	Drugs    []string
	Variants int
}

type Check struct {
	Findings       []Finding
	Duplicates     []Duplicate
	Unchecked      []interactions.Unchecked
	Regimens       map[string]*Regimen // by medication id, nil = unknown abbreviation
	CheckedAgainst interactions.Source
	UsesRegimens   bool
}

// Run checks the list the way the doctor's screen does (frontend
// doctorCheck.js): regimens are split into their drugs, pairs inside one
// regimen are skipped, and duplicates are added.
func Run(meds []model.SharedMedication, db *interactions.DB, table *regimens.Table) Check {
	c := Check{Regimens: map[string]*Regimen{}, CheckedAgainst: interactions.DDInter}

	var toCheck []interactions.Med
	for _, m := range meds {
		if m.Kind != model.KindRegimen {
			toCheck = append(toCheck, interactions.Med{ID: m.ID, Name: m.Name, ActiveSubstance: m.ActiveSubstance})
			continue
		}
		c.UsesRegimens = true
		variants := table.Lookup(m.Name)
		if len(variants) == 0 {
			c.Regimens[m.ID] = nil
			continue
		}
		r := &Regimen{Variants: len(variants)}
		for _, v := range variants {
			for _, d := range v.Drugs {
				if !slices.Contains(r.Drugs, d) {
					r.Drugs = append(r.Drugs, d)
				}
			}
		}
		c.Regimens[m.ID] = r
		for i, d := range r.Drugs {
			toCheck = append(toCheck, interactions.Med{ID: fmt.Sprintf("%s%s%d", m.ID, partSep, i), Name: d, ActiveSubstance: &d})
		}
	}

	res := db.Check(toCheck)

	byRows := map[string]*Finding{}
	var order []string
	for _, p := range res.Pairs {
		a, b := owner(p.A), owner(p.B)
		if a == b {
			// Drugs of one regimen are combined on purpose.
			continue
		}
		key := rowKey(a, b)
		f, ok := byRows[key]
		if !ok {
			f = &Finding{A: a, B: b, Level: p.Level}
			byRows[key] = f
			order = append(order, key)
		}
		f.Pairs = append(f.Pairs, SubstancePair{A: p.SubstanceA, B: p.SubstanceB, Level: p.Level})
		if levelRank[p.Level] > levelRank[f.Level] {
			f.Level = p.Level
		}
	}
	for _, k := range order {
		f := byRows[k]
		slices.SortStableFunc(f.Pairs, func(x, y SubstancePair) int { return levelRank[y.Level] - levelRank[x.Level] })
		c.Findings = append(c.Findings, *f)
	}
	slices.SortStableFunc(c.Findings, func(x, y Finding) int { return levelRank[y.Level] - levelRank[x.Level] })

	for i, m1 := range meds {
		k := substanceKey(m1.ActiveSubstance)
		if k == "" {
			continue
		}
		for _, m2 := range meds[i+1:] {
			if substanceKey(m2.ActiveSubstance) == k {
				c.Duplicates = append(c.Duplicates, Duplicate{A: m1.ID, B: m2.ID, Substance: *m1.ActiveSubstance})
			}
		}
	}

	// Drugs of a regimen are reported under the regimen's row.
	for _, u := range res.Unchecked {
		id := owner(u.ID)
		subs := u.Substances
		if len(subs) == 0 && id != u.ID {
			subs = []string{u.Name}
		}
		if j := slices.IndexFunc(c.Unchecked, func(x interactions.Unchecked) bool { return x.ID == id }); j >= 0 {
			c.Unchecked[j].Substances = append(c.Unchecked[j].Substances, subs...)
			continue
		}
		if id == u.ID {
			c.Unchecked = append(c.Unchecked, u)
			continue
		}
		c.Unchecked = append(c.Unchecked, interactions.Unchecked{ID: id, Name: nameOf(meds, id), Reason: interactions.ReasonPartlyInSrc, Substances: subs})
	}
	for _, m := range meds {
		if r, ok := c.Regimens[m.ID]; ok && r == nil {
			c.Unchecked = append(c.Unchecked, interactions.Unchecked{ID: m.ID, Name: m.Name, Reason: reasonUnknownRegimen})
		}
	}
	return c
}

func owner(id string) string {
	before, _, _ := strings.Cut(id, partSep)
	return before
}

func rowKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func nameOf(meds []model.SharedMedication, id string) string {
	for _, m := range meds {
		if m.ID == id {
			return m.Name
		}
	}
	return id
}

var saltRe = regexp.MustCompile(`\(.*\)`)

// substanceKey compares active substances without case and without the
// salt in brackets that catalogue names carry.
func substanceKey(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(saltRe.ReplaceAllString(strings.ToLower(*s), ""))
}
