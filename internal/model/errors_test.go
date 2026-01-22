package model

import (
	"errors"
	"fmt"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{
			name: "without wrapped error",
			err: &APIError{
				Code:    "TEST_ERROR",
				Message: "something went wrong",
			},
			want: "TEST_ERROR: something went wrong",
		},
		{
			name: "with wrapped error",
			err: &APIError{
				Code:    "TEST_ERROR",
				Message: "something went wrong",
				Err:     errors.New("underlying cause"),
			},
			want: "TEST_ERROR: something went wrong (underlying cause)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIError_Unwrap(t *testing.T) {
	underlying := errors.New("underlying error")
	err := &APIError{
		Code:    "TEST",
		Message: "test",
		Err:     underlying,
	}

	unwrapped := err.Unwrap()
	if unwrapped != underlying {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, underlying)
	}

	// Test nil case
	errNoWrap := &APIError{Code: "TEST", Message: "test"}
	if errNoWrap.Unwrap() != nil {
		t.Error("Unwrap() should return nil when no wrapped error")
	}
}

func TestNewNotFoundError(t *testing.T) {
	err := NewNotFoundError("checkout")

	if err.Code != "NOT_FOUND" {
		t.Errorf("Code = %q, want %q", err.Code, "NOT_FOUND")
	}
	if err.Message != "checkout not found" {
		t.Errorf("Message = %q, want %q", err.Message, "checkout not found")
	}
	if err.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, 404)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Error("error should wrap ErrNotFound sentinel")
	}
}

func TestNewValidationError(t *testing.T) {
	err := NewValidationError("email", "must be valid email address")

	if err.Code != "VALIDATION_ERROR" {
		t.Errorf("Code = %q, want %q", err.Code, "VALIDATION_ERROR")
	}
	if err.Message != "invalid email: must be valid email address" {
		t.Errorf("Message = %q, want %q", err.Message, "invalid email: must be valid email address")
	}
	if err.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, 400)
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Error("error should wrap ErrInvalidRequest sentinel")
	}
}

func TestNewUnauthorizedError(t *testing.T) {
	err := NewUnauthorizedError("invalid API key")

	if err.Code != "UNAUTHORIZED" {
		t.Errorf("Code = %q, want %q", err.Code, "UNAUTHORIZED")
	}
	if err.Message != "invalid API key" {
		t.Errorf("Message = %q, want %q", err.Message, "invalid API key")
	}
	if err.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, 401)
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Error("error should wrap ErrUnauthorized sentinel")
	}
}

func TestNewUpstreamError(t *testing.T) {
	underlying := errors.New("connection refused")
	err := NewUpstreamError("WooCommerce", underlying)

	if err.Code != "UPSTREAM_ERROR" {
		t.Errorf("Code = %q, want %q", err.Code, "UPSTREAM_ERROR")
	}
	if err.Message != "WooCommerce request failed" {
		t.Errorf("Message = %q, want %q", err.Message, "WooCommerce request failed")
	}
	if err.StatusCode != 502 {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, 502)
	}
	if !errors.Is(err, ErrUpstreamError) {
		t.Error("error should wrap ErrUpstreamError sentinel")
	}
	// Verify the underlying error is preserved in the chain
	if err.Err == nil {
		t.Error("wrapped error should not be nil")
	}
}

func TestNewPaymentError(t *testing.T) {
	err := NewPaymentError("card declined")

	if err.Code != "PAYMENT_ERROR" {
		t.Errorf("Code = %q, want %q", err.Code, "PAYMENT_ERROR")
	}
	if err.Message != "card declined" {
		t.Errorf("Message = %q, want %q", err.Message, "card declined")
	}
	if err.StatusCode != 402 {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, 402)
	}
	if !errors.Is(err, ErrPaymentFailed) {
		t.Error("error should wrap ErrPaymentFailed sentinel")
	}
}

func TestNewInternalError(t *testing.T) {
	underlying := errors.New("null pointer dereference")
	err := NewInternalError(underlying)

	if err.Code != "INTERNAL_ERROR" {
		t.Errorf("Code = %q, want %q", err.Code, "INTERNAL_ERROR")
	}
	if err.Message != "an internal error occurred" {
		t.Errorf("Message = %q, want %q", err.Message, "an internal error occurred")
	}
	if err.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, 500)
	}
	if err.Err != underlying {
		t.Error("wrapped error should be preserved")
	}
}

func TestNewRateLimitError(t *testing.T) {
	err := NewRateLimitError("Stripe")

	if err.Code != "RATE_LIMITED" {
		t.Errorf("Code = %q, want %q", err.Code, "RATE_LIMITED")
	}
	if err.Message != "Stripe rate limit exceeded, please retry later" {
		t.Errorf("Message = %q, want %q", err.Message, "Stripe rate limit exceeded, please retry later")
	}
	if err.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want %d", err.StatusCode, 429)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Error("error should wrap ErrRateLimited sentinel")
	}
}

// TestErrorsIs verifies that errors.Is() works correctly with all sentinel errors.
// This is critical for handler code that uses errors.Is() to determine response codes.
func TestErrorsIs(t *testing.T) {
	tests := []struct {
		name     string
		err      *APIError
		sentinel error
	}{
		{"NotFound", NewNotFoundError("x"), ErrNotFound},
		{"Validation", NewValidationError("x", "y"), ErrInvalidRequest},
		{"Unauthorized", NewUnauthorizedError("x"), ErrUnauthorized},
		{"Upstream", NewUpstreamError("x", nil), ErrUpstreamError},
		{"Payment", NewPaymentError("x"), ErrPaymentFailed},
		{"RateLimit", NewRateLimitError("x"), ErrRateLimited},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.sentinel) {
				t.Errorf("errors.Is(%T, %v) = false, want true", tt.err, tt.sentinel)
			}
		})
	}
}

// TestAPIErrorImplementsError verifies the error interface is properly implemented.
func TestAPIErrorImplementsError(t *testing.T) {
	var err error = &APIError{Code: "TEST", Message: "test"}
	_ = err.Error() // Should compile and not panic

	// Verify it works with fmt.Errorf wrapping
	wrapped := fmt.Errorf("outer: %w", err)
	var apiErr *APIError
	if !errors.As(wrapped, &apiErr) {
		t.Error("errors.As should find *APIError in wrapped error")
	}
}
