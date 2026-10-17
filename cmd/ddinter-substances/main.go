// Command ddinter-substances prints the DDInter names of every substance in
// the catalogue and in the regimen table, one per line. It is the input of
// scripts/ddinter_text.py, which downloads interaction descriptions only for
// these substances.
//
//	go run ./cmd/ddinter-substances > /tmp/ddinter/substances.txt
package main

import (
	"fmt"
	"log"
	"slices"

	"pillgaze/internal/catalog"
	"pillgaze/internal/interactions"
	"pillgaze/internal/regimens"
)

func main() {
	db, err := interactions.Load()
	if err != nil {
		log.Fatal(err)
	}
	cat, err := catalog.Load()
	if err != nil {
		log.Fatal(err)
	}
	regs, err := regimens.Load()
	if err != nil {
		log.Fatal(err)
	}

	seen := map[string]bool{}
	add := func(alts []string) {
		if n, ok := db.Resolve(alts); ok {
			seen[n] = true
		}
	}
	for _, p := range cat.Products {
		for _, alts := range interactions.SplitSubstances(p.ActiveSubstance) {
			add(alts)
		}
	}
	for _, d := range regs.Drugs() {
		add([]string{d})
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		fmt.Println(n)
	}
}
