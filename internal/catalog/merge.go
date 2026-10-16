package catalog

import (
	"slices"
	"strings"

	"pillgaze/internal/model"
)

// Merge joins items that are the same drug read from different photos
// (a box and a note) or the same product photographed twice. Only items
// matched to the catalogue are joined; everything else passes through, so a
// guess never swallows another item.
//
// Same drug means the same brand, or a brand with the same active
// substances and strength (a generic swap: the note says one brand, the box
// another). Within one source, the same brand at another strength stays a
// separate item: a patient may take both.
func (c *Catalog) Merge(items []model.Medication) []model.Medication {
	type group struct {
		members []model.Medication
		family  string
		subs    string
		nums    []string
		sources map[string]bool
	}
	var groups []*group
	var out []any // *group or model.Medication, in first-seen order

	for _, it := range items {
		p := c.matched(it)
		if p == nil {
			if len(it.Sources) == 0 {
				it.Sources = []string{it.Source}
			}
			out = append(out, it)
			continue
		}
		subs := strings.Join(p.substances, "+")
		var target *group
		for _, g := range groups {
			sameDrug := g.family == p.family || (g.subs == subs && slices.Equal(g.nums, p.numbers))
			if !sameDrug {
				continue
			}
			if !g.sources[it.Source] || slices.Equal(g.nums, p.numbers) {
				target = g
				break
			}
		}
		if target == nil {
			target = &group{family: p.family, subs: subs, nums: p.numbers, sources: map[string]bool{}}
			groups = append(groups, target)
			out = append(out, target)
		}
		target.members = append(target.members, it)
		target.sources[it.Source] = true
	}

	merged := make([]model.Medication, 0, len(out))
	for _, o := range out {
		switch v := o.(type) {
		case model.Medication:
			merged = append(merged, v)
		case *group:
			merged = append(merged, c.join(v.members))
		}
	}
	return merged
}

func (c *Catalog) matched(it model.Medication) *Product {
	if it.Kind != model.KindDrug || it.Catalog == nil || it.Catalog.Status != StatusMatched || it.Catalog.Product == nil {
		return nil
	}
	return c.byID[it.Catalog.Product.ID]
}

// rank orders sources by how much their identity fields can be trusted:
// a printed box, a printed note, then handwriting.
func rank(m model.Medication) int {
	switch {
	case m.Source == model.SourceBox:
		return 0
	case m.NoteStyle != nil && *m.NoteStyle == model.NoteStylePrinted:
		return 1
	case m.Source == model.SourceNote:
		return 2
	}
	return 3
}

func (c *Catalog) join(ms []model.Medication) model.Medication {
	if len(ms) == 1 {
		m := ms[0]
		m.Sources = []string{m.Source}
		return m
	}
	slices.SortStableFunc(ms, func(a, b model.Medication) int { return rank(a) - rank(b) })
	// Dosing is written on notes, rarely on boxes: notes first for those.
	dosing := slices.Clone(ms)
	slices.SortStableFunc(dosing, func(a, b model.Medication) int {
		ra, rb := rank(a), rank(b)
		if ra == 0 {
			ra = 9
		}
		if rb == 0 {
			rb = 9
		}
		return ra - rb
	})

	out := ms[0]
	out.UncertainFields = []string{}
	out.Conflicts = nil
	pick := func(field string, order []model.Medication, get func(model.Medication) *string) *string {
		for _, m := range order {
			if v := get(m); v != nil {
				if slices.Contains(m.UncertainFields, field) {
					out.UncertainFields = append(out.UncertainFields, field)
				}
				return v
			}
		}
		return nil
	}
	out.Strength = pick("strength", ms, func(m model.Medication) *string { return m.Strength })
	out.Form = pick("form", ms, func(m model.Medication) *string { return m.Form })
	out.ActiveSubstance = pick("active_substance", ms, func(m model.Medication) *string { return m.ActiveSubstance })
	out.Frequency = pick("frequency", dosing, func(m model.Medication) *string { return m.Frequency })
	out.Amount = pick("amount", dosing, func(m model.Medication) *string { return m.Amount })
	if slices.Contains(ms[0].UncertainFields, "name") {
		out.UncertainFields = append(out.UncertainFields, "name")
	}

	out.Sources = nil
	for _, m := range ms {
		if !slices.Contains(out.Sources, m.Source) {
			out.Sources = append(out.Sources, m.Source)
		}
		if out.NoteStyle == nil && m.Source == model.SourceNote {
			out.NoteStyle = m.NoteStyle
		}
	}

	// Different brands joined by substance: show both names.
	conflict(&out, ms, "name", func(m model.Medication) (string, string) {
		return m.Name, m.Catalog.Product.Name
	})
	conflict(&out, ms, "strength", func(m model.Medication) (string, string) {
		if m.Strength == nil {
			return "", ""
		}
		return *m.Strength, strings.Join(Numbers(*m.Strength), "/")
	})
	conflict(&out, ms, "frequency", func(m model.Medication) (string, string) {
		if m.Frequency == nil {
			return "", ""
		}
		return *m.Frequency, strings.Join(strings.Fields(strings.ToLower(*m.Frequency)), "")
	})
	slices.Sort(out.UncertainFields)
	out.UncertainFields = slices.Compact(out.UncertainFields)
	return out
}

// conflict records a field whose comparable value differs between sources.
// get returns the shown value and the value to compare ("" = not read).
func conflict(out *model.Medication, ms []model.Medication, field string, get func(model.Medication) (string, string)) {
	var vals []model.ConflictValue
	seen := map[string]bool{}
	for _, m := range ms {
		shown, cmpVal := get(m)
		if cmpVal == "" {
			continue
		}
		if field == "name" {
			cmpVal = FamilyKey(cmpVal)
		}
		seen[cmpVal] = true
		v := model.ConflictValue{Source: m.Source, Value: shown}
		if !slices.Contains(vals, v) {
			vals = append(vals, v)
		}
	}
	if len(seen) > 1 {
		out.Conflicts = append(out.Conflicts, model.Conflict{Field: field, Values: vals})
		out.UncertainFields = append(out.UncertainFields, field)
	}
}
