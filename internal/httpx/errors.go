// Package httpx holds the HTTP middleware and response helpers.
package httpx

import (
	"encoding/json"
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
