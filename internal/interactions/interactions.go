// Package interactions checks medication lists for drug-drug interactions
// against DDInter (CC BY-NC-SA 4.0, see LICENSE-DDInter.md).
//
// An empty result never means "no interactions": it means nothing was found
// in DDInter, and every substance that could not be checked is reported.
package interactions

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strings"
)

//go:embed ddinter.csv.gz
var ddinterGz []byte

// Source is what the result was checked against.
type Source struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	License string `json:"license"`
	Version string `json:"version"`
}

var DDInter = Source{
	Name:    "DDInter",
	URL:     "https://ddinter.scbdd.com",
	License: "CC BY-NC-SA 4.0",
	Version: "download of 2026-10-16",
}

const (
	LevelMajor    = "major"
	LevelModerate = "moderate"
	LevelMinor    = "minor"
	LevelUnknown  = "unknown"
)

var levelOrder = []string{LevelMajor, LevelModerate, LevelMinor, LevelUnknown}

type DB struct {
	pairs map[[2]string]string // sorted DDInter names -> level
	// byKey maps a spelling key to a DDInter name; "" marks a key shared by
	// two different names, which is never matched.
	byKey map[string]string
	alias map[string]string
}

// Load parses the embedded DDInter table.
func Load() (*DB, error) {
	zr, err := gzip.NewReader(bytes.NewReader(ddinterGz))
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(zr)
	r.FieldsPerRecord = 3
	db := &DB{pairs: map[[2]string]string{}, byKey: map[string]string{}, alias: map[string]string{}}
	names := map[string]bool{}
	strs := map[string]string{}
	intern := func(s string) string {
		if v, ok := strs[s]; ok {
			return v
		}
		strs[s] = s
		return s
	}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// Intern names and levels: 235k pairs share about 2k distinct strings.
		a, b, level := intern(rec[0]), intern(rec[1]), intern(rec[2])
		db.pairs[[2]string{a, b}] = level
		names[a], names[b] = true, true
	}
	for n := range names {
		for _, drop := range []bool{false, true} {
			k := key(n, drop)
			if prev, ok := db.byKey[k]; ok && prev != n {
				if !drop {
					db.byKey[k] = ""
				}
				continue
			}
			if _, ok := db.byKey[k]; !ok {
				db.byKey[k] = n
			}
		}
	}
	for a, target := range aliases {
		if !names[target] {
			return nil, fmt.Errorf("alias %q -> %q: not in DDInter", a, target)
		}
		db.alias[inputKey(a, false)] = target
	}
	return db, nil
}

// Resolve finds the DDInter name for one substance, trying each spelling.
func (db *DB) Resolve(alts []string) (string, bool) {
	for _, drop := range []bool{false, true} {
		for _, a := range alts {
			k := inputKey(a, drop)
			if k == "" {
				continue
			}
			if n, ok := db.alias[k]; ok {
				return n, true
			}
			if n := db.byKey[k]; n != "" {
				return n, true
			}
		}
	}
	return "", false
}

// Med is the part of a medication the check needs.
type Med struct {
	ID              string
	Name            string
	ActiveSubstance *string
}

type Pair struct {
	A          string `json:"a"` // medication ids
	B          string `json:"b"`
	SubstanceA string `json:"substance_a"`
	SubstanceB string `json:"substance_b"`
	Level      string `json:"level"`
}

const (
	ReasonNoSubstance = "no_active_substance"
	ReasonNotInSource = "not_in_source"
	ReasonPartlyInSrc = "partly_in_source"
)

type Unchecked struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Reason     string   `json:"reason"`
	Substances []string `json:"substances,omitempty"` // the ones not found
}

type Result struct {
	Pairs          []Pair      `json:"pairs"`
	CheckedAgainst Source      `json:"checked_against"`
	Unchecked      []Unchecked `json:"unchecked"`
	Note           string      `json:"note"`
}

const note = "Interakce jsou hledány jen ve zdroji DDInter a jen u látek, které se v něm podařilo najít. " +
	"Nenalezení interakce neznamená, že interakce neexistuje. Látky v \"unchecked\" ověřeny nebyly."

// Check looks up every pair of substances from different medications.
func (db *DB) Check(meds []Med) Result {
	type sub struct{ medID, name string }
	var subs []sub
	res := Result{Pairs: []Pair{}, CheckedAgainst: DDInter, Unchecked: []Unchecked{}, Note: note}

	for _, m := range meds {
		if m.ActiveSubstance == nil || strings.TrimSpace(*m.ActiveSubstance) == "" {
			res.Unchecked = append(res.Unchecked, Unchecked{ID: m.ID, Name: m.Name, Reason: ReasonNoSubstance})
			continue
		}
		parts := splitSubstances(*m.ActiveSubstance)
		var missing []string
		found := 0
		for _, alts := range parts {
			n, ok := db.Resolve(alts)
			if !ok {
				missing = append(missing, alts[0])
				continue
			}
			found++
			subs = append(subs, sub{m.ID, n})
		}
		switch {
		case found == 0:
			res.Unchecked = append(res.Unchecked, Unchecked{ID: m.ID, Name: m.Name, Reason: ReasonNotInSource, Substances: missing})
		case len(missing) > 0:
			res.Unchecked = append(res.Unchecked, Unchecked{ID: m.ID, Name: m.Name, Reason: ReasonPartlyInSrc, Substances: missing})
		}
	}

	seen := map[[4]string]bool{}
	for i := range subs {
		for j := i + 1; j < len(subs); j++ {
			a, b := subs[i], subs[j]
			if a.medID == b.medID || a.name == b.name {
				continue
			}
			names := [2]string{a.name, b.name}
			if names[0] > names[1] {
				names[0], names[1] = names[1], names[0]
				a, b = b, a
			}
			level, ok := db.pairs[names]
			if !ok {
				continue
			}
			k := [4]string{a.medID, b.medID, a.name, b.name}
			if seen[k] {
				continue
			}
			seen[k] = true
			res.Pairs = append(res.Pairs, Pair{A: a.medID, B: b.medID, SubstanceA: a.name, SubstanceB: b.name, Level: level})
		}
	}
	slices.SortStableFunc(res.Pairs, func(x, y Pair) int {
		return slices.Index(levelOrder, x.Level) - slices.Index(levelOrder, y.Level)
	})
	return res
}
