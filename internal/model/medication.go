// Package model holds the API data types from docs/api.md, section 2.
package model

const (
	KindDrug    = "drug"
	KindRegimen = "regimen"

	SourceNote   = "note"
	SourceBox    = "box"
	SourceManual = "manual"

	NoteStylePrinted     = "printed"
	NoteStyleHandwritten = "handwritten"
)

// Medication is one item as read from a photo. Fields that could not be read
// are null, never guessed. note_style is present only on items from a note;
// sources and conflicts are filled by the client core, not the backend.
type Medication struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	ActiveSubstance *string    `json:"active_substance"`
	Strength        *string    `json:"strength"`
	Form            *string    `json:"form"`
	Frequency       *string    `json:"frequency"`
	Amount          *string    `json:"amount"`
	Source          string     `json:"source"`
	NoteStyle       *string    `json:"note_style,omitempty"`
	UncertainFields []string   `json:"uncertain_fields"`
	Sources         []string   `json:"sources,omitempty"`
	Conflicts       []Conflict `json:"conflicts,omitempty"`
	// Catalog is set by analyze; it is not shared and not sent back.
	Catalog *CatalogMatch `json:"catalog,omitempty"`
}

type Conflict struct {
	Field  string          `json:"field"`
	Values []ConflictValue `json:"values"`
}

type ConflictValue struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

// AnalyzeResponse is the 200 body of POST /api/analyze.
type AnalyzeResponse struct {
	Medications     []Medication `json:"medications"`
	UnreadableItems int          `json:"unreadable_items"`
}

// SharedMedication is what POST /api/share accepts and claim returns:
// uncertain_fields and conflicts are resolved by the patient and not shared.
type SharedMedication struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Name            string   `json:"name"`
	ActiveSubstance *string  `json:"active_substance"`
	Strength        *string  `json:"strength"`
	Form            *string  `json:"form"`
	Frequency       *string  `json:"frequency"`
	Amount          *string  `json:"amount"`
	Source          string   `json:"source"`
	Sources         []string `json:"sources,omitempty"`
	NoteStyle       *string  `json:"note_style,omitempty"`
}

// CatalogMatch links a read item to the organisers' catalogue.
// Status is "matched" (Product set), "needs_confirmation" (pick from
// Candidates) or "not_found".
type CatalogMatch struct {
	Status     string             `json:"status"`
	Product    *CatalogProduct    `json:"product,omitempty"`
	Candidates []CatalogCandidate `json:"candidates,omitempty"`
}

type CatalogProduct struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Strength        string `json:"strength"`
	Form            string `json:"form"`
	ActiveSubstance string `json:"active_substance"`
	Supplement      bool   `json:"supplement,omitempty"`
}

type CatalogCandidate struct {
	CatalogProduct
	Score float64 `json:"score"`
	By    string  `json:"match"` // name | prefix | substance | word | fuzzy
}
