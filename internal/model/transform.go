package model

// TransformConfig holds merchant-specific data needed for adapter transformations.
// Shared by all platform adapters (WooCommerce, Wix, etc.).
// Populated from config at startup, reused across requests.
type TransformConfig struct {
	StoreDomain  string // e.g., "acme-store.com"
	StoreURL     string // e.g., "https://acme-store.com"
	ProxyBaseURL string // e.g., "http://localhost:8080" - for advertising transport endpoints
	PolicyLinks  []Link // Pre-configured policy URLs
	UCPVersion   string // e.g., "2026-01-11"

	// Registry-style collections (keyed by reverse-domain name)
	Services        map[string][]Service        // Transport bindings (discovery only)
	Capabilities    map[string][]Capability     // Supported capabilities
	PaymentHandlers map[string][]PaymentHandler // Payment handler configurations

	// Escalation triggers - when matched, CompleteCheckout returns requires_escalation
	Escalation *EscalationConfig `json:"escalation,omitempty"`
}

// EscalationConfig defines triggers for browser checkout escalation.
// Products matching ANY condition require browser completion.
type EscalationConfig struct {
	// CustomFields: product meta keys that trigger escalation when present.
	// e.g., ["_requires_disclaimer", "_age_restricted"]
	CustomFields []string `json:"custom_fields,omitempty"`

	// ProductIDs: explicit product IDs that always require escalation.
	// Useful for specific products without custom fields.
	ProductIDs []int `json:"product_ids,omitempty"`
}
