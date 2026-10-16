// Package analyze implements POST /api/analyze.
package analyze

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"

	"pillgaze/internal/ai"
	"pillgaze/internal/fixtures"
	"pillgaze/internal/httpx"
	"pillgaze/internal/model"
)

const (
	MaxImages     = 6
	MaxImageBytes = 10 << 20
	// MaxBodyBytes leaves room for multipart framing around the images.
	MaxBodyBytes = MaxImageBytes + 1<<20
)

// Extractor reads medications from one image. *ai.Client implements it.
type Extractor interface {
	Extract(ctx context.Context, model, source, mime string, img []byte) (ai.Result, error)
}

type Handler struct {
	AI    Extractor // nil in mock mode
	Model string
	Mock  bool
}

var validSources = map[string]bool{"box": true, "note": true, "regimen": true}

type image struct {
	mime string
	data []byte
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	source, images, status, err := readForm(r)
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

	if h.Mock {
		resp, _ := fixtures.Analyze(source)
		httpx.WriteJSON(w, http.StatusOK, resp)
		return
	}

	resp, err := h.extractAll(r.Context(), source, images)
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
	if len(resp.Medications) == 0 && resp.UnreadableItems == 0 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeNothingFound, "no medications found in the photo")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// extractAll calls the model once per image in parallel and merges the
// results in image order. The first failure cancels the remaining calls.
func (h *Handler) extractAll(ctx context.Context, source string, images []image) (model.AnalyzeResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]ai.Result, len(images))
	errs := make([]error, len(images))
	var wg sync.WaitGroup
	for i, img := range images {
		wg.Go(func() {
			results[i], errs[i] = h.AI.Extract(ctx, h.Model, source, img.mime, img.data)
			if errs[i] != nil {
				cancel()
			}
		})
	}
	wg.Wait()

	info := httpx.Info(ctx)
	for _, res := range results {
		info.AddUsage(h.Model, res.PromptTokens, res.CompletionTokens)
	}
	if err := firstError(errs); err != nil {
		return model.AnalyzeResponse{}, err
	}

	kind, medSource := model.KindDrug, source
	if source == "regimen" {
		// Regimen names are read from a note or report.
		kind, medSource = model.KindRegimen, model.SourceNote
	}
	resp := model.AnalyzeResponse{Medications: []model.Medication{}}
	for _, res := range results {
		resp.UnreadableItems += res.UnreadableItems
		for _, it := range res.Items {
			m := model.Medication{
				ID:              newID(),
				Kind:            kind,
				Name:            it.Name,
				ActiveSubstance: it.ActiveSubstance,
				Strength:        it.Strength,
				Form:            it.Form,
				Frequency:       it.Frequency,
				Amount:          it.Amount,
				Source:          medSource,
				UncertainFields: it.UncertainFields,
			}
			if source == "note" {
				m.NoteStyle = it.NoteStyle
			}
			resp.Medications = append(resp.Medications, m)
		}
	}
	return resp, nil
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
func readForm(r *http.Request) (source string, images []image, status int, err error) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "multipart/form-data" {
		return "", nil, http.StatusBadRequest, errors.New("expected multipart/form-data")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return "", nil, http.StatusBadRequest, errors.New("malformed multipart body")
	}

	total := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, bodyErrStatus(err), errors.New("malformed multipart body")
		}
		switch part.FormName() {
		case "source":
			b, err := io.ReadAll(io.LimitReader(part, 32))
			if err != nil {
				return "", nil, bodyErrStatus(err), errors.New("cannot read source")
			}
			source = strings.TrimSpace(string(b))
		case "image":
			if len(images) == MaxImages {
				return "", nil, http.StatusRequestEntityTooLarge, errors.New("at most 6 images")
			}
			data, err := io.ReadAll(io.LimitReader(part, int64(MaxImageBytes-total+1)))
			if err != nil {
				return "", nil, bodyErrStatus(err), errors.New("cannot read image")
			}
			total += len(data)
			if total > MaxImageBytes {
				return "", nil, http.StatusRequestEntityTooLarge, errors.New("images exceed 10 MB in total")
			}
			// Decide by content, not by the part's Content-Type header.
			ct := http.DetectContentType(data)
			if ct != "image/jpeg" && ct != "image/png" {
				return "", nil, http.StatusUnsupportedMediaType, errors.New("images must be JPEG or PNG")
			}
			images = append(images, image{mime: ct, data: data})
		default:
			if _, err := io.Copy(io.Discard, part); err != nil {
				return "", nil, bodyErrStatus(err), errors.New("malformed multipart body")
			}
		}
	}

	if !validSources[source] {
		return "", nil, http.StatusBadRequest, errors.New("source must be box, note or regimen")
	}
	if len(images) == 0 {
		return "", nil, http.StatusBadRequest, errors.New("at least one image is required")
	}
	return source, images, 0, nil
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
