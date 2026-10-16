package interactions

import (
	"regexp"
	"slices"
	"strings"
)

// Salt, hydrate and ester words (Latin, Czech, English). They are dropped
// only when the full name has no match, so "Potassium chloride" still finds
// itself while "losartan potassium" falls back to "losartan".
var saltWords = []string{
	"hydrochloride", "hydrochloridum", "hydrochlorid", "dihydrochloride", "dihydrochloridum", "dihydrochlorid",
	"hydrobromide", "hydrobromidum", "bromide", "bromidum", "bromid",
	"sul", "soli",
	"fumarate", "fumaras", "fumarat", "maleate", "maleas", "maleat", "mesylate", "mesilas", "mesilat",
	"besylate", "besilas", "besilat", "succinate", "succinas", "sukcinat", "tartrate", "tartras", "tartarat",
	"acetate", "acetas", "acetat", "citrate", "citras", "citrat", "sulfate", "sulfas", "sulfat",
	"phosphate", "phosphas", "fosfat", "arginine", "argininum", "arginin", "erbumine", "erbuminum", "erbumin",
	"monohydrate", "monohydricum", "monohydrat", "dihydrate", "dihydricum", "dihydrat", "trihydrate",
	"trihydricum", "trihydrat", "hemihydrate", "hemihydrat", "pentahydricus", "pentahydrat",
	"anhydrous", "anhydricum", "bezvody", "cilexetil", "cilexetilum", "etexilate", "etexilatum", "etexilat",
	"medoxomil", "medoxomilum", "ve", "forme", "jako",
	"kalicum", "dihydricus", "trihydricus", "monohydricus", "hemihydricus", "fosfatu", "fumaratu",
	"chloride", "chloridum", "chlorid",
}

// cationWords are dropped from the input only, never from DDInter names:
// otherwise "Calcium lactate" would reduce to "lactate" and match
// "magnesium lactate".
var cationWords = []string{
	"sodium", "natricum", "sodna", "sodny", "natrium", "potassium", "kalium", "kalicum", "draselna", "draselny",
	"calcium", "magnesium", "magnesii", "calcii",
}

// aliases maps names the spelling rules cannot reach to DDInter names.
// Only exact synonyms: never a related or similar substance.
var aliases = map[string]string{
	"paracetamol":               "Acetaminophen",
	"paracetamolum":             "Acetaminophen",
	"kyselina acetylsalicylová": "Acetylsalicylic acid",
	"kyselina listová":          "Folic acid",
	"kyselina askorbová":        "Ascorbic acid",
	"kyselina valproová":        "Valproic acid",
	"valproát":                  "Valproic acid",
	"kyselina tranexamová":      "Tranexamic acid",
	"kyselina ursodeoxycholová": "Ursodeoxycholic acid",
	"thiamazol":                 "Methimazole",
	"thiamazolum":               "Methimazole",
	"chlorid draselný":          "Potassium chloride",
	"kalii chloridum":           "Potassium chloride",
	"uhličitan vápenatý":        "Calcium carbonate",
	"calcii carbonas":           "Calcium carbonate",
	"cholekalciferol":           "Cholecalciferol",
	"colecalciferolum":          "Cholecalciferol",
	"colecalciferol":            "Cholecalciferol",
	"síran železnatý":           "Ferrous sulfate anhydrous",
	"ferrosi sulfas":            "Ferrous sulfate anhydrous",
	"levothyroxin":              "Levothyroxine",
	"levothyroxinum natricum":   "Levothyroxine",
	"hydrochlorothiazid":        "Hydrochlorothiazide",
	"acidum acetylsalicylicum":  "Acetylsalicylic acid",
	"acidum folicum":            "Folic acid",
	"acidum tranexamicum":       "Tranexamic acid",
	"acidum ursodeoxycholicum":  "Ursodeoxycholic acid",
	"acidum valproicum":         "Valproic acid",
	"acidum ascorbicum":         "Ascorbic acid",
	"kyselina mefenamová":       "Mefenamic acid",
	"kyselina zoledronová":      "Zoledronic acid",
	"kyselina alendronová":      "Alendronic acid",
	"natrium-alendronát":        "Alendronic acid",
	"kyselina ibandronová":      "Ibandronate",
	"kyselina mykofenolová":     "Mycophenolic acid",
	"mofetil-mykofenolát":       "Mycophenolate mofetil",
	"mycophenolas mofetil":      "Mycophenolate mofetil",
	"estradiol-valerát":         "Estradiol",
	"kyselina nikotinová":       "Niacin",
}

var nonLetters = regexp.MustCompile(`[^a-z ]+`)

// key reduces a substance name to a spelling-insensitive form shared by
// Czech, Latin and English INNs: "Amlodipin", "amlodipinum" and "Amlodipine"
// all become "amlodipin".
func key(s string, dropSalts bool) string { return keyOf(s, dropSalts, false) }

// inputKey also drops cations; see cationWords.
func inputKey(s string, dropSalts bool) string { return keyOf(s, dropSalts, dropSalts) }

func keyOf(s string, dropSalts, dropCations bool) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(fold(r))
	}
	s = nonLetters.ReplaceAllString(strings.ReplaceAll(b.String(), "-", " "), " ")
	words := strings.Fields(s)
	out := words[:0]
	for _, w := range words {
		if dropSalts && slices.Contains(saltKeys, w) || dropCations && slices.Contains(cationWords, w) {
			continue
		}
		out = append(out, stem(w))
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

var saltKeys = func() []string {
	ks := make([]string, len(saltWords))
	for i, w := range saltWords {
		ks[i] = strings.ToLower(w)
	}
	return ks
}()

var spelling = strings.NewReplacer("ph", "f", "th", "t", "qu", "kv", "ck", "k", "c", "k", "x", "ks", "z", "s", "y", "i", "w", "v")

// stem unifies spelling and strips Latin and English endings:
// "acetylsalicylicum" -> "asetilsalisilik", "metformini" -> "metformin",
// "amlodipine" -> "amlodipin", "acidum" and "acid" -> "asid".
func stem(w string) string {
	// Latin neuter first ("tacrolimusum" -> "tacrolimus"), then one more ending.
	if strings.HasSuffix(w, "um") && len(w) > 4 {
		w = strings.TrimSuffix(w, "um")
	}
	switch {
	case strings.HasSuffix(w, "us") && len(w) > 6:
		w = strings.TrimSuffix(w, "us")
	case strings.HasSuffix(w, "as") && len(w) > 6:
		w = strings.TrimSuffix(w, "as") + "at"
	case strings.HasSuffix(w, "is") && len(w) > 6:
		w = strings.TrimSuffix(w, "is")
	case strings.HasSuffix(w, "i") && len(w) > 5:
		w = strings.TrimSuffix(w, "i")
	case strings.HasSuffix(w, "u") && len(w) > 6:
		// Czech genitive: "losartanu" -> "losartan".
		w = strings.TrimSuffix(w, "u")
	case strings.HasSuffix(w, "e") && len(w) > 4:
		w = strings.TrimSuffix(w, "e")
	}
	w = spelling.Replace(w)
	// Collapse doubled letters: "allopurinol" and "alopurinol" meet.
	var b strings.Builder
	var prev rune
	for _, r := range w {
		if r != prev {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}

var folds = map[rune]rune{
	'á': 'a', 'č': 'c', 'ď': 'd', 'é': 'e', 'ě': 'e', 'í': 'i', 'ň': 'n', 'ó': 'o',
	'ř': 'r', 'š': 's', 'ť': 't', 'ú': 'u', 'ů': 'u', 'ý': 'y', 'ž': 'z', 'ä': 'a',
	'ö': 'o', 'ü': 'u', 'ľ': 'l', 'ĺ': 'l', 'ŕ': 'r', 'ô': 'o',
}

func fold(r rune) rune {
	if f, ok := folds[r]; ok {
		return f
	}
	return r
}

var (
	parens   = regexp.MustCompile(`\(([^()]*)\)`)
	splitter = regexp.MustCompile(`\s*(?:\+|/|;|,)\s*`)
)

// SplitSubstances turns an active_substance string into the names to look
// up. Each element lists alternative spellings of one substance, e.g.
// "Amlodipin (amlodipinum)" -> [["Amlodipin", "amlodipinum"]].
func SplitSubstances(s string) [][]string {
	var out [][]string
	// "Amlodipin / valsartan (amlodipinum / valsartanum)": split the outer
	// text and the bracket separately and pair them up by position.
	var inner []string
	for _, m := range parens.FindAllStringSubmatch(s, -1) {
		inner = append(inner, splitter.Split(strings.TrimSpace(m[1]), -1)...)
	}
	outer := splitter.Split(strings.TrimSpace(parens.ReplaceAllString(s, " ")), -1)
	for i, o := range outer {
		o = strings.TrimSpace(o)
		alts := []string{}
		if o != "" {
			alts = append(alts, o)
		}
		if len(inner) == len(outer) {
			if in := strings.TrimSpace(inner[i]); in != "" {
				alts = append(alts, in)
			}
		}
		if len(alts) > 0 {
			out = append(out, alts)
		}
	}
	// Brackets that do not line up with the outer parts become their own
	// alternatives for the whole string (e.g. "Probiotika (Lactobacillus)").
	if len(inner) != len(outer) && len(inner) > 0 && len(out) == 1 {
		for _, in := range inner {
			if in = strings.TrimSpace(in); in != "" {
				out[0] = append(out[0], in)
			}
		}
	}
	return out
}

// SubstanceKey is the spelling-insensitive key of one substance name, the
// same one DDInter lookups use: "Amlodipin", "amlodipinum" and "Amlodipine"
// share it.
func SubstanceKey(s string) string { return inputKey(s, true) }

// Fold lowercases s and strips Czech and Slovak diacritics.
func Fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		b.WriteRune(fold(r))
	}
	return b.String()
}
