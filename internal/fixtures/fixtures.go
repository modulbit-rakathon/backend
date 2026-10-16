// Package fixtures holds fixed /api/analyze responses for MOCK_AI=true.
package fixtures

import "pillgaze/internal/model"

func s(v string) *string { return &v }

// Analyze returns a fresh copy of the fixture for an analyze source
// (box, note, regimen). ok is false for an unknown source.
func Analyze(source string) (resp model.AnalyzeResponse, ok bool) {
	switch source {
	case "box":
		return model.AnalyzeResponse{Medications: box()}, true
	case "note":
		return model.AnalyzeResponse{Medications: note(), UnreadableItems: 1}, true
	case "regimen":
		return model.AnalyzeResponse{Medications: regimen()}, true
	}
	return model.AnalyzeResponse{}, false
}

func box() []model.Medication {
	return []model.Medication{
		{
			ID: "med_box001", Kind: model.KindDrug, Name: "Prestarium Neo 5 mg",
			ActiveSubstance: s("perindoprilum argininum"), Strength: s("5 mg"), Form: s("potahované tablety"),
			Source: model.SourceBox, UncertainFields: []string{},
		},
		{
			ID: "med_box002", Kind: model.KindDrug, Name: "Paralen 500",
			ActiveSubstance: s("paracetamolum"), Strength: s("500 mg"), Form: s("tablety"),
			Source: model.SourceBox, UncertainFields: []string{},
		},
		{
			ID: "med_box003", Kind: model.KindDrug, Name: "Warfarin Orion 5 mg",
			ActiveSubstance: s("warfarinum natricum"), Strength: s("5 mg"), Form: s("tablety"),
			Source: model.SourceBox, UncertainFields: []string{},
		},
	}
}

func note() []model.Medication {
	return []model.Medication{
		{
			ID: "med_note001", Kind: model.KindDrug, Name: "Prestarium Neo",
			Strength: s("5 mg"), Frequency: s("1-0-0"), Amount: s("1 tbl"),
			Source: model.SourceNote, NoteStyle: s(model.NoteStyleHandwritten), UncertainFields: []string{},
		},
		{
			ID: "med_note002", Kind: model.KindDrug, Name: "Warfarin",
			Strength: s("5 mg"), Amount: s("1 tbl"),
			Source: model.SourceNote, NoteStyle: s(model.NoteStyleHandwritten), UncertainFields: []string{"frequency"},
		},
		{
			ID: "med_note003", Kind: model.KindDrug, Name: "Ibuprofen 400",
			Strength: s("400 mg"), Frequency: s("při bolesti, max. 3x denně"), Amount: s("1 tbl"),
			Source: model.SourceNote, NoteStyle: s(model.NoteStyleHandwritten), UncertainFields: []string{},
		},
		{
			ID: "med_note004", Kind: model.KindDrug, Name: "Helicid 20",
			Strength: s("20 mg"), Frequency: s("1-0-0"), Amount: s("1 cps"),
			Source: model.SourceNote, NoteStyle: s(model.NoteStyleHandwritten), UncertainFields: []string{},
		},
	}
}

// Regimen items come from a note or report, so their source is "note".
func regimen() []model.Medication {
	return []model.Medication{
		{ID: "med_reg001", Kind: model.KindRegimen, Name: "FOLFOX", Source: model.SourceNote, UncertainFields: []string{}},
		{ID: "med_reg002", Kind: model.KindRegimen, Name: "AC-T", Source: model.SourceNote, UncertainFields: []string{}},
		{ID: "med_reg003", Kind: model.KindRegimen, Name: "R-CHOP", Source: model.SourceNote, UncertainFields: []string{}},
	}
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
