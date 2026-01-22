// Package adapter defines the interface for e-commerce platform integrations.
// Adapters translate platform-specific APIs to UCP format.
package adapter

import (
	"context"

	"ucp-proxy/internal/model"
)

// Adapter abstracts e-commerce platform operations into a unified interface.
// Each platform (WooCommerce, Wix, etc.) provides its own implementation.
//
// All methods return UCP-format responses ready for API serialization.
// Platform-specific error handling is encapsulated within each implementation.
type Adapter interface {
	// GetProfile returns the UCP discovery profile for this merchant.
	// Used by the /.well-known/ucp endpoint to advertise capabilities.
	GetProfile(ctx context.Context) (*model.DiscoveryProfile, error)

	// CreateCheckout creates a new checkout session from a cart or line items.
	// For WooCommerce: Uses Store API checkout endpoint with Cart-Token.
	// Returns the checkout in UCP format with encoded cart token in ID.
	CreateCheckout(ctx context.Context, req *CreateCheckoutRequest) (*model.Checkout, error)

	// GetCheckout retrieves an existing checkout by ID.
	// The checkoutID may contain an encoded cart token (format: gid://domain/Checkout/{id}:{token}).
	GetCheckout(ctx context.Context, checkoutID string) (*model.Checkout, error)

	// UpdateCheckout modifies checkout details.
	// Supports: shipping/billing address, shipping option, discount codes.
	// Returns the updated checkout state.
	UpdateCheckout(ctx context.Context, checkoutID string, req *model.CheckoutUpdateRequest) (*model.Checkout, error)

	// CompleteCheckout submits payment and finalizes the checkout.
	// Routes payment based on handler: Google Pay passes Stripe token, redirect escalates.
	// Returns completed checkout or escalation response with continue_url.
	CompleteCheckout(ctx context.Context, checkoutID string, req *model.CheckoutSubmitRequest) (*model.Checkout, error)

	// CancelCheckout cancels a checkout session.
	// Returns the canceled checkout with status set to canceled.
	CancelCheckout(ctx context.Context, checkoutID string) (*model.Checkout, error)
}

// CreateCheckoutRequest contains data for creating a new checkout.
// Either CartToken (existing cart) or LineItems (new cart) should be provided.
type CreateCheckoutRequest struct {
	// CartToken identifies an existing WooCommerce cart session.
	// If provided, uses cart contents for checkout.
	CartToken string `json:"cart_token,omitempty"`

	// LineItems for creating a new cart. Used when CartToken is not provided.
	LineItems []model.LineItemRequest `json:"line_items,omitempty"`

	// Addresses (optional) - pre-populate checkout with buyer info
	ShippingAddress *model.PostalAddress `json:"shipping_address,omitempty"`
	BillingAddress  *model.PostalAddress `json:"billing_address,omitempty"`
	Buyer           *model.Buyer         `json:"buyer,omitempty"`
}

// Config holds common configuration for adapters.
// Platform-specific config (like TransformConfig) is handled by each adapter.
type Config struct {
	StoreURL  string
	APIKey    string
	APISecret string
}
