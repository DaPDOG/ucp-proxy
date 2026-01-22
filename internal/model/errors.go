package model

import (
	"errors"
	"fmt"
)

// Sentinel errors for common cases.
// Use errors.Is() to check against these.
var (
	ErrNotFound       = errors.New("not found")
	ErrInvalidRequest = errors.New("invalid request")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrPaymentFailed  = errors.New("payment failed")
	ErrUpstreamError  = errors.New("upstream error")
	ErrRateLimited    = errors.New("rate limited")
)

// APIError represents a structured error for API responses.
// Implements error interface and supports unwrapping.
type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"-"` // HTTP status, not serialized
	Err        error  `json:"-"` // Wrapped error, not serialized
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// NewNotFoundError creates a 404 error for missing resources.
func NewNotFoundError(resource string) *APIError {
	return &APIError{
		Code:       "NOT_FOUND",
		Message:    fmt.Sprintf("%s not found", resource),
		StatusCode: 404,
		Err:        ErrNotFound,
	}
}

// NewValidationError creates a 400 error for invalid input.
func NewValidationError(field, reason string) *APIError {
	return &APIError{
		Code:       "VALIDATION_ERROR",
		Message:    fmt.Sprintf("invalid %s: %s", field, reason),
		StatusCode: 400,
		Err:        ErrInvalidRequest,
	}
}

// NewUnauthorizedError creates a 401 error for auth failures.
func NewUnauthorizedError(reason string) *APIError {
	return &APIError{
		Code:       "UNAUTHORIZED",
		Message:    reason,
		StatusCode: 401,
		Err:        ErrUnauthorized,
	}
}

// NewUpstreamError creates a 502 error for backend failures.
func NewUpstreamError(service string, err error) *APIError {
	return &APIError{
		Code:       "UPSTREAM_ERROR",
		Message:    fmt.Sprintf("%s request failed", service),
		StatusCode: 502,
		Err:        fmt.Errorf("%w: %v", ErrUpstreamError, err),
	}
}

// NewPaymentError creates a 402 error for payment issues.
func NewPaymentError(reason string) *APIError {
	return &APIError{
		Code:       "PAYMENT_ERROR",
		Message:    reason,
		StatusCode: 402,
		Err:        ErrPaymentFailed,
	}
}

// NewInternalError creates a 500 error for unexpected failures.
func NewInternalError(err error) *APIError {
	return &APIError{
		Code:       "INTERNAL_ERROR",
		Message:    "an internal error occurred",
		StatusCode: 500,
		Err:        err,
	}
}

// NewRateLimitError creates a 429 error for rate limiting.
func NewRateLimitError(service string) *APIError {
	return &APIError{
		Code:       "RATE_LIMITED",
		Message:    fmt.Sprintf("%s rate limit exceeded, please retry later", service),
		StatusCode: 429,
		Err:        ErrRateLimited,
	}
}
