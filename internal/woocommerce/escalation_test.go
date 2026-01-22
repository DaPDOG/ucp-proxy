package woocommerce

import (
	"testing"

	"ucp-proxy/internal/model"
)

// testClient creates a minimal Client with escalation config for testing.
func testClientWithEscalation(cfg *model.EscalationConfig) *Client {
	return &Client{
		transformConfig: &model.TransformConfig{
			StoreURL:   "https://example.com",
			Escalation: cfg,
		},
	}
}

func TestHasEscalationConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *model.EscalationConfig
		want bool
	}{
		{
			name: "nil config",
			cfg:  nil,
			want: false,
		},
		{
			name: "empty config",
			cfg:  &model.EscalationConfig{},
			want: false,
		},
		{
			name: "only product IDs",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{123, 456},
			},
			want: true,
		},
		{
			name: "only custom fields",
			cfg: &model.EscalationConfig{
				CustomFields: []string{"_requires_disclaimer"},
			},
			want: true,
		},
		{
			name: "both product IDs and custom fields",
			cfg: &model.EscalationConfig{
				ProductIDs:   []int{123},
				CustomFields: []string{"_age_restricted"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClientWithEscalation(tt.cfg)
			if got := c.hasEscalationConfig(); got != tt.want {
				t.Errorf("hasEscalationConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindEscalationMatches(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *model.EscalationConfig
		cart       *WooCartResponse
		wantCount  int
		wantIDs    []int
		wantReason string
	}{
		{
			name: "no config - no matches",
			cfg:  nil,
			cart: &WooCartResponse{
				Items: []WooCartItem{{ID: 123, Name: "Product A"}},
			},
			wantCount: 0,
		},
		{
			name: "nil cart - no matches",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{123},
			},
			cart:      nil,
			wantCount: 0,
		},
		{
			name: "single product match",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{123},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Name: "Escalation Product"},
				},
			},
			wantCount:  1,
			wantIDs:    []int{123},
			wantReason: "product_id",
		},
		{
			name: "no match - different product",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{999},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Name: "Normal Product"},
				},
			},
			wantCount: 0,
		},
		{
			name: "partial match - one of two products triggers escalation",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{456},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Name: "Normal Product"},
					{ID: 456, Name: "Escalation Product"},
				},
			},
			wantCount:  1,
			wantIDs:    []int{456},
			wantReason: "product_id",
		},
		{
			name: "multiple matches - two escalation products",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{123, 456},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Name: "Escalation A"},
					{ID: 456, Name: "Escalation B"},
					{ID: 789, Name: "Normal Product"},
				},
			},
			wantCount: 2,
			wantIDs:   []int{123, 456},
		},
		{
			name: "custom fields configured but no metadata on item",
			cfg: &model.EscalationConfig{
				CustomFields: []string{"_requires_disclaimer"},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Name: "Product A"},
				},
			},
			wantCount: 0,
		},
		{
			name: "custom field match - metadata present",
			cfg: &model.EscalationConfig{
				CustomFields: []string{"_requires_disclaimer"},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{
						ID:   123,
						Name: "Disclaimer Product",
						MetaData: []WooItemMeta{
							{Key: "_requires_disclaimer", Value: "true"},
						},
					},
				},
			},
			wantCount:  1,
			wantIDs:    []int{123},
			wantReason: "_requires_disclaimer",
		},
		{
			name: "custom field match - multiple fields configured, one matches",
			cfg: &model.EscalationConfig{
				CustomFields: []string{"_requires_disclaimer", "_age_restricted"},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{
						ID:   456,
						Name: "Age Restricted Product",
						MetaData: []WooItemMeta{
							{Key: "_age_restricted", Value: "21"},
						},
					},
				},
			},
			wantCount:  1,
			wantIDs:    []int{456},
			wantReason: "_age_restricted",
		},
		{
			name: "product ID takes precedence over custom field",
			cfg: &model.EscalationConfig{
				ProductIDs:   []int{123},
				CustomFields: []string{"_requires_disclaimer"},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{
						ID:   123,
						Name: "Product with Both",
						MetaData: []WooItemMeta{
							{Key: "_requires_disclaimer", Value: "true"},
						},
					},
				},
			},
			wantCount:  1,
			wantIDs:    []int{123},
			wantReason: "product_id", // ID match, not custom field
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClientWithEscalation(tt.cfg)
			matches := c.findEscalationMatches(tt.cart)

			if len(matches) != tt.wantCount {
				t.Errorf("findEscalationMatches() returned %d matches, want %d", len(matches), tt.wantCount)
				return
			}

			// Verify matched product IDs
			for i, wantID := range tt.wantIDs {
				if i >= len(matches) {
					break
				}
				if matches[i].ProductID != wantID {
					t.Errorf("match[%d].ProductID = %d, want %d", i, matches[i].ProductID, wantID)
				}
			}

			// Verify reason if specified
			if tt.wantReason != "" && len(matches) > 0 {
				if matches[0].Reason != tt.wantReason {
					t.Errorf("match[0].Reason = %q, want %q", matches[0].Reason, tt.wantReason)
				}
			}
		})
	}
}

func TestApplyEscalationStatus(t *testing.T) {
	tests := []struct {
		name           string
		cfg            *model.EscalationConfig
		cart           *WooCartResponse
		wantEscalation bool
		wantStatus     model.CheckoutStatus
		wantMsgCount   int
	}{
		{
			name: "no escalation config - no change",
			cfg:  nil,
			cart: &WooCartResponse{
				Items: []WooCartItem{{ID: 123, Name: "Product"}},
			},
			wantEscalation: false,
			wantStatus:     "", // unchanged
			wantMsgCount:   0,
		},
		{
			name: "no matching products - no change",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{999},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{{ID: 123, Name: "Product"}},
			},
			wantEscalation: false,
			wantStatus:     "", // unchanged
			wantMsgCount:   0,
		},
		{
			name: "matching product - escalation triggered",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{123},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{{ID: 123, Name: "Escalation Product"}},
			},
			wantEscalation: true,
			wantStatus:     model.StatusRequiresEscalation,
			wantMsgCount:   1,
		},
		{
			name: "partial match - one normal, one escalation",
			cfg: &model.EscalationConfig{
				ProductIDs: []int{456},
			},
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Name: "Normal Product"},
					{ID: 456, Name: "Escalation Product"},
				},
			},
			wantEscalation: true,
			wantStatus:     model.StatusRequiresEscalation,
			wantMsgCount:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testClientWithEscalation(tt.cfg)
			checkout := &model.Checkout{}

			result := c.applyEscalationStatus(checkout, tt.cart)

			if result != tt.wantEscalation {
				t.Errorf("applyEscalationStatus() = %v, want %v", result, tt.wantEscalation)
			}

			if tt.wantStatus != "" && checkout.Status != tt.wantStatus {
				t.Errorf("checkout.Status = %q, want %q", checkout.Status, tt.wantStatus)
			}

			if len(checkout.Messages) != tt.wantMsgCount {
				t.Errorf("len(checkout.Messages) = %d, want %d", len(checkout.Messages), tt.wantMsgCount)
			}

			// Verify message structure if escalation triggered
			if tt.wantEscalation && tt.wantMsgCount > 0 {
				msg := checkout.Messages[0]
				if msg.Type != "error" {
					t.Errorf("message.Type = %q, want %q", msg.Type, "error")
				}
				if msg.Code != "ESCALATION_REQUIRED" {
					t.Errorf("message.Code = %q, want %q", msg.Code, "ESCALATION_REQUIRED")
				}
				if msg.Severity != string(model.SeverityEscalation) {
					t.Errorf("message.Severity = %q, want %q", msg.Severity, model.SeverityEscalation)
				}
				if checkout.ContinueURL == "" {
					t.Error("checkout.ContinueURL should be set for escalation")
				}
			}
		})
	}
}

func TestBuildShareableCheckoutURL(t *testing.T) {
	tests := []struct {
		name     string
		storeURL string
		cart     *WooCartResponse
		want     string
	}{
		{
			name:     "nil cart - fallback to checkout",
			storeURL: "https://example.com",
			cart:     nil,
			want:     "https://example.com/checkout",
		},
		{
			name:     "empty cart - fallback to checkout",
			storeURL: "https://example.com",
			cart:     &WooCartResponse{Items: []WooCartItem{}},
			want:     "https://example.com/checkout",
		},
		{
			name:     "single product",
			storeURL: "https://example.com",
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Quantity: 1},
				},
			},
			want: "https://example.com/checkout-link/?products=123:1",
		},
		{
			name:     "multiple products",
			storeURL: "https://example.com",
			cart: &WooCartResponse{
				Items: []WooCartItem{
					{ID: 123, Quantity: 2},
					{ID: 456, Quantity: 1},
				},
			},
			want: "https://example.com/checkout-link/?products=123:2,456:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				transformConfig: &model.TransformConfig{
					StoreURL: tt.storeURL,
				},
			}
			if got := c.buildShareableCheckoutURL(tt.cart); got != tt.want {
				t.Errorf("buildShareableCheckoutURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
