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

func src(s string) part { return part{name: "source", data: []byte(s)} }
func img(b []byte) part { return part{name: "image", filename: "a.jpg", data: b} }
func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	httpx.BodyLimit(MaxBodyBytes)(h).ServeHTTP(w, r)
	return w
}

func realHandler(srv *httptest.Server) *Handler {
	return &Handler{AI: ai.New(srv.URL, "k"), Model: "model-a"}
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

func TestAnalyzeSuccess(t *testing.T) {
	srv, calls := fakeAI(t, content(goodContent), content(goodContent))
	w := serve(realHandler(srv), form(t, src("note"), img(jpeg), img(jpeg)))

	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want one per image", calls.Load())
	}
	var resp model.AnalyzeResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Medications) != 2 || resp.UnreadableItems != 2 {
		t.Fatalf("got %d meds, %d unreadable", len(resp.Medications), resp.UnreadableItems)
	}
	m := resp.Medications[0]
	if !strings.HasPrefix(m.ID, "med_") || m.ID == resp.Medications[1].ID {
		t.Errorf("ids %q %q", m.ID, resp.Medications[1].ID)
	}
	// The typo must survive untouched.
	if m.Name != "Prestarim Neo" || m.Kind != "drug" || m.Source != "note" {
		t.Errorf("med = %+v", m)
	}
	if m.NoteStyle == nil || *m.NoteStyle != "handwritten" || m.ActiveSubstance != nil {
		t.Errorf("note_style/active_substance = %v/%v", m.NoteStyle, m.ActiveSubstance)
	}
	if !strings.Contains(w.Body.String(), `"active_substance":null`) {
		t.Errorf("null fields must be present: %s", w.Body)
	}
}

func TestAnalyzeBoxDropsNoteStyle(t *testing.T) {
	srv, _ := fakeAI(t, content(goodContent))
	w := serve(realHandler(srv), form(t, src("box"), img(jpeg)))
	if w.Code != 200 || strings.Contains(w.Body.String(), "note_style") {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestAnalyzeRegimen(t *testing.T) {
	srv, _ := fakeAI(t, content(`{"medications":[{"name":"FOLFOX","active_substance":null,"strength":null,"form":null,"frequency":null,"amount":null,"note_style":null,"uncertain_fields":[]}],"unreadable_items":0}`))
	w := serve(realHandler(srv), form(t, src("regimen"), img(jpeg)))
	var resp model.AnalyzeResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != 200 || resp.Medications[0].Kind != "regimen" || resp.Medications[0].Source != "note" {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestAnalyzeRetryInvalidJSON(t *testing.T) {
	srv, calls := fakeAI(t, content(`{"medications": [`), content(goodContent))
	w := serve(realHandler(srv), form(t, src("box"), img(jpeg)))
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyzeInvalidJSONTwice(t *testing.T) {
	srv, calls := fakeAI(t, content("nope"), content("still nope"))
	w := serve(realHandler(srv), form(t, src("box"), img(jpeg)))
	if w.Code != 422 || calls.Load() != 2 || errCode(t, w) != httpx.CodeNothingFound {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyze5xxTwice(t *testing.T) {
	srv, calls := fakeAI(t, status(500), status(503))
	w := serve(realHandler(srv), form(t, src("box"), img(jpeg)))
	if w.Code != 502 || calls.Load() != 2 || errCode(t, w) != httpx.CodeModelError {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyze4xxNoRetry(t *testing.T) {
	srv, calls := fakeAI(t, status(400))
	w := serve(realHandler(srv), form(t, src("box"), img(jpeg)))
	if w.Code != 502 || calls.Load() != 1 {
		t.Fatalf("status %d, calls %d", w.Code, calls.Load())
	}
}

func TestAnalyzeNothingFound(t *testing.T) {
	srv, _ := fakeAI(t, content(`{"medications":[],"unreadable_items":0}`))
	w := serve(realHandler(srv), form(t, src("box"), img(jpeg)))
	if w.Code != 422 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestAnalyzeOnlyUnreadable(t *testing.T) {
	srv, _ := fakeAI(t, content(`{"medications":[],"unreadable_items":2}`))
	w := serve(realHandler(srv), form(t, src("note"), img(jpeg)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"medications":[]`) {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestAnalyzeInputErrors(t *testing.T) {
	h := &Handler{Mock: true}
	png := []byte("\x89PNG\r\n\x1a\n0000000000")
	big := append(append([]byte{}, jpeg...), make([]byte, MaxImageBytes)...)
	seven := []part{src("box")}
	for range 7 {
		seven = append(seven, img(jpeg))
	}
	tests := []struct {
		name  string
		parts []part
		want  int
	}{
		{"png ok", []part{src("box"), img(png)}, 200},
		{"too large", []part{src("box"), img(big)}, 413},
		{"too large in total", []part{src("box"), img(big[:6<<20]), img(big[:5<<20])}, 413},
		{"too many images", seven, 413},
		{"wrong type", []part{src("box"), img([]byte("GIF89a......"))}, 415},
		{"jpeg header but text body", []part{src("box"), {name: "image", filename: "x.jpg", data: []byte("hello")}}, 415},
		{"missing source", []part{img(jpeg)}, 400},
		{"bad source", []part{src("manual"), img(jpeg)}, 400},
		{"no image", []part{src("box")}, 400},
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
	h := &Handler{Mock: true} // no AI client: any model call would panic
	for _, source := range []string{"box", "note", "regimen"} {
		w := serve(h, form(t, src(source), img(jpeg)))
		var resp model.AnalyzeResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != 200 {
			t.Fatalf("%s: status %d, err %v", source, w.Code, err)
		}
		if len(resp.Medications) < 3 {
			t.Errorf("%s: %d items", source, len(resp.Medications))
		}
	}
}
