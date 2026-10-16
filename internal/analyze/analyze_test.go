package analyze

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"pillgaze/internal/ai"
	"pillgaze/internal/catalog"
	"pillgaze/internal/httpx"
	"pillgaze/internal/model"
)

var jpeg = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{1}, 100)...)

const goodContent = `{"medications":[{"name":"Prestarim Neo","active_substance":null,"strength":"5 mg","form":null,"frequency":"1-0-0","amount":null,"note_style":"handwritten","uncertain_fields":["amount"]}],"unreadable_items":1}`

// fakeAI answers chat completions from a list of scripted responses, one per call.
func fakeAI(t *testing.T, replies ...func(w http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		if r.URL.Path != "/chat/completions" || r.Header.Get("api-key") != "k" {
			t.Errorf("unexpected request %s, api-key %q", r.URL.Path, r.Header.Get("api-key"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		rf, _ := body["response_format"].(map[string]any)
		if rf["type"] != "json_schema" {
			t.Errorf("response_format = %v", rf)
		}
		if n >= len(replies) {
			t.Errorf("unexpected call %d", n+1)
			w.WriteHeader(500)
			return
		}
		replies[n](w)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func content(c string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": c},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20},
		})
	}
}

func status(code int) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(code) }
}

type part struct {
	name, filename string
	data           []byte
}

func form(t *testing.T, parts ...part) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		if p.filename == "" {
			mw.WriteField(p.name, string(p.data))
			continue
		}
		fw, _ := mw.CreateFormFile(p.name, p.filename)
		fw.Write(p.data)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "/api/analyze", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func photo(field string, b []byte) part { return part{name: field, filename: "a.jpg", data: b} }
func note(b []byte) part                { return photo("note", b) }
func box(b []byte) part                 { return photo("box", b) }

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	httpx.BodyLimit(MaxBodyBytes)(h).ServeHTTP(w, r)
	return w
}

var testCatalog = func() *catalog.Catalog {
	c, err := catalog.Load()
	if err != nil {
		panic(err)
	}
	return c
}()

func realHandler(srv *httptest.Server) *Handler {
	return &Handler{AI: ai.New(srv.URL, "k"), Model: "model-a", Catalog: testCatalog}
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	return e.Error.Code
}

func decode(t *testing.T, w *httptest.ResponseRecorder) model.AnalyzeResponse {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var resp model.AnalyzeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// bySource answers with the reply for the photo kind named in the prompt,
// since calls for different photos run in parallel.
func bySource(t *testing.T, replies map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		prompt, _ := body.Messages[0].Content.(string)
		src := "note"
		if strings.Contains(prompt, "krabičk") {
			src = "box"
		}
		content(replies[src])(w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAnalyzeSuccess(t *testing.T) {
	srv, calls := fakeAI(t, content(goodContent), content(goodContent))
	resp := decode(t, serve(realHandler(srv), form(t, note(jpeg), note(jpeg))))

	if calls.Load() != 2 {
		t.Errorf("calls = %d, want one per image", calls.Load())
	}
	if len(resp.Medications) != 2 || resp.UnreadableItems != 2 {
		t.Fatalf("got %d meds, %d unreadable", len(resp.Medications), resp.UnreadableItems)
	}
	m := resp.Medications[0]
	if !strings.HasPrefix(m.ID, "med_") || m.ID == resp.Medications[1].ID {
		t.Errorf("ids %q %q", m.ID, resp.Medications[1].ID)
	}
	// The typo survives untouched; the catalogue only offers candidates.
	if m.Name != "Prestarim Neo" || m.Kind != "drug" || m.Source != "note" {
		t.Errorf("med = %+v", m)
	}
	if m.NoteStyle == nil || *m.NoteStyle != "handwritten" || m.ActiveSubstance != nil {
		t.Errorf("note_style/active_substance = %v/%v", m.NoteStyle, m.ActiveSubstance)
	}
	if m.Catalog == nil || m.Catalog.Status != catalog.StatusNeedsConfirmation || m.Catalog.Candidates[0].Name != "PRESTARIUM NEO" {
		t.Errorf("catalog = %+v", m.Catalog)
	}
}

const notePrestarium = `{"medications":[
	{"name":"Prestarium Neo","active_substance":null,"strength":"5 mg","form":null,"frequency":"1-0-0","amount":"1 tbl","note_style":"handwritten","uncertain_fields":[]},
	{"name":"WARFARIN ORION","active_substance":null,"strength":"5 mg","form":null,"frequency":"0-0-1","amount":null,"note_style":"printed","uncertain_fields":[]}],
	"unreadable_items":0}`

const boxPrestarium = `{"medications":[
	{"name":"Prestarium Neo 5 mg","active_substance":"perindoprilum argininum","strength":"5 mg","form":"potahované tablety","frequency":null,"amount":null,"note_style":null,"uncertain_fields":[]}],
	"unreadable_items":0}`

func TestAnalyzeNoteAndBox(t *testing.T) {
	srv := bySource(t, map[string]string{"note": notePrestarium, "box": boxPrestarium})
	resp := decode(t, serve(realHandler(srv), form(t, note(jpeg), box(jpeg))))
	if len(resp.Medications) != 2 {
		t.Fatalf("got %d items, want 2 (prestarium merged): %s", len(resp.Medications), mustJSON(resp))
	}
	p := resp.Medications[0]
	if p.Source != "box" || strings.Join(p.Sources, ",") != "box,note" || p.Frequency == nil || *p.Frequency != "1-0-0" {
		t.Errorf("merged prestarium: %s", mustJSON(p))
	}
	if p.Catalog.Status != catalog.StatusMatched || p.NoteStyle == nil {
		t.Errorf("prestarium catalog/note_style: %s", mustJSON(p))
	}
	// Warfarin is only on the note: its active substance comes from the catalogue.
	w := resp.Medications[1]
	if w.ActiveSubstance == nil || !strings.Contains(*w.ActiveSubstance, "warfarin") {
		t.Errorf("warfarin active_substance: %s", mustJSON(w))
	}
}

func TestAnalyzeBoxDropsNoteStyle(t *testing.T) {
	srv, _ := fakeAI(t, content(goodContent))
	w := serve(realHandler(srv), form(t, box(jpeg)))
	if w.Code != 200 || strings.Contains(w.Body.String(), "note_style") {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestAnalyzeRegimen(t *testing.T) {
	srv, _ := fakeAI(t, content(`{"medications":[{"name":"FOLFOX","active_substance":null,"strength":null,"form":null,"frequency":null,"amount":null,"note_style":null,"uncertain_fields":[]}],"unreadable_items":0}`))
	resp := decode(t, serve(realHandler(srv), form(t, photo("regimen", jpeg))))
	m := resp.Medications[0]
	if m.Kind != "regimen" || m.Source != "note" || m.Catalog != nil {
		t.Fatalf("regimen: %s", mustJSON(m))
	}
}

func TestAnalyzeRetryInvalidJSON(t *testing.T) {
	srv, calls := fakeAI(t, content(`{"medications": [`), content(goodContent))
	w := serve(realHandler(srv), form(t, box(jpeg)))
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyzeInvalidJSONTwice(t *testing.T) {
	srv, calls := fakeAI(t, content("nope"), content("still nope"))
	w := serve(realHandler(srv), form(t, box(jpeg)))
	if w.Code != 422 || calls.Load() != 2 || errCode(t, w) != httpx.CodeNothingFound {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyze5xxTwice(t *testing.T) {
	srv, calls := fakeAI(t, status(500), status(503))
	w := serve(realHandler(srv), form(t, box(jpeg)))
	if w.Code != 502 || calls.Load() != 2 || errCode(t, w) != httpx.CodeModelError {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyze4xxNoRetry(t *testing.T) {
	srv, calls := fakeAI(t, status(400))
	w := serve(realHandler(srv), form(t, box(jpeg)))
	if w.Code != 502 || calls.Load() != 1 {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyzeNothingFound(t *testing.T) {
	srv, _ := fakeAI(t, content(`{"medications":[],"unreadable_items":0}`))
	w := serve(realHandler(srv), form(t, box(jpeg)))
	if w.Code != 422 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestAnalyzeOnlyUnreadable(t *testing.T) {
	srv, _ := fakeAI(t, content(`{"medications":[],"unreadable_items":2}`))
	w := serve(realHandler(srv), form(t, note(jpeg)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"medications":[]`) {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestAnalyzeInputErrors(t *testing.T) {
	h := &Handler{Mock: true, Catalog: testCatalog}
	png := []byte("\x89PNG\r\n\x1a\n0000000000")
	big := append(append([]byte{}, jpeg...), make([]byte, MaxImageBytes)...)
	eleven := []part{}
	for range MaxImages + 1 {
		eleven = append(eleven, note(jpeg))
	}
	tests := []struct {
		name  string
		parts []part
		want  int
	}{
		{"png ok", []part{box(png)}, 200},
		{"note and box", []part{note(jpeg), box(jpeg), photo("regimen", jpeg)}, 200},
		{"too large", []part{box(big)}, 413},
		{"too large in total", []part{box(big[:8<<20]), note(big[:8<<20])}, 413},
		{"too many images", eleven, 413},
		{"wrong type", []part{box([]byte("GIF89a......"))}, 415},
		{"jpeg name but text body", []part{{name: "box", filename: "x.jpg", data: []byte("hello")}}, 415},
		{"old source field", []part{{name: "source", data: []byte("box")}, photo("image", jpeg)}, 400},
		{"old image field", []part{photo("image", jpeg)}, 400},
		{"no image", []part{{name: "comment", data: []byte("x")}}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := serve(h, form(t, tt.parts...)); w.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body)
			}
		})
	}

	r := httptest.NewRequest("POST", "/api/analyze", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	if w := serve(h, r); w.Code != 400 {
		t.Fatalf("json body: status %d", w.Code)
	}
}

func TestAnalyzeMock(t *testing.T) {
	h := &Handler{Mock: true, Catalog: testCatalog} // no AI client: any model call would panic
	resp := decode(t, serve(h, form(t, note(jpeg), box(jpeg), box(jpeg))))
	status := map[string]string{}
	for _, m := range resp.Medications {
		status[m.Name] = m.Catalog.Status
	}
	want := map[string]string{
		"Prestarium Neo 5 mg": "matched",            // merged note + box
		"Warfarin Orion 5 mg": "matched",            // merged, strength conflict
		"Paralen 500":         "not_found",          // not in the catalogue
		"Ibuprofen 400":       "needs_confirmation", // generic name
		"Pantomil":            "needs_confirmation", // unsure handwriting
	}
	for name, st := range want {
		if status[name] != st {
			t.Errorf("%s: %q, want %q (all: %v)", name, status[name], st, status)
		}
	}
	if len(resp.Medications) != len(want) {
		t.Logf("%s", mustJSON(resp))
		t.Errorf("%d items, want %d", len(resp.Medications), len(want))
	}
	for _, m := range resp.Medications {
		if m.Name == "Warfarin Orion 5 mg" && (len(m.Conflicts) != 1 || m.Conflicts[0].Field != "strength") {
			t.Errorf("warfarin conflicts: %s", mustJSON(m))
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

const boxUnknown = `{"medications":[
	{"name":"Zzyxor 10 mg","active_substance":"zzyxorum","strength":"10 mg","form":null,"frequency":null,"amount":null,"note_style":null,"uncertain_fields":[],"box_match":null}],
	"unreadable_items":0}`

func noteLine(name, strength, boxMatch string, uncertain string) string {
	bm := "null"
	if boxMatch != "" {
		bm = `"` + boxMatch + `"`
	}
	unc := "[]"
	if uncertain != "" {
		unc = `["` + uncertain + `"]`
	}
	return `{"medications":[{"name":"` + name + `","active_substance":null,"strength":"` + strength +
		`","form":null,"frequency":"1-0-0","amount":null,"note_style":"handwritten","uncertain_fields":` + unc +
		`,"box_match":` + bm + `}],"unreadable_items":0}`
}

// A note line the model links to a box joins it even when the box is not in
// the catalogue; the box names reach the note call.
func TestAnalyzeNoteLinkedToBox(t *testing.T) {
	tests := []struct {
		name, note string
		items      int
	}{
		{"linked by model", noteLine("Zyxr", "20 mg", "Zzyxor 10 mg", "name"), 1},
		{"same brand words", noteLine("Zzyxor", "10 mg", "", ""), 1},
		{"brand words but uncertain", noteLine("Zzyxor", "10 mg", "", "name"), 2},
		{"link to unknown box dropped", noteLine("Zyxr", "10 mg", "Other box", ""), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var noteAsk atomic.Value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct {
						Content any `json:"content"`
					} `json:"messages"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if prompt, _ := body.Messages[0].Content.(string); strings.Contains(prompt, "krabičk") {
					content(boxUnknown)(w)
					return
				}
				user, _ := json.Marshal(body.Messages[1].Content)
				noteAsk.Store(string(user))
				content(tt.note)(w)
			}))
			t.Cleanup(srv.Close)

			resp := decode(t, serve(realHandler(srv), form(t, note(jpeg), box(jpeg))))
			if ask, _ := noteAsk.Load().(string); !strings.Contains(ask, "Zzyxor 10 mg") {
				t.Errorf("note call did not list the box: %s", ask)
			}
			if len(resp.Medications) != tt.items {
				t.Fatalf("got %d items, want %d: %s", len(resp.Medications), tt.items, mustJSON(resp))
			}
			if tt.items == 1 {
				m := resp.Medications[0]
				if m.Name != "Zzyxor 10 mg" || strings.Join(m.Sources, ",") != "box,note" || m.Frequency == nil {
					t.Errorf("merged: %s", mustJSON(m))
				}
				if tt.name == "linked by model" && (len(m.Conflicts) != 1 || m.Conflicts[0].Field != "strength") {
					t.Errorf("want a strength conflict only: %s", mustJSON(m))
				}
			}
		})
	}
}
