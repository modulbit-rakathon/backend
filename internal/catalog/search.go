package catalog

import (
	"cmp"
	"slices"
	"strings"

	"pillgaze/internal/interactions"
	"pillgaze/internal/model"
)

// Search finds products for a typed query (brand or substance), best first.
// It runs over the in-memory catalogue only; no model is called.
func (c *Catalog) Search(q string, limit int) []model.CatalogProduct {
	key := familyKey(q)
	if key == "" {
		return []model.CatalogProduct{}
	}
	sub := interactions.SubstanceKey(q)
	type hit struct {
		p     *Product
		score int
	}
	var hits []hit
	for _, p := range c.Products {
		score := 0
		switch {
		case p.family == key:
			score = 4
		case strings.HasPrefix(p.family, key):
			score = 3
		case strings.Contains(p.family, key):
			score = 2
		}
		if score == 0 && sub != "" {
			for _, s := range p.substances {
				if strings.HasPrefix(s, sub) {
					score = 1
					break
				}
			}
		}
		if score > 0 {
			hits = append(hits, hit{p, score})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.p.Name, b.p.Name), cmp.Compare(a.p.Strength, b.p.Strength))
	})
	out := []model.CatalogProduct{}
	for _, h := range hits[:min(len(hits), limit)] {
		out = append(out, *ref(h.p))
	}
	return out
}
