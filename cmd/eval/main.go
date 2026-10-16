// Command eval runs ai.Extract over the split sample photos and compares the
// output with the organisers' ground truth (fictional patients).
//
//	scripts/split_samples.py ../Data/Seznamy_foto data/samples
//	set -a; . ./.env; set +a
//	go run ./cmd/eval -models gpt-6-1-sol-dz-eu,gpt-6-luna-dz-eu
//
// Details per item go to data/eval/<timestamp>.json (gitignored).
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"pillgaze/internal/ai"
)

type gtItem struct {
	Name        string  `json:"name"`
	Alternative *string `json:"alternative"`
	Strength    *string `json:"strength"`
	Frequency   *string `json:"frequency"`
}

type job struct {
	Photo   string `json:"photo"`
	Variant string `json:"variant"` // printed | handwritten
	Patient string `json:"patient"`
	Model   string `json:"model"`
}

// match levels, best first
const (
	lvExact   = "exact"       // same text ignoring case and spacing
	lvNorm    = "normalized"  // same letters ignoring diacritics, punctuation, strength
	lvPartial = "partial"     // one name is the other plus a manufacturer/suffix
	lvAlt     = "alternative" // matches the bracketed alternative brand
	lvFuzzy   = "fuzzy"       // close but different spelling: a misread (or a faithful typo)
	lvMissing = "missing"
)

type itemResult struct {
	Expected      string   `json:"expected"`
	Got           string   `json:"got,omitempty"`
	Level         string   `json:"level"`
	StrengthOK    bool     `json:"strength_ok"`
	FrequencyOK   bool     `json:"frequency_ok"`
	GotStrength   *string  `json:"got_strength,omitempty"`
	GotFrequency  *string  `json:"got_frequency,omitempty"`
	NoteStyle     *string  `json:"note_style,omitempty"`
	Uncertain     []string `json:"uncertain_fields,omitempty"`
	SilentProblem bool     `json:"silent_problem"` // something wrong and not flagged
}

type runResult struct {
	job
	Error      string       `json:"error,omitempty"`
	LatencyMS  int64        `json:"latency_ms"`
	Prompt     int          `json:"prompt_tokens"`
	Tokens     int          `json:"completion_tokens"`
	Items      []itemResult `json:"items"`
	Extra      []string     `json:"extra,omitempty"`
	Invented   []string     `json:"invented,omitempty"` // extras that are a different drug
	Unreadable int          `json:"unreadable_items"`
	Raw        []ai.Item    `json:"raw,omitempty"` // model output, for rescoring without new calls
}

func main() {
	models := flag.String("models", os.Getenv("AI_MODEL_A"), "comma-separated deployment names")
	variants := flag.String("variants", "printed,handwritten", "which splits to run")
	limit := flag.Int("n", 0, "max photos (0 = all)")
	par := flag.Int("p", 4, "parallel model calls")
	detail := flag.String("detail", "", "image detail: low, high, auto (empty = model default)")
	rescore := flag.String("rescore", "", "re-score a saved data/eval/*.json without calling the model")
	flag.Parse()

	var gt map[string][]gtItem
	var manifest map[string]string
	mustJSON("data/ground_truth.json", &gt)
	mustJSON("data/manifest.json", &manifest)

	if *rescore != "" {
		var results []runResult
		mustJSON(*rescore, &results)
		for i := range results {
			if results[i].Error == "" {
				score(&results[i], gt[results[i].Patient])
			}
		}
		summarize(results)
		return
	}

	photos := slices.Sorted(func(yield func(string) bool) {
		for k := range manifest {
			if !yield(k) {
				return
			}
		}
	})
	if *limit > 0 && *limit < len(photos) {
		photos = photos[:*limit]
	}

	var jobs []job
	for _, m := range strings.Split(*models, ",") {
		for _, v := range strings.Split(*variants, ",") {
			for _, p := range photos {
				jobs = append(jobs, job{Photo: p, Variant: v, Patient: manifest[p], Model: m})
			}
		}
	}

	client := ai.New(strings.TrimRight(os.Getenv("AI_BASE_URL"), "/"), os.Getenv("AI_API_KEY"))
	client.Detail = *detail
	results := make([]runResult, len(jobs))
	sem := make(chan struct{}, *par)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = run(client, j, gt[j.Patient])
			fmt.Fprintf(os.Stderr, ".")
		})
	}
	wg.Wait()
	fmt.Fprintln(os.Stderr)

	summarize(results)
	var promptTotal, completionTotal int
	for _, r := range results {
		promptTotal += r.Prompt
		completionTotal += r.Tokens
	}
	fmt.Printf("tokens: %d prompt, %d completion (incl. retries)\n", promptTotal, completionTotal)

	os.MkdirAll("data/eval", 0o755)
	out := filepath.Join("data/eval", time.Now().Format("20060102-150405")+".json")
	b, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("details:", out)
}

func mustJSON(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Fatalf("%s: %v", path, err)
	}
}

func run(c *ai.Client, j job, expected []gtItem) runResult {
	r := runResult{job: j}
	img, err := os.ReadFile(filepath.Join("data/samples", j.Photo+"_"+j.Variant+".jpg"))
	if err != nil {
		r.Error = err.Error()
		return r
	}
	start := time.Now()
	res, err := c.Extract(context.Background(), j.Model, "note", "image/jpeg", img)
	r.LatencyMS = time.Since(start).Milliseconds()
	r.Prompt, r.Tokens = res.PromptTokens, res.CompletionTokens
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Unreadable = res.UnreadableItems
	r.Raw = res.Items
	score(&r, expected)
	return r
}

// score compares r.Raw with the expected items; it fills Items and Extra.
func score(r *runResult, expected []gtItem) {
	res := struct{ Items []ai.Item }{r.Raw}
	r.Items, r.Extra, r.Invented = nil, nil, nil
	used := make([]bool, len(res.Items))
	for _, e := range expected {
		best, bestLv := -1, lvMissing
		for i, it := range res.Items {
			if used[i] {
				continue
			}
			if lv := nameLevel(e, it.Name); rank(lv) < rank(bestLv) {
				best, bestLv = i, lv
			}
		}
		ir := itemResult{Expected: e.Name, Level: bestLv}
		if best >= 0 {
			used[best] = true
			it := res.Items[best]
			ir.Got = it.Name
			ir.GotStrength, ir.GotFrequency = it.Strength, it.Frequency
			ir.NoteStyle, ir.Uncertain = it.NoteStyle, it.UncertainFields
			ir.StrengthOK = sameNumbers(e.Strength, it.Strength)
			// The sheet writes "1 tableta při bolesti" as one field; the model
			// may split it into amount and frequency.
			ir.FrequencyOK = sameValue(e.Frequency, it.Frequency) ||
				(it.Amount != nil && it.Frequency != nil && sameValue(e.Frequency, ptr(*it.Amount+" "+*it.Frequency)))
			// Frequency is left out: handwritten copies rewrite "1-1-1" as
			// "3x denně", which is faithful but not comparable.
			ir.SilentProblem = (bestLv == lvFuzzy && !slices.Contains(it.UncertainFields, "name")) ||
				(!ir.StrengthOK && it.Strength != nil && !slices.Contains(it.UncertainFields, "strength"))
		}
		r.Items = append(r.Items, ir)
	}
	for i, it := range res.Items {
		if !used[i] {
			r.Extra = append(r.Extra, it.Name)
			if extraKind(it.Name, expected) == "invented" {
				r.Invented = append(r.Invented, it.Name)
			}
		}
	}
}

// extraKind sorts an unmatched item: "split" (an alternative brand the model
// split off into its own item), "near" (a misread of an expected name) or
// "invented" (a different drug: the dangerous case).
func extraKind(name string, expected []gtItem) string {
	g := letters(name)
	for _, e := range expected {
		if e.Alternative != nil {
			if a := letters(*e.Alternative); a == g || prefixOf(g, a) || close(g, a) {
				return "split"
			}
		}
	}
	for _, e := range expected {
		n := letters(e.Name)
		if close(g, n) || prefixOf(g, n) || levenshtein(g, n) <= len([]rune(n))/3 {
			return "near"
		}
	}
	return "invented"
}

func ptr(s string) *string { return &s }

func rank(lv string) int {
	return slices.Index([]string{lvExact, lvNorm, lvPartial, lvAlt, lvFuzzy, lvMissing}, lv)
}

func nameLevel(e gtItem, got string) string {
	if collapse(got) == collapse(e.Name) {
		return lvExact
	}
	g, n := letters(got), letters(e.Name)
	if g == n {
		return lvNorm
	}
	if prefixOf(g, n) {
		return lvPartial
	}
	if e.Alternative != nil && (letters(*e.Alternative) == g || prefixOf(g, letters(*e.Alternative))) {
		return lvAlt
	}
	if close(g, n) || (e.Alternative != nil && close(g, letters(*e.Alternative))) {
		return lvFuzzy
	}
	return lvMissing
}

func prefixOf(a, b string) bool {
	if len(a) < 5 || len(b) < 5 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func close(a, b string) bool {
	d := levenshtein(a, b)
	return d <= max(2, len([]rune(b))/4)
}

func collapse(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

var strengthRe = regexp.MustCompile(`(?i)\d[\d,.\s/+]*\s*(mg|g|µg|μg|ug|mcg|ml|iu|ie|%|mikrogram\w*)?`)

// letters keeps lowercase ASCII letters only, after dropping strengths like
// "5 mg" that the model may include in the name.
func letters(s string) string {
	s = strengthRe.ReplaceAllString(s, " ")
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		r = fold(r)
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
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

// sameValue compares strengths or frequencies ignoring case, spaces and the
// µ/mikrogramů spelling.
func sameValue(want, got *string) bool {
	if want == nil {
		return got == nil
	}
	if got == nil {
		return false
	}
	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.NewReplacer("mikrogramů", "µg", "mikrogramy", "µg", "μ", "µ", "mcg", "µg", ".", ",").Replace(s)
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	return norm(*want) == norm(*got)
}

var numberRe = regexp.MustCompile(`\d+(?:[,.]\d+)?`)

// sameNumbers compares the numbers in a strength ("20 mg / 10 mg" vs "20/10"):
// handwritten copies often drop units, which is faithful, not an error.
func sameNumbers(want, got *string) bool {
	if want == nil || got == nil {
		return want == got
	}
	nums := func(s string) string {
		return strings.ReplaceAll(strings.Join(numberRe.FindAllString(s, -1), " "), ".", ",")
	}
	return nums(*want) == nums(*got)
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func summarize(results []runResult) {
	type key struct{ model, variant string }
	type agg struct {
		runs, errors, items, extra, inv  int
		levels                           map[string]int
		strengthOK, freqOK, silent, flag int
		latency                          []int64
		tokens                           int
		styleWrong                       int
	}
	aggs := map[key]*agg{}
	for _, r := range results {
		k := key{r.Model, r.Variant}
		a := aggs[k]
		if a == nil {
			a = &agg{levels: map[string]int{}}
			aggs[k] = a
		}
		a.runs++
		if r.Error != "" {
			a.errors++
			continue
		}
		a.latency = append(a.latency, r.LatencyMS)
		a.tokens += r.Tokens
		a.extra += len(r.Extra)
		a.inv += len(r.Invented)
		for _, it := range r.Items {
			a.items++
			a.levels[it.Level]++
			if it.Level == lvMissing {
				continue
			}
			if it.StrengthOK {
				a.strengthOK++
			}
			if it.FrequencyOK {
				a.freqOK++
			}
			if it.SilentProblem {
				a.silent++
			}
			if len(it.Uncertain) > 0 {
				a.flag++
			}
			if it.NoteStyle == nil || *it.NoteStyle != r.Variant {
				a.styleWrong++
			}
		}
	}

	keys := slices.SortedFunc(func(yield func(key) bool) {
		for k := range aggs {
			if !yield(k) {
				return
			}
		}
	}, func(a, b key) int { return cmp.Or(cmp.Compare(a.model, b.model), cmp.Compare(a.variant, b.variant)) })

	pct := func(n, d int) string {
		if d == 0 {
			return "-"
		}
		return fmt.Sprintf("%d%%", 100*n/d)
	}
	fmt.Printf("%-20s %-12s %5s %6s %6s %6s %6s %6s %6s %6s %6s %6s %6s %6s %6s %6s %7s %7s\n",
		"model", "variant", "items", "exact", "norm", "part", "alt", "fuzzy", "miss", "extra", "inv", "str", "freq", "flag", "silent", "style", "p50ms", "p90ms")
	for _, k := range keys {
		a := aggs[k]
		found := a.items - a.levels[lvMissing]
		slices.Sort(a.latency)
		p := func(q float64) int64 {
			if len(a.latency) == 0 {
				return 0
			}
			return a.latency[int(q*float64(len(a.latency)-1))]
		}
		fmt.Printf("%-20s %-12s %5d %6s %6s %6s %6s %6s %6s %6d %6d %6s %6s %6s %6s %6s %7d %7d\n",
			k.model, k.variant, a.items,
			pct(a.levels[lvExact], a.items), pct(a.levels[lvNorm], a.items), pct(a.levels[lvPartial], a.items), pct(a.levels[lvAlt], a.items),
			pct(a.levels[lvFuzzy], a.items), pct(a.levels[lvMissing], a.items), a.extra, a.inv,
			pct(a.strengthOK, found), pct(a.freqOK, found), pct(a.flag, found), pct(a.silent, found),
			pct(a.styleWrong, found), p(0.5), p(0.9))
		if a.errors > 0 {
			fmt.Printf("  %d of %d calls failed\n", a.errors, a.runs)
		}
	}
	fmt.Println(`
exact/norm/part/alt/fuzzy/miss: share of expected items by best name match
  (part = name without/with manufacturer suffix, alt = the bracketed alternative brand)
  (fuzzy = close but differently spelled: a misread, or a faithful copy of a typo)
str: same numbers in strength as ground truth; freq: same frequency text (of found items)
flag: found items with any uncertain_fields
silent: found items with a misread name or wrong strength numbers NOT flagged as uncertain
extra: returned items matching nothing expected; inv: of those, a different drug
  (not a split-off alternative brand, not a near misread): the dangerous case
style: note_style differs from the photo variant`)
}
