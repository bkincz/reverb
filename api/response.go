package api

import (
	"encoding/json"
	"net/http"
)

// ---------------------------------------------------------------------------
// Codes
// ---------------------------------------------------------------------------

const (
	CodeValidationError = "VALIDATION_ERROR"
	CodeUnauthorized    = "UNAUTHORIZED"
	CodeForbidden       = "FORBIDDEN"
	CodeNotFound        = "NOT_FOUND"
	CodeConflict        = "CONFLICT"
	CodeInternalError   = "INTERNAL_ERROR"
)

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, errorResponse{Error: errorBody{
		Code:    code,
		Message: message,
	}})
}

func FieldError(w http.ResponseWriter, fields map[string]string) {
	JSON(w, http.StatusUnprocessableEntity, errorResponse{Error: errorBody{
		Code:    CodeValidationError,
		Message: "Validation failed",
		Fields:  fields,
	}})
}
