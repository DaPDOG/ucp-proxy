package woocommerce

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ucp-proxy/internal/adapter"
	"ucp-proxy/internal/model"
	"ucp-proxy/internal/negotiation"
	"ucp-proxy/internal/reconcile"
	"ucp-proxy/internal/transport"
)

// =============================================================================
// NONCE AUTHENTICATION STRATEGY
// =============================================================================
//
// The WooCommerce Store API requires a "nonce" for all mutation operations
// (POST, PUT, DELETE). This is a security measure designed for browser-based
// storefront usage, not server-to-server API calls.
//
// CURRENT STRATEGY: Preflight Every Mutation
//
// Before each mutation, we make a GET /cart request to obtain a fresh nonce,
// then immediately use it for the mutation. This approach:
//
//   - Keeps the proxy completely stateless (no nonce caching)
//   - Guarantees nonce validity (always fresh)
//   - Adds ~50-100ms latency per mutation (one extra HTTP call)
//
// Request flow:
//
//   create_checkout:   GET /cart → POST /checkout     (2 calls)
//   update_checkout:   GET /cart → POST /checkout     (2 calls)
//   complete_checkout: GET /cart → POST /checkout     (2 calls)
//   get_checkout:      GET /checkout                  (1 call, no mutation)
//
// POTENTIAL OPTIMIZATIONS (not implemented):
//
// 1. Encode Nonce in Checkout ID
//    Store nonce alongside cart-token in the checkout ID:
//      gid://domain/Checkout/{order_id}:{cart_token}:{nonce}
//    Trade-off: Nonce can expire (~12-24h), requiring retry logic on 401.
//
// 2. Optimistic + Retry
//    Try mutation without preflight; if 401 "missing nonce", fetch nonce
//    and retry once.
//    Trade-off: First call of session always fails; complex retry logic.
//
// 3. In-Memory Cache with TTL
//    Cache nonce per cart-token with conservative TTL (e.g., 1 hour).
//    Trade-off: Requires stateful proxy; cache invalidation complexity.
//
// The current preflight strategy prioritizes simplicity and reliability over
// latency. For high-throughput scenarios, consider optimization #2.
// =============================================================================

// storeAPIPath is the base path for WooCommerce Store API endpoints.
// Must include /wp-json prefix for proper routing.
const storeAPIPath = "/wp-json/wc/store/v1"

// BatchStrategy controls how batch operations are executed.
type BatchStrategy string

const (
	// BatchStrategyMulti uses the /batch endpoint with per-operation headers.
	// Faster (1 HTTP call) - the default and recommended strategy.
	BatchStrategyMulti BatchStrategy = "multi"

	// BatchStrategySequential executes operations one by one with nonce chaining.
	// Slower (N HTTP calls) but useful as fallback if batch endpoint has issues.
	BatchStrategySequential BatchStrategy = "sequential"
)

// Config holds WooCommerce-specific adapter configuration.
type Config struct {
	StoreURL        string
	APIKey          string
	APISecret       string
	TransformConfig *model.TransformConfig
	BatchStrategy   BatchStrategy // Default: sequential (more reliable)
}

// Client implements the adapter interface for WooCommerce stores using the Store API.
// Requires WooCommerce Blocks plugin (included in WC 6.9+) for Store API endpoints.
//
// The Store API uses Cart-Token headers for session management and Nonce headers
// for mutation authentication. See NONCE AUTHENTICATION STRATEGY above.
type Client struct {
	httpClient      *http.Client
	storeURL        string
	apiKey          string
	apiSecret       string
	transformConfig *model.TransformConfig
	batchStrategy   BatchStrategy
}

// generateCartToken creates a random cart token for a new session.
// WooCommerce Store API binds cart sessions to tokens. Without providing one,
// WooCommerce may reuse sessions based on API credentials, causing cart pollution.
func generateCartToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// New creates a WooCommerce client with the given configuration.
func New(cfg Config) (*Client, error) {
	if cfg.StoreURL == "" {
		return nil, fmt.Errorf("store URL is required")
	}
	if cfg.APIKey == "" || cfg.APISecret == "" {
		return nil, fmt.Errorf("API credentials are required")
	}
	if cfg.TransformConfig == nil {
		return nil, fmt.Errorf("transform config is required")
	}

	strategy := cfg.BatchStrategy
	if strategy == "" {
		strategy = BatchStrategyMulti
	}

	// Use Chrome TLS fingerprint transport to avoid JA3-based rate limiting.
	// See internal/transport for rationale.
	return &Client{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport.NewChromeTransport(30 * time.Second),
		},
		storeURL:        strings.TrimSuffix(cfg.StoreURL, "/"),
		apiKey:          cfg.APIKey,
		apiSecret:       cfg.APISecret,
		transformConfig: cfg.TransformConfig,
		batchStrategy:   strategy,
	}, nil
}

// getEffectiveConfig returns a TransformConfig that accounts for UCP negotiation.
// If a NegotiatedContext exists in the context, returns a config with filtered
// capabilities and payment handlers per the negotiation result.
// Otherwise returns the full business config.
func (c *Client) getEffectiveConfig(ctx context.Context) *model.TransformConfig {
	negotiated := negotiation.GetNegotiatedContext(ctx)
	if negotiated == nil {
		return c.transformConfig
	}

	// Create a copy with negotiated caps (avoid mutating the original)
	cfg := *c.transformConfig
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

// GetProfile returns the UCP discovery profile.
// Advertises both REST and MCP transport endpoints via registry pattern.
func (c *Client) GetProfile(ctx context.Context) (*model.DiscoveryProfile, error) {
	return &model.DiscoveryProfile{
		UCP: model.UCPMetadata{
			Version:         c.transformConfig.UCPVersion,
			Services:        c.transformConfig.Services,
			Capabilities:    c.transformConfig.Capabilities,
			PaymentHandlers: c.transformConfig.PaymentHandlers,
		},
	}, nil
}

// CreateCheckout creates a new checkout from the cart.
//
// New flow (cart-centric):
//  1. Build batch request with add-item + update-customer operations
//  2. Execute batch to build cart
//  3. Return UCP Checkout from cart state
//
// The checkout ID uses Cart format: gid://{domain}/Cart/{cart_token}
// This keeps the cart active for further updates before completion.
func (c *Client) CreateCheckout(ctx context.Context, req *adapter.CreateCheckoutRequest) (*model.Checkout, error) {
	// If cart token provided, use existing cart
	// Use mutation instead of GET - WooCommerce GET /cart returns stale data
	if req.CartToken != "" {
		cart, token, err := c.getCartViaMutation(ctx, req.CartToken, nil)
		if err != nil {
			return nil, err
		}
		checkout := CartToUCP(cart, token, c.getEffectiveConfig(ctx))
		c.applyEscalationStatus(checkout, cart)
		return checkout, nil
	}

	// Build batch request for cart creation
	batch := BuildCreateCheckoutBatch(req)
	if batch == nil {
		return nil, model.NewValidationError("line_items", "at least one item required")
	}

	// Generate fresh cart token for new session.
	// WooCommerce Store API reuses cart sessions by API credentials if no token provided.
	// By generating our own token, we ensure each CreateCheckout gets a fresh cart.
	freshToken := generateCartToken()

	// Execute batch and get cart response
	cart, cartToken, err := c.executeBatch(ctx, batch, freshToken)
	if err != nil {
		return nil, err
	}

	// Transform cart to UCP Checkout
	checkout := CartToUCP(cart, cartToken, c.getEffectiveConfig(ctx))
	c.applyEscalationStatus(checkout, cart)
	return checkout, nil
}

// GetCheckout retrieves an existing checkout.
//
// Handles two ID formats:
//   - Cart ID (gid://{domain}/Cart/{token}): Returns cart state
//   - Checkout ID (gid://{domain}/Checkout/{order_id}:{token}): Returns order + cart state
func (c *Client) GetCheckout(ctx context.Context, checkoutID string) (*model.Checkout, error) {
	isCart, orderID, cartToken, err := ParseCheckoutID(checkoutID)
	if err != nil {
		return nil, model.NewValidationError("checkout_id", err.Error())
	}

	if isCart {
		// Cart phase: just return cart state
		// Use mutation instead of GET - WooCommerce GET /cart returns stale data
		cart, token, err := c.getCartViaMutation(ctx, cartToken, nil)
		if err != nil {
			return nil, err
		}
		checkout := CartToUCP(cart, token, c.getEffectiveConfig(ctx))
		c.applyEscalationStatus(checkout, cart)
		return checkout, nil
	}

	// Checkout phase: get draft order + cart for full state
	draft, err := c.getDraftCheckout(ctx, cartToken)
	if err != nil {
		return nil, err
	}

	// Also get cart for line items and totals
	// Use mutation instead of GET - WooCommerce GET /cart returns stale data
	cart, _, err := c.getCartViaMutation(ctx, cartToken, nil)
	if err != nil {
		return nil, err
	}

	// Combine draft and cart into UCP Checkout
	checkout := DraftToUCP(draft, cart, cartToken, c.getEffectiveConfig(ctx))
	checkout.ID = BuildCheckoutID(c.transformConfig.StoreDomain, orderID, cartToken)
	c.applyEscalationStatus(checkout, cart)
	return checkout, nil
}

// UpdateCheckout modifies checkout details using full PUT replacement semantics.
//
// Requires full state on every call:
//   - line_items: REQUIRED - the complete desired line items
//   - discount_codes: REQUIRED - the complete desired discounts (empty = none)
//
// Reconciles current backend state against desired state, executing only
// the necessary mutations. Uses batch API for efficiency (single HTTP call).
//
// Operation order: remove → update → add → addresses → coupons → shipping
func (c *Client) UpdateCheckout(ctx context.Context, checkoutID string, req *model.CheckoutUpdateRequest) (*model.Checkout, error) {
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

	cartToken := ExtractCartToken(checkoutID)

	// 1. Fetch current cart state for reconciliation
	// Use mutation instead of GET - WooCommerce GET /cart returns stale data
	currentCart, _, err := c.getCartViaMutation(ctx, cartToken, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching current cart: %w", err)
	}

	// 2. Build reconciliation batch
	batch := NewBatch()

	// 3. Always reconcile line items (full PUT semantics)
	c.addLineItemReconciliationOps(batch, currentCart.Items, req.LineItems)

	// 4. Always reconcile discount codes (empty array = remove all)
	c.addDiscountReconciliationOps(batch, currentCart.Coupons, req.DiscountCodes)

	// 5. Add standard updates (addresses, buyer, fulfillment)
	batch.UpdateCustomerWithBuyer(req.BillingAddress, req.ShippingAddress, req.Buyer)

	if req.FulfillmentOptionID != "" {
		batch.SelectShippingRate(req.FulfillmentOptionID, 0)
	}

	// 6. Execute batch
	var cart *WooCartResponse
	var token string

	if batch.HasOperations() {
		cart, token, err = c.executeBatch(ctx, batch.Build(), cartToken)
		if err != nil {
			// Check if it's a coupon error - return checkout with warning per UCP spec
			if apiErr, ok := err.(*model.APIError); ok && len(req.DiscountCodes) > 0 {
				return c.getCartWithMessage(ctx, checkoutID, cartToken, model.NewWarningMessageWithPath(
					"discount_code_invalid",
					fmt.Sprintf("Code '%s' could not be applied: %s", req.DiscountCodes[0], apiErr.Message),
					"$.discounts.codes[0]",
				))
			}
			return nil, err
		}
	} else {
		cart = currentCart
		token = cartToken
	}

	checkout := CartToUCP(cart, token, c.getEffectiveConfig(ctx))
	checkout.ID = checkoutID
	c.applyEscalationStatus(checkout, cart)
	return checkout, nil
}

// addLineItemReconciliationOps computes line item diff and adds batch operations.
// Operation order: remove → update → add (prevents conflicts).
func (c *Client) addLineItemReconciliationOps(batch *BatchBuilder, currentItems []WooCartItem, desiredItems []model.LineItemRequest) {
	// Convert current WooCommerce items to reconciler format
	current := make([]reconcile.CurrentItem, len(currentItems))
	for i, item := range currentItems {
		current[i] = reconcile.CurrentItem{
			ProductID: strconv.Itoa(item.ID), // WooCommerce uses int, reconciler uses string
			BackendID: item.Key,              // cart_item_key for remove/update operations
			Quantity:  item.Quantity,
		}
	}

	// Convert desired items to reconciler format
	desired := make([]reconcile.DesiredItem, len(desiredItems))
	for i, item := range desiredItems {
		desired[i] = reconcile.DesiredItem{
			ProductID: item.ProductID,
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
		}
	}

	// Compute diff
	diff := reconcile.DiffLineItems(current, desired)
	if diff.IsEmpty() {
		return
	}

	// Add operations in order: remove → update → add

	// Remove items no longer in desired state
	for _, item := range diff.ToRemove {
		batch.RemoveItem(item.BackendID) // BackendID is cart_item_key
	}

	// Update quantities for existing items
	for _, item := range diff.ToUpdate {
		batch.UpdateItemQuantity(item.BackendID, item.NewQuantity)
	}

	// Add new items
	for _, item := range diff.ToAdd {
		// WooCommerce expects int product ID, convert from string
		productID, err := strconv.Atoi(item.ProductID)
		if err != nil {
			// Skip invalid product IDs - shouldn't happen with valid UCP data
			continue
		}
		batch.AddItem(productID, item.Quantity)
	}
}

// addDiscountReconciliationOps computes discount diff and adds batch operations.
func (c *Client) addDiscountReconciliationOps(batch *BatchBuilder, currentCoupons []WooCoupon, desiredCodes []string) {
	// Build current codes list
	currentCodes := make([]string, len(currentCoupons))
	for i, coupon := range currentCoupons {
		currentCodes[i] = coupon.Code
	}

	// Compute diff
	diff := reconcile.DiffDiscounts(currentCodes, desiredCodes)
	if diff.IsEmpty() {
		return
	}

	// Remove coupons no longer desired
	for _, code := range diff.ToRemove {
		batch.RemoveCoupon(code)
	}

	// Apply new coupons
	for _, code := range diff.ToApply {
		batch.ApplyCoupon(code)
	}
}

// CompleteCheckout submits payment and finalizes the checkout.
func (c *Client) CompleteCheckout(ctx context.Context, checkoutID string, req *model.CheckoutSubmitRequest) (*model.Checkout, error) {
	cartToken := ExtractCartToken(checkoutID)

	// Check for products requiring escalation
	if c.hasEscalationConfig() {
		cart, _, err := c.getCartViaMutation(ctx, cartToken, nil)
		if err != nil {
			return nil, fmt.Errorf("checking cart for escalation: %w", err)
		}
		if matches := c.findEscalationMatches(cart); len(matches) > 0 {
			return c.buildEscalationResponseForProducts(ctx, checkoutID, cart, cartToken, matches)
		}
	}

	// Find selected payment instrument
	instrument := req.Payment.SelectedInstrument()
	if instrument == nil {
		return nil, model.NewValidationError("payment", "no payment instrument selected")
	}

	wcReq := &WooCheckoutRequest{}

	// Route based on credential type (not handler_id - proxy is PSP-agnostic)
	// Credential type is the contract between tokenization and processing.
	if instrument.Credential == nil {
		return c.buildEscalationResponse(ctx, checkoutID, "No payment credential provided")
	}

	switch instrument.Credential.Type {
	case "stripe.payment_method":
		if err := c.configureStripeGateway(wcReq, instrument); err != nil {
			return nil, err
		}

	case "braintree.nonce":
		if err := c.configureBraintreeGateway(wcReq, instrument); err != nil {
			return nil, err
		}

	default:
		// Unknown credential type - escalate to web checkout
		return c.buildEscalationResponse(ctx, checkoutID,
			fmt.Sprintf("Credential type '%s' not supported by WooCommerce adapter", instrument.Credential.Type))
	}

	// Add billing address from instrument if provided
	if instrument.BillingAddress != nil {
		wcReq.BillingAddress = AddressFromUCP(instrument.BillingAddress)
	}

	// IMPORTANT: Capture cart state BEFORE checkout completion.
	// WooCommerce clears the cart after successful payment, so we need to
	// preserve line items, totals, buyer, etc. for the response.
	//
	// NOTE: We use a mutation (update-customer) instead of GET /cart because
	// WooCommerce's Cart-Token session handling is unreliable for GET requests.
	// GET /cart may return empty/stale data, but mutations always return
	// the correct cart state in their response.
	cart, _, err := c.getCartViaMutation(ctx, cartToken, wcReq.BillingAddress)
	if err != nil {
		// Non-fatal: continue with minimal data - checkout can still proceed
		cart = nil
	}

	// Execute checkout with payment
	wcResp, _, err := c.doCheckoutRequest(ctx, http.MethodPost, "/checkout", wcReq, cartToken)
	if err != nil {
		return nil, err
	}

	// Build response using preserved cart data + order info from WooCommerce.
	// WooCommerce POST /checkout returns minimal data after payment (mainly PaymentResult).
	// We use the cart snapshot for line_items, totals, currency, buyer.
	checkout := c.buildCompletionResponse(ctx, cart, wcResp, checkoutID)

	// Check payment result status
	// WooCommerce PaymentStatus: "success", "pending", "failure"
	// RedirectURL can be order-received (success) or 3DS/auth URL (pending action)
	if wcResp.PaymentResult != nil {
		switch wcResp.PaymentResult.PaymentStatus {
		case "success":
			// Payment completed - order is done, redirect is to order-received page
			checkout.Status = model.StatusCompleted
			if wcResp.PaymentResult.RedirectURL != "" {
				checkout.ContinueURL = wcResp.PaymentResult.RedirectURL
			}
		case "pending":
			// Payment requires action (3DS, redirect to external gateway)
			checkout.Status = model.StatusRequiresEscalation
			checkout.ContinueURL = wcResp.PaymentResult.RedirectURL
			checkout.Messages = append(checkout.Messages, model.Message{
				Type:    "info",
				Code:    "3DS_REQUIRED",
				Content: "Payment requires additional authentication",
			})
		case "failure":
			// Payment failed - keep ready_for_complete so they can retry
			checkout.Status = model.StatusReadyForComplete
			checkout.Messages = append(checkout.Messages, model.NewErrorMessage(
				"PAYMENT_FAILED",
				"Payment was declined",
				model.SeverityRecoverable,
			))
		}
	}

	return checkout, nil
}

// buildCompletionResponse constructs checkout response after payment completion.
// Uses cart snapshot for checkout data, WooCommerce response for order info.
func (c *Client) buildCompletionResponse(ctx context.Context, cart *WooCartResponse, wcResp *WooCheckoutResponse, checkoutID string) *model.Checkout {
	var checkout *model.Checkout

	// Use cart data if available for rich response
	if cart != nil {
		cartToken := ExtractCartToken(checkoutID)
		checkout = CartToUCP(cart, cartToken, c.getEffectiveConfig(ctx))
	} else {
		// Fallback: minimal response from checkout
		checkout = CheckoutToUCP(wcResp, c.getEffectiveConfig(ctx))
	}

	// Set checkout ID
	checkout.ID = checkoutID

	// Set order info from WooCommerce response
	// OrderID is gid format: gid://{domain}/Order/{order_id}
	if wcResp.OrderID > 0 {
		checkout.OrderID = fmt.Sprintf("gid://%s/Order/%d",
			c.transformConfig.StoreDomain, wcResp.OrderID)

		// Build permalink URL
		checkout.OrderPermalinkURL = buildOrderPermalinkURL(
			c.transformConfig.StoreURL, wcResp.OrderID, wcResp.OrderKey)

		// Set continue_url to order permalink if not already set by PaymentResult
		if checkout.ContinueURL == "" {
			checkout.ContinueURL = checkout.OrderPermalinkURL
		}
	}

	return checkout
}

// CancelCheckout cancels a checkout session.
// WooCommerce Store API doesn't have a direct cancel endpoint.
// Returns escalation response directing user to web checkout for cancellation.
func (c *Client) CancelCheckout(ctx context.Context, checkoutID string) (*model.Checkout, error) {
	// Get current checkout state
	checkout, err := c.GetCheckout(ctx, checkoutID)
	if err != nil {
		return nil, err
	}

	// WooCommerce Store API doesn't support direct cancellation.
	// Return escalation response directing to web interface.
	checkout.Status = model.StatusRequiresEscalation
	checkout.Messages = append(checkout.Messages, model.Message{
		Type:    "info",
		Code:    "CANCEL_REQUIRES_ESCALATION",
		Content: "Order cancellation requires web checkout",
	})

	return checkout, nil
}

// === Helper Methods ===

// nonceInfo holds nonce and cart token from a preflight request.
type nonceInfo struct {
	nonce     string
	cartToken string
}

// fetchNonce performs a preflight GET /cart request to obtain a fresh nonce.
// The Store API returns nonce in response headers on every request.
// This must be called before any mutation (POST/PUT) operation.
func (c *Client) fetchNonce(ctx context.Context, cartToken string) (*nonceInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.storeURL+storeAPIPath+"/cart", nil)
	if err != nil {
		return nil, fmt.Errorf("creating nonce request: %w", err)
	}

	// Use standard headers (nonce not needed for GET)
	c.setStoreAPIHeaders(req, cartToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, model.NewUpstreamError("WooCommerce", err)
	}
	defer resp.Body.Close()

	// Drain body to allow connection reuse
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		// Surface rate limit errors properly instead of generic 502
		if resp.StatusCode == 429 {
			return nil, model.NewRateLimitError("WooCommerce")
		}
		return nil, model.NewUpstreamError("WooCommerce",
			fmt.Errorf("nonce preflight failed with status %d", resp.StatusCode))
	}

	nonce := resp.Header.Get("Nonce")
	if nonce == "" {
		return nil, model.NewUpstreamError("WooCommerce",
			fmt.Errorf("no nonce returned from Store API"))
	}

	// Use our provided token if we have one (ensures fresh cart sessions).
	// Only fall back to WooCommerce's returned token if we didn't provide one.
	returnedToken := resp.Header.Get("Cart-Token")
	if cartToken != "" {
		returnedToken = cartToken // Use OUR token, not WooCommerce's
	} else if returnedToken == "" {
		returnedToken = cartToken
	}

	return &nonceInfo{
		nonce:     nonce,
		cartToken: returnedToken,
	}, nil
}

// configureStripeGateway sets up WooCommerce request for the Stripe gateway plugin.
// Maps credential token to WooCommerce Stripe plugin's expected format.
func (c *Client) configureStripeGateway(wcReq *WooCheckoutRequest, instrument *model.PaymentInstrument) error {
	wcReq.PaymentMethod = "stripe"
	wcReq.PaymentData = []WooPaymentData{
		{Key: "wc-stripe-payment-method", Value: instrument.Credential.Token},
		{Key: "wc-stripe-is-deferred-intent", Value: "true"},
	}
	return nil
}

// configureBraintreeGateway sets up WooCommerce request for the Braintree gateway plugin.
// Maps credential token to WooCommerce Braintree plugin's expected format.
func (c *Client) configureBraintreeGateway(wcReq *WooCheckoutRequest, instrument *model.PaymentInstrument) error {
	wcReq.PaymentMethod = "braintree_credit_card"
	wcReq.PaymentData = []WooPaymentData{
		{Key: "wc_braintree_credit_card_payment_nonce", Value: instrument.Credential.Token},
	}
	return nil
}

// buildEscalationResponse creates a checkout response for browser escalation.
func (c *Client) buildEscalationResponse(ctx context.Context, checkoutID, reason string) (*model.Checkout, error) {
	checkout, err := c.GetCheckout(ctx, checkoutID)
	if err != nil {
		return nil, err
	}

	checkout.Status = model.StatusRequiresEscalation
	checkout.Messages = append(checkout.Messages, model.Message{
		Type:    "info",
		Code:    "ESCALATION_REQUIRED",
		Content: reason,
	})

	return checkout, nil
}

// escalationMatch captures why a product triggered escalation.
type escalationMatch struct {
	ProductID   int
	ProductName string
	Reason      string // "product_id" or the custom field key that matched
}

// hasEscalationConfig returns true if any escalation triggers are configured.
func (c *Client) hasEscalationConfig() bool {
	cfg := c.transformConfig.Escalation
	return cfg != nil && (len(cfg.ProductIDs) > 0 || len(cfg.CustomFields) > 0)
}

// findEscalationMatches checks cart items against escalation config.
// Returns matches for products that require browser checkout.
func (c *Client) findEscalationMatches(cart *WooCartResponse) []escalationMatch {
	if cart == nil || !c.hasEscalationConfig() {
		return nil
	}

	cfg := c.transformConfig.Escalation
	var matches []escalationMatch

	// Build lookup sets for O(1) checks
	productIDSet := make(map[int]bool)
	for _, id := range cfg.ProductIDs {
		productIDSet[id] = true
	}
	customFieldSet := make(map[string]bool)
	for _, field := range cfg.CustomFields {
		customFieldSet[field] = true
	}

	for _, item := range cart.Items {
		// Check product ID match
		if productIDSet[item.ID] {
			matches = append(matches, escalationMatch{
				ProductID:   item.ID,
				ProductName: item.Name,
				Reason:      "product_id",
			})
			continue // Already matched, skip custom field check
		}

		// Check custom field match (requires merchant to expose via Store API filter)
		for _, meta := range item.MetaData {
			if customFieldSet[meta.Key] {
				matches = append(matches, escalationMatch{
					ProductID:   item.ID,
					ProductName: item.Name,
					Reason:      meta.Key,
				})
				break // One match per item is enough
			}
		}
	}
	return matches
}

// applyEscalationStatus checks cart for escalation triggers and updates checkout status.
// Called during Create/Get/Update to inform agent early. Does NOT block - just sets status.
// Returns true if escalation was detected.
func (c *Client) applyEscalationStatus(checkout *model.Checkout, cart *WooCartResponse) bool {
	if !c.hasEscalationConfig() || cart == nil {
		return false
	}

	matches := c.findEscalationMatches(cart)
	if len(matches) == 0 {
		return false
	}

	// Set status to inform agent (they can still continue iterating)
	checkout.Status = model.StatusRequiresEscalation
	checkout.ContinueURL = c.buildShareableCheckoutURL(cart)

	// Collect product names for message
	var productNames []string
	for _, m := range matches {
		productNames = append(productNames, m.ProductName)
	}

	checkout.Messages = append(checkout.Messages, model.Message{
		Type:     "error",
		Code:     "ESCALATION_REQUIRED",
		Content:  fmt.Sprintf("Products require additional buyer input: %s. Complete checkout in browser.", strings.Join(productNames, ", ")),
		Severity: string(model.SeverityEscalation),
	})

	return true
}

// buildEscalationResponseForProducts creates escalation response for matched products.
// Returns checkout with requires_escalation status and continue_url to web checkout.
func (c *Client) buildEscalationResponseForProducts(ctx context.Context, checkoutID string, cart *WooCartResponse, cartToken string, matches []escalationMatch) (*model.Checkout, error) {
	checkout := CartToUCP(cart, cartToken, c.getEffectiveConfig(ctx))
	checkout.ID = checkoutID
	checkout.Status = model.StatusRequiresEscalation

	// Build shareable checkout URL with products pre-populated
	checkout.ContinueURL = c.buildShareableCheckoutURL(cart)

	// Collect product names for user-friendly message
	var productNames []string
	for _, m := range matches {
		productNames = append(productNames, m.ProductName)
	}

	checkout.Messages = append(checkout.Messages, model.Message{
		Type:     "error",
		Code:     "ESCALATION_REQUIRED",
		Content:  fmt.Sprintf("Products require additional buyer input: %s. Complete checkout in browser.", strings.Join(productNames, ", ")),
		Severity: string(model.SeverityEscalation),
	})

	return checkout, nil
}

// buildShareableCheckoutURL creates a WooCommerce shareable checkout URL.
// Format: /checkout-link/?products=ID:QTY,ID:QTY
// This URL adds products to cart and redirects to checkout in one step.
func (c *Client) buildShareableCheckoutURL(cart *WooCartResponse) string {
	if cart == nil || len(cart.Items) == 0 {
		return c.transformConfig.StoreURL + "/checkout"
	}

	// Build products parameter: ID:QTY,ID:QTY
	var products []string
	for _, item := range cart.Items {
		products = append(products, fmt.Sprintf("%d:%d", item.ID, item.Quantity))
	}

	return fmt.Sprintf("%s/checkout-link/?products=%s",
		c.transformConfig.StoreURL,
		strings.Join(products, ","))
}

// doCheckoutRequest executes a request to the WooCommerce checkout endpoint.
// Path should be relative to Store API (e.g., "/checkout" not "/wc/store/v1/checkout").
// For non-GET methods, performs nonce preflight automatically.
func (c *Client) doCheckoutRequest(ctx context.Context, method, path string, body interface{}, cartToken string) (*WooCheckoutResponse, string, error) {
	var nonce string
	effectiveToken := cartToken

	// Mutations require nonce preflight
	if method != http.MethodGet {
		nonceData, err := c.fetchNonce(ctx, cartToken)
		if err != nil {
			return nil, "", fmt.Errorf("fetching nonce: %w", err)
		}
		nonce = nonceData.nonce
		effectiveToken = nonceData.cartToken
	}

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("marshaling request: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.storeURL+storeAPIPath+path, bodyReader)
	if err != nil {
		return nil, "", fmt.Errorf("creating request: %w", err)
	}

	c.setStoreAPIHeaders(req, effectiveToken, nonce)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", model.NewUpstreamError("WooCommerce", err)
	}
	defer resp.Body.Close()

	// Capture returned cart token
	returnedToken := resp.Header.Get("Cart-Token")

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading response: %w", err)
	}

	// Handle error responses
	if resp.StatusCode >= 400 {
		return nil, "", c.parseErrorResponse(resp.StatusCode, respBody)
	}

	var wcResp WooCheckoutResponse
	if err := json.Unmarshal(respBody, &wcResp); err != nil {
		return nil, "", fmt.Errorf("parsing response: %w", err)
	}

	return &wcResp, returnedToken, nil
}

// userAgent identifies this client to upstream servers.
// Required: WooCommerce CDN/WAF rate-limits requests without User-Agent.
const userAgent = "UCP-Proxy/1.0"

// setStoreAPIHeaders sets headers for WooCommerce Store API requests.
// Store API uses Cart-Token for session and Nonce for mutation auth.
// Unlike REST API v3, Store API does NOT use Basic Auth.
func (c *Client) setStoreAPIHeaders(req *http.Request, cartToken, nonce string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	if cartToken != "" {
		req.Header.Set("Cart-Token", cartToken)
	}
	if nonce != "" {
		req.Header.Set("Nonce", nonce)
	}
}

// handleErrorResponse reads and parses an error response from WooCommerce.
func (c *Client) handleErrorResponse(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return c.parseErrorResponse(resp.StatusCode, body)
}

// parseErrorResponse converts WooCommerce error to APIError.
func (c *Client) parseErrorResponse(statusCode int, body []byte) error {
	var wcErr WooErrorResponse
	json.Unmarshal(body, &wcErr) // Best effort parse

	switch statusCode {
	case 404:
		return model.NewNotFoundError("checkout")
	case 401, 403:
		return model.NewUnauthorizedError("WooCommerce authentication failed")
	case 400:
		msg := wcErr.Message
		if msg == "" {
			msg = "invalid request"
		}
		return model.NewValidationError("request", msg)
	case 429:
		return model.NewRateLimitError("WooCommerce")
	default:
		return model.NewUpstreamError("WooCommerce",
			fmt.Errorf("status %d: %s - %s", statusCode, wcErr.Code, wcErr.Message))
	}
}

// === New Cart-Centric Methods ===

// getCartViaMutation fetches cart state using a mutation (POST) instead of GET.
// This works around WooCommerce's Cart-Token session handling bug where GET /cart
// may return empty/stale data even with a valid token.
//
// Uses POST /cart/update-customer which always returns the correct cart state.
// The billing address parameter is optional - if nil, sends minimal customer update.
func (c *Client) getCartViaMutation(ctx context.Context, cartToken string, billing *WooAddress) (*WooCartResponse, string, error) {
	// Build update-customer request body
	body := map[string]interface{}{}
	if billing != nil {
		body["billing_address"] = billing
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling customer update: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.storeURL+storeAPIPath+"/cart/update-customer", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, "", fmt.Errorf("creating update-customer request: %w", err)
	}

	c.setStoreAPIHeaders(req, cartToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", model.NewUpstreamError("WooCommerce", err)
	}
	defer resp.Body.Close()

	returnedToken := resp.Header.Get("Cart-Token")
	if returnedToken == "" {
		returnedToken = cartToken
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading update-customer response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, "", c.parseErrorResponse(resp.StatusCode, respBody)
	}

	var cart WooCartResponse
	if err := json.Unmarshal(respBody, &cart); err != nil {
		return nil, "", fmt.Errorf("parsing cart response: %w", err)
	}

	return &cart, returnedToken, nil
}

// getCart fetches current cart state.
// Returns cart response, cart token, and any error.
func (c *Client) getCart(ctx context.Context, cartToken string) (*WooCartResponse, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.storeURL+storeAPIPath+"/cart", nil)
	if err != nil {
		return nil, "", fmt.Errorf("creating cart request: %w", err)
	}

	c.setStoreAPIHeaders(req, cartToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", model.NewUpstreamError("WooCommerce", err)
	}
	defer resp.Body.Close()

	// Capture returned cart token for fallback
	returnedToken := resp.Header.Get("Cart-Token")
	if returnedToken == "" {
		returnedToken = cartToken
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading cart response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, "", c.parseErrorResponse(resp.StatusCode, body)
	}

	var cart WooCartResponse
	if err := json.Unmarshal(body, &cart); err != nil {
		return nil, "", fmt.Errorf("parsing cart response: %w", err)
	}

	// Prefer passed-in token over header token.
	// WooCommerce sometimes returns a different (stale) session token in the header
	// even when we query with a specific cart token. The passed-in token is the one
	// we explicitly want, so use it.
	effectiveToken := cartToken
	if effectiveToken == "" {
		effectiveToken = returnedToken
	}

	return &cart, effectiveToken, nil
}

// getDraftCheckout fetches draft order info from GET /checkout.
// Creates a draft order if one doesn't exist for this cart.
func (c *Client) getDraftCheckout(ctx context.Context, cartToken string) (*WooDraftCheckout, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.storeURL+storeAPIPath+"/checkout", nil)
	if err != nil {
		return nil, fmt.Errorf("creating checkout request: %w", err)
	}

	c.setStoreAPIHeaders(req, cartToken, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, model.NewUpstreamError("WooCommerce", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading checkout response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, c.parseErrorResponse(resp.StatusCode, body)
	}

	var draft WooDraftCheckout
	if err := json.Unmarshal(body, &draft); err != nil {
		return nil, fmt.Errorf("parsing checkout response: %w", err)
	}

	return &draft, nil
}

// executeBatch dispatches to the appropriate batch execution strategy.
func (c *Client) executeBatch(ctx context.Context, batch *WooBatchRequest, cartToken string) (*WooCartResponse, string, error) {
	switch c.batchStrategy {
	case BatchStrategySequential:
		return c.executeBatchSequential(ctx, batch, cartToken)
	default:
		return c.executeBatchEndpoint(ctx, batch, cartToken)
	}
}

// executeBatchSequential executes a batch of cart operations one by one.
// Returns the final cart state after all operations complete.
//
// This strategy is more reliable than native batch because:
// 1. Each operation gets fresh nonce from previous response
// 2. We use the cart response from mutation (not separate GET /cart)
// 3. Avoids WooCommerce batch endpoint's header propagation issues
//
// Trade-off: N HTTP calls instead of 1, adding ~50-100ms per operation.
//
// IMPORTANT: We use the cart response from the LAST operation rather than making
// a separate GET /cart call. WooCommerce's GET /cart with Cart-Token header has
// inconsistent session handling - it often returns an empty/different cart even
// when the token is valid. But cart mutation responses (add-item, etc.) always
// return the correct cart state in their response body.
func (c *Client) executeBatchSequential(ctx context.Context, batch *WooBatchRequest, cartToken string) (*WooCartResponse, string, error) {
	// Preflight to get initial nonce (required for Store API mutations)
	nonceData, err := c.fetchNonce(ctx, cartToken)
	if err != nil {
		return nil, "", fmt.Errorf("fetching nonce: %w", err)
	}

	currentToken := nonceData.cartToken
	currentNonce := nonceData.nonce

	var lastCart *WooCartResponse

	// Execute each operation sequentially
	for i, op := range batch.Requests {
		cart, newNonce, newToken, err := c.executeCartOperation(ctx, op, currentToken, currentNonce)
		if err != nil {
			return nil, "", fmt.Errorf("operation %d (%s) failed: %w", i, op.Path, err)
		}

		// Store cart from last operation
		lastCart = cart

		// Chain nonce and token for next operation
		if newNonce != "" {
			currentNonce = newNonce
		}
		if newToken != "" {
			currentToken = newToken
		}
	}

	// Use cart from last operation instead of fetching again
	return lastCart, currentToken, nil
}

// =============================================================================
// MULTI BATCH EXECUTION (uses /batch endpoint with per-operation headers)
// =============================================================================

// executeBatchEndpoint executes batch via the WooCommerce /batch endpoint.
// This is more efficient than sequential execution (1 HTTP call vs N calls).
//
// Key insight: The /batch endpoint doesn't propagate parent request headers to
// sub-operations. We must include Cart-Token and Nonce in each operation's
// headers field for authentication to work.
//
// Returns the cart from the last successful operation, the final cart token,
// and any error encountered.
func (c *Client) executeBatchEndpoint(ctx context.Context, batch *WooBatchRequest, cartToken string) (*WooCartResponse, string, error) {
	if batch == nil || len(batch.Requests) == 0 {
		return nil, "", fmt.Errorf("empty batch request")
	}

	// Preflight to get nonce (required for Store API mutations)
	nonceData, err := c.fetchNonce(ctx, cartToken)
	if err != nil {
		return nil, "", fmt.Errorf("fetching nonce: %w", err)
	}

	// Inject authentication headers into each sub-operation
	// This is the key: batch endpoint requires per-operation auth headers
	authHeaders := map[string]string{
		"Nonce":      nonceData.nonce,
		"Cart-Token": nonceData.cartToken,
	}
	batch.InjectHeaders(authHeaders)

	// Marshal batch request
	batchJSON, err := json.Marshal(batch)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling batch: %w", err)
	}

	// POST to /batch endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.storeURL+storeAPIPath+"/batch", bytes.NewReader(batchJSON))
	if err != nil {
		return nil, "", fmt.Errorf("creating batch request: %w", err)
	}

	// Set parent request headers (may or may not be used by WooCommerce)
	c.setStoreAPIHeaders(req, nonceData.cartToken, nonceData.nonce)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", model.NewUpstreamError("WooCommerce", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading batch response: %w", err)
	}

	// Handle HTTP-level errors (batch endpoint itself failed)
	if resp.StatusCode >= 400 {
		return nil, "", c.parseErrorResponse(resp.StatusCode, body)
	}

	// Parse batch response
	var batchResp WooBatchResponse
	if err := json.Unmarshal(body, &batchResp); err != nil {
		return nil, "", fmt.Errorf("parsing batch response: %w", err)
	}

	// Process responses - find last successful cart response
	var lastCart *WooCartResponse
	var lastToken string

	for _, result := range batchResp.Responses {
		// Check for sub-operation errors - use parseErrorResponse to get proper APIError
		if result.Status >= 400 {
			return nil, "", c.parseErrorResponse(result.Status, result.Body)
		}

		// Parse cart response from successful operation
		var cart WooCartResponse
		if err := json.Unmarshal(result.Body, &cart); err != nil {
			return nil, "", fmt.Errorf("parsing batch result: %w", err)
		}

		lastCart = &cart

		// Capture cart token from sub-operation headers
		if result.Headers.CartToken != "" {
			lastToken = result.Headers.CartToken
		}
	}

	// Use token from last operation, or fall back to preflight token
	if lastToken == "" {
		lastToken = nonceData.cartToken
	}

	return lastCart, lastToken, nil
}

// executeCartOperation executes a single cart operation.
// Returns the cart response, nonce, and cart token from the response.
// All WooCommerce cart operations (add-item, remove-item, update-customer, etc.)
// return the full cart state in their response body.
func (c *Client) executeCartOperation(ctx context.Context, op WooBatchOperation, cartToken, nonce string) (*WooCartResponse, string, string, error) {
	// Strip /wc/store/v1 prefix if present (operation paths include full path)
	path := op.Path
	if strings.HasPrefix(path, "/wc/store/v1") {
		path = strings.TrimPrefix(path, "/wc/store/v1")
	}

	var bodyReader io.Reader
	if len(op.Body) > 0 {
		bodyReader = bytes.NewReader(op.Body)
	}

	fullURL := c.storeURL + storeAPIPath + path
	req, err := http.NewRequestWithContext(ctx, op.Method, fullURL, bodyReader)
	if err != nil {
		return nil, "", "", fmt.Errorf("creating request: %w", err)
	}

	c.setStoreAPIHeaders(req, cartToken, nonce)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", "", model.NewUpstreamError("WooCommerce", err)
	}
	defer resp.Body.Close()

	// Capture response headers for chaining
	newNonce := resp.Header.Get("Nonce")
	newToken := resp.Header.Get("Cart-Token")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, "", "", c.parseErrorResponse(resp.StatusCode, body)
	}

	// Parse cart response - all cart operations return cart state
	var cart WooCartResponse
	if err := json.Unmarshal(body, &cart); err != nil {
		return nil, "", "", fmt.Errorf("parsing cart response: %w", err)
	}

	return &cart, newNonce, newToken, nil
}

// getCartWithMessage fetches cart and adds an error message to the response.
// Uses mutation instead of GET - WooCommerce GET /cart returns stale data.
func (c *Client) getCartWithMessage(ctx context.Context, checkoutID, cartToken string, msg model.Message) (*model.Checkout, error) {
	cart, token, err := c.getCartViaMutation(ctx, cartToken, nil)
	if err != nil {
		return nil, err
	}

	checkout := CartToUCP(cart, token, c.getEffectiveConfig(ctx))
	checkout.ID = checkoutID
	checkout.Messages = append(checkout.Messages, msg)
	c.applyEscalationStatus(checkout, cart)
	return checkout, nil
}
