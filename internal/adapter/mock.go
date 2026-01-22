package adapter

import (
	"context"

	"ucp-proxy/internal/model"
)

// Mock implements Adapter for testing.
// Each method can be configured via function fields.
type Mock struct {
	GetProfileFunc       func(ctx context.Context) (*model.DiscoveryProfile, error)
	CreateCheckoutFunc   func(ctx context.Context, req *CreateCheckoutRequest) (*model.Checkout, error)
	GetCheckoutFunc      func(ctx context.Context, id string) (*model.Checkout, error)
	UpdateCheckoutFunc   func(ctx context.Context, id string, req *model.CheckoutUpdateRequest) (*model.Checkout, error)
	CompleteCheckoutFunc func(ctx context.Context, id string, req *model.CheckoutSubmitRequest) (*model.Checkout, error)
	CancelCheckoutFunc   func(ctx context.Context, id string) (*model.Checkout, error)
}

// GetProfile calls the configured GetProfileFunc or returns a default profile.
func (m *Mock) GetProfile(ctx context.Context) (*model.DiscoveryProfile, error) {
	if m.GetProfileFunc != nil {
		return m.GetProfileFunc(ctx)
	}
	return &model.DiscoveryProfile{
		UCP: model.UCPMetadata{
			Version: "2026-01-11",
			Capabilities: map[string][]model.Capability{
				"dev.ucp.shopping.checkout": {{Version: "2026-01-11"}},
			},
		},
	}, nil
}

// CreateCheckout calls the configured CreateCheckoutFunc or returns an error.
func (m *Mock) CreateCheckout(ctx context.Context, req *CreateCheckoutRequest) (*model.Checkout, error) {
	if m.CreateCheckoutFunc != nil {
		return m.CreateCheckoutFunc(ctx, req)
	}
	return nil, model.NewInternalError(nil)
}

// GetCheckout calls the configured GetCheckoutFunc or returns an error.
func (m *Mock) GetCheckout(ctx context.Context, id string) (*model.Checkout, error) {
	if m.GetCheckoutFunc != nil {
		return m.GetCheckoutFunc(ctx, id)
	}
	return nil, model.NewNotFoundError("checkout")
}

// UpdateCheckout calls the configured UpdateCheckoutFunc or returns an error.
func (m *Mock) UpdateCheckout(ctx context.Context, id string, req *model.CheckoutUpdateRequest) (*model.Checkout, error) {
	if m.UpdateCheckoutFunc != nil {
		return m.UpdateCheckoutFunc(ctx, id, req)
	}
	return nil, model.NewNotFoundError("checkout")
}

// CompleteCheckout calls the configured CompleteCheckoutFunc or returns an error.
func (m *Mock) CompleteCheckout(ctx context.Context, id string, req *model.CheckoutSubmitRequest) (*model.Checkout, error) {
	if m.CompleteCheckoutFunc != nil {
		return m.CompleteCheckoutFunc(ctx, id, req)
	}
	return nil, model.NewNotFoundError("checkout")
}

// CancelCheckout calls the configured CancelCheckoutFunc or returns an error.
func (m *Mock) CancelCheckout(ctx context.Context, id string) (*model.Checkout, error) {
	if m.CancelCheckoutFunc != nil {
		return m.CancelCheckoutFunc(ctx, id)
	}
	return nil, model.NewNotFoundError("checkout")
}

// Verify Mock implements Adapter interface at compile time.
var _ Adapter = (*Mock)(nil)
