// Package ai is a thin client over the OpenAI-compatible chat completions
// endpoint that reads medications from a single photo.
package ai

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

//go:embed prompts/*.txt
var promptFS embed.FS

var (
	// ErrInvalidOutput: the model answered twice, but not with valid JSON.
	ErrInvalidOutput = errors.New("model returned invalid output")
	// ErrUpstream: the gateway or model failed (5xx, timeout, refusal, 4xx).
	ErrUpstream = errors.New("model call failed")
)

// Item is one medication as the model read it. id, kind and source are
// added by the caller.
type Item struct {
	Name            string   `json:"name"`
	ActiveSubstance *string  `json:"active_substance"`
	Strength        *string  `json:"strength"`
	Form            *string  `json:"form"`
	Frequency       *string  `json:"frequency"`
	Amount          *string  `json:"amount"`
	NoteStyle       *string  `json:"note_style"`
	UncertainFields []string `json:"uncertain_fields"`
}

type Result struct {
	Items            []Item `json:"medications"`
	UnreadableItems  int    `json:"unreadable_items"`
	PromptTokens     int    `json:"-"`
	CompletionTokens int    `json:"-"`
}

type Client struct {
	// Detail is the image_url detail level ("low", "high", "auto"); empty
	// leaves it to the model's default.
	Detail string

	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Extract reads one image (JPEG or PNG bytes, mime e.g. "image/jpeg") for an
// analyze source (box, note, regimen). It retries once on invalid JSON, 5xx
// or timeout, and never more.
func (c *Client) Extract(ctx context.Context, model, source, mime string, img []byte) (Result, error) {
	prompt, err := systemPrompt(source)
	if err != nil {
		return Result{}, err
	}
	body, err := json.Marshal(buildRequest(model, prompt, mime, c.Detail, img))
	if err != nil {
		return Result{}, err
	}

	var res Result
	var promptTotal, completionTotal int
	for attempt := range 2 {
		var retryable bool
		res, retryable, err = c.call(ctx, body)
		promptTotal += res.PromptTokens
		completionTotal += res.CompletionTokens
		if err == nil || !retryable || ctx.Err() != nil || attempt == 1 {
			break
		}
	}
	res.PromptTokens, res.CompletionTokens = promptTotal, completionTotal
	return res, err
}

func systemPrompt(source string) (string, error) {
	common, err := promptFS.ReadFile("prompts/common.txt")
	if err != nil {
		return "", err
	}
	specific, err := promptFS.ReadFile("prompts/" + source + ".txt")
	if err != nil {
		return "", fmt.Errorf("unknown source %q", source)
	}
	return string(common) + "\n" + string(specific), nil
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
			Refusal *string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// call does one request. retryable reports whether a second attempt may help.
func (c *Client) call(ctx context.Context, body []byte) (res Result, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return res, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		// Network error or the 60 s timeout.
		return res, true, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return res, true, fmt.Errorf("%w: reading body: %v", ErrUpstream, err)
	}
	if resp.StatusCode >= 500 {
		return res, true, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		// The gateway's error body may echo request details; keep only the status.
		return res, false, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode)
	}

	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return res, true, fmt.Errorf("%w: response envelope: %v", ErrInvalidOutput, err)
	}
	res.PromptTokens = cr.Usage.PromptTokens
	res.CompletionTokens = cr.Usage.CompletionTokens
	if len(cr.Choices) == 0 {
		return res, true, fmt.Errorf("%w: no choices", ErrInvalidOutput)
	}
	msg := cr.Choices[0].Message
	if msg.Refusal != nil && *msg.Refusal != "" {
		return res, false, fmt.Errorf("%w: model refused", ErrUpstream)
	}
	if msg.Content == nil {
		return res, true, fmt.Errorf("%w: empty content (finish_reason=%s)", ErrInvalidOutput, cr.Choices[0].FinishReason)
	}

	var out Result
	if err := json.Unmarshal([]byte(*msg.Content), &out); err != nil {
		return res, true, fmt.Errorf("%w: content (finish_reason=%s)", ErrInvalidOutput, cr.Choices[0].FinishReason)
	}
	if err := out.validate(); err != nil {
		return res, true, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	out.PromptTokens, out.CompletionTokens = res.PromptTokens, res.CompletionTokens
	return out, false, nil
}

// validate checks what strict mode should already guarantee, in case the
// gateway or model does not honour it.
func (r *Result) validate() error {
	if r.Items == nil {
		return errors.New("medications missing")
	}
	if r.UnreadableItems < 0 {
		return errors.New("negative unreadable_items")
	}
	for i, it := range r.Items {
		if it.Name == "" {
			return fmt.Errorf("item %d: empty name", i)
		}
		if it.NoteStyle != nil && *it.NoteStyle != "printed" && *it.NoteStyle != "handwritten" {
			return fmt.Errorf("item %d: bad note_style", i)
		}
		if r.Items[i].UncertainFields == nil {
			r.Items[i].UncertainFields = []string{}
		}
	}
	return nil
}

func buildRequest(model, prompt, mime, detail string, img []byte) map[string]any {
	image := map[string]any{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img)}
	if detail != "" {
		image["detail"] = detail
	}
	return map[string]any{
		"model": model,
		"messages": []any{
			map[string]any{"role": "system", "content": prompt},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Přepiš léky z této fotky podle pravidel."},
				map[string]any{"type": "image_url", "image_url": image},
			}},
		},
		// Reasoning models reject max_tokens and temperature; see docs/notes.md.
		"max_completion_tokens": 8000,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "medications",
				"strict": true,
				"schema": json.RawMessage(schema),
			},
		},
	}
}

// schema is the strict-mode output schema: every property required,
// additionalProperties false, nullable fields as ["string","null"].
const schema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["medications", "unreadable_items"],
  "properties": {
    "medications": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["name", "active_substance", "strength", "form", "frequency", "amount", "note_style", "uncertain_fields"],
        "properties": {
          "name": {"type": "string"},
          "active_substance": {"type": ["string", "null"]},
          "strength": {"type": ["string", "null"]},
          "form": {"type": ["string", "null"]},
          "frequency": {"type": ["string", "null"]},
          "amount": {"type": ["string", "null"]},
          "note_style": {"type": ["string", "null"], "enum": ["printed", "handwritten", null]},
          "uncertain_fields": {
            "type": "array",
            "items": {"type": "string", "enum": ["name", "active_substance", "strength", "form", "frequency", "amount"]}
          }
        }
      }
    },
    "unreadable_items": {"type": "integer"}
  }
}`
