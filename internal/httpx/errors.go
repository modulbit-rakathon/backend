// Package httpx holds the HTTP middleware and response helpers.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Error codes used in {"error": {"code": ...}}.
const (
	CodeInvalidInput     = "invalid_input"
	CodeUnauthorized     = "unauthorized"
	CodeNotFound         = "not_found"
	CodeTooLarge         = "payload_too_large"
	CodeUnsupportedMedia = "unsupported_media_type"
	CodeNothingFound     = "nothing_found"
	CodeRateLimited      = "rate_limited"
	CodeModelError       = "model_error"
	CodeUnavailable      = "unavailable"
	CodeInternal         = "internal"
)

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// WriteError writes the contract's error envelope.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	var b errorBody
	b.Error.Code = code
	b.Error.Message = message
	WriteJSON(w, status, b)
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// DecodeJSON reads one JSON value, rejecting unknown fields. It returns the
// status to answer with on failure (400, or 413 past the body limit).
func DecodeJSON(r *http.Request, v any) (int, error) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return http.StatusRequestEntityTooLarge, errors.New("request body too large")
		}
		// The decoder error names the unknown field, never a value.
		return http.StatusBadRequest, fmt.Errorf("invalid JSON body: %v", err)
	}
	if dec.More() {
		return http.StatusBadRequest, errors.New("invalid JSON body: trailing data")
	}
	return 0, nil
}
