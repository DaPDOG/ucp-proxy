package woocommerce

import (
	"fmt"
	"strconv"
	"strings"

	"ucp-proxy/internal/model"
)

// orderStatusMap translates WooCommerce order statuses to UCP checkout statuses.
// Used ONLY for final order status (after draft checkout is created).
// For cart phase, use determineCartStatus() instead.
var orderStatusMap = map[string]model.CheckoutStatus{
	"checkout-draft": model.StatusIncomplete,         // Draft = still building
	"pending":        model.StatusCompleteInProgress, // Awaiting payment
	"processing":     model.StatusCompleteInProgress, // Payment received, processing
	"on-hold":        model.StatusRequiresEscalation, // Needs human intervention
	"completed":      model.StatusCompleted,
	"cancelled":      model.StatusCanceled,
	"failed":         model.StatusIncomplete, // Payment failed, can retry
	"refunded":       model.StatusCompleted,  // Still completed, refund is separate
}

// MapOrderStatus converts WooCommerce order status to UCP status.
// Returns StatusIncomplete for unknown statuses as a safe default.
func MapOrderStatus(wcStatus string) model.CheckoutStatus {
	if status, ok := orderStatusMap[wcStatus]; ok {
		return status
	}
	return model.StatusIncomplete
}

// determineCartStatus determines UCP status from cart state.
// This is the primary status determination for cart-building phase.
//
// Ready for complete means the agent CAN call complete - billing address will
// come with the payment instrument at complete time (per UCP spec).
// We only need: items, no errors, buyer email, and shipping (if physical).
func determineCartStatus(cart *WooCartResponse) model.CheckoutStatus {
	if cart == nil || len(cart.Items) == 0 {
		return model.StatusIncomplete
	}

	// Cart-level errors = incomplete
	if len(cart.Errors) > 0 {
		return model.StatusIncomplete
	}

	// Buyer email required for order notifications.
	// Note: Full billing address comes via payment instrument at complete time.
	if cart.BillingAddress.Email == "" {
		return model.StatusIncomplete
	}

	// If needs shipping, must have address and selected rate
	if cart.NeedsShipping {
		if !hasRequiredShippingFields(&cart.ShippingAddress) {
			return model.StatusIncomplete
		}
		if !hasSelectedShippingRate(cart.ShippingRates) {
			return model.StatusIncomplete
		}
	}

	return model.StatusReadyForComplete
}

// determineDraftStatus determines UCP status combining WooCommerce order status
// with cart state for accurate readiness assessment.
func determineDraftStatus(wcStatus string, cart *WooCartResponse) model.CheckoutStatus {
	switch wcStatus {
	case "checkout-draft":
		return determineCartStatus(cart)
	case "pending":
		return model.StatusCompleteInProgress
	case "processing":
		return model.StatusCompleteInProgress
	case "on-hold":
		return model.StatusRequiresEscalation
	case "completed":
		return model.StatusCompleted
	case "cancelled":
		return model.StatusCanceled
	case "failed":
		// Payment failed but checkout can be retried
		return model.StatusIncomplete
	default:
		return model.StatusIncomplete
	}
}

// hasRequiredShippingFields checks if shipping address has minimum required fields.
// Required: address_1, city, postcode, country.
func hasRequiredShippingFields(addr *WooAddress) bool {
	if addr == nil {
		return false
	}
	return addr.Address1 != "" &&
		addr.City != "" &&
		addr.Postcode != "" &&
		addr.Country != ""
}

// hasSelectedShippingRate checks if any shipping rate is selected.
func hasSelectedShippingRate(packages []WooShippingPkg) bool {
	for _, pkg := range packages {
		for _, rate := range pkg.ShippingRates {
			if rate.Selected {
				return true
			}
		}
	}
	return false
}

// CheckoutToUCP transforms a WooCommerce checkout response to UCP format.
// Used after POST /checkout completes payment (final order state).
func CheckoutToUCP(wc *WooCheckoutResponse, cfg *model.TransformConfig) *model.Checkout {
	if wc == nil || cfg == nil {
		return nil
	}

	totals := buildTotals(&wc.Totals)
	lineItems := transformLineItems(wc.LineItems)
	checkoutID := BuildCheckoutID(cfg.StoreDomain, wc.OrderID, "")
	continueURL := buildContinueURL(cfg.StoreURL, wc.OrderID, wc.OrderKey)

	return &model.Checkout{
		UCP: model.UCPMetadata{
			Version:         cfg.UCPVersion,
			Capabilities:    cfg.Capabilities,
			PaymentHandlers: cfg.PaymentHandlers,
		},
		ID:          checkoutID,
		Status:      MapOrderStatus(wc.Status),
		Currency:    wc.Totals.CurrencyCode,
		LineItems:   lineItems,
		Totals:      totals,
		Links:       cfg.PolicyLinks,
		Payment:     model.Payment{}, // Instruments submitted by client
		Buyer:       extractBuyer(&wc.BillingAddress),
		ContinueURL: continueURL,
	}
}

// CartToUCP transforms a WooCommerce cart response to UCP checkout format.
// Used during cart-building phase before payment submission.
func CartToUCP(cart *WooCartResponse, cartToken string, cfg *model.TransformConfig) *model.Checkout {
	if cart == nil || cfg == nil {
		return nil
	}

	totals := buildTotals(&cart.Totals)
	lineItems := transformCartItems(cart.Items)
	checkoutID := BuildCartID(cfg.StoreDomain, cartToken)
	messages := transformCartErrors(cart.Errors)
	discounts := transformCoupons(cart.Coupons)
	fulfillmentOptions, selectedFulfillmentID := transformFulfillment(cart.ShippingRates)

	checkout := &model.Checkout{
		UCP: model.UCPMetadata{
			Version:         cfg.UCPVersion,
			Capabilities:    cfg.Capabilities,
			PaymentHandlers: cfg.PaymentHandlers,
		},
		ID:                  checkoutID,
		Status:              determineCartStatus(cart),
		Currency:            cart.Totals.CurrencyCode,
		LineItems:           lineItems,
		Totals:              totals,
		Links:               cfg.PolicyLinks,
		Payment:             model.Payment{}, // Instruments submitted by client
		Buyer:               extractBuyer(&cart.BillingAddress),
		Messages:            messages,
		Discounts:           discounts,
		FulfillmentOptions:  fulfillmentOptions,
		FulfillmentOptionID: selectedFulfillmentID,
		FulfillmentAddress:  extractFulfillmentAddress(&cart.ShippingAddress),
	}

	return checkout
}

// DraftToUCP transforms a draft checkout with cart state to UCP format.
// Used after GET /checkout creates a draft order but before payment.
func DraftToUCP(draft *WooDraftCheckout, cart *WooCartResponse, cartToken string, cfg *model.TransformConfig) *model.Checkout {
	if draft == nil || cart == nil || cfg == nil {
		return nil
	}

	totals := buildTotals(&cart.Totals)
	lineItems := transformCartItems(cart.Items)
	checkoutID := BuildCheckoutID(cfg.StoreDomain, draft.OrderID, cartToken)
	continueURL := buildContinueURL(cfg.StoreURL, draft.OrderID, draft.OrderKey)
	messages := transformCartErrors(cart.Errors)
	discounts := transformCoupons(cart.Coupons)
	fulfillmentOptions, selectedFulfillmentID := transformFulfillment(cart.ShippingRates)

	checkout := &model.Checkout{
		UCP: model.UCPMetadata{
			Version:         cfg.UCPVersion,
			Capabilities:    cfg.Capabilities,
			PaymentHandlers: cfg.PaymentHandlers,
		},
		ID:                  checkoutID,
		Status:              determineDraftStatus(draft.Status, cart),
		Currency:            cart.Totals.CurrencyCode,
		LineItems:           lineItems,
		Totals:              totals,
		Links:               cfg.PolicyLinks,
		Payment:             model.Payment{}, // Instruments submitted by client
		Buyer:               extractBuyer(&cart.BillingAddress),
		ContinueURL:         continueURL,
		Messages:            messages,
		Discounts:           discounts,
		FulfillmentOptions:  fulfillmentOptions,
		FulfillmentOptionID: selectedFulfillmentID,
		FulfillmentAddress:  extractFulfillmentAddress(&cart.ShippingAddress),
	}

	return checkout
}

// transformCartItems converts WooCommerce cart items to UCP line items.
func transformCartItems(items []WooCartItem) []model.LineItem {
	result := make([]model.LineItem, len(items))
	for i, item := range items {
		result[i] = transformCartItem(&item)
	}
	return result
}

// transformCartItem converts a single cart item to UCP format.
// Cart items have different structure than checkout line items.
func transformCartItem(item *WooCartItem) model.LineItem {
	// WooCommerce Store API returns prices in minor units (cents) as strings
	price := model.ParseMinorUnits(item.Prices.Price)
	subtotal := model.ParseMinorUnits(item.Totals.LineSubtotal)
	total := model.ParseMinorUnits(item.Totals.LineTotal)

	return model.LineItem{
		ID: item.Key, // Cart uses string key, not numeric ID
		Item: model.Item{
			ID:       strconv.Itoa(item.ID),
			Title:    item.Name,
			Price:    price,
			ImageURL: firstImageURL(item.Images),
		},
		Quantity:   item.Quantity,
		BaseAmount: price * int64(item.Quantity),
		Subtotal:   subtotal,
		Total:      total,
	}
}

// transformFulfillment converts WooCommerce shipping rates to UCP fulfillment extension.
// Returns fulfillment options array and the selected option ID.
func transformFulfillment(packages []WooShippingPkg) ([]model.FulfillmentOption, string) {
	var options []model.FulfillmentOption
	var selectedID string

	for _, pkg := range packages {
		for _, rate := range pkg.ShippingRates {
			cost := model.ParseMinorUnits(rate.Price)
			options = append(options, model.FulfillmentOption{
				ID:       rate.RateID,
				Type:     "shipping", // WooCommerce shipping rates are all "shipping" type
				Title:    rate.Name,
				Subtotal: cost,
				Total:    cost, // No separate tax breakdown in WooCommerce shipping rates
			})
			if rate.Selected {
				selectedID = rate.RateID
			}
		}
	}
	return options, selectedID
}

// transformCartErrors converts WooCommerce cart errors to UCP messages.
func transformCartErrors(errors []WooCartError) []model.Message {
	if len(errors) == 0 {
		return nil
	}
	messages := make([]model.Message, len(errors))
	for i, err := range errors {
		messages[i] = model.NewErrorMessage(
			err.Code,
			err.Message,
			mapWooErrorSeverity(err.Code, 0),
		)
	}
	return messages
}

// mapWooErrorSeverity determines UCP severity from WooCommerce error code.
func mapWooErrorSeverity(code string, httpStatus int) model.MessageSeverity {
	// Recoverable: agent can fix with different input
	recoverable := map[string]bool{
		"woocommerce_rest_invalid_coupon":    true,
		"woocommerce_rest_cart_invalid_key":  true,
		"woocommerce_rest_invalid_product":   true,
		"rest_invalid_param":                 true,
		"woocommerce_rest_missing_parameter": true,
	}

	// Unrecoverable: can't proceed, need different approach
	unrecoverable := map[string]bool{
		"woocommerce_rest_product_out_of_stock":         true,
		"woocommerce_rest_cart_product_not_purchasable": true,
		"woocommerce_rest_product_does_not_exist":       true,
	}

	// Escalation: human must intervene
	if httpStatus == 401 || httpStatus == 403 {
		return model.SeverityEscalation
	}

	if recoverable[code] {
		return model.SeverityRecoverable
	}
	if unrecoverable[code] {
		return model.SeverityUnrecoverable
	}

	// 5xx = escalation (upstream failure)
	if httpStatus >= 500 {
		return model.SeverityEscalation
	}

	// Default 4xx to recoverable
	return model.SeverityRecoverable
}

// buildTotals creates the UCP totals array from WooCommerce totals.
// Per UCP spec, totals include: items_discount, subtotal, discount, fulfillment, tax, fee, total.
// WooCommerce Store API returns all totals in minor units (cents).
func buildTotals(t *WooTotals) []model.Total {
	itemsBase := model.ParseMinorUnits(t.TotalItems)
	discount := model.ParseMinorUnits(t.TotalDiscount)

	totals := []model.Total{
		{Type: model.TotalTypeItemsDiscount, Amount: discount},
		{Type: model.TotalTypeSubtotal, Amount: itemsBase - discount},
		{Type: model.TotalTypeFulfillment, Amount: model.ParseMinorUnits(t.TotalShipping), DisplayText: "Shipping"},
		{Type: model.TotalTypeTax, Amount: model.ParseMinorUnits(t.TotalTax)},
		{Type: model.TotalTypeFee, Amount: model.ParseMinorUnits(t.TotalFees)},
		{Type: model.TotalTypeTotal, Amount: model.ParseMinorUnits(t.TotalPrice)},
	}

	// Filter out zero-value totals except for required ones
	filtered := make([]model.Total, 0, len(totals))
	for _, total := range totals {
		// Always include subtotal and total; include others only if non-zero
		if total.Amount > 0 ||
			total.Type == model.TotalTypeSubtotal ||
			total.Type == model.TotalTypeTotal {
			filtered = append(filtered, total)
		}
	}

	return filtered
}

// transformLineItems converts WooCommerce line items to UCP format.
func transformLineItems(wcItems []WooLineItem) []model.LineItem {
	items := make([]model.LineItem, len(wcItems))
	for i, item := range wcItems {
		items[i] = transformLineItem(&item)
	}
	return items
}

// transformLineItem converts a single WooCommerce line item to UCP format.
func transformLineItem(item *WooLineItem) model.LineItem {
	price := model.ParseCents(item.Price)

	return model.LineItem{
		ID: strconv.Itoa(item.ID),
		Item: model.Item{
			ID:       strconv.Itoa(item.ID),
			Title:    item.Name,
			Price:    price,
			ImageURL: firstImageURL(item.Images),
		},
		Quantity:   item.Quantity,
		BaseAmount: price * int64(item.Quantity), // Calculated: unit price × quantity
		Subtotal:   model.ParseCents(item.Subtotal),
		Total:      model.ParseCents(item.Total),
	}
}

// firstImageURL extracts the first image URL from a slice, or empty string if none.
func firstImageURL(images []WooImage) string {
	if len(images) > 0 {
		return images[0].Src
	}
	return ""
}

// transformCoupons converts WooCommerce coupons to UCP discounts.
// Returns nil if no coupons are applied.
func transformCoupons(coupons []WooCoupon) *model.Discounts {
	if len(coupons) == 0 {
		return nil
	}

	discounts := &model.Discounts{
		Codes:   make([]string, len(coupons)),
		Applied: make([]model.AppliedDiscount, len(coupons)),
	}

	for i, c := range coupons {
		discounts.Codes[i] = c.Code
		discounts.Applied[i] = model.AppliedDiscount{
			Code:   c.Code,
			Title:  c.Code, // WooCommerce doesn't provide a human title, use code
			Amount: model.ParseMinorUnits(c.Totals.TotalDiscount),
		}
	}

	return discounts
}

// extractBuyer creates a Buyer from WooCommerce billing address.
// Returns nil if no buyer information is present.
func extractBuyer(addr *WooAddress) *model.Buyer {
	if addr == nil {
		return nil
	}
	if addr.FirstName == "" && addr.LastName == "" && addr.Email == "" {
		return nil
	}
	return &model.Buyer{
		FirstName:   addr.FirstName,
		LastName:    addr.LastName,
		Email:       addr.Email,
		PhoneNumber: addr.Phone,
	}
}

// extractFulfillmentAddress converts WooCommerce shipping address to UCP PostalAddress.
// Returns nil if no address information is present.
func extractFulfillmentAddress(addr *WooAddress) *model.PostalAddress {
	if addr == nil {
		return nil
	}
	// Check if address has any meaningful data
	if addr.Address1 == "" && addr.City == "" && addr.Country == "" {
		return nil
	}
	return &model.PostalAddress{
		FirstName:       addr.FirstName,
		LastName:        addr.LastName,
		StreetAddress:   addr.Address1,
		ExtendedAddress: addr.Address2,
		Locality:        addr.City,
		Region:          addr.State,
		Country:         addr.Country,
		PostalCode:      addr.Postcode,
		PhoneNumber:     addr.Phone,
	}
}

// BuildCartID creates a gid:// format ID for cart phase.
// Format: gid://{domain}/Cart/{cart_token}
func BuildCartID(domain string, cartToken string) string {
	return fmt.Sprintf("gid://%s/Cart/%s", domain, cartToken)
}

// BuildCheckoutID creates a gid:// format ID for checkout phase.
// Format: gid://{domain}/Checkout/{order_id}:{cart_token}
// The cart token is embedded for stateless operation.
func BuildCheckoutID(domain string, orderID int, cartToken string) string {
	base := fmt.Sprintf("gid://%s/Checkout/%d", domain, orderID)
	if cartToken != "" {
		return base + ":" + cartToken
	}
	return base
}

// ParseCheckoutID parses a checkout ID and returns its components.
// Returns: isCart, orderID (0 for cart), cartToken, error
func ParseCheckoutID(checkoutID string) (isCart bool, orderID int, cartToken string, err error) {
	// Must start with gid://
	if !strings.HasPrefix(checkoutID, "gid://") {
		return false, 0, "", fmt.Errorf("invalid ID format: must start with gid://")
	}

	// Remove gid:// prefix
	rest := strings.TrimPrefix(checkoutID, "gid://")

	// Find the path part (after domain)
	slashIdx := strings.Index(rest, "/")
	if slashIdx == -1 {
		return false, 0, "", fmt.Errorf("invalid ID format: missing path")
	}
	path := rest[slashIdx+1:]

	// Check if Cart or Checkout
	if strings.HasPrefix(path, "Cart/") {
		// Cart format: Cart/{cart_token}
		cartToken = strings.TrimPrefix(path, "Cart/")
		return true, 0, cartToken, nil
	}

	if strings.HasPrefix(path, "Checkout/") {
		// Checkout format: Checkout/{order_id} or Checkout/{order_id}:{cart_token}
		checkoutPart := strings.TrimPrefix(path, "Checkout/")

		// Check for embedded cart token
		colonIdx := strings.Index(checkoutPart, ":")
		if colonIdx != -1 {
			orderIDStr := checkoutPart[:colonIdx]
			cartToken = checkoutPart[colonIdx+1:]
			orderID, err = strconv.Atoi(orderIDStr)
			if err != nil {
				return false, 0, "", fmt.Errorf("invalid order ID: %w", err)
			}
			return false, orderID, cartToken, nil
		}

		// No cart token
		orderID, err = strconv.Atoi(checkoutPart)
		if err != nil {
			return false, 0, "", fmt.Errorf("invalid order ID: %w", err)
		}
		return false, orderID, "", nil
	}

	return false, 0, "", fmt.Errorf("invalid ID format: unknown type")
}

// buildContinueURL creates the WooCommerce order-pay URL for escalation.
func buildContinueURL(storeURL string, orderID int, orderKey string) string {
	base := strings.TrimSuffix(storeURL, "/")
	if orderKey != "" {
		return fmt.Sprintf("%s/checkout/order-pay/%d/?key=%s", base, orderID, orderKey)
	}
	return fmt.Sprintf("%s/checkout/order-pay/%d/", base, orderID)
}

// buildOrderPermalinkURL creates the WooCommerce order-received (thank-you) URL.
// This is the permanent link to the completed order confirmation page.
func buildOrderPermalinkURL(storeURL string, orderID int, orderKey string) string {
	base := strings.TrimSuffix(storeURL, "/")
	if orderKey != "" {
		return fmt.Sprintf("%s/checkout/order-received/%d/?key=%s", base, orderID, orderKey)
	}
	return fmt.Sprintf("%s/checkout/order-received/%d/", base, orderID)
}

// === Reverse Transformations (UCP → WooCommerce) ===

// AddressFromUCP converts a UCP postal address to WooCommerce format.
func AddressFromUCP(addr *model.PostalAddress) *WooAddress {
	if addr == nil {
		return nil
	}

	return &WooAddress{
		FirstName: addr.FirstName,
		LastName:  addr.LastName,
		Address1:  addr.StreetAddress,
		Address2:  addr.ExtendedAddress,
		City:      addr.Locality,
		State:     addr.Region,
		Country:   addr.Country,
		Postcode:  addr.PostalCode,
		Phone:     addr.PhoneNumber,
	}
}

// === Cart Token Functions ===

// ExtractCartToken extracts cart token from any checkout ID format.
// Works with both Cart and Checkout ID formats.
func ExtractCartToken(checkoutID string) string {
	_, _, token, err := ParseCheckoutID(checkoutID)
	if err != nil {
		return ""
	}
	return token
}
