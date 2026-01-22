package negotiation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ucp-proxy/internal/model"
)

func TestHTTPProfileFetcher_Fetch(t *testing.T) {
	// Create test profile
	profile := AgentProfile{
		UCP: model.UCPMetadata{
			Version: "2026-01-11",
			Capabilities: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
		},
	}

	// Start test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(profile)
	}))
	defer server.Close()

	fetcher := NewHTTPProfileFetcher()
	got, err := fetcher.Fetch(context.Background(), server.URL+"/profile")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if got.UCP.Version != profile.UCP.Version {
		t.Errorf("Version = %s, want %s", got.UCP.Version, profile.UCP.Version)
	}

	if _, ok := got.UCP.Capabilities["dev.ucp.shopping.checkout"]; !ok {
		t.Error("missing capability dev.ucp.shopping.checkout")
	}
}

func TestHTTPProfileFetcher_Cache(t *testing.T) {
	var fetchCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		json.NewEncoder(w).Encode(AgentProfile{
			UCP: model.UCPMetadata{Version: "2026-01-11"},
		})
	}))
	defer server.Close()

	fetcher := NewHTTPProfileFetcher()
	url := server.URL + "/profile"

	// First fetch
	_, err := fetcher.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("First fetch error = %v", err)
	}

	// Second fetch should use cache
	_, err = fetcher.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("Second fetch error = %v", err)
	}

	if count := atomic.LoadInt32(&fetchCount); count != 1 {
		t.Errorf("Fetch count = %d, want 1 (cache should be used)", count)
	}
}

func TestHTTPProfileFetcher_CacheTTL(t *testing.T) {
	var fetchCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=1") // 1 second TTL
		json.NewEncoder(w).Encode(AgentProfile{
			UCP: model.UCPMetadata{Version: "2026-01-11"},
		})
	}))
	defer server.Close()

	fetcher := NewHTTPProfileFetcher()
	url := server.URL + "/profile"

	// First fetch
	_, _ = fetcher.Fetch(context.Background(), url)

	// Wait for cache to expire
	time.Sleep(1100 * time.Millisecond)

	// Second fetch should hit network
	_, _ = fetcher.Fetch(context.Background(), url)

	if count := atomic.LoadInt32(&fetchCount); count != 2 {
		t.Errorf("Fetch count = %d, want 2 (cache should have expired)", count)
	}
}

func TestHTTPProfileFetcher_ETag(t *testing.T) {
	var fetchCount int32
	var conditionalRequest bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)

		// Check for conditional request
		if r.Header.Get("If-None-Match") == `"abc123"` {
			conditionalRequest = true
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Cache-Control", "max-age=0") // Force revalidation
		json.NewEncoder(w).Encode(AgentProfile{
			UCP: model.UCPMetadata{Version: "2026-01-11"},
		})
	}))
	defer server.Close()

	fetcher := NewHTTPProfileFetcher()
	url := server.URL + "/profile"

	// First fetch (no cache)
	_, err := fetcher.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("First fetch error = %v", err)
	}

	// Second fetch should use conditional request
	_, err = fetcher.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("Second fetch error = %v", err)
	}

	if !conditionalRequest {
		t.Error("Expected conditional request with If-None-Match")
	}

	if count := atomic.LoadInt32(&fetchCount); count != 2 {
		t.Errorf("Fetch count = %d, want 2", count)
	}
}

func TestHTTPProfileFetcher_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	fetcher := NewHTTPProfileFetcher()
	_, err := fetcher.Fetch(context.Background(), server.URL+"/profile")

	if err == nil {
		t.Error("Expected error for 500 response")
	}
}

func TestHTTPProfileFetcher_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{invalid json"))
	}))
	defer server.Close()

	fetcher := NewHTTPProfileFetcher()
	_, err := fetcher.Fetch(context.Background(), server.URL+"/profile")

	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestHTTPProfileFetcher_StaleOnError(t *testing.T) {
	var fetchCount int32
	serverHealthy := true

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)

		if !serverHealthy {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=0") // Force revalidation
		json.NewEncoder(w).Encode(AgentProfile{
			UCP: model.UCPMetadata{Version: "2026-01-11"},
		})
	}))
	defer server.Close()

	fetcher := NewHTTPProfileFetcher()
	url := server.URL + "/profile"

	// First fetch (successful)
	_, err := fetcher.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("First fetch error = %v", err)
	}

	// Server becomes unhealthy
	serverHealthy = false

	// Second fetch should return stale cached data
	profile, err := fetcher.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("Expected stale cache fallback, got error = %v", err)
	}

	if profile.UCP.Version != "2026-01-11" {
		t.Errorf("Expected stale cached data, got version = %s", profile.UCP.Version)
	}
}

func TestHTTPProfileFetcher_ParseCacheTTL(t *testing.T) {
	fetcher := NewHTTPProfileFetcher()

	tests := []struct {
		name        string
		cacheHeader string
		want        time.Duration
	}{
		{
			name:        "max-age",
			cacheHeader: "max-age=300",
			want:        300 * time.Second,
		},
		{
			name:        "max-age with other directives",
			cacheHeader: "public, max-age=600, must-revalidate",
			want:        600 * time.Second,
		},
		{
			name:        "max-age=0 forces revalidation",
			cacheHeader: "max-age=0",
			want:        0,
		},
		{
			name:        "no cache header",
			cacheHeader: "",
			want:        DefaultCacheTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tt.cacheHeader != "" {
				resp.Header.Set("Cache-Control", tt.cacheHeader)
			}

			got := fetcher.parseCacheTTL(resp)
			if got != tt.want {
				t.Errorf("parseCacheTTL() = %v, want %v", got, tt.want)
			}
		})
	}
}
