// Package analyze implements POST /api/analyze: read every photo, match the
// items to the catalogue and merge notes with boxes into one list.
package analyze

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync"

	"pillgaze/internal/ai"
	"pillgaze/internal/catalog"
	"pillgaze/internal/fixtures"
	"pillgaze/internal/httpx"
	"pillgaze/internal/model"
)

const (
	MaxImages     = 10
	MaxImageBytes = 15 << 20
	// MaxBodyBytes leaves room for multipart framing around the images.
	MaxBodyBytes = MaxImageBytes + 1<<20
)

// Form fields: each is repeatable and holds photos of that kind.
var sources = []string{"note", "box", "regimen"}

// Extractor reads medications from one image. *ai.Client implements it.
type Extractor interface {
	Extract(ctx context.Context, model, source, mime string, img []byte, boxes []string) (ai.Result, error)
}

type Handler struct {
	AI      Extractor // nil in mock mode
	Model   string
	Mock    bool
	Catalog *catalog.Catalog
}

type image struct {
	source string
	mime   string
	data   []byte
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	images, status, err := readForm(r)
	if err != nil {
		code := httpx.CodeInvalidInput
		switch status {
		case http.StatusRequestEntityTooLarge:
			code = httpx.CodeTooLarge
		case http.StatusUnsupportedMediaType:
			code = httpx.CodeUnsupportedMedia
		}
		httpx.WriteError(w, status, code, err.Error())
		return
	}
	httpx.Info(r.Context()).SetImages(len(images))

	results, err := h.extractAll(r.Context(), images)
	if err != nil {
		slog.Warn("analyze failed", "request_id", httpx.RequestID(r.Context()), "err", err)
	}
	switch {
	case errors.Is(err, ai.ErrInvalidOutput):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeNothingFound, "could not read the photo, try again")
		return
	case err != nil:
		httpx.WriteError(w, http.StatusBadGateway, httpx.CodeModelError, "model call failed")
		return
	}

	resp := h.build(images, results)
	if len(resp.Medications) == 0 && resp.UnreadableItems == 0 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeNothingFound, "no medications found in the photos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// extractAll calls the model once per image. Boxes and regimens go first, in
// parallel; notes follow with the box names read, so the model can link a
// hard-to-read line to its box. The first failure cancels the remaining
// calls. In mock mode every image of a kind returns that kind's fixture,
// once per kind.
func (h *Handler) extractAll(ctx context.Context, images []image) ([]ai.Result, error) {
	results := make([]ai.Result, len(images))
	if h.Mock {
		done := map[string]bool{}
		for i, img := range images {
			if !done[img.source] {
				results[i] = fixtures.Read(img.source)
				done[img.source] = true
			}
		}
		return results, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make([]error, len(images))
	run := func(notes bool, boxes []string) {
		var wg sync.WaitGroup
		for i, img := range images {
			if (img.source == "note") != notes {
				continue
			}
			wg.Go(func() {
				results[i], errs[i] = h.AI.Extract(ctx, h.Model, img.source, img.mime, img.data, boxes)
				if errs[i] != nil {
					cancel()
				}
			})
		}
		wg.Wait()
	}
	run(false, nil)
	if ctx.Err() == nil {
		run(true, boxNames(images, results))
	}

	info := httpx.Info(ctx)
	for _, res := range results {
		info.AddUsage(h.Model, res.PromptTokens, res.CompletionTokens)
	}
	return results, firstError(errs)
}

// boxNames lists the distinct box names read so far, in photo order.
func boxNames(images []image, results []ai.Result) []string {
	var names []string
	for i, res := range results {
		if images[i].source != "box" {
			continue
		}
		for _, it := range res.Items {
			if !slices.Contains(names, it.Name) {
				names = append(names, it.Name)
			}
		}
	}
	return names
}

// build turns model output into the response: ids, kinds and sources, a
// catalogue match per drug, active substance from the catalogue where the
// photo had none, then notes and boxes merged.
func (h *Handler) build(images []image, results []ai.Result) model.AnalyzeResponse {
	resp := model.AnalyzeResponse{Medications: []model.Medication{}}
	var meds []model.Medication
	var links []*string // per med: the box a note line was linked to
	for i, res := range results {
		source := images[i].source
		resp.UnreadableItems += res.UnreadableItems
		for _, it := range res.Items {
			m := model.Medication{
				ID:              newID(),
				Kind:            model.KindDrug,
				Name:            it.Name,
				ActiveSubstance: it.ActiveSubstance,
				Strength:        it.Strength,
				Form:            it.Form,
				Frequency:       it.Frequency,
				Amount:          it.Amount,
				Source:          source,
				UncertainFields: it.UncertainFields,
			}
			if m.UncertainFields == nil {
				m.UncertainFields = []string{}
			}
			switch source {
			case "note":
				m.NoteStyle = it.NoteStyle
			case "regimen":
				// Regimen names are read from a note or report.
				m.Kind, m.Source = model.KindRegimen, model.SourceNote
			}
			if m.Kind == model.KindDrug {
				m.Catalog = h.Catalog.Match(m.Name, m.Strength, slices.Contains(m.UncertainFields, "name"))
				if m.Catalog.Status == catalog.StatusMatched && m.ActiveSubstance == nil {
					as := m.Catalog.Product.ActiveSubstance
					m.ActiveSubstance = &as
				}
			}
			meds = append(meds, m)
			links = append(links, it.BoxMatch)
		}
	}
	if len(meds) > 0 {
		resp.Medications = h.Catalog.MergeLinked(meds, linkNotesToBoxes(meds, links))
	}
	return resp
}

// linkNotesToBoxes returns note item id -> box item id for each note line
// the model linked to a box read in the same request, and gives the line
// that box's catalogue result, so a misread name ("Keron" for Kreon) neither
// shows as a name conflict nor keeps a wrong match. The name stays as
// written and a differing strength still shows as a conflict. Each box takes
// at most one line.
//
// A line the model left unlinked still joins a box when its name was read
// with certainty and its brand words start the box's brand words
// ("Bisoprolol" and "Bisoprolol PMCS 10 mg"), and only one free box fits.
func linkNotesToBoxes(meds []model.Medication, boxMatch []*string) map[string]string {
	links := map[string]string{}
	taken := map[string]bool{}
	link := func(i int, box model.Medication) {
		taken[box.ID] = true
		links[meds[i].ID] = box.ID
		if box.Catalog != nil {
			m := *box.Catalog
			meds[i].Catalog = &m
		}
		if meds[i].ActiveSubstance == nil {
			meds[i].ActiveSubstance = box.ActiveSubstance
		}
	}
	isLine := func(m model.Medication) bool { return m.Source == model.SourceNote && m.Kind == model.KindDrug }
	isFreeBox := func(m model.Medication) bool { return m.Source == model.SourceBox && !taken[m.ID] }

	for i := range meds {
		if boxMatch[i] == nil || !isLine(meds[i]) {
			continue
		}
		for _, box := range meds {
			if isFreeBox(box) && box.Name == *boxMatch[i] {
				link(i, box)
				break
			}
		}
	}
	for i := range meds {
		if _, ok := links[meds[i].ID]; ok || !isLine(meds[i]) || slices.Contains(meds[i].UncertainFields, "name") {
			continue
		}
		words := strings.Fields(catalog.FamilyKey(meds[i].Name))
		if len(words) == 0 {
			continue
		}
		var fits []model.Medication
		for _, box := range meds {
			if isFreeBox(box) && hasPrefix(strings.Fields(catalog.FamilyKey(box.Name)), words) {
				fits = append(fits, box)
			}
		}
		if len(fits) == 1 {
			link(i, fits[0])
		}
	}
	return links
}

func hasPrefix(s, prefix []string) bool {
	return len(s) >= len(prefix) && slices.Equal(s[:len(prefix)], prefix)
}

// firstError prefers a real failure over the context.Canceled that sibling
// calls get after cancel().
func firstError(errs []error) error {
	var canceled error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if !errors.Is(err, context.Canceled) {
			return err
		}
		canceled = err
	}
	return canceled
}

// readForm streams the multipart body into memory; nothing touches disk
// (ParseMultipartForm would spill large files to temp files).
func readForm(r *http.Request) (images []image, status int, err error) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "multipart/form-data" {
		return nil, http.StatusBadRequest, errors.New("expected multipart/form-data")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("malformed multipart body")
	}

	total := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, bodyErrStatus(err), errors.New("malformed multipart body")
		}
		field := part.FormName()
		switch {
		case field == "source" || field == "image":
			return nil, http.StatusBadRequest, errors.New(`the "source" and "image" fields were replaced: send photos as "note", "box" or "regimen" fields`)
		case slices.Contains(sources, field):
			if len(images) == MaxImages {
				return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("at most %d images", MaxImages)
			}
			data, err := io.ReadAll(io.LimitReader(part, int64(MaxImageBytes-total+1)))
			if err != nil {
				return nil, bodyErrStatus(err), errors.New("cannot read image")
			}
			total += len(data)
			if total > MaxImageBytes {
				return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("images exceed %d MB in total", MaxImageBytes>>20)
			}
			// Decide by content, not by the part's Content-Type header.
			ct := http.DetectContentType(data)
			if ct != "image/jpeg" && ct != "image/png" {
				return nil, http.StatusUnsupportedMediaType, errors.New("images must be JPEG or PNG")
			}
			images = append(images, image{source: field, mime: ct, data: data})
		default:
			if _, err := io.Copy(io.Discard, part); err != nil {
				return nil, bodyErrStatus(err), errors.New("malformed multipart body")
			}
		}
	}
	if len(images) == 0 {
		return nil, http.StatusBadRequest, errors.New(`at least one image is required in a "note", "box" or "regimen" field`)
	}
	return images, 0, nil
}

func bodyErrStatus(err error) int {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func newID() string {
	return "med_" + strings.ToLower(rand.Text()[:10])
}
