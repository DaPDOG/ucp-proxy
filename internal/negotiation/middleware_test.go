package negotiation

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"ucp-proxy/internal/model"
)

// mockProfileFetcher is a mock implementation for testing.
type mockProfileFetcher struct {
	profile *AgentProfile
	err     error
}

func (m *mockProfileFetcher) Fetch(ctx context.Context, profileURL string) (*AgentProfile, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.profile, nil
}

func testNegotiator(fetcher ProfileFetcher) *Negotiator {
	businessProfile := &model.DiscoveryProfile{
		UCP: model.UCPMetadata{
			Version: "2026-01-11",
			Capabilities: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
			PaymentHandlers: map[string][]model.PaymentHandler{
				"com.stripe": {{ID: "stripe-1", Version: "2026-01-01"}},
			},
		},
	}
	return NewNegotiator(fetcher, businessProfile)
}

func TestMiddleware_MissingHeader(t *testing.T) {
	fetcher := &mockProfileFetcher{
		profile: &AgentProfile{UCP: model.UCPMetadata{Version: "2026-01-11"}},
	}
	negotiator := testNegotiator(fetcher)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Middleware(negotiator, logger)(handler)

	req := httptest.NewRequest("GET", "/checkout-sessions/123", nil)
	// No UCP-Agent header
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error.Code != UCPAgentRequired {
		t.Errorf("Error code = %s, want %s", resp.Error.Code, UCPAgentRequired)
	}
}

func TestMiddleware_ValidHeader(t *testing.T) {
	fetcher := &mockProfileFetcher{
		profile: &AgentProfile{
			UCP: model.UCPMetadata{
				Version: "2026-01-11",
				Capabilities: map[string][]model.Capability{
					"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
				},
			},
		},
	}
	negotiator := testNegotiator(fetcher)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var negotiatedCtx *NegotiatedContext
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		negotiatedCtx = GetNegotiatedContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Middleware(negotiator, logger)(handler)

	req := httptest.NewRequest("GET", "/checkout-sessions/123", nil)
	req.Header.Set("UCP-Agent", `profile="https://agent.example/profile"`)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d\nBody: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if negotiatedCtx == nil {
		t.Fatal("Expected negotiated context in request")
	}

	if negotiatedCtx.Version != "2026-01-11" {
		t.Errorf("Version = %s, want 2026-01-11", negotiatedCtx.Version)
	}
}

func TestMiddleware_ExemptPaths(t *testing.T) {
	fetcher := &mockProfileFetcher{
		profile: &AgentProfile{UCP: model.UCPMetadata{Version: "2026-01-11"}},
	}
	negotiator := testNegotiator(fetcher)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Middleware(negotiator, logger)(handler)

	exemptPaths := []string{
		"/.well-known/ucp",
		"/health",
		"/healthz",
	}

	for _, path := range exemptPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			// No UCP-Agent header
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Status = %d, want %d for exempt path %s", w.Code, http.StatusOK, path)
			}
		})
	}
}

func TestMiddleware_InvalidHeader(t *testing.T) {
	fetcher := &mockProfileFetcher{
		profile: &AgentProfile{UCP: model.UCPMetadata{Version: "2026-01-11"}},
	}
	negotiator := testNegotiator(fetcher)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Middleware(negotiator, logger)(handler)

	req := httptest.NewRequest("GET", "/checkout-sessions/123", nil)
	req.Header.Set("UCP-Agent", `invalid-header-format`)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestMiddleware_VersionMismatch(t *testing.T) {
	// Agent requests version newer than business supports
	fetcher := &mockProfileFetcher{
		profile: &AgentProfile{
			UCP: model.UCPMetadata{
				Version: "2099-01-01", // Far future version
			},
		},
	}
	negotiator := testNegotiator(fetcher)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Middleware(negotiator, logger)(handler)

	req := httptest.NewRequest("GET", "/checkout-sessions/123", nil)
	req.Header.Set("UCP-Agent", `profile="https://agent.example/profile"`)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error.Code != UCPVersionUnsupported {
		t.Errorf("Error code = %s, want %s", resp.Error.Code, UCPVersionUnsupported)
	}
}

func TestGetNegotiatedContext_NotSet(t *testing.T) {
	ctx := context.Background()
	got := GetNegotiatedContext(ctx)
	if got != nil {
		t.Error("Expected nil for context without negotiation")
	}
}

func TestExtractMCPProfileURL(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]interface{}
		want   string
	}{
		{
			name: "valid profile",
			params: map[string]interface{}{
				"_meta": map[string]interface{}{
					"ucp": map[string]interface{}{
						"profile": "https://agent.example/profile",
					},
				},
			},
			want: "https://agent.example/profile",
		},
		{
			name:   "no _meta",
			params: map[string]interface{}{},
			want:   "",
		},
		{
			name: "no ucp in _meta",
			params: map[string]interface{}{
				"_meta": map[string]interface{}{},
			},
			want: "",
		},
		{
			name: "no profile in ucp",
			params: map[string]interface{}{
				"_meta": map[string]interface{}{
					"ucp": map[string]interface{}{},
				},
			},
			want: "",
		},
		{
			name: "profile with whitespace",
			params: map[string]interface{}{
				"_meta": map[string]interface{}{
					"ucp": map[string]interface{}{
						"profile": "  https://agent.example/profile  ",
					},
				},
			},
			want: "https://agent.example/profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMCPProfileURL(tt.params)
			if got != tt.want {
				t.Errorf("ExtractMCPProfileURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsExemptPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/.well-known/ucp", true},
		{"/health", true},
		{"/healthz", true},
		{"/checkout-sessions", false},
		{"/checkout-sessions/123", false},
		{"/mcp", false},
		{"/", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isExemptPath(tt.path)
			if got != tt.want {
				t.Errorf("isExemptPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
