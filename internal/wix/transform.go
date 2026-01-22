package wix

import (
	"fmt"
	"strings"

	"ucp-proxy/internal/model"
)

// =============================================================================
// WIX → UCP TRANSFORMATION
// =============================================================================
//
// Transforms Wix eCommerce API responses into UCP checkout format.
//
// The Wix adapter uses `requires_escalation` status (not `ready_for_complete`)
// when all non-payment fields are present. This triggers continueURL handoff
// since Wix payment processing requires browser-based checkout.
// =============================================================================

// CheckoutToUCP transforms a Wix checkout to UCP format.
func CheckoutToUCP(wc *WixCheckout, instanceToken string, cfg *model.TransformConfig) *model.Checkout {
	if wc == nil || cfg == nil {
		return nil
	}

	lineItems := transformLineItems(wc.LineItems)
	totals := buildTotals(wc.PriceSummary)
	fulfillmentOptions, selectedID := extractShippingOptions(wc.ShippingInfo)
	discounts := transformDiscounts(wc.AppliedDiscounts)
	buyer := extractBuyer(wc.BuyerInfo, wc.BillingInfo)
	fulfillmentAddress := extractFulfillmentAddress(wc.ShippingInfo)
	status := determineStatus(wc)

	checkout := &model.Checkout{
		UCP: model.UCPMetadata{
			Version:         cfg.UCPVersion,
			Capabilities:    cfg.Capabilities,
			PaymentHandlers: cfg.PaymentHandlers,
		},
		ID:                  BuildCheckoutID(cfg.StoreDomain, wc.ID, instanceToken),
		Status:              status,
		Currency:            wc.Currency,
		LineItems:           lineItems,
		Totals:              totals,
		Links:               cfg.PolicyLinks,
		Payment:             model.Payment{}, // Instruments submitted by client
		Buyer:               buyer,
		Discounts:           discounts,
		FulfillmentOptions:  fulfillmentOptions,
		FulfillmentOptionID: selectedID,
		FulfillmentAddress:  fulfillmentAddress,
	}

	return checkout
}

// =============================================================================
// STATUS DETERMINATION (Escalation Flow)
// =============================================================================
//
// Current implementation escalates when checkout is complete minus payment.
// The status transitions:
//   incomplete → requires_escalation (never ready_for_complete)
//
// This triggers the agent to present continue_url to the buyer.
// Programmatic payment completion to be added.
// =============================================================================

// determineStatus determines the UCP checkout status from Wix checkout state.
// Auto-escalates when all non-payment fields are present.
func determineStatus(wc *WixCheckout) model.CheckoutStatus {
	if wc == nil {
		return model.StatusIncomplete
	}

	// Must have line items
	if len(wc.LineItems) == 0 {
		return model.StatusIncomplete
	}

	// Must have buyer email
	if wc.BuyerInfo == nil || wc.BuyerInfo.Email == "" {
		return model.StatusIncomplete
	}

	// If needs shipping, must have shipping address
	if needsShipping(wc) {
		if wc.ShippingInfo == nil || wc.ShippingInfo.ShippingDestination == nil ||
			wc.ShippingInfo.ShippingDestination.Address == nil {
			return model.StatusIncomplete
		}
		addr := wc.ShippingInfo.ShippingDestination.Address
		if addr.AddressLine == "" || addr.City == "" || addr.Country == "" {
			return model.StatusIncomplete
		}
	}

	// If shipping options available, one must be selected
	if len(wc.AvailableShippingOptions) > 0 {
		if wc.SelectedShippingOption == nil || wc.SelectedShippingOption.Code == "" {
			return model.StatusIncomplete
		}
	}

	// All non-payment fields present → escalate to Wix checkout.
	// Returns requires_escalation (not ready_for_complete) because programmatic
	// payment completion is not yet implemented.
	return model.StatusRequiresEscalation
}

// needsShipping checks if the checkout contains physical items requiring shipping.
// Returns true if ANY item requires shipping OR if ANY item has unknown shipping requirements.
// Only returns false when ALL items explicitly declare they don't need shipping.
func needsShipping(wc *WixCheckout) bool {
	hasUnknown := false
	for _, item := range wc.LineItems {
		if item.PhysicalProperties != nil {
			if item.PhysicalProperties.ShippingRequired {
				return true // At least one item explicitly requires shipping
			}
			// PhysicalProperties set with ShippingRequired=false → doesn't need shipping
		} else {
			hasUnknown = true // No physical properties → unknown, assume needs shipping
		}
	}
	// Only default to needing shipping if any item's requirements are unknown
	return hasUnknown
}

// === Line Items ===

// transformLineItems converts Wix line items to UCP format.
func transformLineItems(items []WixLineItem) []model.LineItem {
	result := make([]model.LineItem, len(items))
	for i, item := range items {
		result[i] = transformLineItem(&item)
	}
	return result
}

// transformLineItem converts a single Wix line item to UCP format.
func transformLineItem(item *WixLineItem) model.LineItem {
	price := parseDecimalToCents(item.Price)
	total := price * int64(item.Quantity)

	title := ""
	if item.ProductName != nil {
		title = item.ProductName.Translated
		if title == "" {
			title = item.ProductName.Original
		}
	}

	imageURL := ""
	if item.Image != nil {
		imageURL = item.Image.URL
	}

	productID := ""
	if item.CatalogReference != nil {
		productID = item.CatalogReference.CatalogItemID
	}

	return model.LineItem{
		ID: item.ID,
		Item: model.Item{
			ID:       productID,
			Title:    title,
			Price:    price,
			ImageURL: imageURL,
		},
		Quantity:   item.Quantity,
		BaseAmount: total,
		Subtotal:   total,
		Total:      total,
	}
}

// === Totals ===

// buildTotals creates the UCP totals array from Wix price summary.
func buildTotals(ps *WixPriceSummary) []model.Total {
	if ps == nil {
		return []model.Total{}
	}

	totals := []model.Total{}

	// Subtotal (items total before cart-level discounts/shipping/tax)
	if ps.Subtotal != nil {
		subtotal := parseDecimalToCents(ps.Subtotal)
		totals = append(totals, model.Total{
			Type:   model.TotalTypeSubtotal,
			Amount: subtotal,
		})
	}

	// Discount
	if ps.Discount != nil {
		discount := parseDecimalToCents(ps.Discount)
		if discount > 0 {
			totals = append(totals, model.Total{
				Type:   model.TotalTypeDiscount,
				Amount: discount,
			})
		}
	}

	// Shipping
	if ps.Shipping != nil {
		shipping := parseDecimalToCents(ps.Shipping)
		totals = append(totals, model.Total{
			Type:        model.TotalTypeFulfillment,
			Amount:      shipping,
			DisplayText: "Shipping",
		})
	}

	// Tax
	if ps.Tax != nil {
		tax := parseDecimalToCents(ps.Tax)
		if tax > 0 {
			totals = append(totals, model.Total{
				Type:   model.TotalTypeTax,
				Amount: tax,
			})
		}
	}

	// Grand total
	if ps.Total != nil {
		total := parseDecimalToCents(ps.Total)
		totals = append(totals, model.Total{
			Type:   model.TotalTypeTotal,
			Amount: total,
		})
	}

	return totals
}

// === Shipping Options ===

// extractShippingOptions extracts fulfillment options from Wix shipping info.
// Wix nests options under carrierServiceOptions[].shippingOptions[].
func extractShippingOptions(info *WixShippingInfo) ([]model.FulfillmentOption, string) {
	if info == nil {
		return nil, ""
	}

	// Flatten all shipping options from all carriers
	var options []WixShippingOption
	for _, carrier := range info.CarrierServiceOptions {
		options = append(options, carrier.ShippingOptions...)
	}

	if len(options) == 0 {
		return nil, ""
	}

	result := make([]model.FulfillmentOption, len(options))
	for i, opt := range options {
		// Cost is nested: cost.price.amount
		var cost int64
		if opt.Cost != nil && opt.Cost.Price != nil {
			cost = parseDecimalToCents(opt.Cost.Price)
		}

		subtitle := ""
		if opt.Logistics != nil && opt.Logistics.DeliveryTime != "" {
			subtitle = opt.Logistics.DeliveryTime
		}
		result[i] = model.FulfillmentOption{
			ID:       opt.Code,
			Type:     "shipping",
			Title:    opt.Title,
			SubTitle: subtitle,
			Subtotal: cost,
			Total:    cost,
		}
	}

	// Get selected option ID from selectedCarrierServiceOption
	selectedID := ""
	if info.SelectedCarrierServiceOption != nil && info.SelectedCarrierServiceOption.Code != "" {
		selectedID = info.SelectedCarrierServiceOption.Code
	}

	return result, selectedID
}

// === Discounts ===

// transformDiscounts converts Wix applied discounts to UCP format.
func transformDiscounts(applied []WixAppliedDiscount) *model.Discounts {
	if len(applied) == 0 {
		return nil
	}

	discounts := &model.Discounts{
		Codes:   make([]string, 0),
		Applied: make([]model.AppliedDiscount, 0, len(applied)),
	}

	for _, d := range applied {
		ad := model.AppliedDiscount{}

		if d.Coupon != nil {
			ad.Code = d.Coupon.Code
			ad.Title = d.Coupon.Name
			if ad.Title == "" {
				ad.Title = d.Coupon.Code
			}
			// Coupon amount is nested inside coupon object
			if d.Coupon.Amount != nil {
				ad.Amount = parseDecimalToCents(d.Coupon.Amount)
			}
			discounts.Codes = append(discounts.Codes, d.Coupon.Code)
		} else {
			ad.Automatic = true
			ad.Title = "Automatic discount"
			// Non-coupon discounts have amount at top level
			ad.Amount = parseDecimalToCents(d.Amount)
		}

		discounts.Applied = append(discounts.Applied, ad)
	}

	return discounts
}

// === Buyer & Address ===

// extractBuyer creates a Buyer from Wix buyer/billing info.
func extractBuyer(bi *WixBuyerInfo, billing *WixBillingInfo) *model.Buyer {
	if bi == nil && billing == nil {
		return nil
	}

	buyer := &model.Buyer{}

	if bi != nil {
		buyer.Email = bi.Email
		buyer.FirstName = bi.FirstName
		buyer.LastName = bi.LastName
		buyer.PhoneNumber = bi.Phone
	}

	// Fall back to billing contact if buyer info incomplete
	if billing != nil && billing.ContactDetails != nil {
		if buyer.FirstName == "" {
			buyer.FirstName = billing.ContactDetails.FirstName
		}
		if buyer.LastName == "" {
			buyer.LastName = billing.ContactDetails.LastName
		}
		if buyer.PhoneNumber == "" {
			buyer.PhoneNumber = billing.ContactDetails.Phone
		}
	}

	if buyer.Email == "" && buyer.FirstName == "" && buyer.LastName == "" {
		return nil
	}

	return buyer
}

// extractFulfillmentAddress converts Wix shipping address to UCP format.
// Wix returns addresses nested under shippingDestination.address.
func extractFulfillmentAddress(si *WixShippingInfo) *model.PostalAddress {
	if si == nil || si.ShippingDestination == nil {
		return nil
	}

	dest := si.ShippingDestination
	addr := dest.Address
	if addr == nil {
		return nil
	}

	if addr.AddressLine == "" && addr.City == "" && addr.Country == "" {
		return nil
	}

	postal := &model.PostalAddress{
		StreetAddress:   addr.AddressLine,
		ExtendedAddress: addr.AddressLine2,
		Locality:        addr.City,
		Country:         addr.Country,
		PostalCode:      addr.PostalCode,
	}

	// Wix uses format "US-CA" for subdivision; extract state code
	if addr.Subdivision != "" {
		parts := strings.Split(addr.Subdivision, "-")
		if len(parts) == 2 {
			postal.Region = parts[1]
		} else {
			postal.Region = addr.Subdivision
		}
	}

	// Contact details are at the destination level, not inside address
	if dest.ContactDetails != nil {
		postal.FirstName = dest.ContactDetails.FirstName
		postal.LastName = dest.ContactDetails.LastName
		postal.PhoneNumber = dest.ContactDetails.Phone
	}

	return postal
}

// === UCP → Wix Transformations ===

// AddressToWix converts UCP postal address to Wix shipping destination format.
// Returns WixShippingDestination with nested address and contactDetails.
func AddressToWix(addr *model.PostalAddress) *WixShippingDestination {
	if addr == nil {
		return nil
	}

	// Build subdivision in Wix format (e.g., "US-CA")
	subdivision := addr.Region
	if addr.Country != "" && addr.Region != "" && !strings.Contains(addr.Region, "-") {
		subdivision = addr.Country + "-" + addr.Region
	}

	return &WixShippingDestination{
		Address: &WixAddress{
			AddressLine:  addr.StreetAddress,
			AddressLine2: addr.ExtendedAddress,
			City:         addr.Locality,
			Subdivision:  subdivision,
			Country:      addr.Country,
			PostalCode:   addr.PostalCode,
		},
		ContactDetails: &WixContactDetails{
			FirstName: addr.FirstName,
			LastName:  addr.LastName,
			Phone:     addr.PhoneNumber,
		},
	}
}

// AddressToWixBilling converts UCP postal address to Wix billing format.
// Billing uses a simpler structure without the nested destination wrapper.
func AddressToWixBilling(addr *model.PostalAddress) *WixAddress {
	if addr == nil {
		return nil
	}

	subdivision := addr.Region
	if addr.Country != "" && addr.Region != "" && !strings.Contains(addr.Region, "-") {
		subdivision = addr.Country + "-" + addr.Region
	}

	return &WixAddress{
		AddressLine:  addr.StreetAddress,
		AddressLine2: addr.ExtendedAddress,
		City:         addr.Locality,
		Subdivision:  subdivision,
		Country:      addr.Country,
		PostalCode:   addr.PostalCode,
	}
}

// BuyerToWix converts UCP buyer to Wix buyer info.
func BuyerToWix(buyer *model.Buyer) *WixBuyerInfo {
	if buyer == nil {
		return nil
	}
	return &WixBuyerInfo{
		Email:     buyer.Email,
		FirstName: buyer.FirstName,
		LastName:  buyer.LastName,
		Phone:     buyer.PhoneNumber,
	}
}

// === Checkout ID Encoding ===

// BuildCheckoutID creates a gid:// format ID for Wix checkout.
// Format: gid://wix.{siteID}/Checkout/{checkoutID}:{instanceToken}
//
// The instance token is embedded for stateless operation - we need it
// for all subsequent Wix API calls.
func BuildCheckoutID(siteID, checkoutID, instanceToken string) string {
	return fmt.Sprintf("gid://wix.%s/Checkout/%s:%s", siteID, checkoutID, instanceToken)
}

// ParseCheckoutID parses a Wix checkout ID and returns its components.
// Returns: checkoutID, instanceToken, error
func ParseCheckoutID(gid string) (checkoutID, instanceToken string, err error) {
	// Must start with gid://wix.
	if !strings.HasPrefix(gid, "gid://wix.") {
		return "", "", fmt.Errorf("invalid Wix checkout ID format")
	}

	// Remove prefix and find /Checkout/
	rest := strings.TrimPrefix(gid, "gid://wix.")
	checkoutIdx := strings.Index(rest, "/Checkout/")
	if checkoutIdx == -1 {
		return "", "", fmt.Errorf("invalid Wix checkout ID: missing /Checkout/")
	}

	// Extract checkout part
	checkoutPart := rest[checkoutIdx+len("/Checkout/"):]

	// Split by colon to get checkoutID and instanceToken
	colonIdx := strings.Index(checkoutPart, ":")
	if colonIdx == -1 {
		return "", "", fmt.Errorf("invalid Wix checkout ID: missing instance token")
	}

	checkoutID = checkoutPart[:colonIdx]
	instanceToken = checkoutPart[colonIdx+1:]

	if checkoutID == "" || instanceToken == "" {
		return "", "", fmt.Errorf("invalid Wix checkout ID: empty component")
	}

	return checkoutID, instanceToken, nil
}

// ExtractInstanceToken extracts the instance token from a checkout ID.
func ExtractInstanceToken(gid string) string {
	_, token, err := ParseCheckoutID(gid)
	if err != nil {
		return ""
	}
	return token
}

// === Helper Functions ===

// parseDecimalToCents converts a WixPrice to cents.
// Wix returns prices as strings like "99.00".
func parseDecimalToCents(p *WixPrice) int64 {
	if p == nil || p.Amount == "" {
		return 0
	}
	return model.ParseCents(p.Amount)
}
