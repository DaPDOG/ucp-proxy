// Package config handles loading and validation of service configuration.
// Supports both development (env vars) and production (Secret Manager) modes.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"

	"ucp-proxy/internal/model"
)

// Config holds all service configuration.
// Environment determines whether secrets load from env vars (development) or Secret Manager (production).
type Config struct {
	// Server settings
	Port        string
	Environment string // "development" or "production"
	LogLevel    string // "debug", "info", "warn", "error"

	// GCP settings (required in production)
	GCPProject string
	MerchantID string

	// Adapter type (currently only "woocommerce" supported)
	AdapterType string

	// Merchant-specific configuration (loaded from secrets)
	Merchant MerchantConfig
}

// MerchantConfig contains merchant-specific settings.
// In production, this is loaded from Secret Manager as JSON.
// In development, loaded from individual env vars or CONFIG_FILE.
type MerchantConfig struct {
	StoreURL     string            `json:"store_url"`
	StoreDomain  string            `json:"store_domain"` // Derived from StoreURL if not set
	APIKey       string            `json:"api_key"`
	APISecret    string            `json:"api_secret"`
	PolicyLinks  map[string]string `json:"policy_links,omitempty"`
	MerchantName string            `json:"merchant_name,omitempty"`

	// Wix OAuth credentials (presence enables Wix adapter)
	WixClientID string `json:"wix_client_id,omitempty"`

	// Payment handlers to advertise - merchant provides full config, proxy passes through.
	// Keyed by reverse-domain handler type (e.g., "com.google.pay").
	// Proxy is PSP-agnostic; handler configs are opaque and passed to agents as-is.
	PaymentHandlers map[string][]model.PaymentHandler `json:"payment_handlers,omitempty"`

	// Escalation triggers for browser checkout.
	// When cart matches ANY condition, CompleteCheckout returns requires_escalation.
	Escalation *model.EscalationConfig `json:"escalation,omitempty"`
}

// Load reads configuration from file, environment, or Secret Manager.
// Priority: CONFIG_FILE (if set) → ENV vars / Secret Manager.
// Validates all required fields and returns an error if any are missing.
func Load(ctx context.Context) (*Config, error) {
	// If CONFIG_FILE is set, load everything from the JSON file
	if configPath := os.Getenv("CONFIG_FILE"); configPath != "" {
		return loadFromFile(configPath)
	}

	// Otherwise, use ENV vars / Secret Manager approach
	cfg := &Config{
		Port:        envOrDefault("PORT", "8080"),
		Environment: envOrDefault("ENVIRONMENT", "development"),
		LogLevel:    envOrDefault("LOG_LEVEL", "info"),
		GCPProject:  os.Getenv("GCP_PROJECT"),
		MerchantID:  os.Getenv("MERCHANT_ID"),
		AdapterType: envOrDefault("ADAPTER_TYPE", "woocommerce"),
	}

	// MerchantID required in all environments
	if cfg.MerchantID == "" {
		return nil, fmt.Errorf("MERCHANT_ID environment variable required")
	}

	// Load merchant config based on environment
	var err error
	if cfg.Environment == "production" {
		if cfg.GCPProject == "" {
			return nil, fmt.Errorf("GCP_PROJECT required in production environment")
		}
		err = cfg.loadFromSecretManager(ctx)
	} else {
		err = cfg.loadFromEnv()
	}
	if err != nil {
		return nil, fmt.Errorf("loading merchant config: %w", err)
	}

	// Derive store domain from URL if not explicitly set
	if cfg.Merchant.StoreDomain == "" && cfg.Merchant.StoreURL != "" {
		cfg.Merchant.StoreDomain = extractDomain(cfg.Merchant.StoreURL)
	}

	// Validate required merchant fields
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadFromFile reads all configuration from a JSON file.
// Used for local development to avoid multiple ENV vars.
func loadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Use a struct that matches the JSON structure
	var fileConfig struct {
		Port        string         `json:"port"`
		Environment string         `json:"environment"`
		LogLevel    string         `json:"log_level"`
		AdapterType string         `json:"adapter_type"`
		MerchantID  string         `json:"merchant_id"`
		Merchant    MerchantConfig `json:"merchant"`
	}

	if err := json.Unmarshal(data, &fileConfig); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	cfg := &Config{
		Port:        withDefault(fileConfig.Port, "8080"),
		Environment: withDefault(fileConfig.Environment, "development"),
		LogLevel:    withDefault(fileConfig.LogLevel, "info"),
		AdapterType: fileConfig.AdapterType,
		MerchantID:  fileConfig.MerchantID,
		Merchant:    fileConfig.Merchant,
	}

	if cfg.AdapterType == "" {
		return nil, fmt.Errorf("adapter_type is required (woocommerce or wix)")
	}

	// Derive store domain from URL if not explicitly set
	if cfg.Merchant.StoreDomain == "" && cfg.Merchant.StoreURL != "" {
		cfg.Merchant.StoreDomain = extractDomain(cfg.Merchant.StoreURL)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// withDefault returns val if non-empty, otherwise defaultVal.
func withDefault(val, defaultVal string) string {
	if val != "" {
		return val
	}
	return defaultVal
}

// loadFromSecretManager fetches merchant config from GCP Secret Manager.
// Secret name format: projects/{project}/secrets/{merchant_id}/versions/latest
func (c *Config) loadFromSecretManager(ctx context.Context) error {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("creating secret manager client: %w", err)
	}
	defer client.Close()

	secretName := fmt.Sprintf("projects/%s/secrets/%s/versions/latest",
		c.GCPProject, c.MerchantID)

	result, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName,
	})
	if err != nil {
		return fmt.Errorf("accessing secret %s: %w", secretName, err)
	}

	if err := json.Unmarshal(result.Payload.Data, &c.Merchant); err != nil {
		return fmt.Errorf("parsing secret JSON: %w", err)
	}

	return nil
}

// loadFromEnv reads merchant config from individual environment variables.
// Used in development mode for local testing.
func (c *Config) loadFromEnv() error {
	c.Merchant = MerchantConfig{
		StoreURL:     os.Getenv("MERCHANT_STORE_URL"),
		StoreDomain:  os.Getenv("MERCHANT_STORE_DOMAIN"),
		APIKey:       os.Getenv("MERCHANT_API_KEY"),
		APISecret:    os.Getenv("MERCHANT_API_SECRET"),
		MerchantName: os.Getenv("MERCHANT_NAME"),
		WixClientID:  os.Getenv("MERCHANT_WIX_CLIENT_ID"),
	}

	// Parse policy links JSON if provided
	if linksJSON := os.Getenv("POLICY_LINKS"); linksJSON != "" {
		if err := json.Unmarshal([]byte(linksJSON), &c.Merchant.PolicyLinks); err != nil {
			return fmt.Errorf("parsing POLICY_LINKS JSON: %w", err)
		}
	}

	// Parse payment handlers JSON if provided
	if handlersJSON := os.Getenv("PAYMENT_HANDLERS"); handlersJSON != "" {
		if err := json.Unmarshal([]byte(handlersJSON), &c.Merchant.PaymentHandlers); err != nil {
			return fmt.Errorf("parsing PAYMENT_HANDLERS JSON: %w", err)
		}
	}

	return nil
}

// validate checks that all required configuration fields are present.
func (c *Config) validate() error {
	// Wix adapter uses OAuth with client_id only
	if c.AdapterType == "wix" {
		if c.Merchant.WixClientID == "" {
			return fmt.Errorf("wix_client_id is required for Wix adapter")
		}
		if c.Merchant.StoreURL == "" {
			return fmt.Errorf("store_url is required for Wix adapter")
		}
		// Validate store URL is well-formed
		if _, err := url.Parse(c.Merchant.StoreURL); err != nil {
			return fmt.Errorf("invalid store_url: %w", err)
		}
		return nil
	}

	// WooCommerce and other adapters require full configuration
	if c.Merchant.StoreURL == "" {
		return fmt.Errorf("store_url is required")
	}
	if c.Merchant.APIKey == "" {
		return fmt.Errorf("api_key is required")
	}
	if c.Merchant.APISecret == "" {
		return fmt.Errorf("api_secret is required")
	}

	// Validate store URL is well-formed
	if _, err := url.Parse(c.Merchant.StoreURL); err != nil {
		return fmt.Errorf("invalid store_url: %w", err)
	}

	return nil
}

// BuildTransformConfig creates the transformation configuration used by adapters.
// Converts merchant config into the format expected by adapter transform functions.
func (c *Config) BuildTransformConfig() *model.TransformConfig {
	links := c.buildPolicyLinks()

	// Build proxy base URL for transport endpoint discovery
	// In production, use PROXY_BASE_URL env var; default to localhost for dev
	proxyBaseURL := os.Getenv("PROXY_BASE_URL")
	if proxyBaseURL == "" {
		proxyBaseURL = fmt.Sprintf("http://localhost:%s", c.Port)
	}

	// Ensure PaymentHandlers is never nil (MCP schema requires arrays)
	handlers := c.Merchant.PaymentHandlers
	if handlers == nil {
		handlers = make(map[string][]model.PaymentHandler)
	}

	return &model.TransformConfig{
		StoreDomain:     c.Merchant.StoreDomain,
		StoreURL:        strings.TrimSuffix(c.Merchant.StoreURL, "/"),
		ProxyBaseURL:    strings.TrimSuffix(proxyBaseURL, "/"),
		PolicyLinks:     links,
		UCPVersion:      "2026-01-11",
		Services:        c.buildServices(proxyBaseURL),
		Capabilities:    defaultCapabilities(),
		PaymentHandlers: handlers,
		Escalation:      c.Merchant.Escalation,
	}
}

// buildPolicyLinks converts the policy links map to model.Link slice.
// Always returns non-nil slice since MCP schema validation requires arrays.
func (c *Config) buildPolicyLinks() []model.Link {
	if len(c.Merchant.PolicyLinks) == 0 {
		return []model.Link{}
	}

	links := make([]model.Link, 0, len(c.Merchant.PolicyLinks))
	for linkType, linkURL := range c.Merchant.PolicyLinks {
		links = append(links, model.Link{
			Type: model.LinkType(linkType),
			URL:  linkURL,
		})
	}
	return links
}

// buildServices creates the service bindings for discovery profile.
// Advertises REST and MCP transports for the checkout capability.
func (c *Config) buildServices(proxyBaseURL string) map[string][]model.Service {
	baseURL := strings.TrimSuffix(proxyBaseURL, "/")
	return map[string][]model.Service{
		"dev.ucp.shopping.checkout": {
			{
				Version:   "2026-01-11",
				Transport: "rest",
				Endpoint:  baseURL + "/checkout-sessions",
				Spec:      "https://ucp.dev/specs/shopping/checkout",
				Schema:    "https://ucp.dev/schemas/shopping/checkout.json",
			},
			{
				Version:   "2026-01-11",
				Transport: "mcp",
				Endpoint:  baseURL + "/mcp",
				Spec:      "https://ucp.dev/specs/shopping/checkout",
				Schema:    "https://ucp.dev/schemas/shopping/checkout.json",
			},
		},
	}
}

// defaultCapabilities returns the UCP capabilities supported by this proxy.
// Uses registry pattern: map keyed by reverse-domain capability name.
// Includes base checkout capability and extensions for discount and fulfillment.
func defaultCapabilities() map[string][]model.Capability {
	return map[string][]model.Capability{
		"dev.ucp.shopping.checkout": {
			{
				Version: "2026-01-11",
				Spec:    "https://ucp.dev/specs/shopping/checkout",
				Schema:  "https://ucp.dev/schemas/shopping/checkout.json",
			},
		},
		"dev.ucp.shopping.discount": {
			{
				Version: "2026-01-11",
				Extends: model.NewSingleExtends("dev.ucp.shopping.checkout"),
				Spec:    "https://ucp.dev/specs/shopping/discount",
				Schema:  "https://ucp.dev/schemas/shopping/discount.json",
			},
		},
		"dev.ucp.shopping.fulfillment": {
			{
				Version: "2026-01-11",
				Extends: model.NewSingleExtends("dev.ucp.shopping.checkout"),
				Spec:    "https://ucp.dev/specs/shopping/fulfillment",
				Schema:  "https://ucp.dev/schemas/shopping/fulfillment.json",
			},
		},
	}
}

// extractDomain parses the domain from a URL string.
func extractDomain(storeURL string) string {
	u, err := url.Parse(storeURL)
	if err != nil {
		// Fallback: strip protocol prefix manually
		domain := strings.TrimPrefix(storeURL, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		return strings.Split(domain, "/")[0]
	}
	return u.Host
}

// envOrDefault returns the environment variable value or the default if not set.
func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
