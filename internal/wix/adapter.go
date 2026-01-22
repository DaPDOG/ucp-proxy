package wix

import (
	"context"
	"fmt"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/model"
	"ucp-proxy/internal/negotiation"
	"ucp-proxy/internal/reconcile"
)

// =============================================================================
// WIX ADAPTER
// =============================================================================
//
// This adapter implements the UCP interface for Wix eCommerce stores.
//
// Current Implementation (Escalation Flow):
// Payment completes via browser handoff. Programmatic completion to be added.
//
// Flow:
//   1. CreateCheckout → creates visitor session + cart + checkout
//   2. UpdateCheckout → updates addresses, shipping selection
//   3. When checkout is complete (minus payment) → status: requires_escalation
//   4. Agent presents continue_url to buyer for Wix hosted checkout
//
// Key Design Decisions:
//   - Instance token encoded in checkout ID for stateless operation
//   - Status transitions: incomplete → requires_escalation (no ready_for_complete)
// =============================================================================

// Config holds Wix-specific adapter configuration.
type Config struct {
	ClientID        string // OAuth app client ID (no secret needed for anonymous flow)
	TransformConfig *model.TransformConfig
}

// Adapter implements the adapter.Adapter interface for Wix stores.
type Adapter struct {
	client *Client
	config *model.TransformConfig
}

// New creates a Wix adapter with the given configuration.
func New(cfg Config) (*Adapter, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("Wix client ID is required")
	}
	if cfg.TransformConfig == nil {
		return nil, fmt.Errorf("transform config is required")
	}

	return &Adapter{
		client: NewClient(cfg.ClientID),
		config: cfg.TransformConfig,
	}, nil
}

// getEffectiveConfig returns a TransformConfig that accounts for UCP negotiation.
// If a NegotiatedContext exists in the context, returns a config with filtered
// capabilities and payment handlers per the negotiation result.
func (a *Adapter) getEffectiveConfig(ctx context.Context) *model.TransformConfig {
	negotiated := negotiation.GetNegotiatedContext(ctx)
	if negotiated == nil {
		return a.config
	}

	cfg := *a.config
	if negotiated.Capabilities != nil {
		cfg.Capabilities = negotiated.Capabilities
	}
	if negotiated.PaymentHandlers != nil {
		cfg.PaymentHandlers = negotiated.PaymentHandlers
	}
	if negotiated.Version != "" {
		cfg.UCPVersion = negotiated.Version
	}
	return &cfg
}

// GetProfile returns the UCP discovery profile for this Wix merchant.
func (a *Adapter) GetProfile(ctx context.Context) (*model.DiscoveryProfile, error) {
	return &model.DiscoveryProfile{
		UCP: model.UCPMetadata{
			Version:         a.config.UCPVersion,
			Services:        a.config.Services,
			Capabilities:    a.config.Capabilities,
			PaymentHandlers: a.config.PaymentHandlers,
		},
	}, nil
}

// CreateCheckout creates a new checkout session.
//
// Flow:
//  1. Get anonymous OAuth token (creates visitor session)
//  2. Add line items to cart
//  3. Create checkout from cart
//  4. Return UCP checkout with encoded access token
func (a *Adapter) CreateCheckout(ctx context.Context, req *adapter.CreateCheckoutRequest) (*model.Checkout, error) {
	// 1. Get anonymous OAuth token
	tokenResp, err := a.client.GetAnonymousToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting anonymous token: %w", err)
	}
	accessToken := tokenResp.AccessToken

	// 2. Add line items to cart
	if len(req.LineItems) == 0 {
		return nil, model.NewValidationError("line_items", "at least one item required")
	}

	items := make([]WixLineItemInput, len(req.LineItems))
	for i, li := range req.LineItems {
		items[i] = WixLineItemInput{
			CatalogReference: &WixCatalogRef{
				CatalogItemID: li.ProductID,
				AppID:         WixStoresAppID,
			},
			Quantity: li.Quantity,
		}
	}

	_, err = a.client.AddToCart(ctx, accessToken, items)
	if err != nil {
		return nil, fmt.Errorf("adding items to cart: %w", err)
	}

	// 3. Create checkout from current cart (cart is tied to token session)
	wixCheckout, err := a.client.CreateCheckoutFromCart(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("creating checkout from cart: %w", err)
	}

	// 4. Apply initial buyer/address info if provided
	if req.Buyer != nil || req.ShippingAddress != nil || req.BillingAddress != nil {
		update := &WixCheckoutUpdate{}

		if req.Buyer != nil {
			update.BuyerInfo = BuyerToWix(req.Buyer)
		}

		if req.ShippingAddress != nil {
			update.ShippingInfo = &WixShippingInfo{
				ShippingDestination: AddressToWix(req.ShippingAddress),
			}
		}

		if req.BillingAddress != nil {
			update.BillingInfo = &WixBillingInfo{
				Address: AddressToWixBilling(req.BillingAddress),
			}
		}

		wixCheckout, err = a.client.UpdateCheckout(ctx, accessToken, wixCheckout.ID, update, "")
		if err != nil {
			// Non-fatal: continue with checkout even if initial update fails
			// The fields can be updated later
		}
	}

	// Transform to UCP format (encodes access token in checkout ID)
	checkout := CheckoutToUCP(wixCheckout, accessToken, a.getEffectiveConfig(ctx))
	return checkout, nil
}

// GetCheckout retrieves an existing checkout by ID.
func (a *Adapter) GetCheckout(ctx context.Context, checkoutID string) (*model.Checkout, error) {
	wixCheckoutID, accessToken, err := ParseCheckoutID(checkoutID)
	if err != nil {
		return nil, model.NewValidationError("checkout_id", err.Error())
	}

	wixCheckout, err := a.client.GetCheckout(ctx, accessToken, wixCheckoutID)
	if err != nil {
		return nil, err
	}

	// Get available shipping options if address is set
	if wixCheckout.ShippingInfo != nil && wixCheckout.ShippingInfo.ShippingDestination != nil {
		options, err := a.client.GetShippingOptions(ctx, accessToken, wixCheckoutID)
		if err == nil && len(options) > 0 {
			wixCheckout.AvailableShippingOptions = options
		}
	}

	checkout := CheckoutToUCP(wixCheckout, accessToken, a.getEffectiveConfig(ctx))

	// If status is requires_escalation, add continue_url
	if checkout.Status == model.StatusRequiresEscalation {
		a.addContinueURL(ctx, checkout, accessToken, wixCheckoutID)
	}

	return checkout, nil
}

// UpdateCheckout modifies checkout details using full PUT replacement semantics.
//
// For line items and discount codes, performs reconciliation:
//   - Fetches current state
//   - Diffs against desired state
//   - Executes add/remove/update operations
//
// For addresses, buyer, and fulfillment: applies updates directly.
//
// Auto-Escalation:
// When all non-payment fields are present, status transitions to requires_escalation
// and continue_url is populated for buyer handoff.
func (a *Adapter) UpdateCheckout(ctx context.Context, checkoutID string, req *model.CheckoutUpdateRequest) (*model.Checkout, error) {
	// Validate required fields for PUT semantics
	if req.LineItems == nil {
		return nil, model.NewValidationError("line_items", "required for PUT - send full desired state")
	}
	if len(req.LineItems) == 0 {
		return nil, model.NewValidationError("line_items", "checkout requires at least one item")
	}
	if req.DiscountCodes == nil {
		return nil, model.NewValidationError("discount_codes", "required for PUT - send empty array for no discounts")
	}

	wixCheckoutID, accessToken, err := ParseCheckoutID(checkoutID)
	if err != nil {
		return nil, model.NewValidationError("checkout_id", err.Error())
	}

	// 1. Fetch current state for reconciliation
	wixCheckout, err := a.client.GetCheckout(ctx, accessToken, wixCheckoutID)
	if err != nil {
		return nil, fmt.Errorf("fetching current checkout: %w", err)
	}

	// 2. Always reconcile line items (full PUT semantics)
	wixCheckout, err = a.reconcileLineItems(ctx, accessToken, wixCheckoutID, wixCheckout, req.LineItems)
	if err != nil {
		return nil, fmt.Errorf("reconciling line items: %w", err)
	}

	// 3. Build standard update request for addresses/buyer/fulfillment
	update := &WixCheckoutUpdate{}
	hasUpdate := false

	if req.ShippingAddress != nil {
		update.ShippingInfo = &WixShippingInfo{
			ShippingDestination: AddressToWix(req.ShippingAddress),
		}
		hasUpdate = true
	}

	if req.BillingAddress != nil {
		update.BillingInfo = &WixBillingInfo{
			Address: AddressToWixBilling(req.BillingAddress),
		}
		hasUpdate = true
	}

	if req.Buyer != nil {
		update.BuyerInfo = BuyerToWix(req.Buyer)
		hasUpdate = true
	}

	if req.FulfillmentOptionID != "" {
		if update.ShippingInfo == nil {
			update.ShippingInfo = &WixShippingInfo{}
		}
		update.ShippingInfo.SelectedCarrierServiceOption = &WixSelectedShipping{
			Code: req.FulfillmentOptionID,
		}
		hasUpdate = true
	}

	// Copy contact details from buyer to shipping for better UX
	if req.Buyer != nil && update.ShippingInfo != nil && update.ShippingInfo.ShippingDestination != nil {
		cd := update.ShippingInfo.ShippingDestination.ContactDetails
		if cd == nil {
			cd = &WixContactDetails{}
			update.ShippingInfo.ShippingDestination.ContactDetails = cd
		}
		if cd.Phone == "" && req.Buyer.PhoneNumber != "" {
			cd.Phone = req.Buyer.PhoneNumber
		}
		if cd.FirstName == "" && req.Buyer.FirstName != "" {
			cd.FirstName = req.Buyer.FirstName
		}
		if cd.LastName == "" && req.Buyer.LastName != "" {
			cd.LastName = req.Buyer.LastName
		}
	}

	// 4. Handle discount codes (Wix only supports one coupon at a time)
	// Empty array = no coupon, non-empty = use first code
	couponCode := ""
	if len(req.DiscountCodes) > 0 {
		couponCode = req.DiscountCodes[0]
		hasUpdate = true
	}

	// 5. Apply updates if any
	if hasUpdate {
		wixCheckout, err = a.client.UpdateCheckout(ctx, accessToken, wixCheckoutID, update, couponCode)
		if err != nil {
			return nil, err
		}
	}

	// 6. Fetch shipping options if we have an address
	if wixCheckout.ShippingInfo != nil && wixCheckout.ShippingInfo.ShippingDestination != nil {
		options, err := a.client.GetShippingOptions(ctx, accessToken, wixCheckoutID)
		if err == nil && len(options) > 0 {
			wixCheckout.AvailableShippingOptions = options
		}
	}

	// 7. Transform to UCP format
	checkout := CheckoutToUCP(wixCheckout, accessToken, a.getEffectiveConfig(ctx))

	// Auto-Escalation: add continue_url when ready
	if checkout.Status == model.StatusRequiresEscalation {
		a.addContinueURL(ctx, checkout, accessToken, wixCheckoutID)
		checkout.Messages = append(checkout.Messages,
			model.NewErrorMessage("PAYMENT_HANDOFF",
				"Checkout is ready. Please complete payment on the merchant checkout page.",
				model.SeverityEscalation))
	}

	return checkout, nil
}

// reconcileLineItems performs diff-based reconciliation of line items.
// Executes operations in order: remove → update → add to prevent conflicts.
func (a *Adapter) reconcileLineItems(ctx context.Context, accessToken, checkoutID string, current *WixCheckout, desired []model.LineItemRequest) (*WixCheckout, error) {
	// Convert current Wix items to reconciler format
	currentItems := make([]reconcile.CurrentItem, len(current.LineItems))
	for i, item := range current.LineItems {
		productID := ""
		if item.CatalogReference != nil {
			productID = item.CatalogReference.CatalogItemID
		}
		currentItems[i] = reconcile.CurrentItem{
			ProductID: productID,
			BackendID: item.ID, // Wix line item ID
			Quantity:  item.Quantity,
		}
	}

	// Convert desired items to reconciler format
	desiredItems := make([]reconcile.DesiredItem, len(desired))
	for i, item := range desired {
		desiredItems[i] = reconcile.DesiredItem{
			ProductID: item.ProductID,
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
		}
	}

	// Compute diff
	diff := reconcile.DiffLineItems(currentItems, desiredItems)
	if diff.IsEmpty() {
		return current, nil // No changes needed
	}

	var err error
	result := current

	// Execute in order: remove → update → add

	// Remove items no longer in desired state
	if len(diff.ToRemove) > 0 {
		ids := make([]string, len(diff.ToRemove))
		for i, item := range diff.ToRemove {
			ids[i] = item.BackendID
		}
		result, err = a.client.RemoveLineItems(ctx, accessToken, checkoutID, ids)
		if err != nil {
			return nil, fmt.Errorf("removing line items: %w", err)
		}
	}

	// Update quantities for existing items
	if len(diff.ToUpdate) > 0 {
		updates := make([]WixQuantityUpdate, len(diff.ToUpdate))
		for i, u := range diff.ToUpdate {
			updates[i] = WixQuantityUpdate{
				ID:       u.BackendID,
				Quantity: u.NewQuantity,
			}
		}
		result, err = a.client.UpdateLineItemsQuantity(ctx, accessToken, checkoutID, updates)
		if err != nil {
			return nil, fmt.Errorf("updating quantities: %w", err)
		}
	}

	// Add new items
	if len(diff.ToAdd) > 0 {
		items := make([]WixLineItemInput, len(diff.ToAdd))
		for i, item := range diff.ToAdd {
			items[i] = WixLineItemInput{
				CatalogReference: &WixCatalogRef{
					CatalogItemID: item.ProductID,
					AppID:         WixStoresAppID,
				},
				Quantity: item.Quantity,
			}
		}
		result, err = a.client.AddToCheckout(ctx, accessToken, checkoutID, items)
		if err != nil {
			return nil, fmt.Errorf("adding line items: %w", err)
		}
	}

	return result, nil
}

// CompleteCheckout attempts to complete the checkout with payment.
//
// Current Implementation:
// Programmatic payment not yet supported. Always returns requires_escalation
// with a continue_url. The buyer must complete payment on
// Wix's hosted checkout page.
func (a *Adapter) CompleteCheckout(ctx context.Context, checkoutID string, req *model.CheckoutSubmitRequest) (*model.Checkout, error) {
	wixCheckoutID, accessToken, err := ParseCheckoutID(checkoutID)
	if err != nil {
		return nil, model.NewValidationError("checkout_id", err.Error())
	}

	// Get current checkout state
	wixCheckout, err := a.client.GetCheckout(ctx, accessToken, wixCheckoutID)
	if err != nil {
		return nil, err
	}

	// Transform to UCP format
	checkout := CheckoutToUCP(wixCheckout, accessToken, a.getEffectiveConfig(ctx))

	// Always escalate - programmatic payment completion not yet implemented
	checkout.Status = model.StatusRequiresEscalation
	a.addContinueURL(ctx, checkout, accessToken, wixCheckoutID)

	checkout.Messages = append(checkout.Messages,
		model.NewErrorMessage("PAYMENT_HANDOFF",
			"Payment cannot be processed via API. Please complete payment on the merchant checkout page.",
			model.SeverityEscalation))
	return checkout, nil
}

// CancelCheckout cancels a checkout session.
// Wix doesn't have a direct cancel API, so we return escalation.
func (a *Adapter) CancelCheckout(ctx context.Context, checkoutID string) (*model.Checkout, error) {
	checkout, err := a.GetCheckout(ctx, checkoutID)
	if err != nil {
		return nil, err
	}

	checkout.Status = model.StatusRequiresEscalation
	checkout.Messages = append(checkout.Messages, model.Message{
		Type:    "info",
		Code:    "CANCEL_REQUIRES_ESCALATION",
		Content: "Order cancellation requires the merchant checkout page.",
	})

	return checkout, nil
}

// === Helper Methods ===

// addContinueURL creates a redirect session and adds the continue URL to the checkout.
func (a *Adapter) addContinueURL(ctx context.Context, checkout *model.Checkout, accessToken, wixCheckoutID string) {
	// Create redirect session for Wix hosted checkout
	redirect, err := a.client.CreateRedirectSession(ctx, accessToken, wixCheckoutID, &WixCallbacks{
		PostFlowURL:     a.config.StoreURL + "/checkout/thank-you",
		ThankYouPageURL: a.config.StoreURL + "/checkout/thank-you",
	})
	if err != nil {
		// Non-fatal: use fallback URL
		checkout.ContinueURL = a.config.StoreURL + "/checkout"
		return
	}

	checkout.ContinueURL = redirect.FullURL
}

// Ensure Adapter implements adapter.Adapter interface.
var _ adapter.Adapter = (*Adapter)(nil)
