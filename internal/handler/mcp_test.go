package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/model"
)

// jsonrpcRequest is a JSON-RPC 2.0 request structure for testing.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response structure for testing.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolCallParams represents the params for tools/call method.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// callToolResult is the expected result structure from a tool call.
type callToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

func testMCPHandler(mock *adapter.Mock) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Pass nil negotiator - skips profile fetch but meta still required for schema validation
	return New(mock, nil, logger)
}

// testMeta returns a standard MCPMeta for tests.
func testMeta() map[string]interface{} {
	return map[string]interface{}{
		"ucp-agent": map[string]interface{}{
			"profile": "https://test.example/agent/profile",
		},
	}
}

func TestMCPServerCreation(t *testing.T) {
	h := testMCPHandler(&adapter.Mock{})
	server := h.NewMCPServer()

	if server == nil {
		t.Fatal("NewMCPServer returned nil")
	}
}

func TestMCPHandlerCreation(t *testing.T) {
	h := testMCPHandler(&adapter.Mock{})
	handler := h.NewMCPHandler()

	if handler == nil {
		t.Fatal("NewMCPHandler returned nil")
	}
}

func TestMCPInitialize(t *testing.T) {
	mock := &adapter.Mock{}
	_, mux := testHandler(mock)

	// MCP initialization request
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2026-01-11",
			"clientInfo": map[string]string{
				"name":    "test-client",
				"version": "1.0.0",
			},
			"capabilities": map[string]interface{}{},
		},
	}

	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, "")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Parse SSE response format
	jsonData, err := parseSSEResponse(w.Body.String())
	if err != nil {
		t.Fatalf("Failed to parse SSE response: %v", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		t.Fatalf("Failed to decode response: %v\nBody: %s", err, string(jsonData))
	}

	if resp.Error != nil {
		t.Errorf("Unexpected error: %+v", resp.Error)
	}

	if resp.Result == nil {
		t.Error("Expected result in response")
	}
}

func TestMCPToolsList(t *testing.T) {
	mock := &adapter.Mock{}
	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// First initialize the session
	initReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2026-01-11",
			"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	}

	initBody, _ := json.Marshal(initReq)
	initHttpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(initBody))
	setMCPHeaders(initHttpReq, "")
	initW := httptest.NewRecorder()
	mux.ServeHTTP(initW, initHttpReq)

	// Get session ID from response header if present
	sessionID := initW.Header().Get("Mcp-Session-Id")

	// Now list tools
	listReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	listBody, _ := json.Marshal(listReq)
	listHttpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(listBody))
	setMCPHeaders(listHttpReq, sessionID)
	listW := httptest.NewRecorder()

	mux.ServeHTTP(listW, listHttpReq)

	if listW.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", listW.Code, http.StatusOK, listW.Body.String())
	}

	jsonData, err := parseSSEResponse(listW.Body.String())
	if err != nil {
		t.Fatalf("Failed to parse SSE response: %v", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Error != nil {
		t.Errorf("Unexpected error: %+v", resp.Error)
	}

	// Parse tools list result
	var toolsResult struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(resp.Result, &toolsResult); err != nil {
		t.Fatalf("Failed to parse tools result: %v", err)
	}

	// Verify all 5 checkout tools are registered
	expectedTools := map[string]bool{
		"create_checkout":   false,
		"get_checkout":      false,
		"update_checkout":   false,
		"complete_checkout": false,
		"cancel_checkout":   false,
	}

	for _, tool := range toolsResult.Tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("Expected tool %q not found in tools list", name)
		}
	}
}

func TestMCPCreateCheckout(t *testing.T) {
	mockCheckout := &model.Checkout{
		ID:       "gid://test.com/Checkout/123",
		Status:   model.StatusIncomplete,
		Currency: "USD",
		UCP: model.UCPMetadata{
			Version:      "2026-01-11",
			Capabilities: map[string][]model.Capability{},
		},
		LineItems: []model.LineItem{},
		Totals:    []model.Total{},
		Links:     []model.Link{},
		Payment:   model.Payment{Instruments: []model.PaymentInstrument{}},
	}

	mock := &adapter.Mock{
		CreateCheckoutFunc: func(ctx context.Context, req *adapter.CreateCheckoutRequest) (*model.Checkout, error) {
			return mockCheckout, nil
		},
	}

	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Initialize session first
	sessionID := initMCPSession(t, mux)

	// Call create_checkout tool
	args, _ := json.Marshal(map[string]interface{}{
		"meta": testMeta(),
		"checkout": map[string]interface{}{
			"cart_token": "test-cart-token",
		},
	})

	callReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "create_checkout",
			Arguments: args,
		},
	}

	body, _ := json.Marshal(callReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, sessionID)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}

	jsonData, err := parseSSEResponse(w.Body.String())
	if err != nil {
		t.Fatalf("Failed to parse SSE response: %v", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Error != nil {
		t.Errorf("Unexpected error: %+v", resp.Error)
	}

	// Parse tool result
	var result callToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	if result.IsError {
		t.Error("Expected success, got error")
	}

	if len(result.Content) == 0 {
		t.Error("Expected content in result")
	}

	// The content should contain the checkout JSON
	if len(result.Content) > 0 && result.Content[0].Type == "text" {
		var checkout model.Checkout
		if err := json.Unmarshal([]byte(result.Content[0].Text), &checkout); err != nil {
			t.Fatalf("Failed to parse checkout from result: %v", err)
		}

		if checkout.ID != mockCheckout.ID {
			t.Errorf("Checkout ID = %s, want %s", checkout.ID, mockCheckout.ID)
		}
	}
}

func TestMCPGetCheckout(t *testing.T) {
	mockCheckout := &model.Checkout{
		ID:       "gid://test.com/Checkout/456",
		Status:   model.StatusReadyForComplete,
		Currency: "USD",
		UCP: model.UCPMetadata{
			Version:      "2026-01-11",
			Capabilities: map[string][]model.Capability{},
		},
		LineItems: []model.LineItem{},
		Totals:    []model.Total{},
		Links:     []model.Link{},
		Payment:   model.Payment{Instruments: []model.PaymentInstrument{}},
	}

	mock := &adapter.Mock{
		GetCheckoutFunc: func(ctx context.Context, id string) (*model.Checkout, error) {
			if id == "456" {
				return mockCheckout, nil
			}
			return nil, model.NewNotFoundError("checkout")
		},
	}

	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	sessionID := initMCPSession(t, mux)

	args, _ := json.Marshal(map[string]interface{}{
		"meta": testMeta(),
		"id":   "456",
	})

	callReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "get_checkout",
			Arguments: args,
		},
	}

	body, _ := json.Marshal(callReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, sessionID)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}

	jsonData, err := parseSSEResponse(w.Body.String())
	if err != nil {
		t.Fatalf("Failed to parse SSE response: %v", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Error != nil {
		t.Errorf("Unexpected error: %+v", resp.Error)
	}
}

func TestMCPGetCheckoutNotFound(t *testing.T) {
	mock := &adapter.Mock{
		GetCheckoutFunc: func(ctx context.Context, id string) (*model.Checkout, error) {
			return nil, model.NewNotFoundError("checkout")
		},
	}

	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	sessionID := initMCPSession(t, mux)

	args, _ := json.Marshal(map[string]interface{}{
		"meta": testMeta(),
		"id":   "nonexistent",
	})

	callReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "get_checkout",
			Arguments: args,
		},
	}

	body, _ := json.Marshal(callReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, sessionID)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	// MCP returns 200 OK even for tool errors, error is in the result
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	jsonData, err := parseSSEResponse(w.Body.String())
	if err != nil {
		t.Fatalf("Failed to parse SSE response: %v", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Tool errors are returned in the result, not as JSON-RPC errors
	// The result should indicate an error via isError flag or error content
}

func TestMCPUpdateCheckout(t *testing.T) {
	mock := &adapter.Mock{
		UpdateCheckoutFunc: func(ctx context.Context, id string, req *model.CheckoutUpdateRequest) (*model.Checkout, error) {
			return &model.Checkout{
				ID:       "gid://test.com/Checkout/" + id,
				Status:   model.StatusReadyForComplete,
				Currency: "USD",
				UCP: model.UCPMetadata{
					Version:      "2026-01-11",
					Capabilities: map[string][]model.Capability{},
				},
				LineItems: []model.LineItem{},
				Totals:    []model.Total{},
				Links:     []model.Link{},
				Payment:   model.Payment{Instruments: []model.PaymentInstrument{}},
			}, nil
		},
	}

	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	sessionID := initMCPSession(t, mux)

	args, _ := json.Marshal(map[string]interface{}{
		"meta": testMeta(),
		"id":   "789",
		"checkout": map[string]interface{}{
			"line_items":     []interface{}{},
			"discount_codes": []interface{}{},
			"shipping_address": map[string]string{
				"street_address":   "123 Test St",
				"address_locality": "Test City",
				"address_region":   "CA",
				"postal_code":      "12345",
				"address_country":  "US",
			},
		},
	})

	callReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "update_checkout",
			Arguments: args,
		},
	}

	body, _ := json.Marshal(callReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, sessionID)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestMCPCompleteCheckout(t *testing.T) {
	mock := &adapter.Mock{
		CompleteCheckoutFunc: func(ctx context.Context, id string, req *model.CheckoutSubmitRequest) (*model.Checkout, error) {
			return &model.Checkout{
				ID:       "gid://test.com/Checkout/" + id,
				Status:   model.StatusCompleted,
				Currency: "USD",
				UCP: model.UCPMetadata{
					Version:      "2026-01-11",
					Capabilities: map[string][]model.Capability{},
				},
				LineItems: []model.LineItem{},
				Totals:    []model.Total{},
				Links:     []model.Link{},
				Payment:   model.Payment{Instruments: []model.PaymentInstrument{}},
			}, nil
		},
	}

	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	sessionID := initMCPSession(t, mux)

	args, _ := json.Marshal(map[string]interface{}{
		"meta": testMeta(),
		"id":   "999",
		"checkout": map[string]interface{}{
			"payment": map[string]interface{}{
				"instruments": []map[string]interface{}{
					{
						"id":         "instr_1",
						"handler_id": "google.pay",
						"type":       "card",
						"selected":   true,
						"credential": map[string]string{
							"type":  "stripe.payment_method",
							"token": "pm_test",
						},
					},
				},
			},
		},
	})

	callReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "complete_checkout",
			Arguments: args,
		},
	}

	body, _ := json.Marshal(callReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, sessionID)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestMCPCancelCheckout(t *testing.T) {
	mock := &adapter.Mock{
		CancelCheckoutFunc: func(ctx context.Context, id string) (*model.Checkout, error) {
			return &model.Checkout{
				ID:       "gid://test.com/Checkout/" + id,
				Status:   model.StatusCanceled,
				Currency: "USD",
				UCP: model.UCPMetadata{
					Version:      "2026-01-11",
					Capabilities: map[string][]model.Capability{},
				},
				LineItems: []model.LineItem{},
				Totals:    []model.Total{},
				Links:     []model.Link{},
				Payment:   model.Payment{Instruments: []model.PaymentInstrument{}},
			}, nil
		},
	}

	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	sessionID := initMCPSession(t, mux)

	args, _ := json.Marshal(map[string]interface{}{
		"meta": testMeta(),
		"id":   "123",
	})

	callReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "cancel_checkout",
			Arguments: args,
		},
	}

	body, _ := json.Marshal(callReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, sessionID)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}

	jsonData, err := parseSSEResponse(w.Body.String())
	if err != nil {
		t.Fatalf("Failed to parse SSE response: %v", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(jsonData, &resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Parse the result to verify status is canceled
	var result callToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	if len(result.Content) > 0 && result.Content[0].Type == "text" {
		var checkout model.Checkout
		if err := json.Unmarshal([]byte(result.Content[0].Text), &checkout); err != nil {
			t.Fatalf("Failed to parse checkout: %v", err)
		}

		if checkout.Status != model.StatusCanceled {
			t.Errorf("Status = %s, want %s", checkout.Status, model.StatusCanceled)
		}
	}
}

func TestMCPMissingRequiredField(t *testing.T) {
	mock := &adapter.Mock{}
	h := testMCPHandler(mock)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	sessionID := initMCPSession(t, mux)

	// Call get_checkout without required 'meta' field
	args, _ := json.Marshal(map[string]interface{}{
		"id": "123",
	})

	callReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: toolCallParams{
			Name:      "get_checkout",
			Arguments: args,
		},
	}

	body, _ := json.Marshal(callReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, sessionID)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	// Should still return 200, with error in the result
	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// initMCPSession initializes an MCP session and returns the session ID.
// setMCPHeaders sets the required headers for MCP Streamable HTTP requests.
func setMCPHeaders(req *http.Request, sessionID string) {
	req.Header.Set("Content-Type", "application/json")
	// MCP Streamable HTTP requires Accept header with both json and event-stream
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
}

// parseSSEResponse extracts JSON data from SSE formatted response.
// SSE format: "event: message\ndata: {json}\n\n"
func parseSSEResponse(body string) ([]byte, error) {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			return []byte(strings.TrimPrefix(line, "data: ")), nil
		}
	}
	// If no SSE format found, assume plain JSON
	return []byte(body), nil
}

// initMCPSession initializes an MCP session and returns the session ID.
func initMCPSession(t *testing.T, mux *http.ServeMux) string {
	t.Helper()

	initReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2026-01-11",
			"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	}

	body, _ := json.Marshal(initReq)
	httpReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	setMCPHeaders(httpReq, "")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Fatalf("Failed to initialize MCP session: %s", w.Body.String())
	}

	return w.Header().Get("Mcp-Session-Id")
}
