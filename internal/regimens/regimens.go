// Package regimens resolves oncology regimen abbreviations (FOLFOX, R-CHOP)
// to the drugs they contain, from a hand-made table checked against
// HemOnc.org (regimens.csv).
//
// Only exact abbreviations match, after case, spaces and punctuation are
// ignored: a regimen is never guessed from a similar spelling. Abbreviations
// with more than one meaning (EC, TC, AC-T) return every variant.
package regimens

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
)

//go:embed regimens.csv
var regimensCSV []byte

type Source struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Version string `json:"version"`
}

var TableSource = Source{
	Name:    "PillGaze tabulka režimů podle HemOnc.org",
	URL:     "https://hemonc.org",
	Version: "2026-10-16",
}

// Regimen lists the antineoplastic drugs only: supportive drugs (mesna,
// antiemetics) and radiotherapy are not part of it. Drugs are English INN
// names, as DDInter spells them.
type Regimen struct {
	Name       string   `json:"name"`
	Indication string   `json:"indication"`
	Drugs      []string `json:"drugs"`
}

type Table struct {
	byKey map[string][]Regimen
}

// Load parses the embedded table.
func Load() (*Table, error) {
	r := csv.NewReader(bytes.NewReader(regimensCSV))
	r.FieldsPerRecord = 4
	if _, err := r.Read(); err != nil {
		return nil, err
	}
	t := &Table{byKey: map[string][]Regimen{}}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		reg := Regimen{Name: rec[0], Indication: rec[2], Drugs: strings.Split(rec[3], "|")}
		names := []string{rec[0]}
		if rec[1] != "" {
			names = append(names, strings.Split(rec[1], "|")...)
		}
		seen := map[string]bool{}
		for _, n := range names {
			k := Key(n)
			if k == "" {
				return nil, fmt.Errorf("regimen %q: empty abbreviation", rec[0])
			}
			if seen[k] {
				continue
			}
			seen[k] = true
			t.byKey[k] = append(t.byKey[k], reg)
		}
	}
	return t, nil
}

// Key ignores case and everything but letters and digits, so "R-CHOP",
// "rchop" and "R CHOP" are the same abbreviation.
func Key(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

// Drugs returns every drug of every regimen, sorted, without duplicates.
func (t *Table) Drugs() []string {
	seen := map[string]bool{}
	var out []string
	for _, regs := range t.byKey {
		for _, r := range regs {
			for _, d := range r.Drugs {
				if !seen[d] {
					seen[d] = true
					out = append(out, d)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// Lookup returns every regimen the abbreviation can mean, or nil.
func (t *Table) Lookup(text string) []Regimen {
	return t.byKey[Key(text)]
}
