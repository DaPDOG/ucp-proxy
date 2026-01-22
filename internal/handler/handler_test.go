package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/model"
)

func testHandler(mock *adapter.Mock) (*Handler, *http.ServeMux) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Pass nil negotiator for backward compat - tests don't require UCP-Agent header
	h := New(mock, nil, logger)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, mux
}

// getUCPErrorCode extracts the error code from a UCP checkout error response.
// UCP errors are returned as Checkout objects with messages array.
func getUCPErrorCode(body []byte) string {
	var checkout model.Checkout
	if err := json.Unmarshal(body, &checkout); err != nil {
		return ""
	}
	if len(checkout.Messages) > 0 && checkout.Messages[0].Type == "error" {
		return checkout.Messages[0].Code
	}
	return ""
}

func TestHandleHealth(t *testing.T) {
	_, mux := testHandler(&adapter.Mock{})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp healthResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "ok" {
		t.Errorf("Status = %s, want ok", resp.Status)
	}
}

func TestHandleWellKnown(t *testing.T) {
	mock := &adapter.Mock{
		GetProfileFunc: func(ctx context.Context) (*model.DiscoveryProfile, error) {
			return &model.DiscoveryProfile{
				UCP: model.UCPMetadata{
					Version: "2026-01-11",
					Capabilities: map[string][]model.Capability{
						"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
					},
				},
			}, nil
		},
	}

	_, mux := testHandler(mock)

	req := httptest.NewRequest("GET", "/.well-known/ucp", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var profile model.DiscoveryProfile
	json.NewDecoder(w.Body).Decode(&profile)
	if profile.UCP.Version != "2026-01-11" {
		t.Errorf("Version = %s, want 2026-01-11", profile.UCP.Version)
	}
}

func TestHandleGetCheckout(t *testing.T) {
	mockCheckout := &model.Checkout{
		ID:       "gid://example.com/Checkout/123",
		Status:   model.StatusReadyForComplete,
		Currency: "USD",
		Totals: []model.Total{
			{Type: model.TotalTypeTotal, Amount: 9900},
		},
	}

	mock := &adapter.Mock{
		GetCheckoutFunc: func(ctx context.Context, id string) (*model.Checkout, error) {
			if id == "123" {
				return mockCheckout, nil
			}
			return nil, model.NewNotFoundError("checkout")
		},
	}

	_, mux := testHandler(mock)

	tests := []struct {
		name       string
		id         string
		wantStatus int
	}{
		{"found", "123", http.StatusOK},
		{"not found", "456", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/checkout-sessions/"+tt.id, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleGetCheckoutResponse(t *testing.T) {
	mockCheckout := &model.Checkout{
		ID:       "gid://example.com/Checkout/123",
		Status:   model.StatusReadyForComplete,
		Currency: "USD",
		LineItems: []model.LineItem{
			{
				ID:       "1",
				Quantity: 2,
				Item:     model.Item{Title: "Test Product", Price: 5000},
			},
		},
		Totals: []model.Total{
			{Type: model.TotalTypeTotal, Amount: 10000},
		},
	}

	mock := &adapter.Mock{
		GetCheckoutFunc: func(ctx context.Context, id string) (*model.Checkout, error) {
			return mockCheckout, nil
		},
	}

	_, mux := testHandler(mock)

	req := httptest.NewRequest("GET", "/checkout-sessions/123", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	var checkout model.Checkout
	json.NewDecoder(w.Body).Decode(&checkout)

	if checkout.Currency != "USD" {
		t.Errorf("Currency = %s, want USD", checkout.Currency)
	}
	if len(checkout.LineItems) != 1 {
		t.Errorf("LineItems len = %d, want 1", len(checkout.LineItems))
	}
	if checkout.LineItems[0].Item.Title != "Test Product" {
		t.Errorf("Title = %s, want Test Product", checkout.LineItems[0].Item.Title)
	}
}

func TestHandleCreateCheckout(t *testing.T) {
	mock := &adapter.Mock{
		CreateCheckoutFunc: func(ctx context.Context, req *adapter.CreateCheckoutRequest) (*model.Checkout, error) {
			return &model.Checkout{
				ID:       "gid://example.com/Checkout/999",
				Status:   model.StatusIncomplete,
				Currency: "USD",
			}, nil
		},
	}

	_, mux := testHandler(mock)

	body := `{"cart_token": "test-token"}`
	req := httptest.NewRequest("POST", "/checkout-sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusCreated)
	}

	var checkout model.Checkout
	json.NewDecoder(w.Body).Decode(&checkout)

	if checkout.ID != "gid://example.com/Checkout/999" {
		t.Errorf("ID = %s, want gid://example.com/Checkout/999", checkout.ID)
	}
}

func TestHandleCreateCheckoutInvalidJSON(t *testing.T) {
	_, mux := testHandler(&adapter.Mock{})

	req := httptest.NewRequest("POST", "/checkout-sessions", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// UCP errors are returned as Checkout with messages
	if code := getUCPErrorCode(w.Body.Bytes()); code != "VALIDATION_ERROR" {
		t.Errorf("Error code = %s, want VALIDATION_ERROR", code)
	}
}

func TestHandleUpdateCheckout(t *testing.T) {
	var receivedReq *model.CheckoutUpdateRequest

	mock := &adapter.Mock{
		UpdateCheckoutFunc: func(ctx context.Context, id string, req *model.CheckoutUpdateRequest) (*model.Checkout, error) {
			receivedReq = req
			return &model.Checkout{
				ID:       "gid://example.com/Checkout/" + id,
				Status:   model.StatusReadyForComplete,
				Currency: "USD",
			}, nil
		},
	}

	_, mux := testHandler(mock)

	body := `{
		"line_items": [{"product_id": "456", "quantity": 2}],
		"discount_codes": ["SAVE10"],
		"shipping_address": {
			"street_address": "123 Main St",
			"address_locality": "San Francisco",
			"address_region": "CA",
			"postal_code": "94102",
			"address_country": "US"
		}
	}`

	req := httptest.NewRequest("PUT", "/checkout-sessions/123", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if receivedReq == nil {
		t.Fatal("Update request not received")
	}

	if receivedReq.ShippingAddress == nil {
		t.Fatal("Shipping address not received")
	}

	if receivedReq.ShippingAddress.StreetAddress != "123 Main St" {
		t.Errorf("Street = %s, want 123 Main St", receivedReq.ShippingAddress.StreetAddress)
	}

	if len(receivedReq.DiscountCodes) != 1 || receivedReq.DiscountCodes[0] != "SAVE10" {
		t.Errorf("DiscountCodes = %v, want [SAVE10]", receivedReq.DiscountCodes)
	}

	if len(receivedReq.LineItems) != 1 || receivedReq.LineItems[0].ProductID != "456" {
		t.Errorf("LineItems = %v, want [{456, 2}]", receivedReq.LineItems)
	}
}

func TestHandleCompleteCheckout(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		mockReturn  *model.Checkout
		mockErr     error
		wantStatus  int
		wantErrCode string
	}{
		{
			name: "successful completion",
			body: `{
				"payment": {
					"instruments": [
						{
							"id": "instr_1",
							"handler_id": "google.pay",
							"type": "card",
							"selected": true,
							"credential": {
								"type": "stripe.payment_method",
								"token": "pm_test123"
							}
						}
					]
				}
			}`,
			mockReturn: &model.Checkout{
				ID:     "gid://example.com/Checkout/123",
				Status: model.StatusCompleted,
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "3DS escalation",
			body: `{
				"payment": {
					"instruments": [{"id": "instr_1", "handler_id": "google.pay", "selected": true}]
				}
			}`,
			mockReturn: &model.Checkout{
				ID:          "gid://example.com/Checkout/123",
				Status:      model.StatusRequiresEscalation,
				ContinueURL: "https://stripe.com/3ds",
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name: "missing instrument selection",
			body: `{
				"payment": {
					"instruments": [{"id": "instr_1", "selected": false}]
				}
			}`,
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "VALIDATION_ERROR",
		},
		{
			name: "no instruments",
			body: `{
				"payment": {
					"instruments": []
				}
			}`,
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "VALIDATION_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &adapter.Mock{
				CompleteCheckoutFunc: func(ctx context.Context, id string, req *model.CheckoutSubmitRequest) (*model.Checkout, error) {
					if tt.mockErr != nil {
						return nil, tt.mockErr
					}
					return tt.mockReturn, nil
				},
			}

			_, mux := testHandler(mock)

			req := httptest.NewRequest("POST", "/checkout-sessions/123/complete", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d\nBody: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			if tt.wantErrCode != "" {
				// UCP errors are returned as Checkout with messages
				if code := getUCPErrorCode(w.Body.Bytes()); code != tt.wantErrCode {
					t.Errorf("Error code = %s, want %s", code, tt.wantErrCode)
				}
			}
		})
	}
}

func TestHandleCompleteCheckoutEscalationStatus(t *testing.T) {
	mock := &adapter.Mock{
		CompleteCheckoutFunc: func(ctx context.Context, id string, req *model.CheckoutSubmitRequest) (*model.Checkout, error) {
			return &model.Checkout{
				ID:          "gid://example.com/Checkout/123",
				Status:      model.StatusRequiresEscalation,
				ContinueURL: "https://shop.example.com/checkout",
				Messages: []model.Message{
					{Type: "info", Code: "3DS_REQUIRED", Content: "3D Secure authentication required"},
				},
			}, nil
		},
	}

	_, mux := testHandler(mock)

	body := `{
		"payment": {
			"instruments": [{"id": "instr_1", "handler_id": "redirect", "selected": true}]
		}
	}`

	req := httptest.NewRequest("POST", "/checkout-sessions/123/complete", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// Escalation should return 202 Accepted
	if w.Code != http.StatusAccepted {
		t.Errorf("Status = %d, want %d (202 Accepted)", w.Code, http.StatusAccepted)
	}

	var checkout model.Checkout
	json.NewDecoder(w.Body).Decode(&checkout)

	if checkout.Status != model.StatusRequiresEscalation {
		t.Errorf("Checkout status = %s, want %s", checkout.Status, model.StatusRequiresEscalation)
	}

	if checkout.ContinueURL == "" {
		t.Error("ContinueURL should be set for escalation")
	}
}

func TestHandleCancelCheckout(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		mockReturn  *model.Checkout
		mockErr     error
		wantStatus  int
		wantErrCode string
	}{
		{
			name: "successful cancellation",
			id:   "123",
			mockReturn: &model.Checkout{
				ID:     "gid://example.com/Checkout/123",
				Status: model.StatusCanceled,
			},
			wantStatus: http.StatusOK,
		},
		{
			name:        "not found",
			id:          "456",
			mockErr:     model.NewNotFoundError("checkout"),
			wantStatus:  http.StatusNotFound,
			wantErrCode: "NOT_FOUND",
		},
		{
			name:        "upstream error",
			id:          "789",
			mockErr:     model.NewUpstreamError("WooCommerce", nil),
			wantStatus:  http.StatusBadGateway,
			wantErrCode: "UPSTREAM_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &adapter.Mock{
				CancelCheckoutFunc: func(ctx context.Context, id string) (*model.Checkout, error) {
					if tt.mockErr != nil {
						return nil, tt.mockErr
					}
					return tt.mockReturn, nil
				},
			}

			_, mux := testHandler(mock)

			req := httptest.NewRequest("POST", "/checkout-sessions/"+tt.id+"/cancel", nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d\nBody: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			if tt.wantErrCode != "" {
				if code := getUCPErrorCode(w.Body.Bytes()); code != tt.wantErrCode {
					t.Errorf("Error code = %s, want %s", code, tt.wantErrCode)
				}
			}

			if tt.mockReturn != nil {
				var checkout model.Checkout
				json.NewDecoder(w.Body).Decode(&checkout)
				if checkout.Status != tt.mockReturn.Status {
					t.Errorf("Status = %s, want %s", checkout.Status, tt.mockReturn.Status)
				}
			}
		})
	}
}

func TestMapStatusToSeverity(t *testing.T) {
	tests := []struct {
		statusCode int
		want       model.MessageSeverity
	}{
		// Recoverable errors - agent can fix and retry
		{400, model.SeverityRecoverable},
		{402, model.SeverityRecoverable},
		{422, model.SeverityRecoverable},
		{429, model.SeverityRecoverable},
		// Unrecoverable - resource or auth issues
		{404, model.SeverityUnrecoverable},
		{401, model.SeverityUnrecoverable},
		{403, model.SeverityUnrecoverable},
		// Server errors
		{500, model.SeverityUnrecoverable},
		{502, model.SeverityUnrecoverable},
		{503, model.SeverityUnrecoverable},
		{504, model.SeverityUnrecoverable},
		// Unknown defaults to unrecoverable
		{418, model.SeverityUnrecoverable}, // I'm a teapot
		{599, model.SeverityUnrecoverable},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.statusCode), func(t *testing.T) {
			got := mapStatusToSeverity(tt.statusCode)
			if got != tt.want {
				t.Errorf("mapStatusToSeverity(%d) = %s, want %s", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		mockErr    error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "not found",
			mockErr:    model.NewNotFoundError("checkout"),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			name:       "validation error",
			mockErr:    model.NewValidationError("field", "invalid"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "VALIDATION_ERROR",
		},
		{
			name:       "upstream error",
			mockErr:    model.NewUpstreamError("WooCommerce", nil),
			wantStatus: http.StatusBadGateway,
			wantCode:   "UPSTREAM_ERROR",
		},
		{
			name:       "unauthorized",
			mockErr:    model.NewUnauthorizedError("invalid credentials"),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name:       "payment error",
			mockErr:    model.NewPaymentError("card declined"),
			wantStatus: http.StatusPaymentRequired,
			wantCode:   "PAYMENT_ERROR",
		},
		{
			name:       "rate limit",
			mockErr:    model.NewRateLimitError("WooCommerce"),
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "RATE_LIMITED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &adapter.Mock{
				GetCheckoutFunc: func(ctx context.Context, id string) (*model.Checkout, error) {
					return nil, tt.mockErr
				},
			}

			_, mux := testHandler(mock)

			req := httptest.NewRequest("GET", "/checkout-sessions/123", nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}

			// UCP errors are returned as Checkout with messages
			if code := getUCPErrorCode(w.Body.Bytes()); code != tt.wantCode {
				t.Errorf("Code = %s, want %s\nBody: %s", code, tt.wantCode, w.Body.String())
			}
		})
	}
}
