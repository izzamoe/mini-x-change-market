// Package response provides standardised JSON response helpers for HTTP handlers.
package response

import (
	"encoding/json"
	"net/http"
)

// envelope is the top-level JSON wrapper for every API response.
type envelope struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *apiError   `json:"error,omitempty"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// apiError carries machine-readable error details.
type apiError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

// FieldError describes a validation failure for a specific request field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Meta carries pagination information.
type Meta struct {
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Pages   int   `json:"pages"`
}

// JSON writes a JSON-encoded value with the given HTTP status code.
func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		Success: true,
		Data:    data,
	})
}

// Paginated writes a JSON-encoded paginated list response.
func Paginated(w http.ResponseWriter, status int, data any, meta Meta) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		Success: true,
		Data:    data,
		Meta:    &meta,
	})
}

// Error writes a JSON-encoded error response.
func Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		Success: false,
		Error: &apiError{
			Code:    code,
			Message: message,
		},
	})
}

// ValidationError writes a 422 JSON response with per-field validation errors.
func ValidationError(w http.ResponseWriter, details []FieldError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(envelope{
		Success: false,
		Error: &apiError{
			Code:    "VALIDATION_ERROR",
			Message: "request validation failed",
			Details: details,
		},
	})
}

// NewMeta builds a Meta from total count + pagination params.
func NewMeta(total int64, page, perPage int) Meta {
	pages := 0
	if perPage > 0 {
		pages = int((total + int64(perPage) - 1) / int64(perPage))
	}
	return Meta{
		Total:   total,
		Page:    page,
		PerPage: perPage,
		Pages:   pages,
	}
}
