// Package fixtures holds fixed /api/analyze responses for MOCK_AI=true.
package fixtures

import (
	"pillgaze/internal/ai"
	"pillgaze/internal/model"
)

func s(v string) *string { return &v }

// Read is what the model would return for one photo of a kind (note, box,
// regimen) in mock mode. analyze then matches and merges it like real
// output, so the mock shows merged items, conflicts and candidates.
func Read(source string) ai.Result {
	switch source {
	case "box":
		return ai.Result{Items: []ai.Item{
			{Name: "Prestarium Neo 5 mg", ActiveSubstance: s("perindoprilum argininum"), Strength: s("5 mg"), Form: s("potahované tablety"), UncertainFields: []string{}},
			{Name: "Warfarin Orion 5 mg", ActiveSubstance: s("warfarinum natricum"), Strength: s("5 mg"), Form: s("tablety"), UncertainFields: []string{}},
			{Name: "Paralen 500", ActiveSubstance: s("paracetamolum"), Strength: s("500 mg"), Form: s("tablety"), UncertainFields: []string{}},
		}}
	case "note":
		hw := s(model.NoteStyleHandwritten)
		return ai.Result{UnreadableItems: 1, Items: []ai.Item{
			// Merges with the box.
			{Name: "Prestarium Neo", Strength: s("5 mg"), Frequency: s("1-0-0"), Amount: s("1 tbl"), NoteStyle: hw, UncertainFields: []string{}},
			// Merges with the box, which disagrees on strength.
			{Name: "Warfarin Orion", Strength: s("3 mg"), Amount: s("1 tbl"), NoteStyle: hw, UncertainFields: []string{"frequency"}},
			// A generic name: candidates to confirm.
			{Name: "Ibuprofen 400", Strength: s("400 mg"), Frequency: s("při bolesti, max. 3x denně"), Amount: s("1 tbl"), NoteStyle: hw, UncertainFields: []string{}},
			// An unsure handwritten read: candidates to confirm.
			{Name: "Pantomil", Strength: s("40 mg"), Frequency: s("1-0-0"), Amount: s("1 tbl"), NoteStyle: hw, UncertainFields: []string{"name"}},
		}}
	case "regimen":
		return ai.Result{Items: []ai.Item{
			{Name: "FOLFOX", UncertainFields: []string{}},
			{Name: "R-CHOP", UncertainFields: []string{}},
		}}
	}
	return ai.Result{}
}

// Shared is the list PIN 0000 returns in mock mode: what a patient would
// share after confirming the box and note fixtures.
func Shared() []model.SharedMedication {
	return []model.SharedMedication{
		{
			ID: "med_box001", Kind: model.KindDrug, Name: "Prestarium Neo 5 mg",
			ActiveSubstance: s("perindoprilum argininum"), Strength: s("5 mg"), Form: s("potahované tablety"),
			Frequency: s("1-0-0"), Amount: s("1 tbl"),
			Source: model.SourceBox, Sources: []string{model.SourceBox, model.SourceNote},
		},
		{
			ID: "med_box003", Kind: model.KindDrug, Name: "Warfarin Orion 5 mg",
			ActiveSubstance: s("warfarinum natricum"), Strength: s("5 mg"), Form: s("tablety"),
			Frequency: s("0-0-1"), Amount: s("1 tbl"),
			Source: model.SourceBox, Sources: []string{model.SourceBox, model.SourceNote},
		},
		{
			ID: "med_note003", Kind: model.KindDrug, Name: "Ibuprofen 400",
			Strength: s("400 mg"), Frequency: s("při bolesti, max. 3x denně"), Amount: s("1 tbl"),
			Source: model.SourceNote, Sources: []string{model.SourceNote}, NoteStyle: s(model.NoteStyleHandwritten),
		},
		{
			ID: "med_reg001", Kind: model.KindRegimen, Name: "FOLFOX",
			Source: model.SourceNote, Sources: []string{model.SourceNote},
		},
	}
}
