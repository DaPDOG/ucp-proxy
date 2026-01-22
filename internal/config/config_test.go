package config

import (
	"context"
	"os"
	"strings"
	"testing"

	"ucp-proxy/internal/model"
)

func TestLoadFromEnv(t *testing.T) {
	// Save and restore environment
	envVars := []string{
		"MERCHANT_ID", "MERCHANT_STORE_URL", "MERCHANT_API_KEY",
		"MERCHANT_API_SECRET", "ENVIRONMENT",
		"PORT", "LOG_LEVEL", "POLICY_LINKS", "PAYMENT_HANDLERS",
	}
	saved := make(map[string]string)
	for _, k := range envVars {
		saved[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	// Set test environment
	os.Setenv("ENVIRONMENT", "development")
	os.Setenv("MERCHANT_ID", "test-merchant")
	os.Setenv("MERCHANT_STORE_URL", "https://shop.example.com")
	os.Setenv("MERCHANT_API_KEY", "ck_test123")
	os.Setenv("MERCHANT_API_SECRET", "cs_test456")
	os.Setenv("PORT", "9090")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("POLICY_LINKS", `{"privacy_policy":"https://shop.example.com/privacy"}`)

	cfg, err := Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Verify server settings
	if cfg.Port != "9090" {
		t.Errorf("Port = %s, want 9090", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %s, want debug", cfg.LogLevel)
	}
	if cfg.MerchantID != "test-merchant" {
		t.Errorf("MerchantID = %s, want test-merchant", cfg.MerchantID)
	}

	// Verify merchant config
	if cfg.Merchant.StoreURL != "https://shop.example.com" {
		t.Errorf("StoreURL = %s, want https://shop.example.com", cfg.Merchant.StoreURL)
	}
	if cfg.Merchant.APIKey != "ck_test123" {
		t.Errorf("APIKey = %s, want ck_test123", cfg.Merchant.APIKey)
	}

	// Verify derived domain
	if cfg.Merchant.StoreDomain != "shop.example.com" {
		t.Errorf("StoreDomain = %s, want shop.example.com", cfg.Merchant.StoreDomain)
	}

	// Verify policy links
	if len(cfg.Merchant.PolicyLinks) != 1 {
		t.Errorf("PolicyLinks len = %d, want 1", len(cfg.Merchant.PolicyLinks))
	}
	if cfg.Merchant.PolicyLinks["privacy_policy"] != "https://shop.example.com/privacy" {
		t.Errorf("PolicyLinks[privacy_policy] = %s", cfg.Merchant.PolicyLinks["privacy_policy"])
	}
}

func TestLoadMissingMerchantID(t *testing.T) {
	os.Unsetenv("MERCHANT_ID")

	_, err := Load(context.Background())
	if err == nil {
		t.Error("Expected error for missing MERCHANT_ID")
	}
}

func TestLoadMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		setup   func()
		wantErr string
	}{
		{
			name: "missing store_url",
			setup: func() {
				os.Setenv("MERCHANT_ID", "test")
				os.Setenv("MERCHANT_API_KEY", "key")
				os.Setenv("MERCHANT_API_SECRET", "secret")
				os.Unsetenv("MERCHANT_STORE_URL")
			},
			wantErr: "store_url is required",
		},
		{
			name: "missing api_key",
			setup: func() {
				os.Setenv("MERCHANT_ID", "test")
				os.Setenv("MERCHANT_STORE_URL", "https://shop.com")
				os.Setenv("MERCHANT_API_SECRET", "secret")
				os.Unsetenv("MERCHANT_API_KEY")
			},
			wantErr: "api_key is required",
		},
		{
			name: "missing api_secret",
			setup: func() {
				os.Setenv("MERCHANT_ID", "test")
				os.Setenv("MERCHANT_STORE_URL", "https://shop.com")
				os.Setenv("MERCHANT_API_KEY", "key")
				os.Unsetenv("MERCHANT_API_SECRET")
			},
			wantErr: "api_secret is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment
			os.Setenv("ENVIRONMENT", "development")
			os.Unsetenv("MERCHANT_ID")
			os.Unsetenv("MERCHANT_STORE_URL")
			os.Unsetenv("MERCHANT_API_KEY")
			os.Unsetenv("MERCHANT_API_SECRET")

			tt.setup()

			_, err := Load(context.Background())
			if err == nil {
				t.Errorf("Expected error containing %q", tt.wantErr)
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildTransformConfig(t *testing.T) {
	cfg := &Config{
		MerchantID:  "test",
		AdapterType: "woocommerce",
		Merchant: MerchantConfig{
			StoreURL:    "https://shop.example.com/",
			StoreDomain: "shop.example.com",
			PolicyLinks: map[string]string{
				"privacy_policy":   "https://shop.example.com/privacy",
				"terms_of_service": "https://shop.example.com/terms",
			},
		},
	}

	tc := cfg.BuildTransformConfig()

	// Verify store URL has trailing slash removed
	if tc.StoreURL != "https://shop.example.com" {
		t.Errorf("StoreURL = %s, want https://shop.example.com (no trailing slash)", tc.StoreURL)
	}

	if tc.StoreDomain != "shop.example.com" {
		t.Errorf("StoreDomain = %s, want shop.example.com", tc.StoreDomain)
	}

	if tc.UCPVersion != "2026-01-11" {
		t.Errorf("UCPVersion = %s, want 2026-01-11", tc.UCPVersion)
	}

	// Verify capabilities (registry pattern: keyed by reverse-domain name)
	if len(tc.Capabilities) != 3 {
		t.Fatalf("Capabilities len = %d, want 3", len(tc.Capabilities))
	}
	if _, ok := tc.Capabilities["dev.ucp.shopping.checkout"]; !ok {
		t.Error("missing dev.ucp.shopping.checkout capability")
	}
	// Verify discount capability has extended fields
	discounts, ok := tc.Capabilities["dev.ucp.shopping.discount"]
	if !ok {
		t.Fatal("missing dev.ucp.shopping.discount capability")
	}
	discountParents := discounts[0].Extends.GetParents()
	if len(discounts) == 0 || len(discountParents) != 1 || discountParents[0] != "dev.ucp.shopping.checkout" {
		t.Errorf("Discount capability extends = %v, want [dev.ucp.shopping.checkout]", discountParents)
	}
	// Verify fulfillment capability
	fulfillment, ok := tc.Capabilities["dev.ucp.shopping.fulfillment"]
	if !ok {
		t.Fatal("missing dev.ucp.shopping.fulfillment capability")
	}
	fulfillmentParents := fulfillment[0].Extends.GetParents()
	if len(fulfillment) == 0 || len(fulfillmentParents) != 1 || fulfillmentParents[0] != "dev.ucp.shopping.checkout" {
		t.Errorf("Fulfillment capability extends = %v, want [dev.ucp.shopping.checkout]", fulfillmentParents)
	}

	// Verify policy links
	if len(tc.PolicyLinks) != 2 {
		t.Errorf("PolicyLinks len = %d, want 2", len(tc.PolicyLinks))
	}

	// Verify PaymentHandlers is empty when not configured (pass-through)
	if len(tc.PaymentHandlers) != 0 {
		t.Errorf("PaymentHandlers should be empty when not configured, got %d", len(tc.PaymentHandlers))
	}
}

func TestBuildTransformConfigWithPaymentHandlers(t *testing.T) {
	// Test that payment handlers from config are passed through
	cfg := &Config{
		MerchantID: "test",
		Merchant: MerchantConfig{
			StoreURL:    "https://shop.example.com",
			StoreDomain: "shop.example.com",
			PaymentHandlers: map[string][]model.PaymentHandler{
				"com.google.pay": {
					{ID: "gpay-1", Version: "2026-01-11"},
				},
			},
		},
	}

	tc := cfg.BuildTransformConfig()

	// Verify handlers are passed through
	gpayHandlers, ok := tc.PaymentHandlers["com.google.pay"]
	if !ok {
		t.Fatal("missing com.google.pay payment handler")
	}
	if len(gpayHandlers) != 1 || gpayHandlers[0].ID != "gpay-1" {
		t.Errorf("Expected gpay-1 handler, got %v", gpayHandlers)
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://shop.example.com", "shop.example.com"},
		{"https://shop.example.com/", "shop.example.com"},
		{"https://shop.example.com/path/to/page", "shop.example.com"},
		{"http://shop.example.com:8080", "shop.example.com:8080"},
		{"https://sub.shop.example.com", "sub.shop.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := extractDomain(tt.url)
			if got != tt.want {
				t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestEnvOrDefault(t *testing.T) {
	// Test with set value
	os.Setenv("TEST_ENV_VAR", "custom")
	if got := envOrDefault("TEST_ENV_VAR", "default"); got != "custom" {
		t.Errorf("envOrDefault with set var = %q, want custom", got)
	}

	// Test with unset value
	os.Unsetenv("TEST_ENV_VAR_UNSET")
	if got := envOrDefault("TEST_ENV_VAR_UNSET", "default"); got != "default" {
		t.Errorf("envOrDefault with unset var = %q, want default", got)
	}

	// Cleanup
	os.Unsetenv("TEST_ENV_VAR")
}

func TestWithDefault(t *testing.T) {
	if got := withDefault("value", "default"); got != "value" {
		t.Errorf("withDefault(value, default) = %q, want value", got)
	}
	if got := withDefault("", "default"); got != "default" {
		t.Errorf("withDefault('', default) = %q, want default", got)
	}
}

func TestLoadFromFile(t *testing.T) {
	// Create temp config file with payment_handlers
	content := `{
		"port": "9090",
		"environment": "test",
		"log_level": "debug",
		"adapter_type": "woocommerce",
		"merchant_id": "file-merchant",
		"merchant": {
			"store_url": "https://file-shop.com",
			"api_key": "ck_file",
			"api_secret": "cs_file",
			"payment_handlers": {
				"com.example.pay": [{"id": "example-1", "version": "2026-01-11"}]
			}
		}
	}`

	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close()

	// Save and restore CONFIG_FILE
	saved := os.Getenv("CONFIG_FILE")
	defer func() {
		if saved == "" {
			os.Unsetenv("CONFIG_FILE")
		} else {
			os.Setenv("CONFIG_FILE", saved)
		}
	}()

	os.Setenv("CONFIG_FILE", tmpFile.Name())

	cfg, err := Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("Port = %s, want 9090", cfg.Port)
	}
	if cfg.MerchantID != "file-merchant" {
		t.Errorf("MerchantID = %s, want file-merchant", cfg.MerchantID)
	}
	if cfg.Merchant.StoreURL != "https://file-shop.com" {
		t.Errorf("StoreURL = %s, want https://file-shop.com", cfg.Merchant.StoreURL)
	}
	if cfg.Merchant.StoreDomain != "file-shop.com" {
		t.Errorf("StoreDomain = %s, want file-shop.com (derived)", cfg.Merchant.StoreDomain)
	}
	// Verify payment handlers loaded from file
	if len(cfg.Merchant.PaymentHandlers) != 1 {
		t.Errorf("PaymentHandlers len = %d, want 1", len(cfg.Merchant.PaymentHandlers))
	}
}

func TestLoadFromFileErrors(t *testing.T) {
	saved := os.Getenv("CONFIG_FILE")
	defer func() {
		if saved == "" {
			os.Unsetenv("CONFIG_FILE")
		} else {
			os.Setenv("CONFIG_FILE", saved)
		}
	}()

	t.Run("file not found", func(t *testing.T) {
		os.Setenv("CONFIG_FILE", "/nonexistent/config.json")
		_, err := Load(context.Background())
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		tmpFile, _ := os.CreateTemp("", "config-*.json")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("{invalid json")
		tmpFile.Close()

		os.Setenv("CONFIG_FILE", tmpFile.Name())
		_, err := Load(context.Background())
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("missing adapter_type", func(t *testing.T) {
		tmpFile, _ := os.CreateTemp("", "config-*.json")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString(`{"merchant_id": "test"}`)
		tmpFile.Close()

		os.Setenv("CONFIG_FILE", tmpFile.Name())
		_, err := Load(context.Background())
		if err == nil || !strings.Contains(err.Error(), "adapter_type is required") {
			t.Errorf("expected adapter_type error, got: %v", err)
		}
	})
}

func TestValidateWixAdapter(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "valid wix config",
			cfg: &Config{
				AdapterType: "wix",
				Merchant: MerchantConfig{
					WixClientID: "client-123",
					StoreURL:    "https://wix-store.com",
				},
			},
			wantErr: "",
		},
		{
			name: "missing wix_client_id",
			cfg: &Config{
				AdapterType: "wix",
				Merchant: MerchantConfig{
					StoreURL: "https://wix-store.com",
				},
			},
			wantErr: "wix_client_id is required",
		},
		{
			name: "missing store_url for wix",
			cfg: &Config{
				AdapterType: "wix",
				Merchant: MerchantConfig{
					WixClientID: "client-123",
				},
			},
			wantErr: "store_url is required for Wix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validate() unexpected error: %v", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("validate() error = %v, want containing %q", err, tt.wantErr)
				}
			}
		})
	}
}
