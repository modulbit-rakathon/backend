package catalog

import (
	"slices"
	"testing"

	"pillgaze/internal/model"
)

func item(t *testing.T, id, source, style, name string, strength, freq *string, unsure ...string) model.Medication {
	t.Helper()
	m := model.Medication{ID: id, Kind: model.KindDrug, Name: name, Strength: strength, Frequency: freq, Source: source, UncertainFields: append([]string{}, unsure...)}
	if style != "" {
		m.NoteStyle = sp(style)
	}
	m.Catalog = load(t).Match(name, strength, slices.Contains(unsure, "name"))
	return m
}

func byID(ms []model.Medication, id string) *model.Medication {
	for i := range ms {
		if ms[i].ID == id {
			return &ms[i]
		}
	}
	return nil
}

func TestMergeNoteAndBox(t *testing.T) {
	c := load(t)
	got := c.Merge([]model.Medication{
		item(t, "n1", "note", "handwritten", "Prestarium Neo", sp("5 mg"), sp("1-0-0")),
		item(t, "n2", "note", "handwritten", "Warfarin Orion", sp("5 mg"), nil, "frequency"),
		item(t, "b1", "box", "", "PRESTARIUM NEO 5 mg", sp("5 mg"), nil),
		item(t, "b2", "box", "", "WARFARIN ORION 5 mg", sp("5 mg"), sp("0-0-1")),
	})
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(got), got)
	}
	p := got[0]
	if p.Name != "PRESTARIUM NEO 5 mg" || !slices.Equal(p.Sources, []string{"box", "note"}) || p.Source != "box" {
		t.Errorf("prestarium: name %q sources %v source %s", p.Name, p.Sources, p.Source)
	}
	if p.Frequency == nil || *p.Frequency != "1-0-0" || p.NoteStyle == nil {
		t.Errorf("dosing should come from the note: %v %v", p.Frequency, p.NoteStyle)
	}
	if len(p.Conflicts) != 0 {
		t.Errorf("unexpected conflicts %+v", p.Conflicts)
	}
	// Note had no frequency (flagged), the box had one: the box fills it.
	w := got[1]
	if w.Frequency == nil || *w.Frequency != "0-0-1" {
		t.Errorf("warfarin frequency %v", w.Frequency)
	}
}

func TestMergeConflicts(t *testing.T) {
	c := load(t)
	got := c.Merge([]model.Medication{
		item(t, "n1", "note", "printed", "AGEN", sp("10 mg"), sp("1-0-0")),
		item(t, "b1", "box", "", "AGEN 5", sp("5 mg"), sp("1-0-1")),
	})
	if len(got) != 1 {
		t.Fatalf("got %d items: %+v", len(got), got)
	}
	m := got[0]
	fields := []string{}
	for _, cf := range m.Conflicts {
		fields = append(fields, cf.Field)
	}
	if !slices.Equal(fields, []string{"strength", "frequency"}) {
		t.Errorf("conflicts %+v", m.Conflicts)
	}
	if !slices.Contains(m.UncertainFields, "strength") || !slices.Contains(m.UncertainFields, "frequency") {
		t.Errorf("uncertain %v", m.UncertainFields)
	}
}

func TestMergeGenericSwap(t *testing.T) {
	c := load(t)
	// Same substance and strength under two brands: one item, both names shown.
	got := c.Merge([]model.Medication{
		item(t, "n1", "note", "handwritten", "Pantomyl", sp("40 mg"), sp("1-0-0")),
		item(t, "b1", "box", "", "Controloc 20 mg", sp("20 mg"), nil),
		item(t, "b2", "box", "", "Helicid 20 Zentiva", sp("20 mg"), nil),
		item(t, "n2", "note", "printed", "CONTROLOC", sp("20 mg"), sp("1-0-0")),
	})
	if len(got) != 3 {
		t.Fatalf("got %d items: %+v", len(got), got)
	}
	ctl := byID(got, "b1")
	if ctl == nil || !slices.Equal(ctl.Sources, []string{"box", "note"}) {
		t.Fatalf("controloc box+note not merged: %+v", got)
	}
	// Pantomyl 40 and Controloc 20 are both pantoprazole, but strengths differ.
	if byID(got, "n1") == nil {
		t.Error("pantomyl 40 mg merged into controloc 20 mg")
	}
}

func TestMergeSwapName(t *testing.T) {
	c := load(t)
	got := c.Merge([]model.Medication{
		item(t, "n1", "note", "printed", "PANTOMYL", sp("40 mg"), sp("1-0-0")),
		item(t, "b1", "box", "", "PANTOMYL", sp("40 mg"), nil),
		item(t, "n2", "note", "printed", "JANUVIA", sp("100 mg"), sp("1-0-0")),
		item(t, "b2", "box", "", "JUZINA", sp("100 mg"), nil),
	})
	if len(got) != 2 {
		t.Fatalf("got %d items: %+v", len(got), got)
	}
	j := byID(got, "b2")
	if j == nil || len(j.Conflicts) != 1 || j.Conflicts[0].Field != "name" || !slices.Contains(j.UncertainFields, "name") {
		t.Errorf("januvia/juzina swap: %+v", j)
	}
}

func TestMergeKeepsUnsureAndSameSourceStrengths(t *testing.T) {
	c := load(t)
	got := c.Merge([]model.Medication{
		// A patient can take two strengths of one drug.
		item(t, "n1", "note", "printed", "PREDNISONE OLIKLA", sp("5 mg"), sp("1-0-0")),
		item(t, "n2", "note", "printed", "PREDNISONE OLIKLA", sp("20 mg"), sp("1-0-0")),
		// Unsure reads are not joined, even with the same brand on a box.
		item(t, "n3", "note", "handwritten", "Pantomil", sp("40 mg"), nil, "name"),
		item(t, "b1", "box", "", "PANTOMYL 40 mg", sp("40 mg"), nil),
		{ID: "r1", Kind: model.KindRegimen, Name: "FOLFOX", Source: "note", UncertainFields: []string{}},
	})
	if len(got) != 5 {
		t.Fatalf("got %d items, want 5: %+v", len(got), got)
	}
	if got[0].ID != "n1" || got[4].ID != "r1" {
		t.Errorf("order changed: %s ... %s", got[0].ID, got[4].ID)
	}
	for _, m := range got {
		if !slices.Equal(m.Sources, []string{m.Source}) {
			t.Errorf("%s: sources %v", m.ID, m.Sources)
		}
	}
}

func TestMergeUnknownStrengthJoinsOtherSource(t *testing.T) {
	c := load(t)
	got := c.Merge([]model.Medication{
		// 3 mg is not in the catalogue, but the brand is read with certainty.
		item(t, "n1", "note", "handwritten", "Warfarin Orion", sp("3 mg"), sp("0-0-1")),
		item(t, "b1", "box", "", "WARFARIN ORION 5 mg", sp("5 mg"), nil),
		// Unsure name: stays apart.
		item(t, "n2", "note", "handwritten", "Agen", sp("7 mg"), nil, "name"),
		item(t, "b2", "box", "", "AGEN 5", sp("5 mg"), nil),
	})
	if len(got) != 3 {
		t.Fatalf("got %d items: %+v", len(got), got)
	}
	w := byID(got, "b1")
	if w == nil || !slices.Equal(w.Sources, []string{"box", "note"}) || len(w.Conflicts) != 1 || w.Conflicts[0].Field != "strength" {
		t.Fatalf("warfarin: %+v", w)
	}
	if byID(got, "n2") == nil {
		t.Error("unsure Agen joined the box")
	}
}
